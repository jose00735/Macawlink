//go:build windows

package vcam

import (
	"errors"
	"syscall"
	"unsafe"
)

var (
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	pOpenFileMapping = kernel32.NewProc("OpenFileMappingW")
)

const (
	fileMapAllAccess = 0xF001F
	// RingNameLocal is the session-local ring read by the DirectShow camera (it runs inside
	// each app's process on Windows 10). We create it at startup.
	RingNameLocal = `Local\Macawlink.Video`
)

var ErrNoRing = errors.New("vcam: camera not opened by any app yet")

// OpenGlobal maps the ring the Windows 11 media source creates inside Frame Server the first
// time an app opens Macawlink Camera. Until then it returns ErrNoRing; callers retry.
func OpenGlobal() (*Ring, error) {
	name, _ := syscall.UTF16PtrFromString(RingName)
	h, _, _ := pOpenFileMapping.Call(fileMapAllAccess, 0, uintptr(unsafe.Pointer(name)))
	if h == 0 {
		return nil, ErrNoRing
	}
	return mapRing(syscall.Handle(h))
}

// CreateLocal creates (or opens) the session-local ring for in-app DirectShow filters.
func CreateLocal() (*Ring, error) {
	name, _ := syscall.UTF16PtrFromString(RingNameLocal)
	h, err := syscall.CreateFileMapping(syscall.InvalidHandle, nil, syscall.PAGE_READWRITE, 0, RingBytes, name)
	if h == 0 {
		return nil, err
	}
	return mapRing(h)
}

func mapRing(h syscall.Handle) (*Ring, error) {
	addr, err := syscall.MapViewOfFile(h, fileMapAllAccess, 0, 0, RingBytes)
	if addr == 0 {
		syscall.CloseHandle(h)
		return nil, err
	}
	base := *(*unsafe.Pointer)(unsafe.Pointer(&addr)) // uintptr from MapViewOfFile -> pointer
	mem := unsafe.Slice((*byte)(base), RingBytes)
	return NewRingFromMemory(mem, func() {
		syscall.UnmapViewOfFile(addr)
		syscall.CloseHandle(h)
	})
}
