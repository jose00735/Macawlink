// DirectShow video capture filter: "Macawlink Camera" on Windows 10 (and for DirectShow-only
// apps). Chrome/Edge, Zoom, Teams, OBS and Discord enumerate it from the video input device
// category; it runs inside the app's process and reads frames from the session-local ring.
// Written against the raw COM interfaces (no DirectShow base classes).
#include "common.h"
#include "ring_reader.h"
#include <strmif.h>
#include <amvideo.h>
#include <uuids.h>
#include <vfwmsgs.h>

extern const GUID MK_CLSID_DShowFilter;
const GUID MK_CLSID_DShowFilter = {0x6e0d1a3b, 0x6f2c, 0x4c1e, {0x9b, 0x7a, 0x4d, 0x61, 0x43, 0x41, 0x57, 0xb2}};
const GUID MK_CLSID_VideoInputDeviceCategory = {0x860bb310, 0x5d01, 0x11d0, {0xbd, 0x3b, 0x00, 0xa0, 0xc9, 0x11, 0xce, 0x86}};
const GUID MK_CLSID_FilterMapper2 = {0xcda42200, 0xbd88, 0x11d0, {0xbd, 0x4e, 0x00, 0xa0, 0xc9, 0x11, 0xce, 0x86}};
const GUID MK_CLSID_MemoryAllocator = {0x1e651cc0, 0xb199, 0x11d0, {0x82, 0x12, 0x00, 0xc0, 0x4f, 0xc3, 0x2c, 0x45}};
const GUID MK_MEDIATYPE_Video = {0x73646976, 0x0000, 0x0010, {0x80, 0x00, 0x00, 0xaa, 0x00, 0x38, 0x9b, 0x71}};
const GUID MK_MEDIASUBTYPE_YUY2 = {0x32595559, 0x0000, 0x0010, {0x80, 0x00, 0x00, 0xaa, 0x00, 0x38, 0x9b, 0x71}};
const GUID MK_MEDIASUBTYPE_NV12 = {0x3231564e, 0x0000, 0x0010, {0x80, 0x00, 0x00, 0xaa, 0x00, 0x38, 0x9b, 0x71}};
const GUID MK_MEDIASUBTYPE_I420 = {0x30323449, 0x0000, 0x0010, {0x80, 0x00, 0x00, 0xaa, 0x00, 0x38, 0x9b, 0x71}};
const GUID MK_FORMAT_VideoInfo = {0x05589f80, 0xc356, 0x11ce, {0xbf, 0x01, 0x00, 0xaa, 0x00, 0x55, 0x59, 0x5a}};
const GUID MK_AMPROPSETID_Pin = {0x9b00f101, 0x1567, 0x11d1, {0xb3, 0xf1, 0x00, 0xaa, 0x00, 0x37, 0x61, 0xc5}};
// PIN_CATEGORY_CAPTURE is the same GUID as PINNAME_VIDEO_CAPTURE.

