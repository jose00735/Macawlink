// DLL plumbing: COM class factory + IMFActivate for Frame Server, self-registration,
// virtual camera lifetime (called from the Go receiver), logging and GUID definitions.
#include "common.h"
#include "vcam_source.h"
#include <stdio.h>
#include <stdarg.h>
#include <shlobj.h>

// ---- GUIDs ----------------------------------------------------------------
const GUID MK_IID_IMFMediaStream2 = {0xc5bc37d6, 0x75c7, 0x46a1, {0xa1, 0x32, 0x81, 0xb5, 0xf7, 0x23, 0xc2, 0x0f}};
const GUID MK_IID_IMFSampleAllocatorControl = {0xda62b958, 0x3a38, 0x4a97, {0xbd, 0x27, 0x14, 0x9c, 0x64, 0x0c, 0x07, 0x71}};
const GUID MK_IID_IMFVirtualCamera = {0x1c08a864, 0xef6c, 0x4c75, {0xaf, 0x59, 0x5f, 0x2d, 0x68, 0xda, 0x95, 0x63}};
const GUID MK_IID_IKsControl = {0x28f54685, 0x06fd, 0x11d2, {0xb2, 0x7a, 0x00, 0xa0, 0xc9, 0x22, 0x31, 0x96}};
const GUID MK_MF_DEVICESTREAM_STREAM_CATEGORY = {0x2939e7b8, 0xa62e, 0x4579, {0xb6, 0x74, 0xd4, 0x07, 0x3d, 0xfa, 0xbb, 0xba}};
const GUID MK_MF_DEVICESTREAM_STREAM_ID = {0x11bd5120, 0xd124, 0x446b, {0x88, 0xe6, 0x17, 0x06, 0x02, 0x57, 0xff, 0xf9}};
const GUID MK_MF_DEVICESTREAM_FRAMESERVER_SHARED = {0x1cb378e9, 0xb279, 0x41d4, {0xaf, 0x97, 0x34, 0xa2, 0x43, 0xe6, 0x83, 0x20}};
const GUID MK_MF_DEVICESTREAM_ATTRIBUTE_FRAMESOURCE_TYPES = {0x17145fd1, 0x1b2b, 0x423c, {0x80, 0x01, 0x2b, 0x68, 0x33, 0xed, 0x35, 0x88}};
const GUID MK_PINNAME_VIDEO_CAPTURE = {0xfb6c4281, 0x0353, 0x11d1, {0x90, 0x5f, 0x00, 0x00, 0xc0, 0xcc, 0x16, 0xba}};
const GUID MK_CLSID_MSH264DecoderMFT = {0x62ce7e72, 0x4c71, 0x4d20, {0xb1, 0x5d, 0x45, 0x28, 0x31, 0xa8, 0x7d, 0x9d}};
const GUID MK_CLSID_MSH265DecoderMFT = {0x420a51a3, 0xd605, 0x430c, {0xb4, 0xfc, 0x45, 0x27, 0x4f, 0xa6, 0xc5, 0x62}};
const GUID MK_CLSID_MediaSource = {0x6e0d1a3b, 0x6f2c, 0x4c1e, {0x9b, 0x7a, 0x4d, 0x61, 0x43, 0x41, 0x57, 0xb1}};

HMODULE g_module = nullptr;
volatile LONG g_objects = 0;
static volatile LONG g_mfStarted = 0;
static SRWLOCK g_logLock = SRWLOCK_INIT;

BOOL WINAPI DllMain(HINSTANCE inst, DWORD reason, LPVOID) {
    if (reason == DLL_PROCESS_ATTACH) {
        g_module = inst;
        DisableThreadLibraryCalls(inst);
    }
    return TRUE;
}

void MkEnsureCom() {
    CoInitializeEx(nullptr, COINIT_MULTITHREADED);  // S_FALSE / RPC_E_CHANGED_MODE are fine
    if (InterlockedCompareExchange(&g_mfStarted, 1, 0) == 0) MFStartup(MF_VERSION, MFSTARTUP_LITE);
}

