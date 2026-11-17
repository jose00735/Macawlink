//go:build windows

package native

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

var (
	dll            *syscall.LazyDLL
	pVersion       *syscall.LazyProc
	pVcamSupported *syscall.LazyProc
	pVcamStart     *syscall.LazyProc
	pVcamStop      *syscall.LazyProc
	pDecCreate     *syscall.LazyProc
	pDecPush       *syscall.LazyProc
	pDecPull       *syscall.LazyProc
	pDecDestroy    *syscall.LazyProc
	pAudOpen       *syscall.LazyProc
	pAudWrite      *syscall.LazyProc
	pAudQueued     *syscall.LazyProc
	pAudClose      *syscall.LazyProc
	pRegister      *syscall.LazyProc
	pCameraMode    *syscall.LazyProc
	pUnregister    *syscall.LazyProc
)

// DLLName is the file shipped next to macawlink.exe.
const DLLName = "macawlink_native.dll"

// Load binds the DLL from dir (normally the executable's folder).
func Load(dir string) error {
	path := filepath.Join(dir, DLLName)
	if _, err := os.Stat(path); err != nil {
		return err
	}
	dll = syscall.NewLazyDLL(path)
	if err := dll.Load(); err != nil {
		return err
	}
	p := dll.NewProc
	pVersion, pVcamSupported, pVcamStart, pVcamStop = p("mk_version"), p("mk_vcam_supported"), p("mk_vcam_start"), p("mk_vcam_stop")
	pDecCreate, pDecPush, pDecPull, pDecDestroy = p("mk_dec_create"), p("mk_dec_push"), p("mk_dec_pull"), p("mk_dec_destroy")
	pAudOpen, pAudWrite, pAudQueued, pAudClose = p("mk_audio_open"), p("mk_audio_write"), p("mk_audio_queued"), p("mk_audio_close")
	pRegister, pUnregister = p("DllRegisterServer"), p("DllUnregisterServer")
	pCameraMode = p("mk_camera_mode")
	return pVersion.Find()
}

func Loaded() bool { return dll != nil }

func hr(r uintptr) error {
	if v := int32(r); v < 0 {
		return HRESULT(v)
	}
	return nil
}

// VirtualCameraSupported reports whether Windows can host a software virtual camera.
func VirtualCameraSupported() bool {
	if dll == nil {
		return false
	}
	r, _, _ := pVcamSupported.Call()
	return r == 1
}

// CameraMode reports which camera front-end this PC uses.
func CameraMode() int {
	if dll == nil {
		return CameraNone
	}
	r, _, _ := pCameraMode.Call()
	return int(r)
}

// StartVirtualCamera registers "Macawlink Camera" for this session.
func StartVirtualCamera(name string) error {
	if dll == nil {
		return ErrUnsupported
	}
	n, _ := syscall.UTF16PtrFromString(name)
	r, _, _ := pVcamStart.Call(uintptr(unsafe.Pointer(n)))
	return hr(r)
}

func StopVirtualCamera() {
	if dll != nil {
		pVcamStop.Call()
	}
}

// Register / Unregister call DllRegisterServer (needs admin).
func Register() error {
	if dll == nil {
		return ErrUnsupported
	}
	r, _, _ := pRegister.Call()
	return hr(r)
}

func Unregister() error {
	if dll == nil {
		return ErrUnsupported
	}
	r, _, _ := pUnregister.Call()
	return hr(r)
}

// ---- decoder ----

type decoder struct{ h uintptr }

func NewDecoder(codec int) (Decoder, error) {
	if dll == nil {
		return nil, ErrUnsupported
	}
	var res int32
	h, _, _ := pDecCreate.Call(uintptr(codec), uintptr(unsafe.Pointer(&res)))
	if h == 0 {
		if res == 0 {
			res = -1
		}
		return nil, HRESULT(res)
	}
	return &decoder{h: h}, nil
}

func (d *decoder) Push(au []byte, pts int64) (bool, error) {
	if len(au) == 0 {
		return false, nil
	}
	r, _, _ := pDecPush.Call(d.h, uintptr(unsafe.Pointer(&au[0])), uintptr(len(au)), uintptr(pts))
	if r == 1 {
		return true, nil
	}
	return false, hr(r)
}

func (d *decoder) Pull(dst []byte) (int, int, int64, bool, error) {
	var w, h int32
	var pts int64
	r, _, _ := pDecPull.Call(d.h, uintptr(unsafe.Pointer(&dst[0])), uintptr(len(dst)),
		uintptr(unsafe.Pointer(&w)), uintptr(unsafe.Pointer(&h)), uintptr(unsafe.Pointer(&pts)))
	switch int32(r) {
	case 1:
		return int(w), int(h), pts, true, nil
	case 0:
		return 0, 0, 0, false, nil
	}
	return 0, 0, 0, false, hr(r)
}

func (d *decoder) Close() {
	if d.h != 0 {
		pDecDestroy.Call(d.h)
		d.h = 0
	}
}

// ---- audio ----

type audioOut struct {
	h    uintptr
	name string
}

// OpenAudio opens the first playback endpoint matching one of names (in order).
func OpenAudio(names []string, sampleRate, channels int) (AudioOut, error) {
	if dll == nil {
		return nil, ErrUnsupported
	}
	list, _ := syscall.UTF16PtrFromString(strings.Join(names, ";"))
	chosen := make([]uint16, 256)
	var res int32
	h, _, _ := pAudOpen.Call(uintptr(unsafe.Pointer(list)), uintptr(sampleRate), uintptr(channels),
		uintptr(unsafe.Pointer(&chosen[0])), uintptr(len(chosen)), uintptr(unsafe.Pointer(&res)))
	if h == 0 {
		if res == 0 {
			res = -1
		}
		return nil, HRESULT(res)
	}
	return &audioOut{h: h, name: syscall.UTF16ToString(chosen)}, nil
}

func (a *audioOut) Queued() int {
	r, _, _ := pAudQueued.Call(a.h)
	return int(int32(r))
}

func (a *audioOut) Endpoint() string { return a.name }

func (a *audioOut) Close() {
	if a.h != 0 {
		pAudClose.Call(a.h)
		a.h = 0
	}
}

func (a *audioOut) Write(pcm []int16, frames int) (int, error) {
	if frames == 0 {
		return 0, nil
	}
	r, _, _ := pAudWrite.Call(a.h, uintptr(unsafe.Pointer(&pcm[0])), uintptr(frames))
	if int32(r) < 0 {
		return 0, HRESULT(int32(r))
	}
	return int(r), nil
}
