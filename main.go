package main

import (
	"context"
	"encoding/xml"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/brutella/hc"
	"github.com/brutella/hc/accessory"
	"github.com/brutella/hc/characteristic"
	hclog "github.com/brutella/hc/log"
	"github.com/brutella/hc/service"
	ssdp "github.com/koron/go-ssdp"
)

var Debug = false

const (
	BridgeDeviceURN      = "urn:Belkin:device:bridge:1"
	ControlleeDeviceURN  = "urn:Belkin:device:controllee:1" // simple switch
	LightDeviceURN       = "urn:Belkin:device:light:1"
	SensorDeviceURN      = "urn:Belkin:device:sensor:1" // motion sensor
	NetCamDeviceURN      = "urn:Belkin:device:netcam:1"
	InsightDeviceURN     = "urn:Belkin:device:insight:1" // switch with power monitoring
	LightSwitchDeviceURN = "urn:Belkin:device:lightswitch:1"

	BasicEventServiceURN = "urn:Belkin:service:basicevent:1"
	InsightServiceURN    = "urn:Belkin:service:insight:1"

	BasicEventControlPath = "/upnp/control/basicevent1"
	BasicEventEventPath   = "/upnp/event/basicevent1"
	InsightPath           = "/upnp/control/insight1"

	subscribeTimeout         = 90 * time.Minute
	resubscribeInterval      = 85 * time.Minute
	discoveryInterval        = 30 * time.Second
	powerConsumptionInterval = 30 * time.Second

	// UUID for power consumption, used by the Eve app
	consumptionUUID = "E863F10D-079E-48FF-8F27-9C2605A29F52"
)

// TODO: Currently only simple switch devices are supported.  It
// should be simple to add to these, though.
var supportedDeviceTypes = []string{
	ControlleeDeviceURN,
	InsightDeviceURN,
	LightSwitchDeviceURN,
}

type Device struct {
	Type            string `xml:"device>deviceType"`
	Name            string `xml:"device>friendlyName"`
	Manufacturer    string `xml:"device>manufacturer"`
	Model           string `xml:"device>modelName"`
	SerialNumber    string `xml:"device>serialNumber"`
	FirmwareVersion string `xml:"device>firmwareVersion"`
	UDN             string `xml:"device>UDN"`

	baseURL string
	sid     string

	accessory *accessory.Switch
	power     *characteristic.Int
	transport hc.Transport
}

func (d Device) String() string {
	return fmt.Sprintf("%s (at %s)", d.Name, d.baseURL)
}

func (d Device) Supported() bool {
	for _, t := range supportedDeviceTypes {
		if t == d.Type {
			return true
		}
	}
	return false
}