namespace {

const wchar_t kName[] = L"Macawlink Camera";
const wchar_t kPinName[] = L"Capture";

struct Res { LONG w, h; };
const Res kRes[] = {{1280, 720}, {1920, 1080}, {640, 480}, {2560, 1440}};
const GUID* const kSub[] = {&MK_MEDIASUBTYPE_YUY2, &MK_MEDIASUBTYPE_NV12, &MK_MEDIASUBTYPE_I420};
const DWORD kFourcc[] = {MAKEFOURCC('Y', 'U', 'Y', '2'), MAKEFOURCC('N', 'V', '1', '2'), MAKEFOURCC('I', '4', '2', '0')};
const WORD kBits[] = {16, 12, 12};
const int kTypeCount = (int)(sizeof(kRes) / sizeof(kRes[0])) * 3;  // sizes x {YUY2, NV12, I420}
const REFERENCE_TIME kDefaultInterval = 333333;  // 30 fps
const REFERENCE_TIME kMinInterval = 166666;      // 60 fps
const REFERENCE_TIME kMaxInterval = 2000000;     // 5 fps

const HRESULT kPropSetUnsupported = HRESULT_FROM_WIN32(ERROR_NOT_FOUND) + 2;  // E_PROP_SET_UNSUPPORTED 0x80070492
const HRESULT kPropIdUnsupported = HRESULT_FROM_WIN32(ERROR_NOT_FOUND);       // E_PROP_ID_UNSUPPORTED 0x80070490

void FreeMT(AM_MEDIA_TYPE& mt) {
    if (mt.cbFormat) CoTaskMemFree(mt.pbFormat);
    mt.cbFormat = 0;
    mt.pbFormat = nullptr;
    SafeRelease(mt.pUnk);
}

void DeleteMT(AM_MEDIA_TYPE* mt) {
    if (!mt) return;
    FreeMT(*mt);
    CoTaskMemFree(mt);
}

HRESULT CopyMT(AM_MEDIA_TYPE& dst, const AM_MEDIA_TYPE& src) {
    dst = src;
    if (src.cbFormat) {
        dst.pbFormat = (BYTE*)CoTaskMemAlloc(src.cbFormat);
        if (!dst.pbFormat) { dst.cbFormat = 0; return E_OUTOFMEMORY; }
        memcpy(dst.pbFormat, src.pbFormat, src.cbFormat);
    }
    if (dst.pUnk) dst.pUnk->AddRef();
    return S_OK;
}

AM_MEDIA_TYPE* CloneMT(const AM_MEDIA_TYPE& src) {
    auto* p = (AM_MEDIA_TYPE*)CoTaskMemAlloc(sizeof(AM_MEDIA_TYPE));
    if (p && FAILED(CopyMT(*p, src))) { CoTaskMemFree(p); return nullptr; }
    return p;
}

int SubIndex(const GUID& g) {
    for (int i = 0; i < 3; i++) if (g == *kSub[i]) return i;
    return -1;
}

void BuildType(int index, REFERENCE_TIME interval, AM_MEDIA_TYPE& mt) {
    const Res& r = kRes[index / 3];
    int s = index % 3;
    memset(&mt, 0, sizeof(mt));
    mt.majortype = MK_MEDIATYPE_Video;
    mt.subtype = *kSub[s];
    mt.bFixedSizeSamples = TRUE;
    mt.bTemporalCompression = FALSE;
    mt.lSampleSize = r.w * r.h * kBits[s] / 8;
    mt.formattype = MK_FORMAT_VideoInfo;
    mt.cbFormat = sizeof(VIDEOINFOHEADER);
    auto* vih = (VIDEOINFOHEADER*)CoTaskMemAlloc(sizeof(VIDEOINFOHEADER));
    memset(vih, 0, sizeof(*vih));
    vih->AvgTimePerFrame = interval;
    vih->dwBitRate = (DWORD)((LONGLONG)mt.lSampleSize * 8 * 10000000 / interval);
    vih->bmiHeader.biSize = sizeof(BITMAPINFOHEADER);
    vih->bmiHeader.biWidth = r.w;
    vih->bmiHeader.biHeight = r.h;  // positive: YUV is always top-down
    vih->bmiHeader.biPlanes = 1;
    vih->bmiHeader.biBitCount = kBits[s];
    vih->bmiHeader.biCompression = kFourcc[s];
    vih->bmiHeader.biSizeImage = mt.lSampleSize;
    mt.pbFormat = (BYTE*)vih;
}

// Checks a media type and extracts what we need; returns false if we can't produce it.
bool Parse(const AM_MEDIA_TYPE* mt, LONG* w, LONG* h, int* sub, REFERENCE_TIME* interval) {
    if (!mt || mt->majortype != MK_MEDIATYPE_Video) return false;
    int s = SubIndex(mt->subtype);
    if (s < 0 || mt->formattype != MK_FORMAT_VideoInfo || mt->cbFormat < sizeof(VIDEOINFOHEADER) || !mt->pbFormat) return false;
    auto* vih = (const VIDEOINFOHEADER*)mt->pbFormat;
    LONG ww = vih->bmiHeader.biWidth, hh = labs(vih->bmiHeader.biHeight);
    bool ok = false;
    for (const Res& r : kRes) ok = ok || (r.w == ww && r.h == hh);
    if (!ok) return false;
    REFERENCE_TIME t = vih->AvgTimePerFrame ? vih->AvgTimePerFrame : kDefaultInterval;
    if (t < kMinInterval || t > kMaxInterval) return false;
    if (w) *w = ww;
    if (h) *h = hh;
    if (sub) *sub = s;
    if (interval) *interval = t;
    return true;
}

}  // namespace

class MkDsFilter;

// ===========================================================================
// Enumerators
// ===========================================================================
class MkEnumTypes final : public IEnumMediaTypes {
public:
    MkEnumTypes(const AM_MEDIA_TYPE* first, int pos = 0) : pos_(pos) {
        hasFirst_ = first != nullptr;
        if (first) CopyMT(first_, *first);
    }
    ~MkEnumTypes() { if (hasFirst_) FreeMT(first_); }
    STDMETHODIMP QueryInterface(REFIID riid, void** ppv) override {
        if (!ppv) return E_POINTER;
        if (riid == __uuidof(IUnknown) || riid == __uuidof(IEnumMediaTypes)) { *ppv = this; AddRef(); return S_OK; }
        *ppv = nullptr;
        return E_NOINTERFACE;
    }
    STDMETHODIMP_(ULONG) AddRef() override { return InterlockedIncrement(&ref_); }
    STDMETHODIMP_(ULONG) Release() override { ULONG r = InterlockedDecrement(&ref_); if (!r) delete this; return r; }

