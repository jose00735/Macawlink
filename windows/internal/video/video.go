// Package video turns VIDEO_FRAME packets into frames for the virtual camera:
// jitter buffer → decoder (FFmpeg/D3D11VA via go-astiav) → rotate/scale → vcam.Frame.
package video

import (
	"time"

	"github.com/jose00735/macawlink/windows/internal/vcam"
)

// StreamConfig mirrors the VIDEO_CONFIG JSON.
type StreamConfig struct {
	Codec    string `json:"codec"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	FPS      int    `json:"fps"`
	Rotation int    `json:"rotation"`
}

// Decoder decodes Annex-B access units.
type Decoder interface {
	// Configure (re)initialises the decoder with new SPS/PPS(/VPS) without tearing down the camera.
	Configure(cfg StreamConfig, codecConfig []byte) error
	Decode(au []byte, pts time.Duration, keyframe bool) (*vcam.Frame, error)
	Close() error
}

// Scaler converts a decoded frame to the size the consuming app requested.
type Scaler interface {
	Scale(in *vcam.Frame, width, height, rotation int, mirror bool) (*vcam.Frame, error)
}
