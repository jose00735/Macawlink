//go:build windows

package vcam

import (
	"syscall"
	"unsafe"
)

var (
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	advapi32         = syscall.NewLazyDLL("advapi32.dll")
	pOpenFileMapping = kernel32.NewProc("OpenFileMappingW")
	pLocalFree       = kernel32.NewProc("LocalFree")
	pSDDLToSD        = advapi32.NewProc("ConvertStringSecurityDescriptorToSecurityDescriptorW")
)

const (
	fileMapAllAccess = 0xF001F
	sddl             = "D:P(A;;GA;;;WD)(A;;GA;;;SY)(A;;GA;;;LS)(A;;GA;;;AC)(A;;GA;;;S-1-15-2-2)S:(ML;;NW;;;LW)"
)

// OpenRing maps the camera's shared memory. The media source normally creates it (Frame
// Server holds the privilege to create Global\ objects); if we are elevated we may create it
// ourselves. Returns an error until one of the two has happened — callers retry.
func OpenRing() (*Ring, error) {
	name, _ := syscall.UTF16PtrFromString(RingName)
	h, _, err := pOpenFileMapping.Call(fileMapAllAccess, 0, uintptr(unsafe.Pointer(name)))
	if h == 0 {
		var sa *syscall.SecurityAttributes
		var sd uintptr
		s, _ := syscall.UTF16PtrFromString(sddl)
		if r, _, _ := pSDDLToSD.Call(uintptr(unsafe.Pointer(s)), 1, uintptr(unsafe.Pointer(&sd)), 0); r != 0 {
			sa = &syscall.SecurityAttributes{Length: uint32(unsafe.Sizeof(syscall.SecurityAttributes{})), SecurityDescriptor: sd}
			defer pLocalFree.Call(sd)
		}
		ch, cerr := syscall.CreateFileMapping(syscall.InvalidHandle, sa, syscall.PAGE_READWRITE, 0, RingBytes, name)
		if cerr != nil && ch == 0 {
			return nil, err
		}
		h = uintptr(ch)
	}
	addr, merr := syscall.MapViewOfFile(syscall.Handle(h), fileMapAllAccess, 0, 0, RingBytes)
	if addr == 0 {
		syscall.CloseHandle(syscall.Handle(h))
		return nil, merr
	}
	base := *(*unsafe.Pointer)(unsafe.Pointer(&addr)) // uintptr from MapViewOfFile -> pointer
	mem := unsafe.Slice((*byte)(base), RingBytes)
	return NewRingFromMemory(mem, func() {
		syscall.UnmapViewOfFile(addr)
		syscall.CloseHandle(syscall.Handle(h))
	})
}
