
# wemo-homekit

HomeKit support for Wemo devices.

Currently this service only supports Wemo (outlet) switches and light
switches.  If it is a newer "insight" switch, power consumption is
reported with characteristics that are compatible with the [Elgato
Eve](https://itunes.apple.com/us/app/elgato-eve/id917695792?mt=8) app.

Once the device is paired with your iOS Home app, you can control it
with any service that integrates with HomeKit, including Siri ("Turn
off the Christmas tree") and Apple Watch.  If you have a home hub like
an Apple TV or iPad, you can control the device remotely.

## Installing

The tool can be installed with:

    go get -u github.com/joeshaw/wemo-homekit

Then you can run the service:

    wemo-homekit

The service will search for Wemo devices on your local network at
startup, and even every 30 seconds afterward.

To pair, open up your Home iOS app, click the + icon, choose "Add
Accessory" and then tap "Don't have a Code or Can't Scan?"  You should
see the Leaf under "Nearby Accessories."  Tap that and enter the PIN
00102003.  You should see one entry appear for each Wemo device on
your network.

## Contributing

This code is fairly hacky, with some hardcoded values like timeouts.
It also has limited device support.

Issues and pull requests are welcome.  When filing a PR, please make
sure the code has been run through `gofmt`.

## License

Copyright 2017 Joe Shaw

`wemo-homekit` is licensed under the MIT License.  See the LICENSE file
for details.


