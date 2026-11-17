// Media Foundation custom media source for the Windows 11 virtual camera ("Macawlink Camera").
// Frame Server loads this inside its own service process; frames come from the Go receiver
// through the shared-memory ring described in ring.h.
#include "vcam_source.h"
#include <sddl.h>

namespace {

struct Format { UINT32 w, h, fps; };
// First entry is the default; apps that do not negotiate get 720p30.
const Format kFormats[] = {
    {1280, 720, 30}, {1920, 1080, 30}, {640, 480, 30}, {1280, 720, 60}, {1920, 1080, 60},
};
const int kFormatCount = sizeof(kFormats) / sizeof(kFormats[0]);

const HRESULT kKsNotFound = HRESULT_FROM_WIN32(ERROR_SET_NOT_FOUND);

HRESULT MakeNv12Type(const Format& f, IMFMediaType** out) {
    IMFMediaType* t = nullptr;
    HRESULT hr = MFCreateMediaType(&t);
    if (FAILED(hr)) return hr;
    t->SetGUID(MF_MT_MAJOR_TYPE, MFMediaType_Video);
    t->SetGUID(MF_MT_SUBTYPE, MFVideoFormat_NV12);
    MFSetAttributeSize(t, MF_MT_FRAME_SIZE, f.w, f.h);
    MFSetAttributeRatio(t, MF_MT_FRAME_RATE, f.fps, 1);
    MFSetAttributeRatio(t, MF_MT_PIXEL_ASPECT_RATIO, 1, 1);
    t->SetUINT32(MF_MT_INTERLACE_MODE, MFVideoInterlace_Progressive);
    t->SetUINT32(MF_MT_ALL_SAMPLES_INDEPENDENT, TRUE);
    t->SetUINT32(MF_MT_FIXED_SIZE_SAMPLES, TRUE);
    t->SetUINT32(MF_MT_DEFAULT_STRIDE, f.w);
    t->SetUINT32(MF_MT_SAMPLE_SIZE, f.w * f.h * 3 / 2);
    t->SetUINT32(MF_MT_AVG_BITRATE, f.w * f.h * 12 * f.fps);
    *out = t;
    return S_OK;
}

}  // namespace

// ===========================================================================
// MkSource
// ===========================================================================
MkSource::MkSource() { InterlockedIncrement(&g_objects); }
MkSource::~MkSource() {
    SafeRelease(queue_);
    SafeRelease(pd_);
    SafeRelease(attrs_);
    SafeRelease(streamAttrs_);
    SafeRelease(allocator_);
    SafeRelease(stream_);
    InterlockedDecrement(&g_objects);
}

HRESULT MkSource::Create(MkSource** out) {
    MkSource* s = new MkSource();
    HRESULT hr = s->Init();
    if (FAILED(hr)) { s->Release(); return hr; }
    *out = s;
    return S_OK;
}

HRESULT MkSource::Init() {
    HRESULT hr = MFCreateEventQueue(&queue_);
    if (FAILED(hr)) return hr;
    hr = MFCreateAttributes(&attrs_, 4);
    if (FAILED(hr)) return hr;

    hr = MFCreateAttributes(&streamAttrs_, 4);
    if (FAILED(hr)) return hr;
    streamAttrs_->SetGUID(MK_MF_DEVICESTREAM_STREAM_CATEGORY, MK_PINNAME_VIDEO_CAPTURE);
    streamAttrs_->SetUINT32(MK_MF_DEVICESTREAM_STREAM_ID, 0);
    streamAttrs_->SetUINT32(MK_MF_DEVICESTREAM_FRAMESERVER_SHARED, 1);
    streamAttrs_->SetUINT32(MK_MF_DEVICESTREAM_ATTRIBUTE_FRAMESOURCE_TYPES, 1 /* MFFrameSourceTypes_Color */);

    IMFMediaType* types[kFormatCount] = {};
    for (int i = 0; i < kFormatCount; i++) {
        hr = MakeNv12Type(kFormats[i], &types[i]);
        if (FAILED(hr)) break;
    }
    IMFStreamDescriptor* sd = nullptr;
    if (SUCCEEDED(hr)) hr = MFCreateStreamDescriptor(0, kFormatCount, types, &sd);
    if (SUCCEEDED(hr)) {
        streamAttrs_->CopyAllItems(sd);
        IMFMediaTypeHandler* handler = nullptr;
        if (SUCCEEDED(sd->GetMediaTypeHandler(&handler))) {
            handler->SetCurrentMediaType(types[0]);
            handler->Release();
        }
        hr = MFCreatePresentationDescriptor(1, &sd, &pd_);
    }
    if (SUCCEEDED(hr)) hr = pd_->SelectStream(0);
    if (SUCCEEDED(hr)) stream_ = new MkStream(this, sd, streamAttrs_);

    for (int i = 0; i < kFormatCount; i++) SafeRelease(types[i]);
    SafeRelease(sd);
    MkLog("source: init hr=0x%08lx", (unsigned long)hr);
    return hr;
}

