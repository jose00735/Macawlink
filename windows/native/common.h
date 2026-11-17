#pragma once
#ifndef WIN32_LEAN_AND_MEAN
#define WIN32_LEAN_AND_MEAN
#endif
#include <windows.h>
#include <objbase.h>
#include <mfapi.h>
#include <mfidl.h>
#include <mferror.h>
#include <mftransform.h>
#include <ks.h>
#include <ksmedia.h>
#include <stdint.h>
#include <string.h>

// ---------------------------------------------------------------------------
// Declarations missing from the mingw headers (values verified against the
// official Windows SDK metadata via the windows-rs bindings).
// ---------------------------------------------------------------------------
extern const GUID MK_IID_IMFMediaStream2;            // c5bc37d6-75c7-46a1-a132-81b5f723c20f
extern const GUID MK_IID_IMFSampleAllocatorControl;  // da62b958-3a38-4a97-bd27-149c640c0771
extern const GUID MK_IID_IMFVirtualCamera;           // 1c08a864-ef6c-4c75-af59-5f2d68da9563
extern const GUID MK_IID_IKsControl;                 // 28f54685-06fd-11d2-b27a-00a0c9223196
extern const GUID MK_MF_DEVICESTREAM_STREAM_CATEGORY;
extern const GUID MK_MF_DEVICESTREAM_STREAM_ID;
extern const GUID MK_MF_DEVICESTREAM_FRAMESERVER_SHARED;
extern const GUID MK_MF_DEVICESTREAM_ATTRIBUTE_FRAMESOURCE_TYPES;
extern const GUID MK_PINNAME_VIDEO_CAPTURE;
extern const GUID MK_CLSID_MSH264DecoderMFT;
extern const GUID MK_CLSID_MSH265DecoderMFT;
extern const GUID MK_CLSID_MediaSource;              // our COM class (IMFActivate)

#define MK_CLSID_STRING L"{6E0D1A3B-6F2C-4C1E-9B7A-4D61434157B1}"

enum MkStreamState { MK_STREAM_STATE_STOPPED = 0, MK_STREAM_STATE_PAUSED = 1, MK_STREAM_STATE_RUNNING = 2 };
enum MkAllocatorUsage { MK_USES_PROVIDED_ALLOCATOR = 0, MK_USES_CUSTOM_ALLOCATOR = 1, MK_DOES_NOT_ALLOCATE = 2 };

struct IMFMediaStream2Mk : public IMFMediaStream {
    virtual HRESULT STDMETHODCALLTYPE SetStreamState(int value) = 0;
    virtual HRESULT STDMETHODCALLTYPE GetStreamState(int* value) = 0;
};

struct IMFSampleAllocatorControlMk : public IUnknown {
    virtual HRESULT STDMETHODCALLTYPE SetDefaultAllocator(DWORD outputStreamId, IUnknown* allocator) = 0;
    virtual HRESULT STDMETHODCALLTYPE GetAllocatorUsage(DWORD outputStreamId, DWORD* inputStreamId, int* usage) = 0;
};

struct IKsControlMk : public IUnknown {
    virtual HRESULT STDMETHODCALLTYPE KsProperty(void* prop, ULONG propLen, void* data, ULONG dataLen, ULONG* returned) = 0;
    virtual HRESULT STDMETHODCALLTYPE KsMethod(void* method, ULONG methodLen, void* data, ULONG dataLen, ULONG* returned) = 0;
    virtual HRESULT STDMETHODCALLTYPE KsEvent(void* ev, ULONG evLen, void* data, ULONG dataLen, ULONG* returned) = 0;
};

// IMFVirtualCamera: IMFAttributes + 11 methods (vtable order from the SDK).
struct IMFVirtualCameraMk : public IMFAttributes {
    virtual HRESULT STDMETHODCALLTYPE AddDeviceSourceInfo(LPCWSTR) = 0;
    virtual HRESULT STDMETHODCALLTYPE AddProperty(const void* key, ULONG type, const BYTE* data, ULONG len) = 0;
    virtual HRESULT STDMETHODCALLTYPE AddRegistryEntry(LPCWSTR name, LPCWSTR subkey, DWORD regType, const BYTE* data, ULONG len) = 0;
    virtual HRESULT STDMETHODCALLTYPE Start(IMFAsyncCallback* cb) = 0;
    virtual HRESULT STDMETHODCALLTYPE Stop() = 0;
    virtual HRESULT STDMETHODCALLTYPE Remove() = 0;
    virtual HRESULT STDMETHODCALLTYPE GetMediaSource(IMFMediaSource** src) = 0;
    virtual HRESULT STDMETHODCALLTYPE SendCameraProperty(REFGUID set, ULONG id, ULONG flags, void* payload, ULONG payloadLen, void* data, ULONG dataLen, ULONG* written) = 0;
    virtual HRESULT STDMETHODCALLTYPE CreateSyncEvent(REFGUID set, ULONG id, ULONG flags, HANDLE ev, IUnknown** obj) = 0;
    virtual HRESULT STDMETHODCALLTYPE CreateSyncSemaphore(REFGUID set, ULONG id, ULONG flags, HANDLE sem, LONG adj, IUnknown** obj) = 0;
    virtual HRESULT STDMETHODCALLTYPE Shutdown() = 0;
};

typedef HRESULT(WINAPI* PFN_MFCreateVirtualCamera)(int type, int lifetime, int access, LPCWSTR friendlyName,
                                                     LPCWSTR sourceId, const GUID* categories, ULONG categoryCount,
                                                     IMFVirtualCameraMk** camera);

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------
template <class T> inline void SafeRelease(T*& p) { if (p) { p->Release(); p = nullptr; } }

void MkLog(const char* fmt, ...);
extern HMODULE g_module;
extern volatile LONG g_objects;

// RAII lock over an SRWLOCK.
struct MkLock {
    SRWLOCK* l;
    explicit MkLock(SRWLOCK* lock) : l(lock) { AcquireSRWLockExclusive(l); }
    ~MkLock() { ReleaseSRWLockExclusive(l); }
};

// Make sure COM + MF are initialised on whatever thread calls an export.
void MkEnsureCom();

// Monotonic time in 100 ns units (QueryPerformanceCounter).
inline LONGLONG MkNow100ns() {
    static LARGE_INTEGER freq = {};
    if (!freq.QuadPart) QueryPerformanceFrequency(&freq);
    LARGE_INTEGER c;
    QueryPerformanceCounter(&c);
    return (LONGLONG)((c.QuadPart / freq.QuadPart) * 10000000 + (c.QuadPart % freq.QuadPart) * 10000000 / freq.QuadPart);
}

// Raises the system timer resolution to 1 ms while a streaming thread runs.
extern "C" __declspec(dllimport) UINT WINAPI timeBeginPeriod(UINT);
extern "C" __declspec(dllimport) UINT WINAPI timeEndPeriod(UINT);
struct MkHighResTimer {
    MkHighResTimer() { timeBeginPeriod(1); }
    ~MkHighResTimer() { timeEndPeriod(1); }
};
