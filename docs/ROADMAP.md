# Macawlink Roadmap

## Phase 0 — Skeleton (this commit)
- [x] Architecture, protocol, icon
- [x] Android and Go project scaffolds with the module layout

## Phase 1 — MVP over USB
- [ ] Android: CameraX → H.264 encoder → USB transport; AudioRecord → Opus
- [ ] Go: adb forward, protocol, FFmpeg decode, Opus decode
- [ ] `vcam-native`: MF virtual camera DLL reading the shared-memory ring ("Macawlink Camera")
- [ ] Audio into VB-Audio Virtual Cable ("CABLE Output" as mic)
- [ ] Tray: connect/disconnect, resolution, fps, bitrate, audio sample rate/codec
- **Exit test:** a Teams/Zoom call using the phone as camera + mic for 30 min, < 80 ms latency

## Phase 2 — Wi-Fi and polish
- [ ] mDNS discovery, QR pairing, TLS 1.3 with pinning
- [ ] Auto bitrate, keyframe-on-demand, A/V sync tuning
- [ ] Live settings changes from phone and PC; presets
- [ ] Waiting-for-phone frame, reconnect, thermal/battery handling
- [ ] Installer (registers DLL, adds firewall rule, bundles adb)

## Phase 3 — Native microphone + extras
- [ ] SYSVAD-based "Macawlink Microphone" driver, EV cert + attestation signing
- [ ] DirectShow fallback camera for Windows 10
- [ ] HEVC, 4K, manual focus/exposure from the PC
- [ ] QUIC transport for lossy Wi-Fi
- [ ] Multiple phones as multiple cameras
