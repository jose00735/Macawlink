package transport

import "testing"

func TestParseDevices(t *testing.T) {
	out := []byte("* daemon started successfully\nList of devices attached\n" +
		"R5CT1234ABC            device usb:1-1 product:o1sxeea model:SM_G991B device:o1s transport_id:1\n" +
		"emulator-5554 unauthorized\n\n")
	ds := ParseDevices(out)
	if len(ds) != 2 || ds[0].Serial != "R5CT1234ABC" || ds[0].State != "device" || ds[0].Model != "SM G991B" || ds[1].State != "unauthorized" {
		t.Fatalf("%+v", ds)
	}
}
