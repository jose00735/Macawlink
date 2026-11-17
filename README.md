<p align="center"><img src="assets/icon/macawlink-192.png" width="128" alt="Macawlink icon"></p>

# Macawlink

Use your Android phone as a **webcam and microphone** for Windows. Plug it in (or pair over
Wi-Fi) and every app — Teams, Zoom, Meet, OBS, Discord — sees **"Macawlink Camera"** and
**"Macawlink Microphone"**, just like a USB webcam.

- **Android app** — Kotlin, CameraX + MediaCodec (H.264/HEVC, Opus)
- **Windows receiver** — Go, with a small Media Foundation virtual-camera DLL and a virtual audio endpoint
- **Tweakable** resolution (480p → 4K), frame rate, bitrate, codec, audio sample rate, channels, Opus bitrate, echo/noise processing — live, from the phone or the PC tray

## Docs
- [Architecture blueprint](docs/ARCHITECTURE.md)
- [Wire protocol](docs/PROTOCOL.md)
- [Roadmap](docs/ROADMAP.md)

## Layout
```
android/     Kotlin app (Gradle)
windows/     Go receiver  ·  vcam-native/ (C++ MF virtual camera)  ·  vmic-driver/
assets/icon/ Old-fashioned webcam icon (SVG, PNG, ICO)
docs/
```

## Build
```bash
# Windows receiver
cd windows && go test ./... && GOOS=windows go build ./cmd/macawlink

# Android
cd android && ./gradlew assembleDebug   # open in Android Studio to generate the Gradle wrapper
```

Requirements: Android 10+, Windows 11 (Windows 10 via DirectShow fallback, planned).
