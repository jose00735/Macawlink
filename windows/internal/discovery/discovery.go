// Package discovery advertises the PC on the LAN (mDNS _macawlink._tcp) and watches adb for USB phones.
package discovery

// Pairing is what the QR code shown on the PC encodes (JSON).
type Pairing struct {
	Host            string `json:"host"`
	Port            int    `json:"port"`
	CertFingerprint string `json:"fp"`    // SHA-256 of the PC's TLS cert, pinned by the phone
	Token           string `json:"token"` // one-time, expires after 2 minutes
}

// Advertiser publishes the service via mDNS (github.com/grandcat/zeroconf).
type Advertiser interface {
	Start(instance string, port int) error
	Stop()
}

// USBWatcher polls `adb devices` and reports phones with USB debugging authorised.
type USBWatcher interface {
	Devices() <-chan []string
}
