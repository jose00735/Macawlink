// Package vcam publishes decoded frames to "Macawlink Camera".
//
// The virtual camera media source (windows/native, running inside Windows' Frame Server)
// creates a named shared-memory ring; this package opens it and writes NV12 frames.
// The layout must match windows/native/ring.h byte for byte.
package vcam

import (
	"errors"
	"sync"
	"sync/atomic"
	"unsafe"
)

const (
	RingName    = `Global\Macawlink.Video.v2`
	Magic       = 0x43564B4D // "MKVC"
	Version     = 2          // 2: slots sized for 2560x1440
	Slots       = 3
	MaxWidth    = 2560
	MaxHeight   = 1440
	SlotBytes   = MaxWidth * MaxHeight * 3 / 2
	HeaderBytes = 256
	RingBytes   = HeaderBytes + Slots*SlotBytes

	FriendlyName = "Macawlink Camera"

	offMagic     = 0
	offVersion   = 4
	offLatest    = 8
	offConnected = 12
	offSeq       = 16
	offSlots     = 24 // + i*24: seq u64, width u32, height u32, pts i64
	slotMetaSize = 24
	offReqW      = 96
	offReqH      = 100
	offReqFPS    = 104
	offDelivered = 112
	offTick      = 120
)

var ErrTooLarge = errors.New("vcam: frame larger than 2560x1440")

// Ring is a view over the shared memory. All methods are safe for one producer goroutine
// plus concurrent readers of Requested/Delivered.
type Ring struct {
	mu   sync.Mutex
	mem  []byte
	free func()
}

// NewRingFromMemory wraps an existing buffer (used by tests and by the Windows opener).
func NewRingFromMemory(mem []byte, free func()) (*Ring, error) {
	if len(mem) < RingBytes {
		return nil, errors.New("vcam: ring memory too small")
	}
	r := &Ring{mem: mem, free: free}
	if r.u32(offMagic).Load() != Magic || r.u32(offVersion).Load() != Version {
		// Fresh mapping (we created it first): initialise the header.
		for i := 0; i < HeaderBytes; i++ {
			mem[i] = 0
		}
		r.u32(offVersion).Store(Version)
		r.u32(offMagic).Store(Magic)
	}
	return r, nil
}

func (r *Ring) u32(off int) *atomic.Uint32 { return (*atomic.Uint32)(unsafe.Pointer(&r.mem[off])) }
func (r *Ring) u64(off int) *atomic.Uint64 { return (*atomic.Uint64)(unsafe.Pointer(&r.mem[off])) }
func (r *Ring) i64(off int) *atomic.Int64  { return (*atomic.Int64)(unsafe.Pointer(&r.mem[off])) }

// Requested returns the format the consuming app opened (0,0,0 if nobody is streaming).
func (r *Ring) Requested() (w, h, fps int) {
	return int(r.u32(offReqW).Load()), int(r.u32(offReqH).Load()), int(r.u32(offReqFPS).Load())
}

// Delivered is the number of frames the camera has handed to apps so far.
func (r *Ring) Delivered() uint64 { return r.u64(offDelivered).Load() }

// ConsumerTick is the GetTickCount64 value of the last delivery (0 = never).
func (r *Ring) ConsumerTick() uint64 { return r.u64(offTick).Load() }

// SetConnected toggles between the live picture and the "waiting for phone" card.
func (r *Ring) SetConnected(on bool) {
	var v uint32
	if on {
		v = 1
	}
	r.u32(offConnected).Store(v)
}

// Publish copies one tight NV12 frame into the next slot and makes it current.
func (r *Ring) Publish(nv12 []byte, w, h int, pts100ns int64) error {
	if w <= 0 || h <= 0 || w > MaxWidth || h > MaxHeight || w%2 != 0 || h%2 != 0 {
		return ErrTooLarge
	}
	if len(nv12) < w*h*3/2 {
		return errors.New("vcam: short frame")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	seq := r.u64(offSeq).Load()
	slot := 0
	if seq != 0 {
		slot = int(r.u32(offLatest).Load()+1) % Slots
	}
	base := HeaderBytes + slot*SlotBytes
	copy(r.mem[base:base+w*h*3/2], nv12[:w*h*3/2])
	meta := offSlots + slot*slotMetaSize
	r.u64(meta).Store(seq + 1)
	r.u32(meta + 8).Store(uint32(w))
	r.u32(meta + 12).Store(uint32(h))
	r.i64(meta + 16).Store(pts100ns)
	r.u32(offLatest).Store(uint32(slot))
	r.u64(offSeq).Store(seq + 1)
	return nil
}

func (r *Ring) Close() {
	r.SetConnected(false)
	if r.free != nil {
		r.free()
		r.free = nil
	}
}
