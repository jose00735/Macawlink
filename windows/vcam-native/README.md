# vcam-native — "Macawlink Camera"

User-mode COM DLL (C++/WRL) implementing a Media Foundation **custom media source** for the
Windows 11 virtual camera API. No kernel driver, no driver signing.

## Responsibilities
1. `IMFMediaSource` + `IMFMediaStream` (+ `IMFGetService`, `IKsControl` stubs as the Frame Server expects).
2. Advertise the format menu from `docs/ARCHITECTURE.md §2.1` (NV12 and YUY2).
3. On each `RequestSample`, copy the newest slot from the shared ring
   (`include/macawlink_ring.h`) into an `IMFSample`; if `connected == 0`, emit the
   built-in "Waiting for phone…" frame (Macawlink orb-cam icon on deep violet with a neon grid).
4. Convert NV12 → YUY2 when the app picks YUY2.

## Registration
The Go service calls `MFCreateVirtualCamera(MFVirtualCameraType_SoftwareCameraSource,
MFVirtualCameraLifetime_Session, MFVirtualCameraAccess_CurrentUser, L"Macawlink Camera",
L"{6E0D1A3B-6F2C-4C1E-9B7A-4D61434157B1}", ...)` then `IMFVirtualCamera::Start`.
The installer runs `regsvr32 macawlink_vcam.dll` once.

## References
- Microsoft *VirtualCamera* sample (Windows-Camera repo, `Samples/VirtualCamera`)
- Windows 10 fallback: a DirectShow source filter (softcam-style) reading the same ring.

## Build
Visual Studio 2022, C++20, Windows SDK 10.0.22000+. Output: `macawlink_vcam.dll` (x64).
