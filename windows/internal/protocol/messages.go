package protocol

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
)

// HelloMsg is sent by the phone right after the PC connects.
type HelloMsg struct {
	Model        string       `json:"model"`
	Manufacturer string       `json:"manufacturer"`
	AppVersion   string       `json:"app_version"`
	Protocol     int          `json:"protocol"`
	Cameras      []CameraInfo `json:"cameras"`
}

type CameraInfo struct {
	ID     string   `json:"id"`
	Facing string   `json:"facing"` // back | front | external
	Sizes  [][2]int `json:"sizes"`
	FPS    []int    `json:"fps"`
}

type HelloAckMsg struct {
	PCName   string `json:"pc_name"`
	Protocol int    `json:"protocol"`
}

type VideoConfigMsg struct {
	Codec    string `json:"codec"` // h264 | hevc
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	FPS      int    `json:"fps"`
	Rotation int    `json:"rotation"`
}

type AudioConfigMsg struct {
	Codec      string `json:"codec"` // pcm (s16le interleaved)
	SampleRate int    `json:"sample_rate"`
	Channels   int    `json:"channels"`
	FrameMs    int    `json:"frame_ms"`
}

type StatsMsg struct {
	BatteryPct  int     `json:"battery"`
	Charging    bool    `json:"charging"`
	TempC       float64 `json:"temp_c"`
	FPS         float64 `json:"fps"`
	BitrateKbps int     `json:"bitrate_kbps"`
	Thermal     int     `json:"thermal"` // PowerManager thermal status 0..6
}

type ByeMsg struct {
	Reason string `json:"reason"`
}

// SplitConfig splits a *_CONFIG payload: JSON, 0x00, codec bytes.
func SplitConfig(payload []byte, v any) (codecData []byte, err error) {
	i := bytes.IndexByte(payload, 0)
	js := payload
	if i >= 0 {
		js, codecData = payload[:i], payload[i+1:]
	}
	if err := json.Unmarshal(js, v); err != nil {
		return nil, err
	}
	return codecData, nil
}

// JoinConfig builds a *_CONFIG payload.
func JoinConfig(v any, codecData []byte) []byte {
	js, _ := json.Marshal(v)
	out := make([]byte, 0, len(js)+1+len(codecData))
	out = append(out, js...)
	out = append(out, 0)
	return append(out, codecData...)
}

// MediaPayload prefixes data with an 8-byte big-endian pts in microseconds.
func MediaPayload(ptsUs int64, data []byte) []byte {
	out := make([]byte, 8+len(data))
	binary.BigEndian.PutUint64(out, uint64(ptsUs))
	copy(out[8:], data)
	return out
}

// PingPayload / PongPayload carry nanosecond timestamps.
func PingPayload(senderNs int64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, uint64(senderNs))
	return b
}

func PongPayload(ping []byte, receiverNs int64) ([]byte, error) {
	if len(ping) < 8 {
		return nil, errors.New("protocol: short ping")
	}
	b := make([]byte, 16)
	copy(b, ping[:8])
	binary.BigEndian.PutUint64(b[8:], uint64(receiverNs))
	return b, nil
}

func ParsePong(b []byte) (sentNs, peerNs int64, err error) {
	if len(b) < 16 {
		return 0, 0, errors.New("protocol: short pong")
	}
	return int64(binary.BigEndian.Uint64(b)), int64(binary.BigEndian.Uint64(b[8:])), nil
}
