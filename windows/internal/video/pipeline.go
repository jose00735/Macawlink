// Package video turns VIDEO_CONFIG / VIDEO_FRAME packets into frames on the virtual camera:
// decode (Media Foundation via windows/native) → crop/scale to what the app opened → ring.
package video

import (
	"bytes"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/jose00735/macawlink/windows/internal/native"
	"github.com/jose00735/macawlink/windows/internal/protocol"
)

// Sink is where finished frames go (vcam.Ring in production).
type Sink interface {
	Requested() (w, h, fps int)
	Publish(nv12 []byte, w, h int, pts100ns int64) error
}

// Stats is a snapshot for the UI.
type Stats struct {
	Codec       string  `json:"codec"`
	Width       int     `json:"width"`
	Height      int     `json:"height"`
	FPS         float64 `json:"fps"`
	BitrateKbps int     `json:"bitrate_kbps"`
	OutWidth    int     `json:"out_width"`
	OutHeight   int     `json:"out_height"`
	Decoded     uint64  `json:"decoded"`
	Errors      uint64  `json:"errors"`
	LastError   string  `json:"last_error,omitempty"`
}

type Pipeline struct {
	NewDecoder      func(codec int) (native.Decoder, error)
	Sinks           func() []Sink // every open camera ring (Windows 11 + Windows 10 paths)
	RequestKeyframe func()
	OnCodecFailed   func(codec string) // e.g. HEVC extension missing → ask phone for H.264
	Mirror          func() bool

	mu        sync.Mutex
	dec       native.Decoder
	cfg       protocol.VideoConfigMsg
	csd       []byte
	needKey   bool
	lastKeyRq time.Time
	frame     []byte
	scaled    []byte
	rotated   []byte

	stats     Stats
	winStart  time.Time
	winFrames int
	winBytes  int
}

// HandleConfig processes VIDEO_CONFIG: (re)creates the decoder when the codec or size changes.
func (p *Pipeline) HandleConfig(payload []byte) error {
	var cfg protocol.VideoConfigMsg
	csd, err := protocol.SplitConfig(payload, &cfg)
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.dec != nil && (cfg.Codec != p.cfg.Codec || cfg.Width != p.cfg.Width || cfg.Height != p.cfg.Height) {
		p.dec.Close()
		p.dec = nil
	}
	// A config that only changes rotation (phone turned) keeps decoding without a resync.
	if p.dec == nil || !bytes.Equal(csd, p.csd) {
		p.needKey = true
	}
	p.cfg = cfg
	p.csd = append([]byte(nil), csd...)
	p.stats.Codec, p.stats.Width, p.stats.Height = cfg.Codec, cfg.Width, cfg.Height
	if p.dec == nil {
		codec := native.CodecH264
		if cfg.Codec == "hevc" {
			codec = native.CodecHEVC
		}
		d, err := p.NewDecoder(codec)
		if err != nil {
			p.fail(fmt.Errorf("create %s decoder: %w", cfg.Codec, err))
			if p.OnCodecFailed != nil {
				go p.OnCodecFailed(cfg.Codec)
			}
			return err
		}
		p.dec = d
	}
	log.Printf("video: config %s %dx%d@%d rot=%d csd=%dB", cfg.Codec, cfg.Width, cfg.Height, cfg.FPS, cfg.Rotation, len(csd))
	return nil
}

// HandleFrame processes VIDEO_FRAME.
func (p *Pipeline) HandleFrame(payload []byte, flags uint8) error {
	pts, au, err := protocol.MediaPTS(payload)
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.account(len(payload))
	if p.dec == nil {
		return nil
	}
	key := flags&protocol.FlagKeyframe != 0
	if p.needKey && !key {
		if time.Since(p.lastKeyRq) > time.Second && p.RequestKeyframe != nil {
			p.lastKeyRq = time.Now()
			go p.RequestKeyframe()
		}
		return nil
	}
	if key {
		p.needKey = false
		if len(p.csd) > 0 {
			au = append(append(make([]byte, 0, len(p.csd)+len(au)), p.csd...), au...)
		}
	}
	pts100 := pts * 10
	for tries := 0; tries < 3; tries++ {
		full, err := p.dec.Push(au, pts100)
		if err != nil {
			p.fail(fmt.Errorf("decode: %w", err))
			p.needKey = true
			return err
		}
		if err := p.drain(); err != nil {
			return err
		}
		if !full {
			return nil
		}
	}
	return nil
}