STDMETHODIMP MkSource::QueryInterface(REFIID riid, void** ppv) {
    if (!ppv) return E_POINTER;
    if (riid == __uuidof(IUnknown) || riid == __uuidof(IMFMediaEventGenerator) ||
        riid == __uuidof(IMFMediaSource) || riid == __uuidof(IMFMediaSourceEx)) {
        *ppv = static_cast<IMFMediaSourceEx*>(this);
    } else if (riid == __uuidof(IMFGetService)) {
        *ppv = static_cast<IMFGetService*>(this);
    } else if (riid == MK_IID_IKsControl) {
        *ppv = static_cast<IKsControlMk*>(this);
    } else if (riid == MK_IID_IMFSampleAllocatorControl) {
        *ppv = static_cast<IMFSampleAllocatorControlMk*>(this);
    } else {
        *ppv = nullptr;
        return E_NOINTERFACE;
    }
    AddRef();
    return S_OK;
}
STDMETHODIMP_(ULONG) MkSource::AddRef() { return InterlockedIncrement(&ref_); }
STDMETHODIMP_(ULONG) MkSource::Release() {
    ULONG r = InterlockedDecrement(&ref_);
    if (r == 0) delete this;
    return r;
}

STDMETHODIMP MkSource::GetEvent(DWORD flags, IMFMediaEvent** ev) {
    IMFMediaEventQueue* q;
    { MkLock l(&lock_); if (shutdown_) return MF_E_SHUTDOWN; q = queue_; q->AddRef(); }
    HRESULT hr = q->GetEvent(flags, ev);  // may block: never hold the lock here
    q->Release();
    return hr;
}
STDMETHODIMP MkSource::BeginGetEvent(IMFAsyncCallback* cb, IUnknown* state) {
    MkLock l(&lock_);
    if (shutdown_) return MF_E_SHUTDOWN;
    return queue_->BeginGetEvent(cb, state);
}
STDMETHODIMP MkSource::EndGetEvent(IMFAsyncResult* result, IMFMediaEvent** ev) {
    MkLock l(&lock_);
    if (shutdown_) return MF_E_SHUTDOWN;
    return queue_->EndGetEvent(result, ev);
}
STDMETHODIMP MkSource::QueueEvent(MediaEventType met, REFGUID ext, HRESULT hr, const PROPVARIANT* val) {
    MkLock l(&lock_);
    if (shutdown_) return MF_E_SHUTDOWN;
    return queue_->QueueEventParamVar(met, ext, hr, val);
}

STDMETHODIMP MkSource::GetCharacteristics(DWORD* c) {
    if (!c) return E_POINTER;
    MkLock l(&lock_);
    if (shutdown_) return MF_E_SHUTDOWN;
    *c = MFMEDIASOURCE_IS_LIVE;
    return S_OK;
}

STDMETHODIMP MkSource::CreatePresentationDescriptor(IMFPresentationDescriptor** pd) {
    if (!pd) return E_POINTER;
    MkLock l(&lock_);
    if (shutdown_) return MF_E_SHUTDOWN;
    return pd_->Clone(pd);
}

