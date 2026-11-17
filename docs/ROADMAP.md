# Macawlink Roadmap

## Phase 0: Blueprint ✅
- [x] Architecture, protocol, icon, scaffolds

## Phase 1: USB MVP ✅ (built, awaiting first run on real hardware)
- [x] Android: Camera2 → hardware H.264/HEVC, PCM mic with AEC/NS/AGC, loopback server, foreground service, Compose settings UI, presets, rotation, torch/zoom/stabilization
- [x] Windows DLL: MF virtual camera source (“Macawlink Camera”), MF H.264/HEVC decoder, WASAPI output
- [x] Go receiver: adb discovery + auto-launch, protocol session, decode → rotate → crop/scale → ring, PCM → gain → CABLE Input, live settings both ways, tray, settings page, installer
- [x] Tests: protocol golden fixtures shared by Go and Kotlin, end-to-end session test with a fake phone, scaler/rotation/ring layout tests, settings API tests
- [ ] **Hardware validation:** Teams/Zoom/OBS call for 30 min on a real phone + Windows 11 PC, target < 80 ms glass-to-app

## Phase 2: Wi-Fi and polish
- [ ] mDNS discovery, QR pairing, TLS 1.3 with pinning
- [ ] Opus audio (needed for Wi-Fi bandwidth)
- [ ] Adaptive bitrate from RTT and queue depth; thermal step-down
- [ ] A/V sync on the PC (audio-clocked presentation)
- [ ] Faster scaler (SIMD or the MF video processor) for 1080p60 → other sizes
- [ ] Phone preview while streaming

## Phase 3: Native microphone and extras
- [ ] SYSVAD-based “Macawlink Microphone” driver (EV cert + attestation signing)
- [ ] DirectShow fallback camera for Windows 10
- [ ] 1440p/4K, manual focus/exposure from the PC
- [ ] Several phones as several cameras
