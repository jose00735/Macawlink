#pragma once
#include "common.h"
#include "ring.h"

class MkStream;

// The media source Frame Server loads for "Macawlink Camera".
class MkSource final : public IMFMediaSourceEx,
                 public IMFGetService,
                 public IKsControlMk,
                 public IMFSampleAllocatorControlMk {
public:
    static HRESULT Create(MkSource** out);

    // IUnknown
    STDMETHODIMP QueryInterface(REFIID riid, void** ppv) override;
    STDMETHODIMP_(ULONG) AddRef() override;
    STDMETHODIMP_(ULONG) Release() override;

    // IMFMediaEventGenerator
    STDMETHODIMP GetEvent(DWORD flags, IMFMediaEvent** ev) override;
    STDMETHODIMP BeginGetEvent(IMFAsyncCallback* cb, IUnknown* state) override;
    STDMETHODIMP EndGetEvent(IMFAsyncResult* result, IMFMediaEvent** ev) override;
    STDMETHODIMP QueueEvent(MediaEventType met, REFGUID ext, HRESULT hr, const PROPVARIANT* val) override;

    // IMFMediaSource
    STDMETHODIMP GetCharacteristics(DWORD* c) override;
    STDMETHODIMP CreatePresentationDescriptor(IMFPresentationDescriptor** pd) override;
    STDMETHODIMP Start(IMFPresentationDescriptor* pd, const GUID* timeFormat, const PROPVARIANT* startPos) override;
    STDMETHODIMP Stop() override;
    STDMETHODIMP Pause() override;
    STDMETHODIMP Shutdown() override;

    // IMFMediaSourceEx
    STDMETHODIMP GetSourceAttributes(IMFAttributes** attrs) override;
    STDMETHODIMP GetStreamAttributes(DWORD streamId, IMFAttributes** attrs) override;
    STDMETHODIMP SetD3DManager(IUnknown* manager) override;

    // IMFGetService
    STDMETHODIMP GetService(REFGUID service, REFIID riid, LPVOID* obj) override;

    // IKsControl
    STDMETHODIMP KsProperty(void*, ULONG, void*, ULONG, ULONG*) override;
    STDMETHODIMP KsMethod(void*, ULONG, void*, ULONG, ULONG*) override;
    STDMETHODIMP KsEvent(void*, ULONG, void*, ULONG, ULONG*) override;

    // IMFSampleAllocatorControl
    STDMETHODIMP SetDefaultAllocator(DWORD outputStreamId, IUnknown* allocator) override;
    STDMETHODIMP GetAllocatorUsage(DWORD outputStreamId, DWORD* inputStreamId, int* usage) override;

    IUnknown* Allocator() { return allocator_; }

private:
    MkSource();
    ~MkSource();
    HRESULT Init();

    volatile LONG ref_ = 1;
    SRWLOCK lock_ = SRWLOCK_INIT;
    bool shutdown_ = false;
    IMFMediaEventQueue* queue_ = nullptr;
    IMFPresentationDescriptor* pd_ = nullptr;
    IMFAttributes* attrs_ = nullptr;
    IMFAttributes* streamAttrs_ = nullptr;
    IUnknown* allocator_ = nullptr;
    MkStream* stream_ = nullptr;
    bool streamAnnounced_ = false;
};

// The single video stream of the source.
class MkStream final : public IMFMediaStream2Mk, public IKsControlMk {
public:
    MkStream(MkSource* source, IMFStreamDescriptor* sd, IMFAttributes* attrs);

    STDMETHODIMP QueryInterface(REFIID riid, void** ppv) override;
    STDMETHODIMP_(ULONG) AddRef() override;
    STDMETHODIMP_(ULONG) Release() override;

    STDMETHODIMP GetEvent(DWORD flags, IMFMediaEvent** ev) override;
    STDMETHODIMP BeginGetEvent(IMFAsyncCallback* cb, IUnknown* state) override;
    STDMETHODIMP EndGetEvent(IMFAsyncResult* result, IMFMediaEvent** ev) override;
    STDMETHODIMP QueueEvent(MediaEventType met, REFGUID ext, HRESULT hr, const PROPVARIANT* val) override;

    STDMETHODIMP GetMediaSource(IMFMediaSource** src) override;
    STDMETHODIMP GetStreamDescriptor(IMFStreamDescriptor** sd) override;
    STDMETHODIMP RequestSample(IUnknown* token) override;

    STDMETHODIMP SetStreamState(int value) override;
    STDMETHODIMP GetStreamState(int* value) override;

    STDMETHODIMP KsProperty(void*, ULONG, void*, ULONG, ULONG*) override;
    STDMETHODIMP KsMethod(void*, ULONG, void*, ULONG, ULONG*) override;
    STDMETHODIMP KsEvent(void*, ULONG, void*, ULONG, ULONG*) override;

    HRESULT Start(IUnknown* allocator);
    HRESULT Stop();
    void Shutdown();

private:
    ~MkStream();
    static DWORD WINAPI WorkerThunk(LPVOID self);
    void Worker();
    HRESULT Deliver(IUnknown* token);
    HRESULT MakeSample(IMFSample** out);
    void FillFrame(BYTE* y, LONG pitch, BYTE* uv, UINT32 w, UINT32 h);
    bool OpenRing();

    volatile LONG ref_ = 1;
    SRWLOCK lock_ = SRWLOCK_INIT;
    MkSource* source_;  // weak: the source owns the stream
    IMFMediaEventQueue* queue_ = nullptr;
    IMFStreamDescriptor* sd_ = nullptr;
    IMFAttributes* attrs_ = nullptr;
    IMFVideoSampleAllocatorEx* allocator_ = nullptr;
    int state_ = MK_STREAM_STATE_STOPPED;
    bool shutdown_ = false;

    UINT32 width_ = 1280, height_ = 720, fps_ = 30;

    // worker: paces deliveries to the negotiated frame rate
    HANDLE thread_ = nullptr;
    HANDLE wake_ = nullptr;
    HANDLE quit_ = nullptr;
    IUnknown* tokens_[64] = {};
    int tokenCount_ = 0;
    LONGLONG nextDue_ = 0;  // QPC-ish in 100ns (MFGetSystemTime)
    uint64_t frameNo_ = 0;

    // shared memory from the Go receiver
    HANDLE ringMap_ = nullptr;
    BYTE* ring_ = nullptr;
    ULONGLONG lastRingTry_ = 0;
};
