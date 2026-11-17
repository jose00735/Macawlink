// Package vcam feeds decoded frames to the "Macawlink Camera" virtual device.
//
// Go writes NV12 frames into a named shared-memory ring; the C++ Media Foundation
// media source in windows/vcam-native reads the newest slot whenever an app pulls a sample.
// See docs/ARCHITECTURE.md §2.1.
package vcam

import "time"

const (
	SharedMemName = `Global\Macawlink.Video`
	NewFrameEvent = `Global\Macawlink.Video.NewFrame`
	Slots         = 3
	MaxWidth      = 3840
	MaxHeight     = 2160
	// NV12 = 12 bits per pixel.
	SlotBytes = MaxWidth * MaxHeight * 3 / 2

	FriendlyName = "Macawlink Camera"
	// CLSID of the media source in vcam-native; must match the DLL's registration.
	MediaSourceCLSID = "{6E0D1A3B-6F2C-4C1E-9B7A-4D61434157B1}"
)

// RingHeader sits at offset 0 of the shared memory. Layout is shared with
// vcam-native/include/macawlink_ring.h — change both together.
type RingHeader struct {
	Magic     uint32 // 'MKVC'
	Version   uint32
	Latest    uint32 // index of the most recently completed slot
	Seq       uint64 // increments on every completed write
	Width     uint32
	Height    uint32
	Stride    uint32
	FourCC    uint32 // 'NV12'
	Timestamp int64  // 100-ns units, QPC-based
	Connected uint32 // 0 = show "Waiting for phone…" frame
}

// Frame is one decoded, already-scaled image ready for the camera.
type Frame struct {
	Width, Height, Stride int
	NV12                  []byte
	PTS                   time.Duration
}

// Camera is the virtual camera lifecycle.
type Camera interface {
	// Register calls MFCreateVirtualCamera (Windows 11) and starts the device.
	Register() error
	// Write publishes the next frame to the ring.
	Write(f Frame) error
	// SetConnected toggles the placeholder frame shown when no phone is attached.
	SetConnected(bool)
	// RequestedFormat reports the format the consuming app opened (so the scaler targets it).
	RequestedFormat() (width, height, fps int)
	Close() error
}
