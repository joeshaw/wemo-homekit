package main

import (
	"context"
	"encoding/xml"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/brutella/hc"
	"github.com/brutella/hc/accessory"
	ssdp "github.com/koron/go-ssdp"
)

const (
	BridgeDeviceURN     = "urn:Belkin:device:bridge:1"
	ControlleeDeviceURN = "urn:Belkin:device:controllee:1"
	LightDeviceURN      = "urn:Belkin:device:light:1"
	SensorDeviceURN     = "urn:Belkin:device:sensor:1"
	NetCamDeviceURN     = "urn:Belkin:device:netcam:1"
	InsightDeviceURN    = "urn:Belkin:device:insight:1"

	BasicEventServiceURN = "urn:Belkin:service:basicevent:1"
	InsightServiceURN    = "urn:Belkin:service:insight:1"

	BasicEventControlPath = "/upnp/control/basicevent1"
	BasicEventEventPath   = "/upnp/event/basicevent1"
	InsightPath           = "/upnp/control/insight1"

	subscribeTimeout    = 90 * time.Minute
	resubscribeInterval = 85 * time.Minute
	discoveryInterval   = 30 * time.Second
)

// TODO: I only have Insight devices, so they're the only ones
// supported today.  It should be trivial to support at least lights
// and sensors though.
var supportedDeviceTypes = []string{InsightDeviceURN}

type Device struct {
	Type         string `xml:"device>deviceType"`
	Name         string `xml:"device>friendlyName"`
	Manufacturer string `xml:"device>manufacturer"`
	Model        string `xml:"device>modelName"`
	SerialNumber string `xml:"device>serialNumber"`
	UDN          string `xml:"device>UDN"`

	baseURL string
	sid     string

	accessory *accessory.Switch
	transport hc.Transport
}

func (d Device) String() string {
	return d.Name
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

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

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

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

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

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

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

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code %d", resp.StatusCode)
	}

	return nil
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
	resp, err := http.Get(u)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

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
	log.Println("Searching for Wemo devices")
	services, err := ssdp.Search(BasicEventServiceURN, 5, "")
	if err != nil {
		return err
	}

	for _, service := range services {
		d, err := getDevice(service.Location)
		if err != nil {
			log.Printf("Skipping %s: %s", service.Location, err)
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

		sw := accessory.NewSwitch(info)
		t, err := hc.NewIPTransport(hcConfig, sw.Accessory)
		if err != nil {
			return err
		}

		sw.Switch.On.OnValueRemoteUpdate(func(on bool) {
			if err := d.Set(on); err != nil {
				log.Printf("Error setting %s to %t: %s", d, on, err)
			}
		})

		d.accessory = sw
		d.transport = t

		devices.m[d.UDN] = d
		go t.Start()
		d.Subscribe()
		devices.Unlock()
	}

	return nil
}

func main() {
	// TODO: make configurable
	hcConfig := hc.Config{
		Pin:         "00102003",
		StoragePath: filepath.Join(os.Getenv("HOME"), ".homecontrol", "wemo"),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
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
	})

	go func() {
		panic(http.ListenAndServe(":1225", nil))
	}()

	// Discover devices right away, and then every 30s afterward
	go func(ctx context.Context) {
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
				for _, d := range devices.m {
					if err := d.Subscribe(); err != nil {
						log.Printf("error resubscribing %s: %s", d, err)
					}
				}
				devices.Unlock()

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
