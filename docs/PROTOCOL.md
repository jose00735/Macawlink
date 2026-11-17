# Macawlink Wire Protocol v1

One TCP connection carries everything. All integers are **big-endian**.
- **USB:** plain TCP through `adb forward`; the **phone listens** on `127.0.0.1:47800` and the PC dials.
- **Wi-Fi:** TLS 1.3; the **PC listens** on `:47802` and the phone dials. The PC advertises `_macawlink._tcp.local` over mDNS with TXT `v=1`, `name=<pc>` and `fp=<first 4 bytes of the cert SHA-256, hex>`.

The phone always sends `HELLO` first and **waits for `HELLO_ACK`** before sending anything else. Over Wi-Fi the PC may run pairing in between.
`proto-golden/` holds fixtures that both the Go and the Kotlin tests check byte for byte.

## Packet header (16 bytes)

| Offset | Size | Field | Notes |
|---|---|---|---|
| 0 | 2 | `magic` | `0x4D4B` ("MK") |
| 2 | 1 | `version` | `1` |
| 3 | 1 | `type` | see below |
| 4 | 1 | `flags` | bit0 `KEYFRAME`, bit1 `CONFIG`, bit2 `END_OF_FRAME` |
| 5 | 3 | reserved | zero |
| 8 | 4 | `seq` | per-connection counter |
| 12 | 4 | `length` | payload bytes (max 4 MiB) |

- **Media payloads** (`*_FRAME`) start with an 8-byte `pts_us` (capture time in µs), followed by the data.
- **`*_CONFIG` payloads** are UTF-8 JSON, one `0x00` byte, then the raw codec bytes (may be empty).

## Messages

| Type | Name | Dir | Payload |
|---|---|---|---|
| `0x01` | `HELLO` | phone→PC | `{model, manufacturer, app_version, protocol, cameras:[{id, facing, sizes:[[w,h]…], fps:[…]}]}`. Sent immediately on accept. |
| `0x02` | `HELLO_ACK` | PC→phone | `{pc_name, protocol}` |
| `0x10` | `VIDEO_CONFIG` | phone→PC | `{codec:"h264"\|"hevc", width, height, fps, rotation}` + `0x00` + SPS/PPS(/VPS) Annex-B. Re-sent with identical codec bytes when only `rotation` (0/90/180/270, clockwise to upright) changes. The PC keeps decoding without a resync. |
| `0x11` | `VIDEO_FRAME` | phone→PC | `pts_us` + one Annex-B access unit; `KEYFRAME` flag on IDR. The PC prepends SPS/PPS to keyframes. |
| `0x20` | `AUDIO_CONFIG` | phone→PC | `{codec:"pcm", sample_rate, channels, frame_ms}` + `0x00` |
| `0x21` | `AUDIO_FRAME` | phone→PC | `pts_us` + interleaved PCM s16 **little-endian** |
| `0x30` | `SET_CONFIG` | PC→phone | **full** StreamSettings JSON (never partial) |
| `0x31` | `CONFIG_STATE` | phone→PC | full StreamSettings after any change (from either side). The PC adopts it but keeps its own `mirror` and `gain_db`. |
| `0x32` | `REQUEST_KEYFRAME` | PC→phone | empty. Sent when an app opens the camera, after loss, or by “Refresh picture”. |
| `0x40` | `PING` | both | 8-byte sender time (ns) |
| `0x41` | `PONG` | both | 8-byte echoed time + 8-byte receiver time; the sender computes RTT |
| `0x42` | `STATS` | phone→PC | `{battery, charging, temp_c, fps, bitrate_kbps, thermal}` every 2 s |
| `0x50` | `PAIR_BEGIN` | PC→phone | `{commit: hex SHA-256(pc_nonce), pc_name}` |
| `0x51` | `PAIR_NONCE` | phone→PC | `{nonce}` (32 random bytes, hex) |
| `0x52` | `PAIR_REVEAL` | PC→phone | `{nonce}` = pc_nonce; the phone checks it against `commit` |
| `0x53` | `PAIR_CONFIRM` | phone→PC | `{ok}` after the phone user compared the code |
| `0x54` | `PAIR_RESULT` | PC→phone | `{ok, token?, reason?}`. `ok` only if both users accepted. |
| `0x7F` | `BYE` | both | `{reason}` (e.g. `busy`) |

`HELLO` over Wi-Fi also carries `phone_id` (random, stable) and `token` (from an earlier pairing).

### Wi-Fi pairing (numeric comparison, like Bluetooth)
1. Without a valid `phone_id` + `token`, the PC answers `HELLO` with `PAIR_BEGIN`, which commits to its nonce.
2. The phone sends its nonce, and the PC reveals its own.
3. Both show `SAS = SHA-256("macawlink-pair-v1" ‖ certFP ‖ pc_nonce ‖ phone_nonce)[0:4] as uint32 mod 10⁶`, as 6 digits. `certFP` is the SHA-256 of the PC's TLS certificate as each side sees it, so a man in the middle makes the codes differ. The commitment stops anyone grinding nonces to force a match.
4. Both users confirm. The PC stores `SHA-256(token)`, and the phone stores the token and **pins** `certFP`.

`0x33 CAMERA_CONTROL` is reserved. Torch, zoom and lens currently travel inside `SET_CONFIG`.

## StreamSettings

```json
{
  "video": {"width":1280,"height":720,"fps":30,"codec":"h264","bitrate_kbps":0,"rate_control":"cbr",
            "lens":"back","mirror":false,"stabilization":true,"torch":false,"zoom":1.0},
  "audio": {"enabled":true,"codec":"pcm","sample_rate":48000,"channels":1,"frame_ms":10,
            "aec":true,"ns":true,"agc":true,"gain_db":0}
}
```
`bitrate_kbps: 0` = auto. `mirror` and `gain_db` are applied on the PC.

## Session

```
PC                                       phone
 │── TCP connect (adb forward) ─────────▶│   (Wi-Fi: phone dials PC over TLS)
 │◀──────────────────────────── HELLO ───│
 │   [Wi-Fi, unpaired: PAIR_BEGIN … PAIR_RESULT]
 │── HELLO_ACK ─────────────────────────▶│
 │◀──────────────────────── CONFIG_STATE ┤
 │◀──── AUDIO_CONFIG, VIDEO_CONFIG, frames…
 │── PING ───▶ ◀─── PONG     ◀── STATS (2 s)
 │── SET_CONFIG (user changed 1080p60) ─▶│  encoder restarts
 │◀──── CONFIG_STATE, VIDEO_CONFIG, frames…
 │── BYE / socket close ────────────────▶│  camera + mic released, phone waits for next PC
```
