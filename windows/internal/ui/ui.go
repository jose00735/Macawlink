// Package ui is the tray icon (futuristic cyberpunk orb-cam, assets/icon/macawlink.ico) and settings window.
//
// Tray menu:
//
//	● Status: Connected — Pixel 8 over USB · 1280×720@30 · 4.2 Mbps · 48 kHz Opus
//	Connect via USB / Wi-Fi…   (Wi-Fi shows the pairing QR)
//	Preset ▸ Low bandwidth | Meeting | Max quality
//	Settings…                  (resolution, fps, codec, bitrate, sample rate, channels,
//	                            Opus bitrate, AEC/NS/AGC, gain, lens, mirror)
//	Preview
//	Quit
package ui

import "github.com/jose00735/macawlink/windows/internal/config"

// Controller is implemented by main; the UI calls it, never the session directly.
type Controller interface {
	Settings() config.Settings
	Apply(config.Settings) error // validates, saves, and pushes SET_CONFIG to the phone
	ConnectUSB() error
	ShowPairingQR() (pngBytes []byte, err error)
	Quit()
}