func (d Device) On() (bool, error) {
	req, err := newBasicEventRequest(d.baseURL, "GetBinaryState")
	if err != nil {
		return false, err
	}

	if Debug {
		log.Printf("Requesting binary state for %s:", d)
		dump, _ := httputil.DumpRequestOut(req, true)
		log.Printf("%s", string(dump))
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	if Debug {
		log.Println("Response:")
		dump, _ := httputil.DumpResponse(resp, true)
		log.Printf("%s", string(dump))
	}

	var res struct {
		BinaryState int `xml:"Body>GetBinaryStateResponse>BinaryState"`
	}
	dec := xml.NewDecoder(resp.Body)
	if err := dec.Decode(&res); err != nil {
		return false, err
	}

	return res.BinaryState == 1, nil
}

func (d Device) Set(on bool) error {
	req, err := setBinaryStateRequest(d.baseURL, on)
	if err != nil {
		return err
	}

	if Debug {
		log.Printf("Setting binary state for %s to %t", d, on)
		dump, _ := httputil.DumpRequestOut(req, true)
		log.Printf("%s", string(dump))
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if Debug {
		log.Println("Response:")
		dump, _ := httputil.DumpResponse(resp, true)
		log.Printf("%s", string(dump))
	}

	return nil
}

func (d *Device) Subscribe() error {
	ip := getIPv4Address()
	if ip == nil {
		panic("unable to get local IPv4 address")
	}

	req, err := subscribeRequest(d.baseURL, d.sid, ip, subscribeTimeout)
	if err != nil {
		return err
	}

	if Debug {
		log.Printf("Subscribing to %s:", d)
		dump, _ := httputil.DumpRequestOut(req, true)
		log.Printf("%s", string(dump))
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if Debug {
		log.Println("Response:")
		dump, _ := httputil.DumpResponse(resp, true)
		log.Printf("%s", string(dump))
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code %d", resp.StatusCode)
	}

	d.sid = resp.Header.Get("SID")
	return nil
}

func (d *Device) Unsubscribe() error {
	if d.sid == "" {
		return nil
	}

	req, err := unsubscribeRequest(d.baseURL, d.sid)
	if err != nil {
		return err
	}

	if Debug {
		log.Printf("Unsubscribing from %s:", d)
		dump, _ := httputil.DumpRequestOut(req, true)
		log.Printf("%s", string(dump))
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if Debug {
		log.Println("Response")
		dump, _ := httputil.DumpResponse(resp, true)
		log.Printf("%s", string(dump))
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code %d", resp.StatusCode)
	}

	return nil
}

func (d *Device) InsightParams() (*InsightParams, error) {
	req, err := newInsightRequest(d.baseURL, "GetInsightParams")
	if err != nil {
		return nil, err
	}

	if Debug {
		log.Printf("Request for insight params from %s:", d)
		dump, _ := httputil.DumpRequestOut(req, true)
		log.Printf("%s", string(dump))
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if Debug {
		log.Println("Response")
		dump, _ := httputil.DumpResponse(resp, true)
		log.Printf("%s", string(dump))
	}

	var res struct {
		InsightParams string `xml:"Body>GetInsightParamsResponse>InsightParams"`
	}

	dec := xml.NewDecoder(resp.Body)
	if err := dec.Decode(&res); err != nil {
		return nil, err
	}

	atoi := func(x string) int {
		i, err := strconv.Atoi(x)
		if err != nil {
			panic(err)
		}
		return i
	}

	// Meanings of these parts taken mostly from pywemo
	parts := strings.Split(res.InsightParams, "|")
	return &InsightParams{
		State:            parts[0] != "0",
		LastChange:       time.Unix(int64(atoi(parts[1])), 0),
		OnFor:            time.Duration(atoi(parts[2])) * time.Second,
		OnToday:          time.Duration(atoi(parts[3])) * time.Second,
		OnTotal:          time.Duration(atoi(parts[4])) * time.Second,
		TimePeriod:       time.Duration(atoi(parts[5])) * time.Second,
		CurrentMW:        atoi(parts[7]),
		TodayMWPerMinute: atoi(parts[8]),
		// TotalMW is presented as a float, but always has 0 fractional part
		TotalMWPerMinute: atoi(strings.Split(parts[9], ".")[0]),
		PowerThreshold:   atoi(parts[10]),
	}, nil

}

type InsightParams struct {
	State            bool
	LastChange       time.Time
	OnFor            time.Duration
	OnToday          time.Duration
	OnTotal          time.Duration
	TimePeriod       time.Duration
	CurrentMW        int
	TodayMWPerMinute int
	TotalMWPerMinute int
	PowerThreshold   int
}

var devices = struct {
	sync.Mutex
	m map[string]*Device
}{
	m: map[string]*Device{},
}

func getIPv4Address() net.IP {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}

	for _, addr := range addrs {
		ipnet, ok := addr.(*net.IPNet)
		if !ok {
			continue
		}

		ipv4 := ipnet.IP.To4()
		if ipv4 == nil || ipv4.IsLoopback() {
			continue
		}

		return ipv4
	}

	return nil
}

func getDevice(u string) (*Device, error) {
	if Debug {
		log.Printf("Requesting get info from %s:", u)
	}

	resp, err := http.Get(u)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if Debug {
		log.Printf("Response:")
		dump, _ := httputil.DumpResponse(resp, true)
		log.Printf("%s", string(dump))
	}

	var d Device
	dec := xml.NewDecoder(resp.Body)
	if err := dec.Decode(&d); err != nil {
		return nil, err
	}

	parsedURL, err := url.Parse(u)
	if err != nil {
		return nil, err
	}
	parsedURL.Path = ""
	d.baseURL = parsedURL.String()

	return &d, nil
}

func discoverDevices(hcConfig hc.Config) error {
	services, err := ssdp.Search(BasicEventServiceURN, 5, "")
	if err != nil {
		return err
	}

	for _, s := range services {
		d, err := getDevice(s.Location)
		if err != nil {
			log.Printf("Skipping %s: %s", s.Location, err)
			continue
		}

		if !d.Supported() {
			continue
		}

		// Are we already subscribed to this device?
		devices.Lock()
		if _, ok := devices.m[d.UDN]; ok {
			devices.Unlock()
			continue
		}

		info := accessory.Info{
			Name:         d.Name,
			Manufacturer: d.Manufacturer,
			Model:        d.Model,
			SerialNumber: d.SerialNumber,
		}
		log.Printf("Found %s", d)

		acc := &accessory.Switch{
			Accessory: accessory.New(info, accessory.TypeSwitch),
			Switch:    service.NewSwitch(),
		}
		acc.Info.FirmwareRevision.SetValue(d.FirmwareVersion)

		if d.Type == InsightDeviceURN {
			d.power = characteristic.NewInt(consumptionUUID)
			d.power.Format = characteristic.FormatUInt16
			d.power.Perms = characteristic.PermsRead()
			d.power.Unit = "W"

			acc.Switch.AddCharacteristic(d.power.Characteristic)
			updatePower(d)
		}

		acc.AddService(acc.Switch.Service)

		on, err := d.On()
		if err != nil {
			log.Printf("Couldn't get initial state for %s: %v", d, err)
		} else {
			acc.Switch.On.SetValue(on)
		}

		//TODO: Change this to a external configuration
		hcConfig.StoragePath = filepath.Join(os.Getenv("HOME"), ".homecontrol", "wemo", info.Name)

		t, err := hc.NewIPTransport(hcConfig, acc.Accessory)
		if err != nil {
			return err
		}

		acc.Switch.On.OnValueRemoteUpdate(func(on bool) {
			if err := d.Set(on); err != nil {
				log.Printf("Error setting %s to %t: %s", d, on, err)
			}
		})

		d.accessory = acc
		d.transport = t

		devices.m[d.UDN] = d
		go t.Start()
		d.Subscribe()
		devices.Unlock()
	}

	return nil
}

func updatePower(d *Device) {
	if d.power == nil {
		log.Printf("No power characteristic on %s", d)
		return
	}

	p, err := d.InsightParams()
	if err != nil {
		log.Printf("Unable to get power usage for %s: %s", d, err)
		return
	}

	d.power.SetValue(p.CurrentMW / 1000)
}

func main() {
	// TODO: make configurable
	hcConfig := hc.Config{
		Pin:         "00102003",
		StoragePath: filepath.Join(os.Getenv("HOME"), ".homecontrol", "wemo"),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if x := os.Getenv("DEBUG"); x != "" {
		hclog.Debug.Enable()
		Debug = true
	}

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if Debug {
			log.Printf("Incoming request from %s:", r.RemoteAddr)
			dump, _ := httputil.DumpRequest(r, true)
			log.Printf("%s", string(dump))
		}

		if r.Method != "NOTIFY" {
			log.Printf("unknown method %s", r.Method)
			return
		}

		var prop struct {
			BinaryState string `xml:"property>BinaryState"`
		}
		dec := xml.NewDecoder(r.Body)
		if err := dec.Decode(&prop); err != nil {
			log.Printf("error decoding NOTIFY")
			return
		}

		if prop.BinaryState == "" {
			// A notification for a property we don't care about
			return
		}

		sid := r.Header.Get("SID")
		var d *Device

		devices.Lock()
		for _, device := range devices.m {
			if device.sid == sid {
				d = device
				break
			}
		}
		devices.Unlock()

		if d == nil {
			return
		}

		fields := strings.Split(prop.BinaryState, "|")

		// Values here are weird.  Sometimes we get a value of
		// "8" even when it's on.  But 0 seems to always mean
		// it's off.
		on := true
		if fields[0] == "0" {
			on = false
		}
		d.accessory.Switch.On.SetValue(on)
		log.Printf("%s switched to %t", d, on)

		if d.Type == InsightDeviceURN {
			updatePower(d)
		}
	})

	go func() {
		log.Println("Starting HTTP server on :1225")
		panic(http.ListenAndServe(":1225", nil))
	}()

	// Discover devices right away, and then every 30s afterward
	go func(ctx context.Context) {
		log.Printf("Starting device discovery loop")
		defer log.Printf("Editing device discovery loop")

		discoverDevices(hcConfig)

		t := time.NewTicker(discoveryInterval)
		defer t.Stop()

		for {
			select {
			case <-t.C:
				discoverDevices(hcConfig)
			case <-ctx.Done():
			}
		}
	}(ctx)

	// Refresh device subscriptions
	go func(ctx context.Context) {
		t := time.NewTicker(resubscribeInterval)
		defer t.Stop()

		for {
			select {
			case <-t.C:
				devices.Lock()
				for udn, d := range devices.m {
					if err := d.Subscribe(); err != nil {
						log.Printf("error resubscribing %s: %s", d, err)

						// When resubscribing fails, it's best to nuke it and start from scratch.
						d.Unsubscribe()
						<-d.transport.Stop()
						delete(devices.m, udn)
					}
				}
				devices.Unlock()

			case <-ctx.Done():
			}
		}
	}(ctx)

	// Update power usage stats on Insight switches
	go func(ctx context.Context) {
		t := time.NewTicker(powerConsumptionInterval)
		defer t.Stop()

		for {
			select {
			case <-t.C:
				var insights []*Device

				devices.Lock()
				for _, d := range devices.m {
					if d.Type == InsightDeviceURN {
						insights = append(insights, d)
					}
				}
				devices.Unlock()

				for _, d := range insights {
					updatePower(d)
				}

			case <-ctx.Done():
			}
		}
	}(ctx)

	hc.OnTermination(func() {
		devices.Lock()
		for udn, d := range devices.m {
			d.Unsubscribe()
			<-d.transport.Stop()
			delete(devices.m, udn)
		}
		devices.Unlock()
		cancel()
	})

	<-ctx.Done()
}

func newBasicEventRequest(baseURL, action string) (*http.Request, error) {
	const payloadFmt = `
<?xml version="1.0" encoding="utf-8"?>
<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">
  <s:Body>
    <u:%s xmlns:u="urn:Belkin:service:basicevent:1" />
  </s:Body>
</s:Envelope>`

	url := baseURL + BasicEventControlPath
	payload := fmt.Sprintf(payloadFmt, action)

	req, err := http.NewRequest("POST", url, strings.NewReader(payload))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "text/xml; charset=\"utf-8\"")

	// The case of the SOAPACTION header name is important, so it
	// must be set explicitly in the map rather than using .Set()
	req.Header["SOAPACTION"] = []string{
		fmt.Sprintf(`"%s#%s"`, BasicEventServiceURN, action),
	}

	return req, nil
}

func newInsightRequest(baseURL, action string) (*http.Request, error) {
	const payloadFmt = `
<?xml version="1.0" encoding="utf-8"?>
  <s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">
    <s:Body>
      <u:%s xmlns:u="urn:Belkin:service:insight:1" />
    </s:Body>
</s:Envelope>`

	url := baseURL + InsightPath
	payload := fmt.Sprintf(payloadFmt, action)

	req, err := http.NewRequest("POST", url, strings.NewReader(payload))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "text/xml; charset=\"utf-8\"")

	// The case of the SOAPACTION header name is important, so it
	// must be set explicitly in the map rather than using .Set()
	req.Header["SOAPACTION"] = []string{
		fmt.Sprintf(`"%s#%s"`, InsightServiceURN, action),
	}

	return req, nil
}

func setBinaryStateRequest(baseURL string, on bool) (*http.Request, error) {
	const payloadFmt = `
<?xml version="1.0" encoding="utf-8"?>
<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">
  <s:Body>
    <u:SetBinaryState xmlns:u="urn:Belkin:service:basicevent:1">
      <BinaryState>%d</BinaryState>
    </u:SetBinaryState>
  </s:Body>
</s:Envelope>`

	url := baseURL + BasicEventControlPath
	var payload string
	if on {
		payload = fmt.Sprintf(payloadFmt, 1)
	} else {
		payload = fmt.Sprintf(payloadFmt, 0)
	}

	req, err := http.NewRequest("POST", url, strings.NewReader(payload))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "text/xml; charset=\"utf-8\"")

	// The case of SOAPACTION is important, so it must be set
	// explicitly in the map rather than using .Set()
	req.Header["SOAPACTION"] = []string{`"` + BasicEventServiceURN + `#SetBinaryState"`}

	return req, nil
}

func subscribeRequest(baseURL, sid string, ip net.IP, timeout time.Duration) (*http.Request, error) {
	url := baseURL + BasicEventEventPath
	req, err := http.NewRequest("SUBSCRIBE", url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Add("Timeout", fmt.Sprintf("Second-%d", int64(timeout/time.Second)))

	if sid != "" {
		req.Header.Add("SID", sid)
	} else {
		req.Header.Add("NT", "upnp:event")
		req.Header.Add("Callback", fmt.Sprintf("<http://%s:1225/>", ip))
	}

	return req, nil
}

func unsubscribeRequest(baseURL, sid string) (*http.Request, error) {
	url := baseURL + BasicEventEventPath
	req, err := http.NewRequest("UNSUBSCRIBE", url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Add("SID", sid)

	return req, nil

}
