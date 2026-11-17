// Package native binds macawlink_native.dll (windows/native) without cgo.
// On non-Windows platforms every call returns ErrUnsupported so the rest of the
// receiver can be built and unit-tested anywhere.
package native

import (
	"errors"
	"fmt"
)

var ErrUnsupported = errors.New("native: only available on Windows")

// HRESULT error.
type HRESULT int32

func (h HRESULT) Error() string { return fmt.Sprintf("HRESULT 0x%08X", uint32(h)) }

// Camera front-ends (mk_camera_mode).
const (
	CameraNone            = 0
	CameraDirectShow      = 1 // Windows 10: DirectShow filter loaded by each app
	CameraMediaFoundation = 2 // Windows 11: virtual camera hosted by Frame Server
)

// Codec IDs for NewDecoder.
const (
	CodecH264 = 0
	CodecHEVC = 1
)

// Decoder decodes Annex-B H.264/HEVC into tight NV12.
type Decoder interface {
	// Push feeds an access unit. full=true means: pull frames, then push the same AU again.
	Push(au []byte, pts100ns int64) (full bool, err error)
	// Pull returns a frame (ok=true) or ok=false when more input is needed.
	Pull(dst []byte) (w, h int, pts100ns int64, ok bool, err error)
	Close()
}

// AudioOut renders s16 PCM to a Windows playback endpoint.
type AudioOut interface {
	// Write renders interleaved s16 PCM holding `frames` frames; returns frames accepted.
	Write(pcm []int16, frames int) (int, error)
	Queued() int // frames waiting in the endpoint buffer
	Close()
	Endpoint() string
}
