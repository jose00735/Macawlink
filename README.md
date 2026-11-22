<p align="center"><img src="assets/icon/macawlink-192.png" width="128" alt="Macawlink icon"></p>

# Macawlink

Use your Android phone as a **webcam and microphone** for Windows. Plug it in by USB and every
app (Teams, Zoom, Meet, OBS, Discord, the Windows Camera app) sees **“Macawlink Camera”** like a
real USB webcam. The phone's microphone feeds a virtual mic.

- **Android app:** Kotlin, Camera2 feeding the hardware H.264/HEVC encoder (zero copy), PCM mic, foreground service
- **Windows receiver:** Go (standard library only) plus one native DLL that holds the Windows 11 virtual camera, the Media Foundation decoder and WASAPI output
- **USB or Wi-Fi, switching automatically** when you plug or unplug the cable; Wi-Fi uses TLS 1.3 with Bluetooth-style code pairing, mDNS discovery, adaptive bitrate, A/V sync
- **Tweakable live** from the phone or the PC: resolution (480p–1440p), 15–60 fps, bitrate (or auto), H.264/HEVC, lens, zoom, torch, stabilization, mirror, manual focus, exposure compensation, white balance, AE/AWB lock, sample rate (16–48 kHz), mono/stereo, echo/noise/gain processing, PC-side gain, plus presets

## Teleprompter on the phone (with TeleCyborg)

With [TeleCyborg](https://github.com/jose00735/telecyborg) 1.1+ on the same PC, press **P** in its
prompter: your script appears on the phone's screen beside the front lens while the phone streams
as your webcam, so you read next to the camera. Use the **front** lens. Tap the phone to pause,
swipe up/down to change paragraph, ✕ to go back to the settings. The text is shown on the phone
only — it is never part of the video your call receives.

## Setup (one time)

1. **PC: Windows 10 or 11.** Run **`MacawlinkSetup-<version>.exe`** and accept the administrator prompt.
   It installs to *Program Files*, adds *Macawlink Camera*, a Start menu shortcut and a firewall rule for Wi-Fi.
   It also offers VB-Audio Virtual Cable if you don't have it yet. Updates use the same file; uninstall from **Settings → Apps → Macawlink**.
   Unsigned builds show *“Windows protected your PC”* the first time: click **More info → Run anyway**.
   For unattended installs: `MacawlinkSetup.exe /S`.
   - **Windows 11** uses the built-in virtual camera, so it works in every app, including the Camera app.
   - **Windows 10** uses a DirectShow camera (the same approach as OBS Virtual Camera). It shows up in desktop apps: Chrome, Edge, Firefox, Zoom, Teams, Discord, OBS, Skype. Restart the browser once after setup.
     Microsoft Store apps such as the built-in **Camera** app (error `0xA00F4244 NoCamerasAreAttached`) only accept cameras that have a kernel driver. That needs a signed driver, and it is on the roadmap. On Windows 11 they work today.
2. **Virtual mic:** install the free [VB-Audio Virtual Cable](https://vb-audio.com/Cable/).
   Macawlink plays the phone mic into *CABLE Input*. In your call app choose **CABLE Output** as the microphone.
   An own *Macawlink Microphone* driver is on the roadmap.
3. **Phone: Android 10+.** Install `macawlink.apk`. Turn on *Developer options → USB debugging*.
4. Plug the phone in and tap **Allow** on the USB-debugging prompt. Macawlink opens on the phone by itself.
   Tap **Start streaming** the first time to grant camera and mic permission.
5. In Teams/Zoom/Meet/OBS pick **Macawlink Camera** and **CABLE Output**.

### USB ⇄ Wi-Fi switching
In the phone app, under **Connection → Link**:
- **Auto** (default): plug in the cable and the stream moves to USB, which has lower latency and charges the phone. Unplug it and the stream continues over Wi-Fi a moment later. The camera stays on in your call app the whole time; it only blinks for a second.
- **USB only** / **Wi-Fi only**: stick to one link. With *Wi-Fi only* the cable just charges.

### Wi-Fi (no cable)
On the phone open **Wi-Fi**, tap your PC in the list (or type its address), and tap **Start streaming**.
The first time, both screens show a 6-digit code: accept on both only if they match. After that the
phone reconnects by itself. Phone and PC must be on the same network. The installer allows Macawlink
through Windows Firewall on private networks; if your network is set to *Public*, switch it to *Private*.

Hold the phone in landscape; the picture is turned upright automatically. The tray icon (left
click) opens the settings page at `http://127.0.0.1:47801`.

## Build

```bash
# Windows receiver: cross-compiles from Linux/macOS too (needs Go 1.22+ and `pip install ziglang`)
cd windows && ./build.sh          # -> windows/dist/ (exe, DLLs, adb) + windows/MacawlinkSetup-<version>.exe

# Android app (JDK 17+, Android SDK)
cd android && ./gradlew assembleRelease   # -> app/build/outputs/apk/release/app-release.apk
```

Test the receiver without a phone: `fakephone.exe -h264 clip.h264`, then `macawlink.exe -addr 127.0.0.1:47800`.

## Docs
- [Architecture](docs/ARCHITECTURE.md)
- [Wire protocol](docs/PROTOCOL.md)
- [Roadmap](docs/ROADMAP.md)

## Layout
```
android/       Kotlin app
windows/       Go receiver (cmd/, internal/), native/ (C++ DLL), vmic-driver/ (Phase 3)
proto-golden/  wire-format fixtures checked by both the Go and the Kotlin tests
assets/icon/   orb-cam icon (SVG, PNG, ICO)
docs/
```

## Troubleshooting
| Symptom | Fix |
|---|---|
| Tray says *Tap “Allow USB debugging”* | Unlock the phone and accept the prompt (tick *Always allow*). |
| *Found phone — open Macawlink* | Open the app and tap **Start streaming**. |
| Wi-Fi: PC not in the phone's list | Same network? Firewall profile *Private*? Type the PC address shown on the settings page instead. |
| Wi-Fi: “PC certificate changed” | Macawlink was reinstalled on the PC (or something is impersonating it). Forget the PC on the phone and pair again. |
| Windows 10: Camera app says *NoCamerasAreAttached* | Expected on Windows 10, because Store apps need a camera driver. Use a desktop app or Windows 11. |
| Windows 10: camera missing in the browser | Close every browser window (also background ones in the tray) and reopen it after setup. |
| Camera shows a violet card with a moving cyan line | Macawlink Camera works but no phone is streaming yet. |
| *Virtual microphone not found* | Install VB-Audio Virtual Cable and restart Macawlink. |
| HEVC picture never appears | Install *HEVC Video Extensions* from the Microsoft Store, or use H.264 (Macawlink switches automatically). |
| Anything else | Tray → *Open log folder*. The camera DLL logs to `C:\ProgramData\Macawlink\native.log`. |
