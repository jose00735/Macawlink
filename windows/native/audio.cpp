// WASAPI shared-mode render into the virtual microphone's playback side
// (Phase 1: "CABLE Input" of VB-Audio Virtual Cable).
#include "common.h"
#include <mmdeviceapi.h>
#include <audioclient.h>
#include <functiondiscoverykeys_devpkey.h>
#include <propvarutil.h>

#ifndef AUDCLNT_STREAMFLAGS_AUTOCONVERTPCM
#define AUDCLNT_STREAMFLAGS_AUTOCONVERTPCM 0x80000000
#endif
#ifndef AUDCLNT_STREAMFLAGS_SRC_DEFAULT_QUALITY
#define AUDCLNT_STREAMFLAGS_SRC_DEFAULT_QUALITY 0x08000000
#endif

static const CLSID kCLSID_MMDeviceEnumerator = {0xbcde0395, 0xe52f, 0x467c, {0x8e, 0x3d, 0xc4, 0x57, 0x92, 0x91, 0x69, 0x2e}};

namespace {
struct MkAudio {
    IAudioClient* client = nullptr;
    IAudioRenderClient* render = nullptr;
    UINT32 bufferFrames = 0;
    int channels = 1;
};

// Case-insensitive substring match against a ';'-separated list.
bool MatchesAny(const wchar_t* name, const wchar_t* list) {
    wchar_t lname[256];
    lstrcpynW(lname, name, 256);
    CharLowerW(lname);
    const wchar_t* p = list;
    while (*p) {
        wchar_t needle[128];
        int n = 0;
        while (*p && *p != L';' && n < 127) needle[n++] = *p++;
        needle[n] = 0;
        if (*p == L';') p++;
        CharLowerW(needle);
        if (n && wcsstr(lname, needle)) return true;
    }
    return false;
}
}  // namespace

// Opens the first active render endpoint whose name matches one of `names` (";"-separated,
// tried in order). PCM is s16 interleaved at sampleRate/channels; Windows converts as needed.
extern "C" __declspec(dllexport) void* __stdcall mk_audio_open(const wchar_t* names, int sampleRate, int channels,
                                                              wchar_t* chosen, int chosenCap, int* hrOut) {
    MkEnsureCom();
    HRESULT hr;
    IMMDeviceEnumerator* en = nullptr;
    IMMDeviceCollection* col = nullptr;
    IMMDevice* pick = nullptr;
    hr = CoCreateInstance(kCLSID_MMDeviceEnumerator, nullptr, CLSCTX_ALL, __uuidof(IMMDeviceEnumerator), (void**)&en);
    if (SUCCEEDED(hr)) hr = en->EnumAudioEndpoints(eRender, DEVICE_STATE_ACTIVE, &col);
    UINT count = 0;
    if (SUCCEEDED(hr)) col->GetCount(&count);

    // Respect preference order: iterate names, then devices.
    const wchar_t* p = names ? names : L"";
    while (SUCCEEDED(hr) && !pick && *p) {
        wchar_t one[128];
        int n = 0;
        while (*p && *p != L';' && n < 127) one[n++] = *p++;
        one[n] = 0;
        if (*p == L';') p++;
        for (UINT i = 0; i < count && !pick; i++) {
            IMMDevice* dev = nullptr;
            IPropertyStore* ps = nullptr;
            if (FAILED(col->Item(i, &dev))) continue;
            if (SUCCEEDED(dev->OpenPropertyStore(STGM_READ, &ps))) {
                PROPVARIANT v;
                PropVariantInit(&v);
                if (SUCCEEDED(ps->GetValue(PKEY_Device_FriendlyName, &v)) && v.vt == VT_LPWSTR && MatchesAny(v.pwszVal, one)) {
                    pick = dev;
                    dev->AddRef();
                    if (chosen && chosenCap > 0) lstrcpynW(chosen, v.pwszVal, chosenCap);
                }
                PropVariantClear(&v);
                ps->Release();
            }
            dev->Release();
        }
    }
    SafeRelease(col);
    SafeRelease(en);
    if (SUCCEEDED(hr) && !pick) hr = HRESULT_FROM_WIN32(ERROR_NOT_FOUND);

    MkAudio* a = nullptr;
    if (SUCCEEDED(hr)) {
        a = new MkAudio();
        a->channels = channels;
        hr = pick->Activate(__uuidof(IAudioClient), CLSCTX_ALL, nullptr, (void**)&a->client);
    }
    SafeRelease(pick);
    if (SUCCEEDED(hr)) {
        WAVEFORMATEX wf = {};
        wf.wFormatTag = WAVE_FORMAT_PCM;
        wf.nChannels = (WORD)channels;
        wf.nSamplesPerSec = (DWORD)sampleRate;
        wf.wBitsPerSample = 16;
        wf.nBlockAlign = (WORD)(channels * 2);
        wf.nAvgBytesPerSec = wf.nSamplesPerSec * wf.nBlockAlign;
        hr = a->client->Initialize(AUDCLNT_SHAREMODE_SHARED,
                                   AUDCLNT_STREAMFLAGS_AUTOCONVERTPCM | AUDCLNT_STREAMFLAGS_SRC_DEFAULT_QUALITY,
                                   2000000 /* 200 ms */, 0, &wf, nullptr);
    }
    if (SUCCEEDED(hr)) hr = a->client->GetBufferSize(&a->bufferFrames);
    if (SUCCEEDED(hr)) hr = a->client->GetService(__uuidof(IAudioRenderClient), (void**)&a->render);
    if (SUCCEEDED(hr)) hr = a->client->Start();
    if (hrOut) *hrOut = hr;
    MkLog("audio: open %d Hz x%d hr=0x%08lx", sampleRate, channels, (unsigned long)hr);
    if (FAILED(hr)) {
        if (a) { SafeRelease(a->render); SafeRelease(a->client); delete a; }
        return nullptr;
    }
    return a;
}

// Writes up to `frames` frames; returns frames accepted (drops the rest to keep latency low)
// or a negative HRESULT.
extern "C" __declspec(dllexport) int __stdcall mk_audio_write(void* h, const short* pcm, int frames) {
    MkAudio* a = (MkAudio*)h;
    if (!a || !pcm || frames <= 0) return 0;
    UINT32 padding = 0;
    HRESULT hr = a->client->GetCurrentPadding(&padding);
    if (FAILED(hr)) return hr;
    UINT32 room = a->bufferFrames - padding;
    UINT32 n = (UINT32)frames < room ? (UINT32)frames : room;
    if (n == 0) return 0;
    BYTE* dst = nullptr;
    hr = a->render->GetBuffer(n, &dst);
    if (FAILED(hr)) return hr;
    memcpy(dst, pcm, (size_t)n * a->channels * 2);
    hr = a->render->ReleaseBuffer(n, 0);
    return FAILED(hr) ? hr : (int)n;
}

// Frames currently queued in the endpoint buffer (our latency).
extern "C" __declspec(dllexport) int __stdcall mk_audio_queued(void* h) {
    MkAudio* a = (MkAudio*)h;
    if (!a) return 0;
    UINT32 padding = 0;
    return SUCCEEDED(a->client->GetCurrentPadding(&padding)) ? (int)padding : 0;
}

extern "C" __declspec(dllexport) void __stdcall mk_audio_close(void* h) {
    MkAudio* a = (MkAudio*)h;
    if (!a) return;
    a->client->Stop();
    SafeRelease(a->render);
    SafeRelease(a->client);
    delete a;
}