STDMETHODIMP MkSource::Start(IMFPresentationDescriptor* pd, const GUID* timeFormat, const PROPVARIANT* startPos) {
    if (!pd) return E_INVALIDARG;
    if (timeFormat && *timeFormat != GUID_NULL) return MF_E_UNSUPPORTED_TIME_FORMAT;
    MkLock l(&lock_);
    if (shutdown_) return MF_E_SHUTDOWN;

    BOOL selected = FALSE;
    IMFStreamDescriptor* sd = nullptr;
    HRESULT hr = pd->GetStreamDescriptorByIndex(0, &selected, &sd);
    if (FAILED(hr)) return hr;

    // Adopt the media type the pipeline selected on the caller's descriptor.
    IMFMediaTypeHandler* inH = nullptr;
    IMFMediaType* chosen = nullptr;
    if (SUCCEEDED(sd->GetMediaTypeHandler(&inH)) && SUCCEEDED(inH->GetCurrentMediaType(&chosen))) {
        IMFStreamDescriptor* ours = nullptr;
        BOOL dummy;
        IMFMediaTypeHandler* ourH = nullptr;
        if (SUCCEEDED(pd_->GetStreamDescriptorByIndex(0, &dummy, &ours)) && SUCCEEDED(ours->GetMediaTypeHandler(&ourH))) {
            ourH->SetCurrentMediaType(chosen);
        }
        SafeRelease(ourH);
        SafeRelease(ours);
    }
    SafeRelease(chosen);
    SafeRelease(inH);
    SafeRelease(sd);

    if (selected) {
        hr = queue_->QueueEventParamUnk(streamAnnounced_ ? MEUpdatedStream : MENewStream, GUID_NULL, S_OK,
                                         static_cast<IMFMediaStream*>(stream_));
        if (FAILED(hr)) return hr;
        streamAnnounced_ = true;
        hr = stream_->Start(allocator_);
        if (FAILED(hr)) return hr;
    }

    PROPVARIANT v;
    PropVariantInit(&v);
    v.vt = VT_I8;
    v.hVal.QuadPart = (startPos && startPos->vt == VT_I8) ? startPos->hVal.QuadPart : MFGetSystemTime();
    hr = queue_->QueueEventParamVar(MESourceStarted, GUID_NULL, S_OK, &v);
    MkLog("source: start selected=%d hr=0x%08lx", selected, (unsigned long)hr);
    return hr;
}

STDMETHODIMP MkSource::Stop() {
    MkLock l(&lock_);
    if (shutdown_) return MF_E_SHUTDOWN;
    stream_->Stop();
    return queue_->QueueEventParamVar(MESourceStopped, GUID_NULL, S_OK, nullptr);
}

STDMETHODIMP MkSource::Pause() { return MF_E_INVALID_STATE_TRANSITION; }

STDMETHODIMP MkSource::Shutdown() {
    MkLock l(&lock_);
    if (shutdown_) return MF_E_SHUTDOWN;
    shutdown_ = true;
    if (stream_) stream_->Shutdown();
    if (queue_) queue_->Shutdown();
    MkLog("source: shutdown");
    return S_OK;
}

STDMETHODIMP MkSource::GetSourceAttributes(IMFAttributes** attrs) {
    if (!attrs) return E_POINTER;
    MkLock l(&lock_);
    if (shutdown_) return MF_E_SHUTDOWN;
    *attrs = attrs_;
    attrs_->AddRef();
    return S_OK;
}
STDMETHODIMP MkSource::GetStreamAttributes(DWORD streamId, IMFAttributes** attrs) {
    if (!attrs) return E_POINTER;
    MkLock l(&lock_);
    if (shutdown_) return MF_E_SHUTDOWN;
    if (streamId != 0) return MF_E_INVALIDSTREAMNUMBER;
    *attrs = streamAttrs_;
    streamAttrs_->AddRef();
    return S_OK;
}
STDMETHODIMP MkSource::SetD3DManager(IUnknown*) { return S_OK; }  // we produce system-memory frames

STDMETHODIMP MkSource::GetService(REFGUID, REFIID, LPVOID* obj) {
    if (obj) *obj = nullptr;
    return MF_E_UNSUPPORTED_SERVICE;
}

