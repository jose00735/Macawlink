# Macawlink — Architecture Blueprint

Macawlink turns an Android phone into a **webcam + microphone for a Windows PC**.
To every Windows app (Teams, Zoom, Meet in Chrome, OBS, Discord, the Windows Camera app)
it must look like someone just plugged in a USB camera and a USB mic:

- a camera device named **"Macawlink Camera"**
- a recording device named **"Macawlink Microphone"**

| Side | Language | Role |
|---|---|---|
| Android sender | Kotlin | Capture camera + mic, encode, stream, apply settings |
| Windows receiver | Go (+ two thin native shims) | Receive, decode, feed the virtual camera and virtual mic, tray UI |

---

## 1. Big picture

```
┌──────────────────────── ANDROID (Kotlin) ────────────────────────┐
│                                                                  │
│  CameraX/Camera2 ──Surface──▶ MediaCodec H.264/HEVC encoder ─┐   │
│                                                              │   │
│  AudioRecord (VOICE_COMMUNICATION, AEC/NS) ─▶ Opus or PCM ───┤   │
│                                                              ▼   │
│  Settings store ◀──── control msgs ────▶  Transport (mux/framing)│
│  (DataStore)                              USB (adb) | Wi-Fi TLS   │
└──────────────────────────────────────────────────┬───────────────┘
                                                   │ TCP, 1 connection,
                                                   │ multiplexed channels
┌──────────────────────── WINDOWS (Go) ────────────▼───────────────┐
│                                                                  │
│  Transport ──▶ Demux ──┬─▶ Video: jitter buf ▶ decoder ▶ scaler  │
│      ▲                 │        (FFmpeg/D3D11VA)  ▶ NV12 frames ─┼─▶ Frame ring (shared memory)
│      │                 │                                         │         │
│  Discovery (mDNS)      └─▶ Audio: jitter buf ▶ Opus dec ▶ resample┼─▶ WASAPI render ─▶ virtual mic endpoint
│  Pairing (QR/PIN)                                                │         │
│  Tray UI + settings ── control msgs ──▶ phone                    │         │
└──────────────────────────────────────────────────────────────────┘         │
        ┌───────────────────────────────────────────────────────────────────┘
        ▼                                              ▼
 vcam-native (C++ DLL, Media Foundation          vmic driver (WDM audio, SYSVAD-based)
 virtual camera, registered by the Go service)   exposes "Macawlink Microphone"
        ▼                                              ▼
 Zoom / Teams / OBS see "Macawlink Camera"      Apps see "Macawlink Microphone"
```

**Key decision:** the Go process does everything that is not a driver. Windows only
accepts cameras and microphones through COM media sources and kernel audio drivers, which
Go cannot produce, so those two pieces are small native shims with one job each:
read frames/samples that Go hands them and present them to Windows.

---

## 2. How "it looks like I plugged in a camera and mic"

### 2.1 Virtual camera — `windows/vcam-native` (C++)

| Option | Works in | Notes |
|---|---|---|
| **A. Media Foundation Virtual Camera** (`MFCreateVirtualCamera`) — **chosen** | Windows 11 (build 22000+) | Official API. Shows up in *every* app incl. the Windows Camera app and UWP. No driver signing needed — it is a user-mode COM DLL. Registered by the Go service at start, removed on exit (or kept with `MFVirtualCameraLifetime_System`). |
| B. DirectShow source filter (softcam-style) — **fallback** | Windows 10 + 11 | Seen by DirectShow apps (Zoom, OBS, Chrome) but **not** by MF-only apps (Windows Camera, some UWP). Use only for Win10 users. |
| C. AVStream kernel driver | All | Needs kernel driver + EV signing. Not worth it. |

The DLL implements `IMFMediaSource` + `IMFMediaStream`. It advertises a **fixed menu of
formats** (apps choose one when they open the camera):

| Resolution | FPS | Pixel formats |
|---|---|---|
| 640×480 | 30 | NV12, YUY2 |
| 1280×720 | 30, 60 | NV12, YUY2 |
| 1920×1080 | 30, 60 | NV12, YUY2 |
| 3840×2160 | 30 | NV12 |