// Appends to %ProgramData%\Macawlink\native.log (best effort; Frame Server can write there
// because the installer creates the folder with Users/LOCAL SERVICE modify rights).
void MkLog(const char* fmt, ...) {
    char msg[512];
    va_list ap;
    va_start(ap, fmt);
    vsnprintf(msg, sizeof(msg), fmt, ap);
    va_end(ap);
    char line[600];
    SYSTEMTIME st;
    GetLocalTime(&st);
    snprintf(line, sizeof(line), "%02d:%02d:%02d.%03d [%lu] %s\r\n", st.wHour, st.wMinute, st.wSecond,
             st.wMilliseconds, GetCurrentProcessId(), msg);
    OutputDebugStringA(line);

    MkLock l(&g_logLock);
    wchar_t path[MAX_PATH];
    DWORD n = GetEnvironmentVariableW(L"ProgramData", path, MAX_PATH);
    if (!n || n > MAX_PATH - 40) return;
    wcscat(path, L"\\Macawlink\\native.log");
    HANDLE f = CreateFileW(path, FILE_APPEND_DATA, FILE_SHARE_READ | FILE_SHARE_WRITE, nullptr, OPEN_ALWAYS,
                           FILE_ATTRIBUTE_NORMAL, nullptr);
    if (f == INVALID_HANDLE_VALUE) return;
    LARGE_INTEGER size;
    if (GetFileSizeEx(f, &size) && size.QuadPart > 2 * 1024 * 1024) {
        CloseHandle(f);
        f = CreateFileW(path, GENERIC_WRITE, FILE_SHARE_READ, nullptr, CREATE_ALWAYS, FILE_ATTRIBUTE_NORMAL, nullptr);
        if (f == INVALID_HANDLE_VALUE) return;
    }
    DWORD written;
    WriteFile(f, line, (DWORD)strlen(line), &written, nullptr);
    CloseHandle(f);
}

// ===========================================================================
// IMFActivate wrapper: what Frame Server instantiates from our CLSID.
// ===========================================================================
class MkActivate final : public IMFActivate {
public:
    MkActivate() { InterlockedIncrement(&g_objects); MFCreateAttributes(&attrs_, 1); }
    ~MkActivate() { SafeRelease(source_); SafeRelease(attrs_); InterlockedDecrement(&g_objects); }

    STDMETHODIMP QueryInterface(REFIID riid, void** ppv) override {
        if (!ppv) return E_POINTER;
        if (riid == __uuidof(IUnknown) || riid == __uuidof(IMFAttributes) || riid == __uuidof(IMFActivate)) {
            *ppv = static_cast<IMFActivate*>(this);
            AddRef();
            return S_OK;
        }
        *ppv = nullptr;
        return E_NOINTERFACE;
    }
    STDMETHODIMP_(ULONG) AddRef() override { return InterlockedIncrement(&ref_); }
    STDMETHODIMP_(ULONG) Release() override {
        ULONG r = InterlockedDecrement(&ref_);
        if (r == 0) delete this;
        return r;
    }

    STDMETHODIMP ActivateObject(REFIID riid, void** ppv) override {
        MkEnsureCom();
        MkLock l(&lock_);
        if (!source_) {
            HRESULT hr = MkSource::Create(&source_);
            if (FAILED(hr)) { MkLog("activate: create source failed 0x%08lx", (unsigned long)hr); return hr; }
            // Pass through any configuration the pipeline put on us.
            IMFAttributes* sa = nullptr;
            if (SUCCEEDED(source_->GetSourceAttributes(&sa))) { attrs_->CopyAllItems(sa); sa->Release(); }
        }
        MkLog("activate: ActivateObject");
        return source_->QueryInterface(riid, ppv);
    }
    STDMETHODIMP ShutdownObject() override {
        MkLock l(&lock_);
        if (source_) { source_->Shutdown(); SafeRelease(source_); }
        return S_OK;
    }
    STDMETHODIMP DetachObject() override {
        MkLock l(&lock_);
        SafeRelease(source_);
        return S_OK;
    }