STDMETHODIMP MkSource::KsProperty(void*, ULONG, void*, ULONG, ULONG* r) { if (r) *r = 0; return kKsNotFound; }
STDMETHODIMP MkSource::KsMethod(void*, ULONG, void*, ULONG, ULONG* r) { if (r) *r = 0; return kKsNotFound; }
STDMETHODIMP MkSource::KsEvent(void*, ULONG, void*, ULONG, ULONG* r) { if (r) *r = 0; return kKsNotFound; }

STDMETHODIMP MkSource::SetDefaultAllocator(DWORD outputStreamId, IUnknown* allocator) {
    if (outputStreamId != 0) return MF_E_INVALIDSTREAMNUMBER;
    MkLock l(&lock_);
    SafeRelease(allocator_);
    allocator_ = allocator;
    if (allocator_) allocator_->AddRef();
    return S_OK;
}
STDMETHODIMP MkSource::GetAllocatorUsage(DWORD outputStreamId, DWORD* inputStreamId, int* usage) {
    if (!usage) return E_POINTER;
    if (outputStreamId != 0) return MF_E_INVALIDSTREAMNUMBER;
    if (inputStreamId) *inputStreamId = outputStreamId;
    *usage = MK_USES_PROVIDED_ALLOCATOR;
    return S_OK;
}

// ===========================================================================
// MkStream
// ===========================================================================
MkStream::MkStream(MkSource* source, IMFStreamDescriptor* sd, IMFAttributes* attrs)
    : source_(source), sd_(sd), attrs_(attrs) {
    InterlockedIncrement(&g_objects);
    sd_->AddRef();
    attrs_->AddRef();
    MFCreateEventQueue(&queue_);
    wake_ = CreateEventW(nullptr, FALSE, FALSE, nullptr);
    quit_ = CreateEventW(nullptr, TRUE, FALSE, nullptr);
}

MkStream::~MkStream() {
    Shutdown();
    SafeRelease(queue_);
    SafeRelease(sd_);
    SafeRelease(attrs_);
    if (wake_) CloseHandle(wake_);
    if (quit_) CloseHandle(quit_);
    InterlockedDecrement(&g_objects);
}

STDMETHODIMP MkStream::QueryInterface(REFIID riid, void** ppv) {
    if (!ppv) return E_POINTER;
    if (riid == __uuidof(IUnknown) || riid == __uuidof(IMFMediaEventGenerator) ||
        riid == __uuidof(IMFMediaStream) || riid == MK_IID_IMFMediaStream2) {
        *ppv = static_cast<IMFMediaStream2Mk*>(this);
    } else if (riid == MK_IID_IKsControl) {
        *ppv = static_cast<IKsControlMk*>(this);
    } else {
        *ppv = nullptr;
        return E_NOINTERFACE;
    }
    AddRef();
    return S_OK;
}
STDMETHODIMP_(ULONG) MkStream::AddRef() { return InterlockedIncrement(&ref_); }
STDMETHODIMP_(ULONG) MkStream::Release() {
    ULONG r = InterlockedDecrement(&ref_);
    if (r == 0) delete this;
    return r;
}

STDMETHODIMP MkStream::GetEvent(DWORD flags, IMFMediaEvent** ev) {
    IMFMediaEventQueue* q;
    { MkLock l(&lock_); if (shutdown_) return MF_E_SHUTDOWN; q = queue_; q->AddRef(); }
    HRESULT hr = q->GetEvent(flags, ev);
    q->Release();
    return hr;
}
STDMETHODIMP MkStream::BeginGetEvent(IMFAsyncCallback* cb, IUnknown* state) {
    MkLock l(&lock_);
    if (shutdown_) return MF_E_SHUTDOWN;
    return queue_->BeginGetEvent(cb, state);
}
STDMETHODIMP MkStream::EndGetEvent(IMFAsyncResult* result, IMFMediaEvent** ev) {
    MkLock l(&lock_);
    if (shutdown_) return MF_E_SHUTDOWN;
    return queue_->EndGetEvent(result, ev);
}
STDMETHODIMP MkStream::QueueEvent(MediaEventType met, REFGUID ext, HRESULT hr, const PROPVARIANT* val) {
    MkLock l(&lock_);
    if (shutdown_) return MF_E_SHUTDOWN;
    return queue_->QueueEventParamVar(met, ext, hr, val);
}

