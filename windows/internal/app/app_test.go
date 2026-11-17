package app

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"github.com/jose00735/macawlink/windows/internal/config"
	"github.com/jose00735/macawlink/windows/internal/fakephone"
	"github.com/jose00735/macawlink/windows/internal/native"
	"github.com/jose00735/macawlink/windows/internal/protocol"
	"github.com/jose00735/macawlink/windows/internal/vcam"
)

// ---- fakes standing in for the Windows DLL ----

type fakeDecoder struct {
	mu      sync.Mutex
	pushed  [][]byte
	pending int
}

func (d *fakeDecoder) Push(au []byte, pts int64) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.pushed = append(d.pushed, append([]byte(nil), au...))
	d.pending++
	return false, nil
}
func (d *fakeDecoder) Pull(dst []byte) (int, int, int64, bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.pending == 0 {
		return 0, 0, 0, false, nil
	}
	d.pending--
	w, h := 1280, 720
	for i := 0; i < w*h; i++ {
		dst[i] = 180
	}
	for i := w * h; i < w*h*3/2; i++ {
		dst[i] = 128
	}
	return w, h, 0, true, nil
}
func (d *fakeDecoder) Close() {}

type fakeAudio struct {
	mu     sync.Mutex
	frames int
	peak   int16
}

func (a *fakeAudio) Write(pcm []int16, frames int) (int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.frames += frames
	for _, v := range pcm {
		if v > a.peak {
			a.peak = v
		}
	}
	return frames, nil
}
func (a *fakeAudio) Queued() int      { return 480 } // pretend 10 ms queued at 48 kHz
func (a *fakeAudio) Close()           {}
func (a *fakeAudio) Endpoint() string { return "CABLE Input (VB-Audio Virtual Cable)" }

// A tiny fake H.264 stream: SPS, PPS, IDR, then two P slices.
var fakeH264 = []byte{
	0, 0, 0, 1, 0x67, 0x42, 0xc0, 0x1f, // SPS
	0, 0, 0, 1, 0x68, 0xce, 0x3c, 0x80, // PPS
	0, 0, 0, 1, 0x65, 0x88, 0x84, 0x00, // IDR
	0, 0, 0, 1, 0x41, 0x9a, 0x02, // P
	0, 0, 0, 1, 0x41, 0x9a, 0x04, // P
}