    int Count() const { return kTypeCount + (hasFirst_ ? 1 : 0); }
    STDMETHODIMP Next(ULONG n, AM_MEDIA_TYPE** out, ULONG* fetched) override {
        if (!out) return E_POINTER;
        ULONG got = 0;
        while (got < n && pos_ < Count()) {
            AM_MEDIA_TYPE mt;
            if (hasFirst_ && pos_ == 0) CopyMT(mt, first_);
            else BuildType(pos_ - (hasFirst_ ? 1 : 0), kDefaultInterval, mt);
            auto* p = (AM_MEDIA_TYPE*)CoTaskMemAlloc(sizeof(AM_MEDIA_TYPE));
            *p = mt;
            out[got++] = p;
            pos_++;
        }
        if (fetched) *fetched = got;
        return got == n ? S_OK : S_FALSE;
    }
    STDMETHODIMP Skip(ULONG n) override { pos_ += n; return pos_ <= Count() ? S_OK : S_FALSE; }
    STDMETHODIMP Reset() override { pos_ = 0; return S_OK; }
    STDMETHODIMP Clone(IEnumMediaTypes** out) override {
        if (!out) return E_POINTER;
        *out = new MkEnumTypes(hasFirst_ ? &first_ : nullptr, pos_);
        return S_OK;
    }

private:
    volatile LONG ref_ = 1;
    int pos_;
    bool hasFirst_ = false;
    AM_MEDIA_TYPE first_ = {};
};

class MkEnumPins final : public IEnumPins {
public:
    MkEnumPins(IPin* pin, int pos = 0) : pin_(pin), pos_(pos) { pin_->AddRef(); }
    ~MkEnumPins() { pin_->Release(); }
    STDMETHODIMP QueryInterface(REFIID riid, void** ppv) override {
        if (!ppv) return E_POINTER;
        if (riid == __uuidof(IUnknown) || riid == __uuidof(IEnumPins)) { *ppv = this; AddRef(); return S_OK; }
        *ppv = nullptr;
        return E_NOINTERFACE;
    }
    STDMETHODIMP_(ULONG) AddRef() override { return InterlockedIncrement(&ref_); }
    STDMETHODIMP_(ULONG) Release() override { ULONG r = InterlockedDecrement(&ref_); if (!r) delete this; return r; }
    STDMETHODIMP Next(ULONG n, IPin** out, ULONG* fetched) override {
        if (!out) return E_POINTER;
        ULONG got = 0;
        if (n > 0 && pos_ == 0) { out[0] = pin_; pin_->AddRef(); got = 1; pos_ = 1; }
        if (fetched) *fetched = got;
        return got == n ? S_OK : S_FALSE;
    }
    STDMETHODIMP Skip(ULONG n) override { pos_ += n; return pos_ <= 1 ? S_OK : S_FALSE; }
    STDMETHODIMP Reset() override { pos_ = 0; return S_OK; }
    STDMETHODIMP Clone(IEnumPins** out) override { if (!out) return E_POINTER; *out = new MkEnumPins(pin_, pos_); return S_OK; }

private:
    volatile LONG ref_ = 1;
    IPin* pin_;
    int pos_;
};

// ===========================================================================
// Output pin
// ===========================================================================
class MkDsPin final : public IPin, public IAMStreamConfig, public IKsPropertySet {
public:
    explicit MkDsPin(MkDsFilter* filter) : filter_(filter) {}
    ~MkDsPin() {
        Disconnect();
        if (hasFormat_) FreeMT(format_);
        free(nv12_);
        if (quit_) CloseHandle(quit_);
    }

    // The pin shares the filter's lifetime (COM identity rules for pins).
    STDMETHODIMP QueryInterface(REFIID riid, void** ppv) override;
    STDMETHODIMP_(ULONG) AddRef() override;
    STDMETHODIMP_(ULONG) Release() override;

    // IPin
    STDMETHODIMP Connect(IPin* receive, const AM_MEDIA_TYPE* pmt) override;
    STDMETHODIMP ReceiveConnection(IPin*, const AM_MEDIA_TYPE*) override { return E_UNEXPECTED; }
    STDMETHODIMP Disconnect() override;
    STDMETHODIMP ConnectedTo(IPin** pin) override {
        if (!pin) return E_POINTER;
        MkLock l(&lock_);
        if (!peer_) { *pin = nullptr; return VFW_E_NOT_CONNECTED; }
        *pin = peer_;
        peer_->AddRef();
        return S_OK;
    }
    STDMETHODIMP ConnectionMediaType(AM_MEDIA_TYPE* mt) override {
        if (!mt) return E_POINTER;
        MkLock l(&lock_);
        if (!peer_) { memset(mt, 0, sizeof(*mt)); return VFW_E_NOT_CONNECTED; }
        return CopyMT(*mt, mt_);
    }
    STDMETHODIMP QueryPinInfo(PIN_INFO* info) override;
    STDMETHODIMP QueryDirection(PIN_DIRECTION* dir) override { if (!dir) return E_POINTER; *dir = PINDIR_OUTPUT; return S_OK; }
    STDMETHODIMP QueryId(LPWSTR* id) override {
        if (!id) return E_POINTER;
        *id = (LPWSTR)CoTaskMemAlloc(sizeof(kPinName));
        if (!*id) return E_OUTOFMEMORY;
        memcpy(*id, kPinName, sizeof(kPinName));
        return S_OK;
    }
    STDMETHODIMP QueryAccept(const AM_MEDIA_TYPE* mt) override { return Parse(mt, nullptr, nullptr, nullptr, nullptr) ? S_OK : S_FALSE; }
    STDMETHODIMP EnumMediaTypes(IEnumMediaTypes** e) override {
        if (!e) return E_POINTER;
        MkLock l(&lock_);
        *e = new MkEnumTypes(hasFormat_ ? &format_ : nullptr);
        return S_OK;
    }
    STDMETHODIMP QueryInternalConnections(IPin**, ULONG*) override { return E_NOTIMPL; }
    STDMETHODIMP EndOfStream() override { return E_UNEXPECTED; }
    STDMETHODIMP BeginFlush() override { return E_UNEXPECTED; }
    STDMETHODIMP EndFlush() override { return E_UNEXPECTED; }
    STDMETHODIMP NewSegment(REFERENCE_TIME, REFERENCE_TIME, double) override { return S_OK; }