STDMETHODIMP MkStream::GetMediaSource(IMFMediaSource** src) {
    if (!src) return E_POINTER;
    MkLock l(&lock_);
    if (shutdown_) return MF_E_SHUTDOWN;
    *src = static_cast<IMFMediaSource*>(source_);
    (*src)->AddRef();
    return S_OK;
}
STDMETHODIMP MkStream::GetStreamDescriptor(IMFStreamDescriptor** sd) {
    if (!sd) return E_POINTER;
    MkLock l(&lock_);
    if (shutdown_) return MF_E_SHUTDOWN;
    *sd = sd_;
    sd_->AddRef();
    return S_OK;
}

STDMETHODIMP MkStream::RequestSample(IUnknown* token) {
    MkLock l(&lock_);
    if (shutdown_) return MF_E_SHUTDOWN;
    if (state_ != MK_STREAM_STATE_RUNNING) return MF_E_MEDIA_SOURCE_WRONGSTATE;
    if (tokenCount_ >= (int)(sizeof(tokens_) / sizeof(tokens_[0]))) return MF_E_NOTACCEPTING;
    if (token) token->AddRef();
    tokens_[tokenCount_++] = token;
    SetEvent(wake_);
    return S_OK;
}

STDMETHODIMP MkStream::SetStreamState(int value) {
    if (value == MK_STREAM_STATE_RUNNING) return Start(source_ ? source_->Allocator() : nullptr);
    if (value == MK_STREAM_STATE_STOPPED) return Stop();
    MkLock l(&lock_);
    state_ = value;
    return S_OK;
}
STDMETHODIMP MkStream::GetStreamState(int* value) {
    if (!value) return E_POINTER;
    MkLock l(&lock_);
    *value = state_;
    return S_OK;
}

STDMETHODIMP MkStream::KsProperty(void*, ULONG, void*, ULONG, ULONG* r) { if (r) *r = 0; return kKsNotFound; }
STDMETHODIMP MkStream::KsMethod(void*, ULONG, void*, ULONG, ULONG* r) { if (r) *r = 0; return kKsNotFound; }
STDMETHODIMP MkStream::KsEvent(void*, ULONG, void*, ULONG, ULONG* r) { if (r) *r = 0; return kKsNotFound; }

HRESULT MkStream::Start(IUnknown* allocator) {
    MkLock l(&lock_);
    if (shutdown_) return MF_E_SHUTDOWN;

    IMFMediaTypeHandler* h = nullptr;
    IMFMediaType* type = nullptr;
    HRESULT hr = sd_->GetMediaTypeHandler(&h);
    if (SUCCEEDED(hr)) hr = h->GetCurrentMediaType(&type);
    if (SUCCEEDED(hr)) {
        UINT32 n = 30, d = 1;
        MFGetAttributeSize(type, MF_MT_FRAME_SIZE, &width_, &height_);
        if (SUCCEEDED(MFGetAttributeRatio(type, MF_MT_FRAME_RATE, &n, &d)) && d) fps_ = n / d;
        if (fps_ == 0) fps_ = 30;
    }

    SafeRelease(allocator_);
    if (allocator && type) {
        if (SUCCEEDED(allocator->QueryInterface(__uuidof(IMFVideoSampleAllocatorEx), (void**)&allocator_))) {
            if (FAILED(allocator_->InitializeSampleAllocatorEx(2, 10, nullptr, type))) SafeRelease(allocator_);
        }
    }
    SafeRelease(type);
    SafeRelease(h);

    state_ = MK_STREAM_STATE_RUNNING;
    nextDue_ = 0;
    if (!thread_) thread_ = CreateThread(nullptr, 0, WorkerThunk, this, 0, nullptr);
    OpenRing();
    if (ring_) {
        auto* hd = reinterpret_cast<volatile MkRingHeader*>(ring_);
        hd->reqWidth = width_;
        hd->reqHeight = height_;
        hd->reqFps = fps_;
    }
    MkLog("stream: start %ux%u@%u allocator=%d ring=%d", width_, height_, fps_, allocator_ != nullptr, ring_ != nullptr);
    return queue_->QueueEventParamVar(MEStreamStarted, GUID_NULL, S_OK, nullptr);
}