    // IMFAttributes: delegate to the inner store.
    STDMETHODIMP GetItem(REFGUID k, PROPVARIANT* v) override { return attrs_->GetItem(k, v); }
    STDMETHODIMP GetItemType(REFGUID k, MF_ATTRIBUTE_TYPE* t) override { return attrs_->GetItemType(k, t); }
    STDMETHODIMP CompareItem(REFGUID k, REFPROPVARIANT v, BOOL* r) override { return attrs_->CompareItem(k, v, r); }
    STDMETHODIMP Compare(IMFAttributes* o, MF_ATTRIBUTES_MATCH_TYPE m, BOOL* r) override { return attrs_->Compare(o, m, r); }
    STDMETHODIMP GetUINT32(REFGUID k, UINT32* v) override { return attrs_->GetUINT32(k, v); }
    STDMETHODIMP GetUINT64(REFGUID k, UINT64* v) override { return attrs_->GetUINT64(k, v); }
    STDMETHODIMP GetDouble(REFGUID k, double* v) override { return attrs_->GetDouble(k, v); }
    STDMETHODIMP GetGUID(REFGUID k, GUID* v) override { return attrs_->GetGUID(k, v); }
    STDMETHODIMP GetStringLength(REFGUID k, UINT32* n) override { return attrs_->GetStringLength(k, n); }
    STDMETHODIMP GetString(REFGUID k, LPWSTR s, UINT32 n, UINT32* l) override { return attrs_->GetString(k, s, n, l); }
    STDMETHODIMP GetAllocatedString(REFGUID k, LPWSTR* s, UINT32* l) override { return attrs_->GetAllocatedString(k, s, l); }
    STDMETHODIMP GetBlobSize(REFGUID k, UINT32* n) override { return attrs_->GetBlobSize(k, n); }
    STDMETHODIMP GetBlob(REFGUID k, UINT8* b, UINT32 n, UINT32* l) override { return attrs_->GetBlob(k, b, n, l); }
    STDMETHODIMP GetAllocatedBlob(REFGUID k, UINT8** b, UINT32* n) override { return attrs_->GetAllocatedBlob(k, b, n); }
    STDMETHODIMP GetUnknown(REFGUID k, REFIID r, LPVOID* p) override { return attrs_->GetUnknown(k, r, p); }
    STDMETHODIMP SetItem(REFGUID k, REFPROPVARIANT v) override { return attrs_->SetItem(k, v); }
    STDMETHODIMP DeleteItem(REFGUID k) override { return attrs_->DeleteItem(k); }
    STDMETHODIMP DeleteAllItems() override { return attrs_->DeleteAllItems(); }
    STDMETHODIMP SetUINT32(REFGUID k, UINT32 v) override { return attrs_->SetUINT32(k, v); }
    STDMETHODIMP SetUINT64(REFGUID k, UINT64 v) override { return attrs_->SetUINT64(k, v); }
    STDMETHODIMP SetDouble(REFGUID k, double v) override { return attrs_->SetDouble(k, v); }
    STDMETHODIMP SetGUID(REFGUID k, REFGUID v) override { return attrs_->SetGUID(k, v); }
    STDMETHODIMP SetString(REFGUID k, LPCWSTR v) override { return attrs_->SetString(k, v); }
    STDMETHODIMP SetBlob(REFGUID k, const UINT8* b, UINT32 n) override { return attrs_->SetBlob(k, b, n); }
    STDMETHODIMP SetUnknown(REFGUID k, IUnknown* u) override { return attrs_->SetUnknown(k, u); }
    STDMETHODIMP LockStore() override { return attrs_->LockStore(); }
    STDMETHODIMP UnlockStore() override { return attrs_->UnlockStore(); }
    STDMETHODIMP GetCount(UINT32* n) override { return attrs_->GetCount(n); }
    STDMETHODIMP GetItemByIndex(UINT32 i, GUID* k, PROPVARIANT* v) override { return attrs_->GetItemByIndex(i, k, v); }
    STDMETHODIMP CopyAllItems(IMFAttributes* d) override { return attrs_->CopyAllItems(d); }

private:
    volatile LONG ref_ = 1;
    SRWLOCK lock_ = SRWLOCK_INIT;
    IMFAttributes* attrs_ = nullptr;
    MkSource* source_ = nullptr;
};

HRESULT MkCreateDsFilter(REFIID riid, void** ppv);
HRESULT MkRegisterDsCategory(bool add);
extern const GUID MK_CLSID_DShowFilter;
extern "C" __declspec(dllexport) int __stdcall mk_vcam_supported();

