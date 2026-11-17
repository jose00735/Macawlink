// Package audio turns AUDIO_FRAME packets into PCM for the virtual mic:
// adaptive jitter buffer (20–60 ms) → Opus/PCM decode → resample → gain → vmic.Sink.
// Audio is the master clock for A/V sync (docs/ARCHITECTURE.md §7).
package audio

import "time"

// StreamConfig mirrors the AUDIO_CONFIG JSON.
type StreamConfig struct {
	Codec      string `json:"codec"`
	SampleRate int    `json:"sample_rate"`
	Channels   int    `json:"channels"`
	FrameMs    int    `json:"frame_ms"`
	Bitrate    int    `json:"bitrate"`
}

type Decoder interface {
	Configure(cfg StreamConfig) error
	Decode(packet []byte) ([]float32, error)
}

// Clock exposes the playout position so video can be presented in sync.
type Clock interface {
	PlayoutPTS() time.Duration
}

// ApplyGain scales samples by a dB value in place, clamping to [-1, 1].
func ApplyGain(pcm []float32, gainDB int) {
	if gainDB == 0 {
		return
	}
	g := dbToLinear(float64(gainDB))
	for i, s := range pcm {
		v := float64(s) * g
		if v > 1 {
			v = 1
		} else if v < -1 {
			v = -1
		}
		pcm[i] = float32(v)
	}
}