    // IAMStreamConfig
    STDMETHODIMP SetFormat(AM_MEDIA_TYPE* pmt) override;
    STDMETHODIMP GetFormat(AM_MEDIA_TYPE** pmt) override {
        if (!pmt) return E_POINTER;
        MkLock l(&lock_);
        if (peer_) *pmt = CloneMT(mt_);
        else if (hasFormat_) *pmt = CloneMT(format_);
        else { AM_MEDIA_TYPE mt; BuildType(0, kDefaultInterval, mt); *pmt = CloneMT(mt); FreeMT(mt); }
        return *pmt ? S_OK : E_OUTOFMEMORY;
    }
    STDMETHODIMP GetNumberOfCapabilities(int* count, int* size) override {
        if (!count || !size) return E_POINTER;
        *count = kTypeCount;
        *size = sizeof(VIDEO_STREAM_CONFIG_CAPS);
        return S_OK;
    }
    STDMETHODIMP GetStreamCaps(int index, AM_MEDIA_TYPE** pmt, BYTE* scc) override {
        if (!pmt || !scc) return E_POINTER;
        if (index < 0 || index >= kTypeCount) return S_FALSE;
        AM_MEDIA_TYPE mt;
        BuildType(index, kDefaultInterval, mt);
        *pmt = CloneMT(mt);
        FreeMT(mt);
        const Res& r = kRes[index / 3];
        auto* c = (VIDEO_STREAM_CONFIG_CAPS*)scc;
        memset(c, 0, sizeof(*c));
        c->guid = MK_FORMAT_VideoInfo;
        c->InputSize = {r.w, r.h};
        c->MinCroppingSize = c->MaxCroppingSize = c->InputSize;
        c->CropGranularityX = c->CropGranularityY = 1;
        c->CropAlignX = c->CropAlignY = 1;
        c->MinOutputSize = c->MaxOutputSize = c->InputSize;
        c->OutputGranularityX = c->OutputGranularityY = 1;
        c->MinFrameInterval = kMinInterval;
        c->MaxFrameInterval = kMaxInterval;
        LONG bytes = r.w * r.h * kBits[index % 3] / 8;
        c->MinBitsPerSecond = (LONG)((LONGLONG)bytes * 8 * 10000000 / kMaxInterval);
        c->MaxBitsPerSecond = (LONG)((LONGLONG)bytes * 8 * 10000000 / kMinInterval);
        return *pmt ? S_OK : E_OUTOFMEMORY;
    }

    // IKsPropertySet: lets apps find our capture pin.
    STDMETHODIMP Set(REFGUID, DWORD, LPVOID, DWORD, LPVOID, DWORD) override { return E_NOTIMPL; }
    STDMETHODIMP Get(REFGUID set, DWORD id, LPVOID, DWORD, LPVOID data, DWORD cb, DWORD* returned) override {
        if (set != MK_AMPROPSETID_Pin) return kPropSetUnsupported;
        if (id != 0 /* AMPROPERTY_PIN_CATEGORY */) return kPropIdUnsupported;
        if (returned) *returned = sizeof(GUID);
        if (!data) return S_OK;
        if (cb < sizeof(GUID)) return E_UNEXPECTED;
        *(GUID*)data = MK_PINNAME_VIDEO_CAPTURE;
        return S_OK;
    }
    STDMETHODIMP QuerySupported(REFGUID set, DWORD id, DWORD* support) override {
        if (set != MK_AMPROPSETID_Pin) return kPropSetUnsupported;
        if (id != 0) return kPropIdUnsupported;
        if (support) *support = 1; /* KSPROPERTY_SUPPORT_GET */
        return S_OK;
    }

    // Streaming, driven by the filter's state changes.
    HRESULT Active();     // Stopped -> Paused: commit allocator
    void Run(REFERENCE_TIME start, IReferenceClock* clock);
    void Inactive();      // -> Stopped: stop thread, decommit
    void Halt();          // Running -> Paused: stop thread only

private:
    HRESULT TryConnect(IPin* receive, const AM_MEDIA_TYPE& mt);
    HRESULT DecideAllocator();
    static DWORD WINAPI ThreadThunk(LPVOID p);
    void Worker();
    void Deliver(BYTE* dst, LONG w, LONG h, int sub);