func (p *Pipeline) drain() error {
	need := p.cfg.Width * p.cfg.Height * 3 / 2
	if need < 1920*1088*3/2 {
		need = 1920 * 1088 * 3 / 2
	}
	if len(p.frame) < need {
		p.frame = make([]byte, need)
	}
	for {
		w, h, pts, ok, err := p.dec.Pull(p.frame)
		if err != nil {
			p.fail(fmt.Errorf("decode output: %w", err))
			return err
		}
		if !ok {
			return nil
		}
		p.stats.Decoded++
		p.winFrames++
		p.output(p.frame[:w*h*3/2], w, h, pts)
	}
}

func (p *Pipeline) output(nv12 []byte, w, h int, pts int64) {
	var sinks []Sink
	if p.Sinks != nil {
		sinks = p.Sinks()
	}
	used := false
	for _, s := range sinks {
		if rw, _, _ := s.Requested(); rw > 0 {
			used = true
		}
	}
	if !used {
		return // nobody has the camera open: skip the copies
	}
	mirror := p.Mirror != nil && p.Mirror()
	if r := p.cfg.Rotation; r == 90 || r == 180 || r == 270 {
		if len(p.rotated) < len(nv12) {
			p.rotated = make([]byte, len(nv12))
		}
		w, h = RotateNV12(p.rotated, nv12, w, h, r)
		nv12 = p.rotated[:w*h*3/2]
	}
	lastW, lastH := 0, 0
	var out []byte
	for _, s := range sinks {
		rw, rh, _ := s.Requested()
		if rw == 0 || rh == 0 {
			continue
		}
		if rw != lastW || rh != lastH { // apps sharing a size share one scaled frame
			out = nv12
			if rw != w || rh != h || mirror {
				if len(p.scaled) < rw*rh*3/2 {
					p.scaled = make([]byte, rw*rh*3/2)
				}
				ScaleNV12(p.scaled, rw, rh, nv12, w, h, mirror)
				out = p.scaled[:rw*rh*3/2]
			}
			lastW, lastH = rw, rh
		}
		p.stats.OutWidth, p.stats.OutHeight = rw, rh
		if err := s.Publish(out, rw, rh, pts); err != nil {
			p.fail(err)
		}
	}
}

func (p *Pipeline) account(n int) {
	now := time.Now()
	if p.winStart.IsZero() {
		p.winStart = now
	}
	p.winBytes += n
	if el := now.Sub(p.winStart); el >= time.Second {
		p.stats.FPS = float64(p.winFrames) / el.Seconds()
		p.stats.BitrateKbps = int(float64(p.winBytes*8) / el.Seconds() / 1000)
		p.winStart, p.winFrames, p.winBytes = now, 0, 0
	}
}

func (p *Pipeline) fail(err error) {
	p.stats.Errors++
	p.stats.LastError = err.Error()
	log.Printf("video: %v", err)
}

func (p *Pipeline) Stats() Stats {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stats
}

// Discontinuity is called when packets were dropped upstream: wait for the next keyframe.
func (p *Pipeline) Discontinuity() {
	p.mu.Lock()
	p.needKey = true
	p.mu.Unlock()
	if p.RequestKeyframe != nil {
		go p.RequestKeyframe()
	}
}

// Reset drops decoder state (phone disconnected).
func (p *Pipeline) Reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.dec != nil {
		p.dec.Close()
		p.dec = nil
	}
	p.stats = Stats{}
	p.winStart = time.Time{}
	p.winFrames, p.winBytes = 0, 0
}
