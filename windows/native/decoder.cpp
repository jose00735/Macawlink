// H.264 / HEVC decoding with the Media Foundation decoders that ship with Windows.
// Exposed as a tiny C API so the Go receiver can call it via syscall (no cgo).
#include "common.h"
#include <codecapi.h>

namespace {

struct MkDecoder {
    IMFTransform* mft = nullptr;
    UINT32 width = 0, height = 0;      // display size
    UINT32 codedH = 0;                 // buffer rows (may be 1088 for 1080p)
    LONG stride = 0;
    bool providesSamples = false;
    DWORD outSize = 0;
};

HRESULT SetNv12Output(MkDecoder* d) {
    for (DWORD i = 0;; i++) {
        IMFMediaType* t = nullptr;
        HRESULT hr = d->mft->GetOutputAvailableType(0, i, &t);
        if (FAILED(hr)) return hr;
        GUID sub = GUID_NULL;
        t->GetGUID(MF_MT_SUBTYPE, &sub);
        if (sub == MFVideoFormat_NV12) {
            hr = d->mft->SetOutputType(0, t, 0);
            if (SUCCEEDED(hr)) {
                UINT32 w = 0, h = 0;
                MFGetAttributeSize(t, MF_MT_FRAME_SIZE, &w, &h);
                d->codedH = h;
                d->width = w;
                d->height = h;
                MFVideoArea area;
                if (SUCCEEDED(t->GetBlob(MF_MT_MINIMUM_DISPLAY_APERTURE, (UINT8*)&area, sizeof(area), nullptr))) {
                    if (area.Area.cx > 0 && area.Area.cy > 0) { d->width = area.Area.cx; d->height = area.Area.cy; }
                }
                UINT32 s = 0;
                d->stride = SUCCEEDED(t->GetUINT32(MF_MT_DEFAULT_STRIDE, &s)) ? (LONG)s : (LONG)w;
                MFT_OUTPUT_STREAM_INFO info = {};
                d->mft->GetOutputStreamInfo(0, &info);
                d->providesSamples = (info.dwFlags & (MFT_OUTPUT_STREAM_PROVIDES_SAMPLES | MFT_OUTPUT_STREAM_CAN_PROVIDE_SAMPLES)) != 0;
                d->outSize = info.cbSize ? info.cbSize : w * h * 3 / 2;
            }
            t->Release();
            return hr;
        }
        t->Release();
    }
}

}  // namespace

// codec: 0 = H.264, 1 = HEVC. Returns a handle or null; *hrOut gets the HRESULT.
extern "C" __declspec(dllexport) void* __stdcall mk_dec_create(int codec, int* hrOut) {
    MkEnsureCom();
    MkDecoder* d = new MkDecoder();
    const GUID& clsid = codec == 1 ? MK_CLSID_MSH265DecoderMFT : MK_CLSID_MSH264DecoderMFT;
    HRESULT hr = CoCreateInstance(clsid, nullptr, CLSCTX_INPROC_SERVER, __uuidof(IMFTransform), (void**)&d->mft);
    if (SUCCEEDED(hr)) {
        IMFAttributes* a = nullptr;
        if (SUCCEEDED(d->mft->GetAttributes(&a))) {
            a->SetUINT32(CODECAPI_AVLowLatencyMode, TRUE);
            a->Release();
        }
        IMFMediaType* in = nullptr;
        hr = MFCreateMediaType(&in);
        if (SUCCEEDED(hr)) {
            in->SetGUID(MF_MT_MAJOR_TYPE, MFMediaType_Video);
            in->SetGUID(MF_MT_SUBTYPE, codec == 1 ? MFVideoFormat_HEVC : MFVideoFormat_H264);
            in->SetUINT32(MF_MT_INTERLACE_MODE, MFVideoInterlace_Progressive);
            hr = d->mft->SetInputType(0, in, 0);
            in->Release();
        }
    }
    if (SUCCEEDED(hr)) hr = SetNv12Output(d);
    if (SUCCEEDED(hr)) {
        d->mft->ProcessMessage(MFT_MESSAGE_NOTIFY_BEGIN_STREAMING, 0);
        d->mft->ProcessMessage(MFT_MESSAGE_NOTIFY_START_OF_STREAM, 0);
    }
    if (hrOut) *hrOut = hr;
    if (FAILED(hr)) {
        MkLog("decoder: create codec=%d failed 0x%08lx", codec, (unsigned long)hr);
        SafeRelease(d->mft);
        delete d;
        return nullptr;
    }
    return d;
}

