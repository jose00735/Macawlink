// Package config holds the stream settings the user can tweak (see docs/ARCHITECTURE.md §5).
// The JSON shape matches StreamSettings in docs/PROTOCOL.md and the Kotlin data class.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type Video struct {
	Width         int    `json:"width"`
	Height        int    `json:"height"`
	FPS           int    `json:"fps"`
	Codec         string `json:"codec"`        // "h264" | "hevc"
	BitrateKbps   int    `json:"bitrate_kbps"` // 0 = auto
	RateControl   string `json:"rate_control"` // "cbr" | "vbr"
	Lens          string `json:"lens"`         // "back" | "wide" | "front"
	Mirror        bool   `json:"mirror"`
	Stabilization bool   `json:"stabilization"`
}

type Audio struct {
	Enabled     bool   `json:"enabled"`
	Codec       string `json:"codec"` // "opus" | "pcm"
	SampleRate  int    `json:"sample_rate"`
	Channels    int    `json:"channels"`
	BitrateKbps int    `json:"bitrate_kbps"`
	FrameMs     int    `json:"frame_ms"`
	AEC         bool   `json:"aec"`
	NS          bool   `json:"ns"`
	AGC         bool   `json:"agc"`
	GainDB      int    `json:"gain_db"` // applied on the PC only
}

type Settings struct {
	Video Video `json:"video"`
	Audio Audio `json:"audio"`
}

// Resolutions the virtual camera advertises to Windows apps.
var Resolutions = [][2]int{{640, 480}, {1280, 720}, {1920, 1080}, {2560, 1440}, {3840, 2160}}

var (
	FrameRates  = []int{15, 24, 30, 60}
	SampleRates = []int{16000, 24000, 32000, 44100, 48000}
)

func Default() Settings {
	return Presets["meeting"]
}

var Presets = map[string]Settings{
	"low": {
		Video: Video{Width: 640, Height: 480, FPS: 30, Codec: "h264", BitrateKbps: 1500, RateControl: "cbr", Lens: "back", Stabilization: true},
		Audio: Audio{Enabled: true, Codec: "opus", SampleRate: 16000, Channels: 1, BitrateKbps: 32, FrameMs: 20, AEC: true, NS: true, AGC: true},
	},
	"meeting": {
		Video: Video{Width: 1280, Height: 720, FPS: 30, Codec: "h264", BitrateKbps: 0, RateControl: "cbr", Lens: "back", Stabilization: true},
		Audio: Audio{Enabled: true, Codec: "opus", SampleRate: 48000, Channels: 1, BitrateKbps: 96, FrameMs: 10, AEC: true, NS: true, AGC: true},
	},
	"max": {
		Video: Video{Width: 1920, Height: 1080, FPS: 60, Codec: "hevc", BitrateKbps: 20000, RateControl: "vbr", Lens: "back", Stabilization: true},
		Audio: Audio{Enabled: true, Codec: "pcm", SampleRate: 48000, Channels: 2, FrameMs: 10},
	},
}

func contains(xs []int, v int) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

// Validate rejects values the pipeline cannot honour.
func (s Settings) Validate() error {
	okRes := false
	for _, r := range Resolutions {
		if r[0] == s.Video.Width && r[1] == s.Video.Height {
			okRes = true
		}
	}
	switch {
	case !okRes:
		return fmt.Errorf("unsupported resolution %dx%d", s.Video.Width, s.Video.Height)
	case !contains(FrameRates, s.Video.FPS):
		return fmt.Errorf("unsupported fps %d", s.Video.FPS)
	case s.Video.Codec != "h264" && s.Video.Codec != "hevc":
		return fmt.Errorf("unsupported video codec %q", s.Video.Codec)
	case s.Video.BitrateKbps != 0 && (s.Video.BitrateKbps < 500 || s.Video.BitrateKbps > 40000):
		return fmt.Errorf("video bitrate %d kbps out of range", s.Video.BitrateKbps)
	case !contains(SampleRates, s.Audio.SampleRate):
		return fmt.Errorf("unsupported sample rate %d", s.Audio.SampleRate)
	case s.Audio.Channels != 1 && s.Audio.Channels != 2:
		return fmt.Errorf("channels must be 1 or 2")
	case s.Audio.Codec != "opus" && s.Audio.Codec != "pcm":
		return fmt.Errorf("unsupported audio codec %q", s.Audio.Codec)
	case s.Audio.Codec == "opus" && (s.Audio.BitrateKbps < 16 || s.Audio.BitrateKbps > 256):
		return fmt.Errorf("opus bitrate %d kbps out of range", s.Audio.BitrateKbps)
	case s.Audio.FrameMs != 10 && s.Audio.FrameMs != 20:
		return fmt.Errorf("audio frame must be 10 or 20 ms")
	case s.Audio.GainDB < -20 || s.Audio.GainDB > 20:
		return fmt.Errorf("gain must be within ±20 dB")
	}
	return nil
}

// Path returns %APPDATA%\Macawlink\settings.json.
func Path() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "Macawlink", "settings.json"), nil
}

func Load() (Settings, error) {
	p, err := Path()
	if err != nil {
		return Default(), err
	}
	b, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return Default(), nil
	} else if err != nil {
		return Default(), err
	}
	s := Default()
	if err := json.Unmarshal(b, &s); err != nil {
		return Default(), err
	}
	return s, s.Validate()
}

func Save(s Settings) error {
	if err := s.Validate(); err != nil {
		return err
	}
	p, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(s, "", "  ")
	return os.WriteFile(p, b, 0o644)
}
