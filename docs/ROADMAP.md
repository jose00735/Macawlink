# Macawlink Roadmap

## Phase 0: Blueprint ✅
- [x] Architecture, protocol, icon, scaffolds

## Phase 1: USB MVP ✅ (built, awaiting first run on real hardware)
- [x] Android: Camera2 → hardware H.264/HEVC, PCM mic with AEC/NS/AGC, loopback server, foreground service, Compose settings UI, presets, rotation, torch/zoom/stabilization
- [x] Windows DLL: MF virtual camera source (“Macawlink Camera”), MF H.264/HEVC decoder, WASAPI output
- [x] Go receiver: adb discovery + auto-launch, protocol session, decode → rotate → crop/scale → ring, PCM → gain → CABLE Input, live settings both ways, tray, settings page, installer
- [x] Windows 10: DirectShow camera filter (x64 + x86) reading a session-local ring
- [x] Tests: protocol golden fixtures shared by Go and Kotlin, end-to-end session test with a fake phone, scaler/rotation/ring layout tests, settings API tests
- [ ] **Hardware validation:** Teams/Zoom/OBS call for 30 min on a real phone + Windows 11 PC, target < 80 ms glass-to-app

## Phase 2: Wi-Fi and polish ✅ (built, awaiting hardware validation)
- [x] Wi-Fi: TLS 1.3, mDNS discovery (own responder), manual address, Bluetooth-style code pairing, certificate pinning, token reconnect, firewall rule
- [x] Adaptive bitrate from send-queue depth; thermal step-down
- [x] A/V sync on the PC (video delayed by queued audio)
- [x] Faster scaler: fixed-point taps + parallel bands (1080p→720p 8.8 ms → 2.5 ms on 2 cores)
- [x] Phone preview while streaming
- [ ] Opus audio for constrained networks (PCM is 0.77 Mbps, fine on normal Wi-Fi)

## Phase 3: Drivers and extras (needs an EV code-signing certificate)
- [ ] SYSVAD-based “Macawlink Microphone” driver (EV cert + attestation signing)
- [ ] AVStream camera driver for Windows 10 Store apps (Camera app); same signing path
- [ ] 1440p/4K, manual focus/exposure from the PC
- [ ] Several phones as several cameras
