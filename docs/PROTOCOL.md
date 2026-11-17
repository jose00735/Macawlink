# Macawlink Wire Protocol v1

One TCP connection (plain over adb/USB, TLS 1.3 over Wi-Fi) carries every channel.
All integers are **big-endian**. The Android side (`com.macawlink.protocol`) and the Go side
(`internal/protocol`) must stay byte-for-byte identical to this document.

## Packet header (16 bytes)

| Offset | Size | Field | Notes |
|---|---|---|---|
| 0 | 2 | `magic` | `0x4D4B` ("MK") |
| 2 | 1 | `version` | `1` |
| 3 | 1 | `type` | see table below |
| 4 | 1 | `flags` | bit0 `KEYFRAME`, bit1 `CONFIG`, bit2 `END_OF_FRAME` |
| 5 | 3 | reserved | zero |
| 8 | 4 | `seq` | per-type counter, for loss detection |
| 12 | 4 | `length` | payload length in bytes (max 4 MiB) |

`*_CONFIG` payloads are: UTF-8 JSON, one `0x00` byte, then the raw codec config bytes (may be empty).

Media packets add an 8-byte `pts_us` (capture time, microseconds, phone `CLOCK_MONOTONIC` — `System.nanoTime()/1000`, the same base MediaCodec uses for Surface input) at the start of the payload.

## Message types

| Type | Name | Direction | Payload |
|---|---|---|---|
| `0x01` | `HELLO` | phone → PC | JSON: device model, app version, capabilities (cameras, resolutions, fps, codecs, audio rates) |
| `0x02` | `HELLO_ACK` | PC → phone | JSON: PC name, accepted protocol version, pairing result |
| `0x10` | `VIDEO_CONFIG` | phone → PC | JSON `{codec, width, height, fps, rotation}` + codec config bytes (SPS/PPS/VPS, Annex-B) |
| `0x11` | `VIDEO_FRAME` | phone → PC | `pts_us` + Annex-B access unit |
| `0x20` | `AUDIO_CONFIG` | phone → PC | JSON `{codec, sample_rate, channels, frame_ms, bitrate}` + Opus header if any |
| `0x21` | `AUDIO_FRAME` | phone → PC | `pts_us` + one Opus packet or interleaved PCM s16le |
| `0x30` | `SET_CONFIG` | both | JSON partial `StreamSettings` (only changed keys) |
| `0x31` | `CONFIG_STATE` | phone → PC | JSON full current `StreamSettings` (after any change) |
| `0x32` | `REQUEST_KEYFRAME` | PC → phone | empty |
| `0x33` | `CAMERA_CONTROL` | PC → phone | JSON `{lens?, zoom?, torch?, focus?, exposure?}` |
| `0x40` | `PING` | both | 8-byte sender timestamp |
| `0x41` | `PONG` | both | echoed timestamp + 8-byte receiver timestamp |
| `0x42` | `STATS` | both | JSON: bitrate, fps, dropped, queue depth, battery, thermal |
| `0x7F` | `BYE` | both | JSON `{reason}` |

## StreamSettings (JSON)

```json
{
  "video": {
    "width": 1280, "height": 720, "fps": 30,
    "codec": "h264", "bitrate_kbps": 0, "rate_control": "cbr",
    "lens": "back", "mirror": false, "stabilization": true
  },
  "audio": {
    "enabled": true, "codec": "opus", "sample_rate": 48000,
    "channels": 1, "bitrate_kbps": 96, "frame_ms": 10,
    "aec": true, "ns": true, "agc": true
  }
}
```
`bitrate_kbps: 0` means *Auto*.

## Session flow

```
phone                                   PC
  │──── HELLO (capabilities) ─────────────▶│
  │◀──── HELLO_ACK ────────────────────────│
  │◀──── SET_CONFIG (PC's saved prefs) ────│   (optional)
  │──── CONFIG_STATE ─────────────────────▶│
  │──── VIDEO_CONFIG / AUDIO_CONFIG ──────▶│
  │──── VIDEO_FRAME / AUDIO_FRAME ... ────▶│
  │◀──── PING / PONG / STATS (1 Hz) ──────▶│
  │◀──── SET_CONFIG (user changed res) ────│
  │──── CONFIG_STATE + new VIDEO_CONFIG ──▶│   decoder re-init, virtual device stays
```

Ports: TCP **47800** (media + control). mDNS service: `_macawlink._tcp.local.`