static HRESULT CreateActivate(REFIID riid, void** ppv) {
    MkActivate* a = new MkActivate();
    HRESULT hr = a->QueryInterface(riid, ppv);
    a->Release();
    return hr;
}

class MkFactory final : public IClassFactory {
public:
    explicit MkFactory(HRESULT (*create)(REFIID, void**)) : create_(create) {}
    STDMETHODIMP QueryInterface(REFIID riid, void** ppv) override {
        if (!ppv) return E_POINTER;
        if (riid == __uuidof(IUnknown) || riid == __uuidof(IClassFactory)) {
            *ppv = static_cast<IClassFactory*>(this);
            AddRef();
            return S_OK;
        }
        *ppv = nullptr;
        return E_NOINTERFACE;
    }
    STDMETHODIMP_(ULONG) AddRef() override { return 2; }   // static instance
    STDMETHODIMP_(ULONG) Release() override { return 1; }
    STDMETHODIMP CreateInstance(IUnknown* outer, REFIID riid, void** ppv) override {
        if (outer) return CLASS_E_NOAGGREGATION;
        MkEnsureCom();
        return create_(riid, ppv);
    }
    STDMETHODIMP LockServer(BOOL lock) override {
        if (lock) InterlockedIncrement(&g_objects); else InterlockedDecrement(&g_objects);
        return S_OK;
    }

private:
    HRESULT (*create_)(REFIID, void**);
};
static MkFactory g_mfFactory(CreateActivate);
static MkFactory g_dsFactory(MkCreateDsFilter);

extern "C" HRESULT __stdcall DllGetClassObject(REFCLSID clsid, REFIID riid, void** ppv) {
    if (clsid == MK_CLSID_MediaSource) return g_mfFactory.QueryInterface(riid, ppv);
    if (clsid == MK_CLSID_DShowFilter) return g_dsFactory.QueryInterface(riid, ppv);
    return CLASS_E_CLASSNOTAVAILABLE;
}

extern "C" HRESULT __stdcall DllCanUnloadNow() { return g_objects == 0 ? S_OK : S_FALSE; }

// HKLM registration: Frame Server runs as LOCAL SERVICE and cannot see the user's HKCU.
// A 32-bit build registered by SysWOW64\regsvr32 lands in the 32-bit view automatically.
#define MK_DSHOW_CLSID_STRING L"{6E0D1A3B-6F2C-4C1E-9B7A-4D61434157B2}"

static HRESULT RegisterClass(const wchar_t* clsid, const wchar_t* name, const wchar_t* path) {
    wchar_t key[128] = L"SOFTWARE\\Classes\\CLSID\\";
    wcscat(key, clsid);
    HKEY k = nullptr, inproc = nullptr;
    LONG r = RegCreateKeyExW(HKEY_LOCAL_MACHINE, key, 0, nullptr, 0, KEY_WRITE, nullptr, &k, nullptr);
    if (r != ERROR_SUCCESS) return HRESULT_FROM_WIN32(r);
    RegSetValueExW(k, nullptr, 0, REG_SZ, (const BYTE*)name, (DWORD)((wcslen(name) + 1) * 2));
    r = RegCreateKeyExW(k, L"InprocServer32", 0, nullptr, 0, KEY_WRITE, nullptr, &inproc, nullptr);
    if (r == ERROR_SUCCESS) {
        const wchar_t* model = L"Both";
        RegSetValueExW(inproc, nullptr, 0, REG_SZ, (const BYTE*)path, (DWORD)((wcslen(path) + 1) * 2));
        RegSetValueExW(inproc, L"ThreadingModel", 0, REG_SZ, (const BYTE*)model, (DWORD)((wcslen(model) + 1) * 2));
        RegCloseKey(inproc);
    }
    RegCloseKey(k);
    return HRESULT_FROM_WIN32(r);
}

static void UnregisterClass(const wchar_t* clsid) {
    wchar_t key[128] = L"SOFTWARE\\Classes\\CLSID\\";
    wcscat(key, clsid);
    RegDeleteTreeW(HKEY_LOCAL_MACHINE, key);
}