    MkDsFilter* filter_;
    SRWLOCK lock_ = SRWLOCK_INIT;
    IPin* peer_ = nullptr;
    IMemInputPin* input_ = nullptr;
    IMemAllocator* alloc_ = nullptr;
    AM_MEDIA_TYPE mt_ = {};
    bool hasFormat_ = false;
    AM_MEDIA_TYPE format_ = {};

    HANDLE thread_ = nullptr;
    HANDLE quit_ = CreateEventW(nullptr, TRUE, FALSE, nullptr);
    REFERENCE_TIME start_ = 0;
    IReferenceClock* clock_ = nullptr;
    MkRingReader ring_{MkRingReader::kLocalOnly};
    BYTE* nv12_ = nullptr;
    size_t nv12Size_ = 0;
    uint64_t frameNo_ = 0;
};

// ===========================================================================
// Filter
// ===========================================================================
class MkDsFilter final : public IBaseFilter, public IAMFilterMiscFlags {
public:
    MkDsFilter() : pin_(this) { InterlockedIncrement(&g_objects); }
    ~MkDsFilter() { pin_.Inactive(); SafeRelease(clock_); InterlockedDecrement(&g_objects); }

    STDMETHODIMP QueryInterface(REFIID riid, void** ppv) override {
        if (!ppv) return E_POINTER;
        if (riid == __uuidof(IUnknown) || riid == __uuidof(IPersist) || riid == __uuidof(IMediaFilter) || riid == __uuidof(IBaseFilter))
            *ppv = static_cast<IBaseFilter*>(this);
        else if (riid == __uuidof(IAMFilterMiscFlags))
            *ppv = static_cast<IAMFilterMiscFlags*>(this);
        else if (riid == __uuidof(IAMStreamConfig))  // some apps ask the filter instead of the pin
            return pin_.QueryInterface(riid, ppv);
        else { *ppv = nullptr; return E_NOINTERFACE; }
        AddRef();
        return S_OK;
    }
    STDMETHODIMP_(ULONG) AddRef() override { return InterlockedIncrement(&ref_); }
    STDMETHODIMP_(ULONG) Release() override { ULONG r = InterlockedDecrement(&ref_); if (!r) delete this; return r; }

    STDMETHODIMP GetClassID(CLSID* id) override { if (!id) return E_POINTER; *id = MK_CLSID_DShowFilter; return S_OK; }

    STDMETHODIMP Stop() override {
        MkLock l(&lock_);
        pin_.Inactive();
        state_ = State_Stopped;
        return S_OK;
    }
    STDMETHODIMP Pause() override {
        MkLock l(&lock_);
        if (state_ == State_Stopped) {
            HRESULT hr = pin_.Active();
            if (FAILED(hr)) return hr;
        } else if (state_ == State_Running) {
            pin_.Halt();
        }
        state_ = State_Paused;
        return S_OK;
    }
    STDMETHODIMP Run(REFERENCE_TIME start) override {
        MkLock l(&lock_);
        if (state_ == State_Stopped) {
            HRESULT hr = pin_.Active();
            if (FAILED(hr)) return hr;
        }
        if (state_ != State_Running) pin_.Run(start, clock_);
        state_ = State_Running;
        return S_OK;
    }
    STDMETHODIMP GetState(DWORD, FILTER_STATE* st) override {
        if (!st) return E_POINTER;
        MkLock l(&lock_);
        *st = state_;
        return state_ == State_Paused ? VFW_S_CANT_CUE : S_OK;  // live source: no data while paused
    }
    STDMETHODIMP SetSyncSource(IReferenceClock* clock) override {
        MkLock l(&lock_);
        SafeRelease(clock_);
        clock_ = clock;
        if (clock_) clock_->AddRef();
        return S_OK;
    }
    STDMETHODIMP GetSyncSource(IReferenceClock** clock) override {
        if (!clock) return E_POINTER;
        MkLock l(&lock_);
        *clock = clock_;
        if (clock_) clock_->AddRef();
        return S_OK;
    }
    STDMETHODIMP EnumPins(IEnumPins** e) override { if (!e) return E_POINTER; *e = new MkEnumPins(&pin_); return S_OK; }
    STDMETHODIMP FindPin(LPCWSTR id, IPin** pin) override {
        if (!pin) return E_POINTER;
        if (id && lstrcmpiW(id, kPinName) == 0) { *pin = &pin_; pin_.AddRef(); return S_OK; }
        *pin = nullptr;
        return VFW_E_NOT_FOUND;
    }
    STDMETHODIMP QueryFilterInfo(FILTER_INFO* info) override {
        if (!info) return E_POINTER;
        MkLock l(&lock_);
        lstrcpynW(info->achName, name_[0] ? name_ : kName, MAX_FILTER_NAME);
        info->pGraph = graph_;
        if (graph_) graph_->AddRef();
        return S_OK;
    }
    STDMETHODIMP JoinFilterGraph(IFilterGraph* graph, LPCWSTR name) override {
        MkLock l(&lock_);
        graph_ = graph;  // not AddRef'd: the graph owns us
        if (name) lstrcpynW(name_, name, MAX_FILTER_NAME); else name_[0] = 0;
        return S_OK;
    }
    STDMETHODIMP QueryVendorInfo(LPWSTR*) override { return E_NOTIMPL; }
    STDMETHODIMP_(ULONG) GetMiscFlags() override { return AM_FILTER_MISC_FLAGS_IS_SOURCE; }

