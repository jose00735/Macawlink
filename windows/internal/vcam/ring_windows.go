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
	// RingNameLocal is used when the media source cannot create Global\ objects.
	RingNameLocal = `Local\Macawlink.Video`
)

var ErrNoRing = errors.New("vcam: camera not opened by any app yet")

// OpenRing maps the camera's shared memory. The media source (consumer) creates it the
// first time an app opens Macawlink Camera; until then this returns ErrNoRing and the
// caller retries. Global\ is preferred (Frame Server in session 0), Local\ is the fallback.
func OpenRing() (*Ring, error) {
	for _, n := range []string{RingName, RingNameLocal} {
		name, _ := syscall.UTF16PtrFromString(n)
		h, _, _ := pOpenFileMapping.Call(fileMapAllAccess, 0, uintptr(unsafe.Pointer(name)))
		if h == 0 {
			continue
		}
		addr, err := syscall.MapViewOfFile(syscall.Handle(h), fileMapAllAccess, 0, 0, RingBytes)
		if addr == 0 {
			syscall.CloseHandle(syscall.Handle(h))
			return nil, err
		}
		base := *(*unsafe.Pointer)(unsafe.Pointer(&addr)) // uintptr from MapViewOfFile -> pointer
		mem := unsafe.Slice((*byte)(base), RingBytes)
		return NewRingFromMemory(mem, func() {
			syscall.UnmapViewOfFile(addr)
			syscall.CloseHandle(syscall.Handle(h))
		})
	}
	return nil, ErrNoRing
}