HRESULT MkStream::Stop() {
    MkLock l(&lock_);
    if (shutdown_) return MF_E_SHUTDOWN;
    state_ = MK_STREAM_STATE_STOPPED;
    for (int i = 0; i < tokenCount_; i++) SafeRelease(tokens_[i]);
    tokenCount_ = 0;
    if (ring_) {
        auto* hd = reinterpret_cast<volatile MkRingHeader*>(ring_);
        hd->reqWidth = hd->reqHeight = hd->reqFps = 0;
    }
    MkLog("stream: stop");
    return queue_->QueueEventParamVar(MEStreamStopped, GUID_NULL, S_OK, nullptr);
}

void MkStream::Shutdown() {
    HANDLE t = nullptr;
    {
        MkLock l(&lock_);
        if (shutdown_) return;
        shutdown_ = true;
        state_ = MK_STREAM_STATE_STOPPED;
        t = thread_;
        thread_ = nullptr;
        SetEvent(quit_);
    }
    if (t) { WaitForSingleObject(t, 2000); CloseHandle(t); }
    MkLock l(&lock_);
    for (int i = 0; i < tokenCount_; i++) SafeRelease(tokens_[i]);
    tokenCount_ = 0;
    if (queue_) queue_->Shutdown();
    SafeRelease(allocator_);
    if (ring_) {
        auto* hd = reinterpret_cast<volatile MkRingHeader*>(ring_);
        hd->reqWidth = hd->reqHeight = hd->reqFps = 0;
        UnmapViewOfFile(ring_);
        ring_ = nullptr;
    }
    if (ringMap_) { CloseHandle(ringMap_); ringMap_ = nullptr; }
}

DWORD WINAPI MkStream::WorkerThunk(LPVOID self) {
    CoInitializeEx(nullptr, COINIT_MULTITHREADED);
    static_cast<MkStream*>(self)->Worker();
    CoUninitialize();
    return 0;
}

void MkStream::Worker() {
    HANDLE waits[2] = {quit_, wake_};
    for (;;) {
        DWORD w = WaitForMultipleObjects(2, waits, FALSE, INFINITE);
        if (w == WAIT_OBJECT_0) return;
        for (;;) {
            IUnknown* token = nullptr;
            LONGLONG interval;
            {
                MkLock l(&lock_);
                if (shutdown_) return;
                if (state_ != MK_STREAM_STATE_RUNNING || tokenCount_ == 0) break;
                interval = 10000000LL / fps_;
            }
            // Pace to the negotiated frame rate, like a real sensor.
            LONGLONG now = MFGetSystemTime();
            if (nextDue_ == 0 || now - nextDue_ > interval * 2) nextDue_ = now;
            if (now < nextDue_) {
                DWORD ms = (DWORD)((nextDue_ - now) / 10000);
                if (ms && WaitForSingleObject(quit_, ms) == WAIT_OBJECT_0) return;
            }
            nextDue_ += interval;
            {
                MkLock l(&lock_);
                if (shutdown_) return;
                if (tokenCount_ == 0) break;
                token = tokens_[0];
                memmove(tokens_, tokens_ + 1, sizeof(tokens_[0]) * (tokenCount_ - 1));
                tokenCount_--;
            }
            HRESULT hr = Deliver(token);
            if (token) token->Release();
            if (FAILED(hr)) MkLog("stream: deliver failed 0x%08lx", (unsigned long)hr);
        }
    }
}

HRESULT MkStream::Deliver(IUnknown* token) {
    IMFSample* sample = nullptr;
    HRESULT hr = MakeSample(&sample);
    if (FAILED(hr)) return hr;
    sample->SetSampleTime(MFGetSystemTime());
    sample->SetSampleDuration(10000000LL / fps_);
    if (token) sample->SetUnknown(MFSampleExtension_Token, token);
    {
        MkLock l(&lock_);
        hr = shutdown_ ? MF_E_SHUTDOWN : queue_->QueueEventParamUnk(MEMediaSample, GUID_NULL, S_OK, sample);
    }
    sample->Release();
    frameNo_++;
    return hr;
}