func TestEndToEndWithFakePhone(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	phoneSettings := config.Default()
	rec := &fakephone.Received{}
	done := make(chan struct{})
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		fakephone.Serve(c, fakephone.Options{Model: "Pixel Test", Settings: phoneSettings, H264: fakeH264}, rec, done)
	}()

	mem := make([]byte, vcam.RingBytes)
	ring, _ := vcam.NewRingFromMemory(mem, nil)
	// Second ring, like the Windows 10 DirectShow camera opened by another app at 1280x720.
	mem2 := make([]byte, vcam.RingBytes)
	ring2, _ := vcam.NewRingFromMemory(mem2, nil)
	binary.LittleEndian.PutUint32(mem2[96:], 1280)
	binary.LittleEndian.PutUint32(mem2[100:], 720)
	binary.LittleEndian.PutUint32(mem2[104:], 30)
	// An app opened the camera at 640x480@30.
	binary.LittleEndian.PutUint32(mem[96:], 640)
	binary.LittleEndian.PutUint32(mem[100:], 480)
	binary.LittleEndian.PutUint32(mem[104:], 30)

	dec := &fakeDecoder{}
	aud := &fakeAudio{}
	start := config.Default()
	start.Audio.GainDB = 6
	a := New(Options{
		Addr:       ln.Addr().String(),
		NewDecoder: func(int) (native.Decoder, error) { return dec, nil },
		OpenAudio:  func(rate, ch int) (native.AudioOut, error) { return aud, nil },
		RingSources: []func() (*vcam.Ring, error){
			func() (*vcam.Ring, error) { return ring, nil },
			func() (*vcam.Ring, error) { return ring2, nil },
		},
	}, start)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.Run(ctx)

	waitFor(t, "streaming + frames", func() bool {
		st := a.Status()
		return st.Phase == PhaseStreaming && ld64(mem, 16) >= 2
	})

	st := a.Status()
	if st.Device != "Fake Pixel Test" {
		t.Errorf("device = %q", st.Device)
	}
	if ld32(mem, 12) != 1 {
		t.Error("ring not marked connected")
	}
	// Frames published at the size the app asked for.
	latest := ld32(mem, 8)
	meta := 24 + int(latest)*24
	if w, h := ld32(mem, meta+8), ld32(mem, meta+12); w != 640 || h != 480 {
		t.Errorf("published %dx%d, want 640x480", w, h)
	}
	// The second consumer gets its own size.
	waitFor(t, "second ring", func() bool { return ld64(mem2, 16) >= 1 })
	meta2 := 24 + int(ld32(mem2, 8))*24
	if w, h := ld32(mem2, meta2+8), ld32(mem2, meta2+12); w != 1280 || h != 720 {
		t.Errorf("ring2 published %dx%d, want 1280x720", w, h)
	}
	// Keyframe was prefixed with SPS+PPS.
	dec.mu.Lock()
	first := dec.pushed[0]
	dec.mu.Unlock()
	if !bytes.HasPrefix(first, fakeH264[:16]) || !bytes.Contains(first, []byte{0x65, 0x88}) {
		t.Errorf("first AU not csd+IDR: % x", first)
	}

	waitFor(t, "audio", func() bool { aud.mu.Lock(); defer aud.mu.Unlock(); return aud.frames >= 48000/10 })
	aud.mu.Lock()
	peak := aud.peak
	aud.mu.Unlock()
	if peak < 15000 { // 8000 amplitude tone * +6 dB ≈ 15963
		t.Errorf("gain not applied, peak=%d", peak)
	}
	if a.Status().Audio.Endpoint == "" {
		t.Error("audio endpoint not reported")
	}

	// Live settings change from the PC reaches the phone and comes back as CONFIG_STATE.
	s := a.Settings()
	s.Video.Width, s.Video.Height, s.Video.FPS = 1920, 1080, 60
	s.Audio.SampleRate = 24000
	if err := a.ApplySettings(s); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "SET_CONFIG at phone", func() bool { _, sc, _ := rec.Snapshot(); return len(sc) > 0 })
	_, sc, _ := rec.Snapshot()
	if got := sc[len(sc)-1]; got.Video.Width != 1920 || got.Video.FPS != 60 || got.Audio.SampleRate != 24000 {
		t.Errorf("phone got %+v", got)
	}
	if a.Settings().Audio.GainDB != 6 {
		t.Error("PC-only gain lost after CONFIG_STATE")
	}
	waitFor(t, "rtt", func() bool { return a.Status().RTTms > 0 })

	// Bad settings are rejected.
	bad := a.Settings()
	bad.Video.Width = 1234
	if err := a.ApplySettings(bad); err == nil {
		t.Error("expected validation error")
	}
	if err := a.ApplyPreset("low"); err != nil || a.Settings().Video.Width != 640 {
		t.Errorf("preset: %v %+v", err, a.Settings().Video)
	}

	// Phone leaves → placeholder.
	close(done)
	waitFor(t, "disconnect", func() bool { return ld32(mem, 12) == 0 && ld32(mem2, 12) == 0 })
	if st := a.Status(); st.Phase == PhaseStreaming {
		t.Errorf("still streaming after BYE")
	}
}

func TestSplitAnnexB(t *testing.T) {
	aus, csd := fakephone.SplitAnnexB(fakeH264)
	if len(aus) != 3 || !aus[0].Key || aus[1].Key || len(csd) != 16 {
		t.Fatalf("aus=%d key0=%v csd=%d", len(aus), aus[0].Key, len(csd))
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// Atomic reads, like the consumer DLL in the other process.
func ld32(mem []byte, off int) uint32 { return atomic.LoadUint32((*uint32)(unsafe.Pointer(&mem[off]))) }
func ld64(mem []byte, off int) uint64 { return atomic.LoadUint64((*uint64)(unsafe.Pointer(&mem[off]))) }

var _ = errors.New
var _ = protocol.Hello