// Registers both camera front-ends. The DirectShow device only goes into the video input
// category when Windows lacks MF virtual cameras (Windows 10), so Windows 11 apps don't
// see two "Macawlink Camera" entries.
extern "C" HRESULT __stdcall DllRegisterServer() {
    MkEnsureCom();
    wchar_t path[MAX_PATH];
    if (!GetModuleFileNameW(g_module, path, MAX_PATH)) return HRESULT_FROM_WIN32(GetLastError());
    HRESULT hr = RegisterClass(MK_CLSID_STRING, L"Macawlink Camera Source", path);
    if (FAILED(hr)) return hr;
    hr = RegisterClass(MK_DSHOW_CLSID_STRING, L"Macawlink Camera", path);
    if (FAILED(hr)) return hr;
    if (mk_vcam_supported()) return MkRegisterDsCategory(false);
    return MkRegisterDsCategory(true);
}

extern "C" HRESULT __stdcall DllUnregisterServer() {
    MkEnsureCom();
    MkRegisterDsCategory(false);
    UnregisterClass(MK_CLSID_STRING);
    UnregisterClass(MK_DSHOW_CLSID_STRING);
    return S_OK;
}

// Which camera front-end this PC uses: 2 = Media Foundation virtual camera (Win11),
// 1 = DirectShow filter (Win10), 0 = neither registered.
extern "C" __declspec(dllexport) int __stdcall mk_camera_mode() {
    HKEY k = nullptr;
    if (mk_vcam_supported()) return 2;
    if (RegOpenKeyExW(HKEY_CLASSES_ROOT,
            L"CLSID\\{860BB310-5D01-11d0-BD3B-00A0C911CE86}\\Instance\\Macawlink Camera", 0, KEY_READ, &k) == ERROR_SUCCESS) {
        RegCloseKey(k);
        return 1;
    }
    return 0;
}

// ===========================================================================
// Virtual camera lifetime, driven by macawlink.exe
// ===========================================================================
static IMFVirtualCameraMk* g_vcam = nullptr;

extern "C" __declspec(dllexport) int __stdcall mk_version() { return 1; }

// 1 if this Windows build supports software virtual cameras (Windows 11 22000+).
extern "C" __declspec(dllexport) int __stdcall mk_vcam_supported() {
    HMODULE m = LoadLibraryW(L"mfsensorgroup.dll");
    if (!m) return 0;
    typedef HRESULT(WINAPI * Fn)(int, BOOL*);
    Fn fn = (Fn)(void*)GetProcAddress(m, "MFIsVirtualCameraTypeSupported");
    BOOL ok = FALSE;
    if (!fn || FAILED(fn(0, &ok))) ok = FALSE;
    return ok ? 1 : 0;
}

// Registers and starts "Macawlink Camera" for this session. Returns an HRESULT.
extern "C" __declspec(dllexport) int __stdcall mk_vcam_start(const wchar_t* friendlyName) {
    MkEnsureCom();
    if (g_vcam) return S_OK;
    HMODULE m = LoadLibraryW(L"mfsensorgroup.dll");
    if (!m) return HRESULT_FROM_WIN32(GetLastError());
    auto create = (PFN_MFCreateVirtualCamera)(void*)GetProcAddress(m, "MFCreateVirtualCamera");
    if (!create) return E_NOTIMPL;
    HRESULT hr = create(0 /*SoftwareCameraSource*/, 0 /*Session*/, 0 /*CurrentUser*/,
                        friendlyName ? friendlyName : L"Macawlink Camera", MK_CLSID_STRING, nullptr, 0, &g_vcam);
    if (SUCCEEDED(hr)) {
        hr = g_vcam->Start(nullptr);
        if (FAILED(hr)) SafeRelease(g_vcam);
    }
    MkLog("vcam: start hr=0x%08lx", (unsigned long)hr);
    return hr;
}

extern "C" __declspec(dllexport) void __stdcall mk_vcam_stop() {
    MkEnsureCom();
    if (!g_vcam) return;
    g_vcam->Stop();
    g_vcam->Shutdown();
    SafeRelease(g_vcam);
    MkLog("vcam: stopped");
}