HRESULT MkStream::MakeSample(IMFSample** out) {
    IMFSample* sample = nullptr;
    HRESULT hr;
    if (allocator_) {
        hr = allocator_->AllocateSample(&sample);
    } else {
        IMFMediaBuffer* mb = nullptr;
        hr = MFCreateSample(&sample);
        if (SUCCEEDED(hr)) hr = MFCreateMemoryBuffer(width_ * height_ * 3 / 2, &mb);
        if (SUCCEEDED(hr)) hr = sample->AddBuffer(mb);
        SafeRelease(mb);
    }
    if (FAILED(hr)) { SafeRelease(sample); return hr; }

    IMFMediaBuffer* buf = nullptr;
    hr = sample->GetBufferByIndex(0, &buf);
    if (FAILED(hr)) { sample->Release(); return hr; }

    IMF2DBuffer* b2d = nullptr;
    BYTE* scan0 = nullptr;
    LONG pitch = 0;
    if (SUCCEEDED(buf->QueryInterface(__uuidof(IMF2DBuffer), (void**)&b2d)) && SUCCEEDED(b2d->Lock2D(&scan0, &pitch))) {
        FillFrame(scan0, pitch, scan0 + (size_t)pitch * height_, width_, height_);
        b2d->Unlock2D();
    } else {
        BYTE* data = nullptr;
        DWORD maxLen = 0;
        hr = buf->Lock(&data, &maxLen, nullptr);
        if (SUCCEEDED(hr)) {
            FillFrame(data, (LONG)width_, data + (size_t)width_ * height_, width_, height_);
            buf->Unlock();
        }
    }
    buf->SetCurrentLength(width_ * height_ * 3 / 2);
    SafeRelease(b2d);
    buf->Release();
    if (FAILED(hr)) { sample->Release(); return hr; }
    *out = sample;
    return S_OK;
}

bool MkStream::OpenRing() {
    if (ring_) return true;
    ULONGLONG now = GetTickCount64();
    if (lastRingTry_ && now - lastRingTry_ < 1000) return false;
    lastRingTry_ = now;

    PSECURITY_DESCRIPTOR sd = nullptr;
    SECURITY_ATTRIBUTES sa = {sizeof(sa), nullptr, FALSE};
    if (ConvertStringSecurityDescriptorToSecurityDescriptorW(MK_RING_SDDL, SDDL_REVISION_1, &sd, nullptr))
        sa.lpSecurityDescriptor = sd;
    ringMap_ = CreateFileMappingW(INVALID_HANDLE_VALUE, sd ? &sa : nullptr, PAGE_READWRITE, 0, MK_RING_BYTES, MK_RING_NAME);
    bool existed = GetLastError() == ERROR_ALREADY_EXISTS;
    if (sd) LocalFree(sd);
    if (!ringMap_) {
        ringMap_ = OpenFileMappingW(FILE_MAP_ALL_ACCESS, FALSE, MK_RING_NAME);
        existed = true;
    }
    if (!ringMap_) {
        MkLog("ring: unavailable (err %lu)", GetLastError());
        return false;
    }
    ring_ = (BYTE*)MapViewOfFile(ringMap_, FILE_MAP_ALL_ACCESS, 0, 0, MK_RING_BYTES);
    if (!ring_) { CloseHandle(ringMap_); ringMap_ = nullptr; return false; }
    auto* hd = reinterpret_cast<MkRingHeader*>(ring_);
    if (!existed || hd->magic != MK_RING_MAGIC) {
        memset(hd, 0, sizeof(*hd));
        hd->magic = MK_RING_MAGIC;
        hd->version = MK_RING_VERSION;
    }
    MkLog("ring: mapped (existed=%d)", existed);
    return true;
}