When an app opens, say, 1280×720@30 but the phone is sending 1920×1080, the Go scaler
converts so the app always gets exactly what it asked for. When the phone is disconnected
the camera stays present and shows a branded "Waiting for phone…" frame (no app crashes,
no device disappearing mid-call).

**Frame hand-off Go → DLL:** a named shared-memory ring buffer
(`Local\Macawlink.Video`) with 3 slots + a header (`width, height, fourcc, stride,
timestamp, seq`) and a named event `Local\Macawlink.Video.NewFrame`. Go writes, the DLL
reads the newest slot. Zero sockets, zero copies beyond one memcpy. The Frame Server
runs the DLL in a different session, so the objects are created with a security
descriptor that allows `LOCAL SERVICE` read access (or under `Global\` from the service).

### 2.2 Virtual microphone — `windows/vmic-driver`

There is no user-mode API to create a recording device; it needs a WDM audio driver.

| Phase | Approach | Device name apps see |
|---|---|---|
| **Phase 1 (MVP)** | Use an existing virtual cable (VB-Audio Virtual Cable). Go plays the phone audio into **"CABLE Input"** via WASAPI; apps pick **"CABLE Output"** as mic. Installer detects it and links the user to download it (its licence does not allow bundling). | CABLE Output |
| **Phase 2 (target)** | Own driver forked from Microsoft's **SYSVAD** sample, stripped to one capture endpoint + one hidden render endpoint (loopback pair). Go writes into the hidden render side; the capture side appears as **"Macawlink Microphone"**. | Macawlink Microphone |

Phase 2 requires an **EV code-signing certificate** and Microsoft **attestation signing**
through the Partner Center (Windows 10/11 refuse unsigned kernel drivers). Budget for the
certificate yearly. Everything else in the system is independent of which phase is active:
the Go side only talks to "an output endpoint by ID".

---

## 3. Android sender (Kotlin)

### 3.1 Modules

```
android/app/src/main/java/com/macawlink/
├── MacawlinkApp.kt              Application, DI wiring
├── ui/                          Jetpack Compose: Home, Settings, Pairing (QR scan), Preview
├── service/StreamService.kt     Foreground service (type camera|microphone), owns the session
├── capture/
│   ├── CameraPipeline.kt        CameraX → encoder input Surface; lens switch, torch, zoom, focus
│   └── AudioPipeline.kt         AudioRecord + AEC/NS/AGC → encoder or raw PCM
├── codec/
│   ├── VideoEncoder.kt          MediaCodec async, H.264 (baseline/high) or HEVC, CBR/VBR
│   └── AudioEncoder.kt          MediaCodec Opus (API 29+) or PCM passthrough
├── transport/
│   ├── Transport.kt             interface: connect, send(frame), incoming control flow
│   ├── UsbTransport.kt          listens on localhost:port; PC reaches it via `adb forward`
│   └── WifiTransport.kt         TLS client to PC, found via mDNS (NsdManager)
├── protocol/                    Packet framing, message types (mirrors docs/PROTOCOL.md)
└── settings/StreamSettings.kt   Data class + DataStore persistence
```

### 3.2 Capture and encode

- **Video:** CameraX `Preview` + an encoder `Surface` (via `Preview.SurfaceProvider` or a
  Camera2 session if manual exposure/focus is needed). MediaCodec in **async mode**,
  `KEY_LATENCY=0`, `KEY_PRIORITY=0` (realtime), I-frame interval 2 s, `PARAMETER_KEY_REQUEST_SYNC_FRAME`
  when the PC asks (new app opened the camera, packet loss).
- **Audio:** `AudioRecord` with `VOICE_COMMUNICATION` source (gets hardware echo
  cancellation / noise suppression on most phones), 10 ms or 20 ms frames.
- **Orientation:** sensor rotation is sent as metadata; the PC rotates, so the encoder
  never restarts when the phone is turned.
- **Lifecycle:** foreground service with `foregroundServiceType="camera|microphone"`
  (mandatory on Android 14+), persistent notification with Stop / Switch lens / Mute.
  Screen can be turned off while streaming (keeps camera via service + wake lock).

### 3.3 Permissions
`CAMERA`, `RECORD_AUDIO`, `FOREGROUND_SERVICE`, `FOREGROUND_SERVICE_CAMERA`,
`FOREGROUND_SERVICE_MICROPHONE`, `POST_NOTIFICATIONS`, `INTERNET`,
`ACCESS_NETWORK_STATE`, `CHANGE_WIFI_MULTICAST_STATE` (mDNS), `WAKE_LOCK`.

Min SDK 29 (Android 10: Opus encoder, modern camera APIs). Target latest.

---

## 4. Windows receiver (Go)

### 4.1 Packages

```
windows/
├── cmd/macawlink/main.go        Entry: tray app + service loop
├── internal/
│   ├── config/                  Settings (JSON in %APPDATA%\Macawlink), validation, presets
│   ├── discovery/               mDNS advertiser (_macawlink._tcp), adb device watcher
│   ├── transport/               TCP/TLS listener, USB via `adb forward`, reconnect logic
│   ├── protocol/                Framing + message types (mirrors docs/PROTOCOL.md)
│   ├── session/                 One phone session: demux, stats, control loop
│   ├── video/                   Jitter buffer, decoder (FFmpeg via go-astiav, D3D11VA), scaler, rotate
│   ├── audio/                   Jitter buffer, Opus decode, resampler, WASAPI render
│   ├── vcam/                    Shared-memory frame ring + MFCreateVirtualCamera registration
│   ├── vmic/                    Picks the target endpoint (Macawlink driver or CABLE Input)
│   └── ui/                      Tray icon + settings window
├── vcam-native/                 C++ MF media source DLL (see 2.1)
└── vmic-driver/                 Phase 2 SYSVAD-based driver (see 2.2)
```

### 4.2 Library choices

| Need | Choice | Why |
|---|---|---|
| H.264/HEVC decode | **go-astiav** (FFmpeg bindings) with D3D11VA hw decode | Mature, hardware decode on Intel/AMD/NVIDIA, software fallback |
| Opus decode | `gopkg.in/hraban/opus.v2` (libopus) | Standard |
| WASAPI | `github.com/moutend/go-wca` | Pure-Go COM bindings for audio endpoints |
| COM / MF calls | `github.com/go-ole/go-ole` + `golang.org/x/sys/windows` | Register the virtual camera, shared memory, events |
| mDNS | `github.com/grandcat/zeroconf` | Advertise `_macawlink._tcp` |
| Tray + window | `fyne.io/systray` + Fyne (or Wails if a web UI is preferred) | Single binary |
| Build | cgo with MinGW-w64 / MSYS2; installer via WiX or Inno Setup | Needs to register DLL + (Phase 2) driver |

### 4.3 Threading model

One goroutine per concern, connected by bounded channels (drop-oldest for video, never
block the network reader):

```
netReader ─▶ demux ─┬─▶ videoJitter ─▶ decode ─▶ scale ─▶ vcamWriter (shared mem)
                    ├─▶ audioJitter ─▶ opusDec ─▶ resample ─▶ wasapiWriter
                    └─▶ controlHandler ◀──▶ ui
```

---

## 5. Tweaking audio and video quality

Settings can be changed **from either the phone or the PC tray**; both send the same
`SET_CONFIG` message and the phone is the source of truth (it is the one encoding).
Changes apply live — the phone reconfigures its encoder, sends a new `VIDEO_CONFIG`
(SPS/PPS), and the PC re-initialises its decoder without dropping the virtual device.

### Video
| Setting | Values | Default |
|---|---|---|
| Resolution | 640×480, 1280×720, 1920×1080, 2560×1440, 3840×2160 (filtered by phone capability) | 1280×720 |
| Frame rate | 15, 24, 30, 60 | 30 |
| Codec | H.264, HEVC | H.264 |
| Bitrate | 1–40 Mbps, or *Auto* (adapts to RTT / loss) | Auto |
| Rate control | CBR, VBR | CBR |
| Lens | Back main / wide / front | Back |
| Extras | Mirror, rotation lock, zoom, torch, focus lock, exposure compensation, stabilisation | — |

### Audio
| Setting | Values | Default |
|---|---|---|
| Sample rate | 16, 24, 32, 44.1, 48 kHz | 48 kHz |
| Channels | Mono, Stereo (if phone has 2 mics) | Mono |
| Codec | Opus, PCM 16-bit (USB only; ~1.5 Mbps stereo 48 k) | Opus |
| Opus bitrate | 16–256 kbps | 96 kbps |
| Frame size | 10, 20 ms | 10 ms |
| Processing | Echo cancel, noise suppress, auto gain (each toggle) | On |
| PC gain | −20 to +20 dB | 0 |

**Presets** for quick switching: *Low bandwidth* (480p30, 1.5 Mbps, Opus 32 k),
*Meeting* (720p30, Auto, Opus 96 k), *Max quality* (1080p60 HEVC 20 Mbps, PCM over USB).

---

## 6. Connection modes

| Mode | How | Latency target |
|---|---|---|
| **USB** | PC runs `adb forward tcp:47800 tcp:47800` (bundled adb, USB debugging on) and connects to the phone | < 60 ms glass-to-app |
| **Wi-Fi** | PC advertises `_macawlink._tcp` via mDNS; phone connects with TLS 1.3 | < 120 ms on 5 GHz |
| Wi-Fi manual | User types PC IP if mDNS is blocked | same |

**Pairing (Wi-Fi):** the PC shows a QR code holding `{host, port, cert fingerprint,
one-time token}`. The phone scans it, connects, pins the PC certificate, and both store a
long-lived pairing key. Unpaired devices are rejected. USB is trusted by adb
authorisation.

**Why one TCP connection instead of UDP/WebRTC:** simpler, works through adb, and on a LAN
the latency cost is small if Nagle is off and queues are drop-oldest. QUIC (datagrams for
media, stream for control) is the Phase 3 upgrade path for lossy Wi-Fi.

---

## 7. A/V sync and latency

- Phone stamps every video and audio packet with a capture timestamp from one monotonic
  clock (`CLOCK_MONOTONIC`, i.e. `System.nanoTime()` — the base MediaCodec uses for Surface timestamps).
- PC estimates the phone→PC clock offset with periodic `PING/PONG`.
- **Audio is the master clock.** Video frames are presented when their timestamp matches the
  audio playout position (±1 frame); late frames are dropped, never queued.
- Jitter buffers: audio 20–60 ms adaptive, video 0–2 frames.
- If an app opens the camera mid-stream, PC requests a keyframe so the first image appears instantly.

---

## 8. Failure handling

| Event | Behaviour |
|---|---|
| Phone disconnects / locks | Virtual camera shows "Waiting for phone…" frame; mic outputs silence. Devices never disappear. Auto-reconnect with backoff. |
| Wi-Fi congestion | Auto bitrate steps down (RTT + send-queue depth), then fps, then resolution |
| Phone overheating | Android thermal API → notify PC, step quality down |
| Incoming call on phone | Audio focus lost → mute + banner on PC tray |
| Second phone | Rejected in v1 (one session); multi-camera is roadmap |

---

## 9. Security
- Wi-Fi traffic is TLS 1.3 with certificate pinning from pairing; no cloud relay, LAN only.
- The service listens only on the chosen interface; firewall rule scoped to private networks.
- The phone shows a persistent notification and the camera indicator dot whenever it streams.

---

## 10. Repository layout

```
Macawlink/
├── README.md
├── assets/icon/            macawlink.svg (+ PNG/ICO renders), Android adaptive icon source
├── docs/
│   ├── ARCHITECTURE.md     this file
│   ├── PROTOCOL.md         wire format
│   └── ROADMAP.md          phases and milestones
├── android/                Kotlin app (Gradle)
└── windows/                Go module + vcam-native (C++) + vmic-driver
```

## 11. Platform requirements

| | Minimum |
|---|---|
| Android | 10 (API 29) |
| Windows | 11 (22000+) for the full virtual camera; Windows 10 via DirectShow fallback |
| PC GPU | Any D3D11 GPU for hardware decode; CPU decode works up to 1080p30 |
