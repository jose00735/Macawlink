// Package audio turns AUDIO_CONFIG / AUDIO_FRAME packets into sound on the virtual microphone:
// PCM s16le from the phone → gain → WASAPI render into the mic's playback side.
package audio

import (
	"encoding/binary"
	"fmt"
	"log"
	"math"
	"sync"
	"time"

	"github.com/jose00735/macawlink/windows/internal/native"
	"github.com/jose00735/macawlink/windows/internal/protocol"
)

// Endpoints the phone audio is played into, in order of preference. Apps then pick the
// matching recording device ("Macawlink Microphone" / "CABLE Output") as their mic.
var Endpoints = []string{"Macawlink Microphone", "CABLE Input"}

type Stats struct {
	Endpoint   string  `json:"endpoint"`
	SampleRate int     `json:"sample_rate"`
	Channels   int     `json:"channels"`
	QueuedMs   int     `json:"queued_ms"`
	LevelDB    float64 `json:"level_db"` // recent peak, dBFS
	Dropped    uint64  `json:"dropped"`
	Underruns  uint64  `json:"underruns"`
	LastError  string  `json:"last_error,omitempty"`
}

type Pipeline struct {
	Open   func(rate, channels int) (native.AudioOut, error)
	GainDB func() int

	mu      sync.Mutex
	out     native.AudioOut
	cfg     protocol.AudioConfigMsg
	retryAt time.Time
	buf     []int16
	silence []int16
	stats   Stats
	peak    int
	peakAt  time.Time
}

// Latency window: keep between ~frame and maxQueue of audio in the endpoint buffer.
const maxQueueMs = 120

func (p *Pipeline) HandleConfig(payload []byte) error {
	var cfg protocol.AudioConfigMsg
	if _, err := protocol.SplitConfig(payload, &cfg); err != nil {
		return err
	}
	if cfg.Codec != "pcm" {
		return fmt.Errorf("audio: unsupported codec %q", cfg.Codec)
	}
	if cfg.SampleRate <= 0 || (cfg.Channels != 1 && cfg.Channels != 2) {
		return fmt.Errorf("audio: bad format %d Hz x%d", cfg.SampleRate, cfg.Channels)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.out != nil && (cfg.SampleRate != p.cfg.SampleRate || cfg.Channels != p.cfg.Channels) {
		p.out.Close()
		p.out = nil
	}
	p.cfg = cfg
	p.stats.SampleRate, p.stats.Channels = cfg.SampleRate, cfg.Channels
	p.retryAt = time.Time{}
	p.ensureOpen()
	log.Printf("audio: config %d Hz x%d, %d ms frames", cfg.SampleRate, cfg.Channels, cfg.FrameMs)
	return nil
}

func (p *Pipeline) ensureOpen() {
	if p.out != nil || p.cfg.SampleRate == 0 || time.Now().Before(p.retryAt) {
		return
	}
	out, err := p.Open(p.cfg.SampleRate, p.cfg.Channels)
	if err != nil {
		p.retryAt = time.Now().Add(5 * time.Second)
		p.stats.Endpoint = ""
		p.stats.LastError = "virtual microphone not found — install VB-Audio Virtual Cable (" + err.Error() + ")"
		log.Printf("audio: %s", p.stats.LastError)
		return
	}
	p.out = out
	p.stats.Endpoint = out.Endpoint()
	p.stats.LastError = ""
	log.Printf("audio: playing into %q", out.Endpoint())
}

func (p *Pipeline) HandleFrame(payload []byte) error {
	_, data, err := protocol.MediaPTS(payload)
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ensureOpen()
	if p.out == nil {
		return nil
	}
	ch := p.cfg.Channels
	n := len(data) / 2
	if cap(p.buf) < n {
		p.buf = make([]int16, n)
	}
	pcm := p.buf[:n]
	gain := 1.0
	if p.GainDB != nil {
		if g := p.GainDB(); g != 0 {
			gain = math.Pow(10, float64(g)/20)
		}
	}
	peak := 0
	for i := 0; i < n; i++ {
		v := int(int16(binary.LittleEndian.Uint16(data[2*i:])))
		if gain != 1 {
			v = int(float64(v) * gain)
			if v > math.MaxInt16 {
				v = math.MaxInt16
			} else if v < math.MinInt16 {
				v = math.MinInt16
			}
		}
		if a := abs(v); a > peak {
			peak = a
		}
		pcm[i] = int16(v)
	}
	p.trackLevel(peak)

	frames := n / ch
	rate := p.cfg.SampleRate
	queued := p.out.Queued()
	if queued*1000/rate > maxQueueMs {
		// Phone clock runs slightly fast vs. the PC: drop to keep latency bounded.
		p.stats.Dropped++
		p.stats.QueuedMs = queued * 1000 / rate
		return nil
	}
	if queued == 0 {
		// Underrun: pad 20 ms of silence so the next packets have headroom.
		p.stats.Underruns++
		pad := rate / 50 * ch
		if len(p.silence) < pad {
			p.silence = make([]int16, pad)
		}
		p.out.Write(p.silence[:pad], pad/ch)
	}
	if _, err := p.out.Write(pcm, frames); err != nil {
		p.stats.LastError = err.Error()
		p.out.Close()
		p.out = nil
		p.retryAt = time.Now().Add(2 * time.Second)
		return err
	}
	p.stats.QueuedMs = p.out.Queued() * 1000 / rate
	return nil
}

func (p *Pipeline) trackLevel(peak int) {
	now := time.Now()
	if peak > p.peak || now.Sub(p.peakAt) > 300*time.Millisecond {
		p.peak, p.peakAt = peak, now
	}
	if p.peak <= 0 {
		p.stats.LevelDB = -90
	} else {
		p.stats.LevelDB = math.Round(20*math.Log10(float64(p.peak)/32768)*10) / 10
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func (p *Pipeline) Stats() Stats {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stats
}

// Reset closes the endpoint (phone disconnected). Apps keep the mic; it just goes silent.
func (p *Pipeline) Reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.out != nil {
		p.out.Close()
		p.out = nil
	}
	ep := p.stats.Endpoint
	p.stats = Stats{Endpoint: ep}
	p.cfg = protocol.AudioConfigMsg{}
}
