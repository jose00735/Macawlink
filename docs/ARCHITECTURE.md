# Macawlink — Architecture

Macawlink turns an Android phone into a **webcam + microphone for a Windows PC**. To every
Windows app (Teams, Zoom, Meet, OBS, Discord, the Windows Camera app) it looks like a USB camera
and mic were plugged in:

- a camera named **“Macawlink Camera”** (Windows 11 virtual camera)
- a microphone: **“CABLE Output”** today (VB-Audio Virtual Cable), **“Macawlink Microphone”** with the Phase 3 driver

| Side | Language | Role |
|---|---|---|
| Android sender | Kotlin | Capture camera + mic, encode, stream over USB, apply settings |
| Windows receiver | Go (stdlib only) | Connect, decode, scale, feed the camera and mic, tray and settings page |
| `macawlink_native.dll` | C++ | Virtual camera media source, H.264/HEVC decoder, WASAPI output (what Go can't do) |

---

## 1. Big picture

```
┌──────────────────────────── ANDROID (Kotlin) ─────────────────────────────┐
│ Camera2 ──Surface──▶ MediaCodec H.264/HEVC ──▶ VIDEO_CONFIG / VIDEO_FRAME  │
│ AudioRecord (VOICE_COMMUNICATION + AEC/NS/AGC) ─▶ PCM ─▶ AUDIO_FRAME       │
│ OrientationEventListener ─▶ rotation in VIDEO_CONFIG                       │
│ SessionHub.settings ◀── UI ── SET_CONFIG ──▶ camera/audio restart ─▶ CONFIG_STATE │
│ StreamService (FGS camera|microphone) ── PcLink: 127.0.0.1:47800 ──┐       │
└────────────────────────────────────────────────────────────────────┼───────┘
                                                  USB cable, `adb forward`
┌──────────────────────────── WINDOWS (Go) ──────────────────────────┼───────┐
│ app.connLoop: find adb ▶ list devices ▶ forward ▶ dial ▶ HELLO ◀───┘       │
│   ├─ video.Pipeline: MF decoder (DLL) ▶ rotate ▶ crop/scale to app size ─┐  │
│   ├─ audio.Pipeline: PCM ▶ gain ▶ WASAPI (DLL) ▶ "CABLE Input" ──────────┼──┼─▶ apps record "CABLE Output"
│   ├─ ping/RTT, phone stats, live SET_CONFIG                              │  │
│ tray (Win32) + settings page http://127.0.0.1:47801                      │  │
└──────────────────────────────────────────────────────────────────────────┼──┘
                     shared-memory ring  Global\Macawlink.Video (NV12 × 3) ▼
┌──────── macawlink_native.dll inside Windows Frame Server ────────────────────┐
│ IMFActivate ▶ MkSource (IMFMediaSourceEx) ▶ MkStream (IMFMediaStream2)        │
│ paces samples at the negotiated fps; copies the newest frame, or a           │
│ “waiting for phone” card when no phone is streaming                          │
└────────────────────────────────▶ Teams / Zoom / OBS see “Macawlink Camera” ───┘
```

---

## 2. How “it looks like I plugged in a camera and mic”

### 2.1 Virtual camera (`windows/native`)
- **Windows 11 Media Foundation virtual camera** (`MFCreateVirtualCamera`). It is a user-mode COM DLL with no kernel driver and no driver signing, and it shows up in every app, including the Windows Camera app.
- The DLL is registered in **HKLM** once (installer, admin). Frame Server runs as LOCAL SERVICE and cannot see HKCU.
- `macawlink.exe` calls `mk_vcam_start` at launch (session lifetime, current user), so the camera exists while the tray app runs.
- Formats offered (NV12): 1280×720@30 (default), 1920×1080@30, 640×480@30, 1280×720@60, 1920×1080@60.
- **Frame hand-off:** the DLL (consumer) creates `Global\Macawlink.Video` with a DACL for the user, LOCAL SERVICE and app containers. If it can't create Global objects it falls back to `Local\`. Go (producer) opens it.
  - The 256-byte header holds the latest slot, the per-slot size and pts, the `connected` flag, and the format the app opened (`reqWidth/Height/Fps`).
  - Layout is pinned by `static_assert`s in `ring.h` and by `vcam_test.go`.
- Go scales to exactly what the app opened. The DLL does nearest-neighbour as a stop-gap during format switches.
- No phone → the DLL renders a violet card with a moving cyan scan line. Apps never lose the device mid-call.
- The DLL also implements `IMFSampleAllocatorControl` (uses the pipeline's allocator when given) and `IKsControl` stubs, as Frame Server expects.

### 2.1b Windows 10: DirectShow camera (`windows/native/dshow_filter.cpp`)
- Windows 10 has no virtual-camera API, so the same DLL also contains a **DirectShow video capture filter**. It is registered in the video input device category, which is how OBS Virtual Camera appears too.
- Apps load it into their own process: Chrome/Edge (and WebView2 apps like the new Teams), Zoom, Discord and OBS. It reads the **session-local** ring `Local\Macawlink.Video`, which `macawlink.exe` creates at startup.
- It is a single capture pin with `IAMStreamConfig` (9 formats: 720p/1080p/480p × YUY2/NV12/I420, 5–60 fps) and `IKsPropertySet` (pin category capture). Samples are pushed through the downstream allocator from a paced worker thread.
- **Limit:** Microsoft Store apps on Windows 10 (the Camera app, error `NoCamerasAreAttached`) enumerate only kernel-streaming devices. Reaching them needs an AVStream kernel driver (Phase 3, same signing requirement as the microphone driver).
- A 32-bit build (`macawlink_native32.dll`, registered with SysWOW64 `regsvr32`) covers 32-bit apps.
- `DllRegisterServer` adds the DirectShow device **only when the MF virtual camera is unavailable**, so Windows 11 never lists the camera twice.
- Go publishes each frame to every open ring at the size that ring's app asked for.

### 2.2 Virtual microphone (`windows/vmic-driver`)
- **Now:** VB-Audio Virtual Cable. Go renders PCM into *CABLE Input* via WASAPI shared mode with auto format conversion; apps record *CABLE Output*.
- **Phase 3:** a SYSVAD-based driver exposing **“Macawlink Microphone”**. It needs an EV certificate plus Microsoft attestation signing. `audio.Endpoints` already prefers it when present, so no code change is needed.

---

## 3. Android sender

```
com.macawlink
├── MacawlinkApp            loads saved settings into SessionHub
├── session/SessionHub      StateFlows: settings (single source of truth) + link state
├── service/StreamService   FGS camera|microphone, wake lock; accept loop; one PC session at a time
├── transport/PcLink        loopback ServerSocket; Connection = writer thread + bounded video queue
├── capture/CameraController Camera2 → encoder Surface; fps range, torch, zoom, stabilization; rotation
├── capture/AudioCapture    AudioRecord PCM s16le in 10/20 ms frames + AEC/NS/AGC effects
├── codec/VideoEncoder      MediaCodec async, realtime priority, no B-frames, live bitrate, keyframe on request
├── protocol/               Packet framing + JSON messages (golden-tested against Go)
├── settings/               StreamSettings (+ presets) and SharedPreferences store
└── ui/MainActivity         Compose: status, start/stop, presets, video and audio settings
```
- Camera and mic are opened **only while a PC is connected**.
- Settings changed in the UI or by `SET_CONFIG` both land in `SessionHub.settings`.
  - The service applies the diff: bitrate, torch, zoom and stabilization update live; size, fps, codec and lens restart the encoder.
  - It then answers with `CONFIG_STATE`.
- Congestion: once >3 MB is queued, video frames are dropped until the next keyframe, which is requested immediately. Audio is never dropped on the phone.
- If HEVC is unavailable on the device, it falls back to H.264.
- Size picking: an exact match first, then the nearest size with the same aspect ratio, then the nearest area. FPS: an exact `[n,n]` range first.
- The PC can launch the app with `am start … --ez autostart true` once per plug-in.

## 4. Windows receiver

```
windows/
├── cmd/macawlink      tray app; --install / --uninstall; --addr (direct TCP); --serial
├── cmd/fakephone      phone emulator for testing without a device
├── internal/app       connection loop, session, status, settings API
├── internal/video     pipeline + NV12 rotate / crop-scale (bilinear, mirror, parallel bands), A/V sync delay
├── internal/mdns      DNS-SD responder for _macawlink._tcp (stdlib only)
├── internal/pairing   TLS identity, paired-phone store
├── internal/audio     pipeline: PCM, gain, latency window (drop >120 ms, pad on underrun), level meter
├── internal/vcam      shared-memory ring writer
├── internal/native    syscall bindings to macawlink_native.dll (no cgo)
├── internal/transport adb discovery/forward/launch, dial + HELLO handshake
├── internal/web       settings page + JSON API (localhost-only, header-guarded POSTs)
├── internal/tray      Shell_NotifyIcon tray icon and menu (pure Win32)
├── internal/install   elevate, copy to Program Files, DllRegisterServer, Start menu shortcut
└── native/            the C++ DLL (cross-compiled with zig: `native/build.sh`)
```
- The Go module has **no third-party dependencies**. Windows APIs go through `syscall`. Media Foundation and WASAPI COM work lives in the DLL behind a small C API.
- Threads: the network reader feeds a video worker (bounded queue; overflow flushes and resyncs on a keyframe) and an audio worker (drop-oldest). There is also a 1 Hz ping/RTT and a ring watcher that requests a keyframe when an app opens the camera.

## 5. Tweaking quality

Changeable from the phone, the tray (presets, lens) or the settings page. All changes apply live.

| Video | Values |
|---|---|
| Resolution | 640×480, 1280×720, 1920×1080 |
| Frame rate | 15, 24, 30, 60 (device permitting) |
| Bitrate | Auto (~0.1 bpp) or 1.5–40 Mbps; CBR/VBR |
| Codec | H.264, HEVC |
| Camera | back / front, zoom 1–5×, torch, stabilization, mirror (PC side) |

| Audio | Values |
|---|---|
| Sample rate | 16, 24, 32, 44.1, 48 kHz |
| Channels | mono, stereo |
| Packet | 10 or 20 ms |
| Processing | echo cancellation, noise suppression, auto gain (phone); gain −20…+20 dB (PC) |

Presets: **Low** (480p30, 1.5 Mbps, 16 kHz), **Meeting** (720p30 auto, 48 kHz mono), **Max** (1080p60 20 Mbps VBR, 48 kHz stereo raw).

## 6. Connection
- **USB:** the phone listens on loopback `127.0.0.1:47800` only, so nothing is exposed on the network. The PC runs the bundled `adb forward`.
- **Wi-Fi:** the PC listens with **TLS 1.3** on `:47802` and advertises itself over **mDNS** (`internal/mdns`, our own responder on every IPv4 interface). The phone finds PCs with `NsdManager`, or the user types an address, and keeps reconnecting with backoff.
  - **Pairing:** numeric comparison with commitment (see PROTOCOL.md). Both screens show a 6-digit code bound to the certificate fingerprint, and both users confirm. Typed codes would need TLS key export, which Android only has from version 12.
  - **Identity:** the PC's ECDSA P-256 certificate lives in `%APPDATA%\Macawlink\tls.pem`, and paired phones (token hashes) in `paired.json`. The phone pins the fingerprint.
  - **Firewall:** the installer adds an inbound rule for `macawlink.exe` on private and domain networks.
- One phone session at a time, whichever transport connects first. The other side gets `BYE busy`.
- Audio stays PCM on Wi-Fi: 0.77 Mbps for 48 kHz mono is small next to video (3–20 Mbps). Opus is planned for constrained networks.

### Adaptive quality (phone)
- **Auto bitrate** runs every 0.5 s on the size of the send queue. Over 512 KB queued it drops the bitrate by 25%. After 4 s with a near-empty queue it raises it by 10%, up to the ~0.1 bpp ceiling. The ceiling is applied live with `PARAMETER_KEY_VIDEO_BITRATE`, with no encoder restart.
- **Thermal step-down:** the ceiling is capped at 75% when the phone is moderately hot (thermal status MODERATE) and at 50% from SEVERE. This applies to fixed bitrates too. The UI shows it.
- If the queue overflows, frames are skipped until the next requested keyframe.

### A/V sync (PC)
Video is held back by the audio currently queued in the virtual mic (0–200 ms) so lips match the voice. A pool of 12 frame buffers bounds memory, and if it runs out, frames are published immediately.

### Phone preview
Optional. The UI's `SurfaceView` becomes a second output of the Camera2 session, sized like the encoder. If the device can't run both at that size, the session is rebuilt without the preview.

## 7. Latency and sync
- Low-latency MediaCodec settings, no B-frames, a realtime-priority encoder, `CODECAPI_AVLowLatencyMode` on the decoder, and TCP_NODELAY on both ends.
- Video is shown as soon as it is decoded; the camera paces delivery at the negotiated fps.
- Audio keeps 10–120 ms queued in WASAPI. If the phone clock drifts, packets are dropped or padded.
- Timestamps are carried end-to-end for the Phase 2 A/V sync work.

## 8. Failure handling
| Event | Behaviour |
|---|---|
| Phone unplugged / app stopped | Camera shows the waiting card, mic goes silent, PC retries every ~2 s |
| USB-debugging not authorised | Tray says “Tap Allow USB debugging” |
| App not running on phone | PC launches it once per plug-in |
| Link congested | Phone drops to next keyframe; PC flushes and resyncs |
| HEVC decoder missing on PC | PC switches the phone to H.264 |
| No virtual mic installed | Settings page explains; retried every 5 s |

## 9. Security
- USB only: phone socket bound to loopback; PC settings page bound to 127.0.0.1.
- The settings page rejects foreign `Host` headers (DNS rebinding) and POSTs without `X-Macawlink: 1` (cross-site).
- The phone shows a persistent notification while streaming, and Android's camera/mic indicators apply.

## 10. Requirements
| | Minimum |
|---|---|
| Android | 10 (API 29) |
| Windows | 10 (DirectShow camera: desktop apps, not Microsoft Store apps such as Camera) or 11 (virtual camera: every app) |
| PC | any x64; hardware-accelerated decode used when the MF decoder provides it |
