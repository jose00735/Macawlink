//go:build !windows

package vcam

import "errors"

// OpenRing is Windows-only; tests use NewRingFromMemory.
func OpenRing() (*Ring, error) { return nil, errors.New("vcam: Windows only") }
