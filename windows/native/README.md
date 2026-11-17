# macawlink_native.dll

One x64 DLL with everything Windows only exposes through COM:

| Part | File | What it does |
|---|---|---|
| Virtual camera | `vcam_source.*`, `exports.cpp` | `IMFActivate` → `MkSource` (`IMFMediaSourceEx`, `IMFGetService`, `IKsControl`, `IMFSampleAllocatorControl`) → `MkStream` (`IMFMediaStream2`). Frame Server loads it for “Macawlink Camera”. It paces samples at the negotiated fps and copies frames from the shared ring (`ring.h`), or draws the waiting card. |
| Camera lifetime | `exports.cpp` | `mk_vcam_supported/start/stop` wrap `MFCreateVirtualCamera` (loaded dynamically from `mfsensorgroup.dll`). `DllRegisterServer` writes HKLM. |
| Decoder | `decoder.cpp` | `mk_dec_*`: Microsoft H.264/HEVC MFT in low-latency mode, output as tight NV12 |
| Audio out | `audio.cpp` | `mk_audio_*`: WASAPI shared-mode render with auto PCM conversion, endpoint chosen by name |

Interfaces missing from mingw headers are declared in `common.h`. Their GUIDs were checked against the Windows SDK metadata (windows-rs).

## Build
```bash
pip install ziglang
./build.sh ../dist        # cross-compiles from Linux/macOS/Windows → macawlink_native.dll
```
Dependencies are only system DLLs (ole32, mfplat, advapi32, user32, UCRT).

## Debugging
- Logs: `C:\ProgramData\Macawlink\native.log` (Frame Server process and the tray app) and `OutputDebugString` (DebugView).
- Frame Server runs the source as LOCAL SERVICE; to iterate on the DLL, restart the *Windows Camera Frame Server* service after re-registering.