// Feeds one Annex-B access unit. Returns 0 on success, 1 if the decoder is full
// (call mk_dec_pull until it returns 0, then push again), or a negative HRESULT.
extern "C" __declspec(dllexport) int __stdcall mk_dec_push(void* h, const BYTE* data, int len, long long pts100ns) {
    MkEnsureCom();
    MkDecoder* d = (MkDecoder*)h;
    if (!d || !data || len <= 0) return E_INVALIDARG;
    IMFSample* s = nullptr;
    IMFMediaBuffer* b = nullptr;
    HRESULT hr = MFCreateSample(&s);
    if (SUCCEEDED(hr)) hr = MFCreateMemoryBuffer(len, &b);
    if (SUCCEEDED(hr)) {
        BYTE* p = nullptr;
        hr = b->Lock(&p, nullptr, nullptr);
        if (SUCCEEDED(hr)) {
            memcpy(p, data, len);
            b->Unlock();
            b->SetCurrentLength(len);
            hr = s->AddBuffer(b);
        }
    }
    if (SUCCEEDED(hr)) {
        s->SetSampleTime(pts100ns);
        hr = d->mft->ProcessInput(0, s, 0);
    }
    SafeRelease(b);
    SafeRelease(s);
    if (hr == MF_E_NOTACCEPTING) return 1;
    return FAILED(hr) ? hr : 0;
}

// Pulls one decoded frame as tight NV12 into out (cap bytes). Returns 1 with a frame,
// 0 when more input is needed, or a negative HRESULT. outW/outH/outPts are filled on 1.
extern "C" __declspec(dllexport) int __stdcall mk_dec_pull(void* h, BYTE* out, int cap, int* outW, int* outH,
                                                           long long* outPts) {
    MkEnsureCom();
    MkDecoder* d = (MkDecoder*)h;
    if (!d) return E_INVALIDARG;
    for (int attempt = 0; attempt < 4; attempt++) {
        MFT_OUTPUT_DATA_BUFFER ob = {};
        IMFSample* mine = nullptr;
        if (!d->providesSamples) {
            IMFMediaBuffer* b = nullptr;
            MFCreateSample(&mine);
            MFCreateMemoryBuffer(d->outSize, &b);
            mine->AddBuffer(b);
            b->Release();
            ob.pSample = mine;
        }
        DWORD status = 0;
        HRESULT hr = d->mft->ProcessOutput(0, 1, &ob, &status);
        if (ob.pEvents) ob.pEvents->Release();
        if (hr == MF_E_TRANSFORM_NEED_MORE_INPUT) { SafeRelease(mine); return 0; }
        if (hr == MF_E_TRANSFORM_STREAM_CHANGE) {
            SafeRelease(mine);
            hr = SetNv12Output(d);
            MkLog("decoder: stream change -> %ux%u stride %ld hr=0x%08lx", d->width, d->height, d->stride, (unsigned long)hr);
            if (FAILED(hr)) return hr;
            continue;
        }
        if (FAILED(hr)) { SafeRelease(mine); if (ob.pSample && ob.pSample != mine) ob.pSample->Release(); return hr; }

        IMFSample* s = ob.pSample;
        int need = (int)(d->width * d->height * 3 / 2);
        int result = 1;
        if (need > cap) {
            result = E_NOT_SUFFICIENT_BUFFER;
        } else {
            IMFMediaBuffer* buf = nullptr;
            s->ConvertToContiguousBuffer(&buf);
            IMF2DBuffer* b2 = nullptr;
            BYTE* src = nullptr;
            LONG pitch = d->stride;
            bool locked2d = false;
            if (buf && SUCCEEDED(buf->QueryInterface(__uuidof(IMF2DBuffer), (void**)&b2)) && SUCCEEDED(b2->Lock2D(&src, &pitch))) {
                locked2d = true;
            } else if (buf) {
                DWORD len = 0;
                buf->Lock(&src, nullptr, &len);
            }
            if (src) {
                for (UINT32 r = 0; r < d->height; r++) memcpy(out + (size_t)r * d->width, src + (size_t)r * pitch, d->width);
                const BYTE* suv = src + (size_t)pitch * d->codedH;
                BYTE* duv = out + (size_t)d->width * d->height;
                for (UINT32 r = 0; r < d->height / 2; r++) memcpy(duv + (size_t)r * d->width, suv + (size_t)r * pitch, d->width);
                if (locked2d) b2->Unlock2D(); else buf->Unlock();
            } else {
                result = E_FAIL;
            }
            SafeRelease(b2);
            SafeRelease(buf);
            LONGLONG t = 0;
            s->GetSampleTime(&t);
            if (outW) *outW = (int)d->width;
            if (outH) *outH = (int)d->height;
            if (outPts) *outPts = t;
        }
        if (ob.pSample != mine) ob.pSample->Release();
        SafeRelease(mine);
        return result;
    }
    return E_FAIL;
}

extern "C" __declspec(dllexport) void __stdcall mk_dec_destroy(void* h) {
    MkDecoder* d = (MkDecoder*)h;
    if (!d) return;
    if (d->mft) {
        d->mft->ProcessMessage(MFT_MESSAGE_NOTIFY_END_OF_STREAM, 0);
        d->mft->ProcessMessage(MFT_MESSAGE_COMMAND_FLUSH, 0);
    }
    SafeRelease(d->mft);
    delete d;
}
