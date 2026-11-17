//go:build !windows

package vcam

import "errors"

// OpenGlobal and CreateLocal are Windows-only; tests use NewRingFromMemory.
func OpenGlobal() (*Ring, error)  { return nil, errors.New("vcam: Windows only") }
func CreateLocal() (*Ring, error) { return nil, errors.New("vcam: Windows only") }
