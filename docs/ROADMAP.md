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

## Phase 3: Easy switching and pro controls ✅ (v0.3.0, awaiting hardware validation)
- [x] Link mode on the phone: **Auto** (cable → USB, unplug → Wi-Fi, automatic hand-over), USB only, Wi-Fi only
- [x] PC upgrades a running Wi-Fi session to USB when the cable is plugged in
- [x] Manual focus, exposure compensation, white balance presets, AE/AWB lock (phone + PC, live)
- [x] 1440p (2560×1440) end to end: phone encoder, frame ring, Windows 11 and Windows 10 cameras
- [ ] Several phones as several cameras (moved to Phase 5)

## TeleCyborg integration ✅ (v0.4.0)
- [x] Teleprompter feed: `PROMPTER_SCRIPT/STATE/CLEAR/KEY`, `features` in HELLO, local API `/api/prompter/*`
- [x] Phone: full-screen teleprompter beside the front lens, tap/swipe control, optional preview behind it
- [x] Forward compatibility: the phone ignores unknown message types
- [x] Tests: feed only to capable phones, re-send on reconnect, taps back; Kotlin fixtures; rendered snapshot; `cmd/devreceiver`

## Phase 4: Drivers (blocked: needs an EV code-signing certificate + Microsoft attestation signing)
Windows refuses to load unsigned kernel drivers, so these can't ship until a certificate is bought (~US$300/yr).
- [ ] SYSVAD-based “Macawlink Microphone” driver (replaces VB-Audio Virtual Cable)
- [ ] AVStream camera driver so Windows 10 Store apps (Camera, WhatsApp desktop) see Macawlink

## Phase 5: Extras
- [ ] Several phones as several cameras
- [ ] 4K
- [ ] Opus audio for constrained networks