    FILTER_STATE State() { return state_; }
    IFilterGraph* Graph() { return graph_; }

private:
    volatile LONG ref_ = 1;
    SRWLOCK lock_ = SRWLOCK_INIT;
    FILTER_STATE state_ = State_Stopped;
    IReferenceClock* clock_ = nullptr;
    IFilterGraph* graph_ = nullptr;
    wchar_t name_[MAX_FILTER_NAME] = {};
    MkDsPin pin_;
};

// ---- pin methods that need the filter ----

STDMETHODIMP MkDsPin::QueryInterface(REFIID riid, void** ppv) {
    if (!ppv) return E_POINTER;
    if (riid == __uuidof(IUnknown) || riid == __uuidof(IPin)) *ppv = static_cast<IPin*>(this);
    else if (riid == __uuidof(IAMStreamConfig)) *ppv = static_cast<IAMStreamConfig*>(this);
    else if (riid == __uuidof(IKsPropertySet)) *ppv = static_cast<IKsPropertySet*>(this);
    else { *ppv = nullptr; return E_NOINTERFACE; }
    AddRef();
    return S_OK;
}
STDMETHODIMP_(ULONG) MkDsPin::AddRef() { return filter_->AddRef(); }
STDMETHODIMP_(ULONG) MkDsPin::Release() { return filter_->Release(); }

STDMETHODIMP MkDsPin::QueryPinInfo(PIN_INFO* info) {
    if (!info) return E_POINTER;
    info->pFilter = static_cast<IBaseFilter*>(filter_);
    filter_->AddRef();
    info->dir = PINDIR_OUTPUT;
    lstrcpynW(info->achName, kPinName, MAX_PIN_NAME);
    return S_OK;
}

STDMETHODIMP MkDsPin::Connect(IPin* receive, const AM_MEDIA_TYPE* pmt) {
    if (!receive) return E_POINTER;
    {
        MkLock l(&lock_);
        if (peer_) return VFW_E_ALREADY_CONNECTED;
    }
    if (filter_->State() != State_Stopped) return VFW_E_NOT_STOPPED;

    // A fully specified type from the caller wins, then a SetFormat choice, then our list.
    if (pmt && pmt->majortype != GUID_NULL && pmt->subtype != GUID_NULL && pmt->formattype != GUID_NULL) {
        return Parse(pmt, nullptr, nullptr, nullptr, nullptr) ? TryConnect(receive, *pmt) : VFW_E_TYPE_NOT_ACCEPTED;
    }
    if (hasFormat_ && SUCCEEDED(TryConnect(receive, format_))) return S_OK;
    for (int i = 0; i < kTypeCount; i++) {
        AM_MEDIA_TYPE mt;
        BuildType(i, kDefaultInterval, mt);
        bool match = !pmt || ((pmt->majortype == GUID_NULL || pmt->majortype == mt.majortype) &&
                              (pmt->subtype == GUID_NULL || pmt->subtype == mt.subtype));
        HRESULT hr = match ? TryConnect(receive, mt) : E_FAIL;
        FreeMT(mt);
        if (SUCCEEDED(hr)) return S_OK;
    }
    return VFW_E_NO_ACCEPTABLE_TYPES;
}

HRESULT MkDsPin::TryConnect(IPin* receive, const AM_MEDIA_TYPE& mt) {
    HRESULT hr = receive->ReceiveConnection(this, &mt);
    if (FAILED(hr)) return hr;
    MkLock l(&lock_);
    peer_ = receive;
    peer_->AddRef();
    FreeMT(mt_);
    CopyMT(mt_, mt);
    hr = DecideAllocator();
    if (FAILED(hr)) {
        receive->Disconnect();
        SafeRelease(input_);
        SafeRelease(alloc_);
        SafeRelease(peer_);
        FreeMT(mt_);
        return hr;
    }
    LONG w = 0, h = 0;
    Parse(&mt_, &w, &h, nullptr, nullptr);
    MkLog("dshow: connected %ldx%ld", w, h);
    return S_OK;
}