// Writes one NV12 frame: the phone's latest picture when connected, otherwise a
// "waiting" card in the Macawlink palette (deep violet with a sweeping cyan scanline).
void MkStream::FillFrame(BYTE* y, LONG pitch, BYTE* uv, UINT32 w, UINT32 h) {
    if (!ring_) OpenRing();
    if (ring_) {
        auto* hd = reinterpret_cast<volatile MkRingHeader*>(ring_);
        hd->reqWidth = w;
        hd->reqHeight = h;
        hd->reqFps = fps_;
        hd->consumerTick = GetTickCount64();
        if (hd->connected && hd->seq != 0 && hd->magic == MK_RING_MAGIC) {
            MemoryBarrier();
            uint32_t idx = hd->latest % MK_RING_SLOTS;
            uint32_t sw = hd->slots[idx].width, sh = hd->slots[idx].height;
            if (sw && sh && sw <= MK_MAX_W && sh <= MK_MAX_H && !(sw & 1) && !(sh & 1)) {
                const BYTE* src = ring_ + MK_HEADER_BYTES + (size_t)idx * MK_SLOT_BYTES;
                const BYTE* suv = src + (size_t)sw * sh;
                if (sw == w && sh == h) {
                    for (UINT32 r = 0; r < h; r++) memcpy(y + (size_t)r * pitch, src + (size_t)r * w, w);
                    for (UINT32 r = 0; r < h / 2; r++) memcpy(uv + (size_t)r * pitch, suv + (size_t)r * w, w);
                } else {
                    // Format switch in flight: nearest-neighbour until Go rescales to the new size.
                    for (UINT32 r = 0; r < h; r++) {
                        const BYTE* srow = src + (size_t)(r * sh / h) * sw;
                        BYTE* drow = y + (size_t)r * pitch;
                        for (UINT32 c = 0; c < w; c++) drow[c] = srow[c * sw / w];
                    }
                    for (UINT32 r = 0; r < h / 2; r++) {
                        const BYTE* srow = suv + (size_t)(r * (sh / 2) / (h / 2)) * sw;
                        BYTE* drow = uv + (size_t)r * pitch;
                        for (UINT32 c = 0; c < w / 2; c++) {
                            UINT32 sc = (c * (sw / 2) / (w / 2)) * 2;
                            drow[c * 2] = srow[sc];
                            drow[c * 2 + 1] = srow[sc + 1];
                        }
                    }
                }
                hd->delivered = hd->delivered + 1;
                return;
            }
        }
    }

    // Placeholder card.
    const BYTE bgY = 25, bgU = 140, bgV = 129;      // #0D0522
    const BYTE cyY = 162, cyU = 170, cyV = 22;      // #00F0FF
    const BYTE mgY = 124, mgU = 172, mgV = 209;     // #FF2BD6
    UINT32 band = h / 60 ? h / 60 : 1;
    UINT32 scan = (UINT32)((frameNo_ * 6) % h);
    UINT32 boxW = w / 6, boxH = h / 6, bx = (w - boxW) / 2, by = (h - boxH) / 2;
    UINT32 t = h / 180 ? h / 180 : 1;
    for (UINT32 r = 0; r < h; r++) {
        BYTE* row = y + (size_t)r * pitch;
        bool inScan = r >= scan && r < scan + band;
        memset(row, inScan ? cyY : bgY, w);
        if (!inScan && r >= by && r < by + boxH) {
            bool edge = r < by + t || r >= by + boxH - t;
            if (edge) memset(row + bx, mgY, boxW);
            else { memset(row + bx, mgY, t); memset(row + bx + boxW - t, mgY, t); }
        }
    }
    for (UINT32 r = 0; r < h / 2; r++) {
        BYTE* row = uv + (size_t)r * pitch;
        bool inScan = r * 2 >= scan && r * 2 < scan + band;
        bool inBox = r * 2 >= by && r * 2 < by + boxH;
        bool edge = r * 2 < by + t || r * 2 >= by + boxH - t;
        for (UINT32 c = 0; c < w / 2; c++) {
            BYTE u = bgU, v = bgV;
            UINT32 px = c * 2;
            if (inScan) { u = cyU; v = cyV; }
            else if (inBox && px >= bx && px < bx + boxW &&
                     (edge || px < bx + t || px >= bx + boxW - t)) { u = mgU; v = mgV; }
            row[c * 2] = u;
            row[c * 2 + 1] = v;
        }
    }
}
