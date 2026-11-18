package config

import (
	"encoding/json"
	"testing"
)

func TestManualControlsValidation(t *testing.T) {
	s := Default()
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []func(*Settings){
		func(s *Settings) { s.Video.FocusMode = "macro" },
		func(s *Settings) { s.Video.Focus = 1.5 },
		func(s *Settings) { s.Video.Exposure = -3 },
		func(s *Settings) { s.Video.WhiteBalance = "neon" },
		func(s *Settings) { s.Video.Width, s.Video.Height = 3840, 2160 },
	} {
		x := Default()
		bad(&x)
		if x.Validate() == nil {
			t.Errorf("accepted %+v", x.Video)
		}
	}
	x := Default()
	x.Video.Width, x.Video.Height = 2560, 1440
	x.Video.FocusMode, x.Video.Focus, x.Video.Exposure, x.Video.WhiteBalance = "manual", 0.4, -1, "cloudy"
	if err := x.Validate(); err != nil {
		t.Fatal(err)
	}
}

// Settings JSON from a 0.2 phone (no Phase 3 fields) still validates after Normalize.
func TestOldPhoneSettings(t *testing.T) {
	var s Settings
	json.Unmarshal([]byte(`{"video":{"width":1280,"height":720,"fps":30,"codec":"h264","rate_control":"cbr","lens":"back","zoom":1},"audio":{"codec":"pcm","sample_rate":48000,"channels":1,"frame_ms":10}}`), &s)
	s.Normalize()
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
}