HRESULT MkDsPin::DecideAllocator() {
    HRESULT hr = peer_->QueryInterface(__uuidof(IMemInputPin), (void**)&input_);
    if (FAILED(hr)) return hr;
    ALLOCATOR_PROPERTIES req = {}, got = {};
    input_->GetAllocatorRequirements(&req);
    ALLOCATOR_PROPERTIES want = req;
    want.cBuffers = req.cBuffers > 3 ? req.cBuffers : 3;
    want.cbBuffer = (LONG)mt_.lSampleSize > req.cbBuffer ? (LONG)mt_.lSampleSize : req.cbBuffer;
    if (want.cbAlign < 1) want.cbAlign = 1;

    for (int attempt = 0; attempt < 2; attempt++) {
        IMemAllocator* a = nullptr;
        hr = attempt == 0 ? input_->GetAllocator(&a)
                          : CoCreateInstance(MK_CLSID_MemoryAllocator, nullptr, CLSCTX_INPROC_SERVER, __uuidof(IMemAllocator), (void**)&a);
        if (FAILED(hr) || !a) continue;
        hr = a->SetProperties(&want, &got);
        if (SUCCEEDED(hr) && got.cbBuffer >= (LONG)mt_.lSampleSize) hr = input_->NotifyAllocator(a, FALSE);
        else if (SUCCEEDED(hr)) hr = E_FAIL;
        if (SUCCEEDED(hr)) { alloc_ = a; return S_OK; }
        a->Release();
    }
    return VFW_E_NO_ALLOCATOR;
}

STDMETHODIMP MkDsPin::Disconnect() {
    MkLock l(&lock_);
    if (!peer_) return S_FALSE;
    if (alloc_) alloc_->Decommit();
    SafeRelease(alloc_);
    SafeRelease(input_);
    SafeRelease(peer_);
    FreeMT(mt_);
    return S_OK;
}

STDMETHODIMP MkDsPin::SetFormat(AM_MEDIA_TYPE* pmt) {
    MkLock l(&lock_);
    if (!pmt) {  // reset to default
        if (hasFormat_) FreeMT(format_);
        hasFormat_ = false;
        return S_OK;
    }
    if (!Parse(pmt, nullptr, nullptr, nullptr, nullptr)) return VFW_E_INVALIDMEDIATYPE;
    if (filter_->State() != State_Stopped) return VFW_E_NOT_STOPPED;
    if (hasFormat_) FreeMT(format_);
    CopyMT(format_, *pmt);
    hasFormat_ = true;
    if (peer_) {
        // Already connected: ask the graph to reconnect with the new format.
        if (peer_->QueryAccept(pmt) != S_OK) return VFW_E_INVALIDMEDIATYPE;
        IFilterGraph* g = filter_->Graph();
        if (g) {
            ReleaseSRWLockExclusive(&lock_);
            HRESULT hr = g->Reconnect(this);
            AcquireSRWLockExclusive(&lock_);
            return hr;
        }
    }
    return S_OK;
}

HRESULT MkDsPin::Active() {
    MkLock l(&lock_);
    if (!alloc_) return S_OK;  // not connected: nothing to stream
    return alloc_->Commit();
}

void MkDsPin::Run(REFERENCE_TIME start, IReferenceClock* clock) {
    MkLock l(&lock_);
    if (!alloc_ || thread_) return;
    start_ = start;
    SafeRelease(clock_);
    clock_ = clock;
    if (clock_) clock_->AddRef();
    ResetEvent(quit_);
    thread_ = CreateThread(nullptr, 0, ThreadThunk, this, 0, nullptr);
}

void MkDsPin::Halt() {
    HANDLE t;
    {
        MkLock l(&lock_);
        t = thread_;
        thread_ = nullptr;
        SetEvent(quit_);
    }
    if (t) { WaitForSingleObject(t, 3000); CloseHandle(t); }
    ring_.SetRequested(0, 0, 0);
}

void MkDsPin::Inactive() {
    {
        MkLock l(&lock_);
        SetEvent(quit_);
        if (alloc_) alloc_->Decommit();  // unblocks a GetBuffer in the worker
    }
    Halt();
    MkLock l(&lock_);
    SafeRelease(clock_);
}

DWORD WINAPI MkDsPin::ThreadThunk(LPVOID p) {
    CoInitializeEx(nullptr, COINIT_MULTITHREADED);
    static_cast<MkDsPin*>(p)->Worker();
    CoUninitialize();
    return 0;
}

void MkDsPin::Worker() {
    LONG w = 1280, h = 720;
    int sub = 0;
    REFERENCE_TIME interval = kDefaultInterval;
    IMemAllocator* alloc;
    IMemInputPin* input;
    {
        MkLock l(&lock_);
        Parse(&mt_, &w, &h, &sub, &interval);
        alloc = alloc_;
        input = input_;
        if (!alloc || !input) return;
        alloc->AddRef();
        input->AddRef();
    }
    ring_.Open();
    UINT32 fps = (UINT32)(10000000 / interval);
    ring_.SetRequested(w, h, fps);
    MkLog("dshow: streaming %ldx%ld %s @%u", w, h, sub == 0 ? "YUY2" : sub == 1 ? "NV12" : "I420", fps);

    LONGLONG next = 0;
    REFERENCE_TIME streamTime = 0;
    MkHighResTimer hires;  // 1 ms waits instead of the default 15.6 ms tick
    for (;;) {
        // Pace like a sensor would.
        LONGLONG now = MkNow100ns();
        if (next == 0 || now - next > interval * 2) next = now;
        if (now < next) {
            if (WaitForSingleObject(quit_, (DWORD)((next - now) / 10000)) == WAIT_OBJECT_0) break;
        } else if (WaitForSingleObject(quit_, 0) == WAIT_OBJECT_0) {
            break;
        }
        next += interval;

        IMediaSample* s = nullptr;
        if (FAILED(alloc->GetBuffer(&s, nullptr, nullptr, 0)) || !s) break;  // decommitted: stopping
        BYTE* dst = nullptr;
        if (SUCCEEDED(s->GetPointer(&dst)) && s->GetSize() >= (LONG)(w * h * kBits[sub] / 8)) {
            Deliver(dst, w, h, sub);
            s->SetActualDataLength(w * h * kBits[sub] / 8);
            REFERENCE_TIME t0 = streamTime;
            if (clock_) {
                REFERENCE_TIME c = 0;
                if (SUCCEEDED(clock_->GetTime(&c))) t0 = c - start_;
            }
            REFERENCE_TIME t1 = t0 + interval;
            s->SetTime(&t0, &t1);
            s->SetSyncPoint(TRUE);
            streamTime += interval;
            HRESULT hr = input->Receive(s);
            s->Release();
            if (hr != S_OK) break;  // downstream is flushing or refused
        } else {
            s->Release();
        }
        frameNo_++;
    }
    input->Release();
    alloc->Release();
}

// Copies the latest frame (always NV12 from the ring) into the sample in the negotiated format.
void MkDsPin::Deliver(BYTE* dst, LONG w, LONG h, int sub) {
    if (sub == 1) {  // NV12: straight through
        ring_.Fill(dst, w, dst + (size_t)w * h, w, h, frameNo_);
        return;
    }
    size_t need = (size_t)w * h * 3 / 2;
    if (nv12Size_ < need) {
        free(nv12_);
        nv12_ = (BYTE*)malloc(need);
        nv12Size_ = nv12_ ? need : 0;
        if (!nv12_) return;
    }
    BYTE* y = nv12_;
    BYTE* uv = nv12_ + (size_t)w * h;
    ring_.Fill(y, w, uv, w, h, frameNo_);
    if (sub == 2) {  // I420: Y, then U plane, then V plane
        memcpy(dst, y, (size_t)w * h);
        BYTE* u = dst + (size_t)w * h;
        BYTE* v = u + (size_t)(w / 2) * (h / 2);
        for (LONG i = 0; i < (w / 2) * (h / 2); i++) { u[i] = uv[2 * i]; v[i] = uv[2 * i + 1]; }
        return;
    }
    // YUY2: Y0 U Y1 V per pixel pair; chroma rows shared by two luma rows.
    for (LONG r = 0; r < h; r++) {
        const BYTE* yr = y + (size_t)r * w;
        const BYTE* cr = uv + (size_t)(r / 2) * w;
        BYTE* o = dst + (size_t)r * w * 2;
        for (LONG c = 0; c < w; c += 2) {
            o[0] = yr[c];
            o[1] = cr[c];
            o[2] = yr[c + 1];
            o[3] = cr[c + 1];
            o += 4;
        }
    }
}

// ===========================================================================
// Creation + registration (called from exports.cpp)
// ===========================================================================
HRESULT MkCreateDsFilter(REFIID riid, void** ppv) {
    MkDsFilter* f = new MkDsFilter();
    HRESULT hr = f->QueryInterface(riid, ppv);
    f->Release();
    return hr;
}

// Adds/removes "Macawlink Camera" in the DirectShow video input device category.
HRESULT MkRegisterDsCategory(bool add) {
    IFilterMapper2* fm = nullptr;
    HRESULT hr = CoCreateInstance(MK_CLSID_FilterMapper2, nullptr, CLSCTX_INPROC_SERVER, __uuidof(IFilterMapper2), (void**)&fm);
    if (FAILED(hr)) return hr;
    if (add) {
        REGPINTYPES types[3] = {{&MK_MEDIATYPE_Video, &MK_MEDIASUBTYPE_YUY2},
                                {&MK_MEDIATYPE_Video, &MK_MEDIASUBTYPE_NV12},
                                {&MK_MEDIATYPE_Video, &MK_MEDIASUBTYPE_I420}};
        REGFILTERPINS2 pin = {};
        pin.dwFlags = REG_PINFLAG_B_OUTPUT;
        pin.cInstances = 1;
        pin.nMediaTypes = 3;
        pin.lpMediaType = types;
        pin.clsPinCategory = &MK_PINNAME_VIDEO_CAPTURE;
        REGFILTER2 rf = {};
        rf.dwVersion = 2;
        rf.dwMerit = MERIT_DO_NOT_USE;
        rf.cPins2 = 1;
        rf.rgPins2 = &pin;
        hr = fm->RegisterFilter(MK_CLSID_DShowFilter, kName, nullptr, &MK_CLSID_VideoInputDeviceCategory, kName, &rf);
    } else {
        hr = fm->UnregisterFilter(&MK_CLSID_VideoInputDeviceCategory, kName, MK_CLSID_DShowFilter);
        if (hr == HRESULT_FROM_WIN32(ERROR_FILE_NOT_FOUND)) hr = S_OK;
    }
    fm->Release();
    MkLog("dshow: %s category hr=0x%08lx", add ? "register" : "unregister", (unsigned long)hr);
    return hr;
}
