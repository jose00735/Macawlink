// Package app is the receiver's brain: it keeps a phone connected (USB via adb, or a direct
// address), runs the video and audio pipelines, feeds the virtual camera ring and exposes
// state + settings to the tray and the web settings page.
package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"sync"
	"time"

	"github.com/jose00735/macawlink/windows/internal/audio"
	"github.com/jose00735/macawlink/windows/internal/config"
	"github.com/jose00735/macawlink/windows/internal/native"
	"github.com/jose00735/macawlink/windows/internal/protocol"
	"github.com/jose00735/macawlink/windows/internal/transport"
	"github.com/jose00735/macawlink/windows/internal/vcam"
	"github.com/jose00735/macawlink/windows/internal/video"
)

const Version = "0.1.0"

// Phases shown in the UI.
const (
	PhaseStarting     = "starting"
	PhaseNoADB        = "no-adb"
	PhaseNoDevice     = "no-device"
	PhaseUnauthorized = "unauthorized"
	PhaseWaitingApp   = "waiting-app"
	PhaseStreaming    = "streaming"
)

type CameraStatus struct {
	Supported  bool   `json:"supported"`
	Registered bool   `json:"registered"`
	Error      string `json:"error,omitempty"`
	RingOpen   bool   `json:"ring_open"`
	InUse      bool   `json:"in_use"`
	AppWidth   int    `json:"app_width"`
	AppHeight  int    `json:"app_height"`
	AppFPS     int    `json:"app_fps"`
	Delivered  uint64 `json:"delivered"`
}

type Status struct {
	Version   string                `json:"version"`
	Phase     string                `json:"phase"`
	Message   string                `json:"message"`
	Device    string                `json:"device"`
	Serial    string                `json:"serial"`
	Transport string                `json:"transport"`
	Since     time.Time             `json:"since"`
	RTTms     float64               `json:"rtt_ms"`
	Camera    CameraStatus          `json:"camera"`
	Video     video.Stats           `json:"video"`
	Audio     audio.Stats           `json:"audio"`
	Phone     protocol.StatsMsg     `json:"phone"`
	Cameras   []protocol.CameraInfo `json:"cameras,omitempty"`
}

type Options struct {
	ExeDir     string
	Addr       string // direct host:port instead of adb (testing / Wi-Fi manual)
	Serial     string // pick a specific phone when several are attached
	NewDecoder func(codec int) (native.Decoder, error)
	OpenAudio  func(rate, ch int) (native.AudioOut, error)
	OpenRing   func() (*vcam.Ring, error)
	Camera     CameraStatus // filled by main after registering the virtual camera
	SaveConfig bool         // persist settings to %APPDATA% (off in tests)
}

type App struct {
	opt Options

	mu       sync.Mutex
	settings config.Settings
	status   Status
	ring     *vcam.Ring
	conn     net.Conn
	autoAt   map[string]time.Time

	wmu sync.Mutex
	seq uint32

	video *video.Pipeline
	audio *audio.Pipeline
}

func New(opt Options, s config.Settings) *App {
	a := &App{opt: opt, settings: s, autoAt: map[string]time.Time{}}
	a.status = Status{Version: Version, Phase: PhaseStarting, Message: "Starting…", Camera: opt.Camera}
	a.video = &video.Pipeline{
		NewDecoder:      opt.NewDecoder,
		Sink:            a.sink,
		RequestKeyframe: func() { a.send(protocol.RequestKeyframe, 0, nil) },
		OnCodecFailed:   a.codecFailed,
		Mirror:          func() bool { a.mu.Lock(); defer a.mu.Unlock(); return a.settings.Video.Mirror },
	}
	a.audio = &audio.Pipeline{
		Open:   opt.OpenAudio,
		GainDB: func() int { a.mu.Lock(); defer a.mu.Unlock(); return a.settings.Audio.GainDB },
	}
	return a
}

func (a *App) sink() video.Sink {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.ring == nil {
		return nil
	}
	return a.ring
}

// Run blocks until ctx ends.
func (a *App) Run(ctx context.Context) {
	go a.ringLoop(ctx)
	a.connLoop(ctx)
}

// ---------------------------------------------------------------------------
// camera ring

func (a *App) ringLoop(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		a.mu.Lock()
		ring := a.ring
		a.mu.Unlock()
		if ring == nil && a.opt.OpenRing != nil {
			if r, err := a.opt.OpenRing(); err == nil {
				a.mu.Lock()
				a.ring = r
				ring = r
				r.SetConnected(a.status.Phase == PhaseStreaming)
				a.mu.Unlock()
				log.Printf("camera: frame ring open")
			}
		}
		if ring != nil {
			w, h, fps := ring.Requested()
			a.mu.Lock()
			wasInUse := a.status.Camera.InUse
			a.status.Camera.RingOpen = true
			a.status.Camera.AppWidth, a.status.Camera.AppHeight, a.status.Camera.AppFPS = w, h, fps
			a.status.Camera.InUse = w > 0
			a.status.Camera.Delivered = ring.Delivered()
			a.mu.Unlock()
			if !wasInUse && w > 0 {
				log.Printf("camera: app opened %dx%d@%d", w, h, fps)
				a.send(protocol.RequestKeyframe, 0, nil) // first picture right away
			}
		}
		select {
		case <-ctx.Done():
			a.mu.Lock()
			if a.ring != nil {
				a.ring.Close()
				a.ring = nil
			}
			a.mu.Unlock()
			return
		case <-t.C:
		}
	}
}

// ---------------------------------------------------------------------------
// connection

func (a *App) setPhase(phase, msg string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.status.Phase != phase || a.status.Message != msg {
		log.Printf("status: %s — %s", phase, msg)
		a.status.Phase, a.status.Message = phase, msg
		a.status.Since = time.Now()
	}
}

func (a *App) connLoop(ctx context.Context) {
	for ctx.Err() == nil {
		conn, hello, dev, err := a.connect(ctx)
		if err != nil {
			sleep(ctx, 2*time.Second)
			continue
		}
		err = a.session(ctx, conn, hello, dev)
		if err != nil && ctx.Err() == nil {
			log.Printf("session ended: %v", err)
		}
		a.video.Reset()
		a.audio.Reset()
		a.mu.Lock()
		a.conn = nil
		if a.ring != nil {
			a.ring.SetConnected(false)
		}
		a.status.Video, a.status.Phone, a.status.RTTms, a.status.Cameras = video.Stats{}, protocol.StatsMsg{}, 0, nil
		a.mu.Unlock()
		a.setPhase(PhaseWaitingApp, "Phone disconnected — reconnecting…")
		sleep(ctx, 1500*time.Millisecond)
	}
}

func (a *App) connect(ctx context.Context) (net.Conn, protocol.Packet, transport.Device, error) {
	if a.opt.Addr != "" {
		c, h, err := transport.Dial(ctx, a.opt.Addr)
		if err != nil {
			a.setPhase(PhaseWaitingApp, "Waiting for phone at "+a.opt.Addr)
			return nil, h, transport.Device{}, err
		}
		return c, h, transport.Device{Serial: a.opt.Addr}, nil
	}
	path, err := transport.FindADB(a.opt.ExeDir)
	if err != nil {
		a.setPhase(PhaseNoADB, "adb.exe not found — keep the bundled platform-tools next to macawlink.exe")
		return nil, protocol.Packet{}, transport.Device{}, err
	}
	adb := transport.ADB{Path: path}
	devs, err := adb.Devices(ctx)
	if err != nil {
		a.setPhase(PhaseNoADB, "adb is not responding: "+err.Error())
		return nil, protocol.Packet{}, transport.Device{}, err
	}
	var dev transport.Device
	unauthorized := false
	for _, d := range devs {
		if a.opt.Serial != "" && d.Serial != a.opt.Serial {
			continue
		}
		if d.State == "device" {
			dev = d
			break
		}
		if d.State == "unauthorized" {
			unauthorized = true
		}
	}
	a.mu.Lock()
	for serial := range a.autoAt { // forget phones that were unplugged
		present := false
		for _, d := range devs {
			present = present || d.Serial == serial
		}
		if !present {
			delete(a.autoAt, serial)
		}
	}
	a.mu.Unlock()
	if dev.Serial == "" {
		if unauthorized {
			a.setPhase(PhaseUnauthorized, "Tap “Allow USB debugging” on your phone")
		} else {
			a.setPhase(PhaseNoDevice, "Plug in your phone by USB (USB debugging on)")
		}
		return nil, protocol.Packet{}, dev, errors.New("no device")
	}
	if err := adb.Forward(ctx, dev.Serial, protocol.DefaultPort); err != nil {
		a.setPhase(PhaseNoDevice, "adb forward failed: "+err.Error())
		return nil, protocol.Packet{}, dev, err
	}
	c, h, err := transport.Dial(ctx, fmt.Sprintf("127.0.0.1:%d", protocol.DefaultPort))
	if err != nil {
		name := dev.Model
		if name == "" {
			name = dev.Serial
		}
		a.setPhase(PhaseWaitingApp, "Found "+name+" — open Macawlink on the phone and tap Start")
		// Open the app on the phone once per plug-in (not repeatedly, so Stop on the phone sticks).
		a.mu.Lock()
		_, done := a.autoAt[dev.Serial]
		a.autoAt[dev.Serial] = time.Now()
		a.mu.Unlock()
		if !done {
			if err := adb.StartApp(ctx, dev.Serial); err == nil {
				log.Printf("asked %s to open Macawlink", dev.Serial)
			}
		}
		return nil, h, dev, err
	}
	return c, h, dev, nil
}

func (a *App) session(ctx context.Context, conn net.Conn, helloPkt protocol.Packet, dev transport.Device) error {
	defer conn.Close()
	var hello protocol.HelloMsg
	if err := json.Unmarshal(helloPkt.Payload, &hello); err != nil {
		return fmt.Errorf("bad HELLO: %w", err)
	}
	name := hello.Model
	if hello.Manufacturer != "" {
		name = hello.Manufacturer + " " + hello.Model
	}
	a.mu.Lock()
	a.conn = conn
	a.seq = 0
	a.status.Device, a.status.Serial, a.status.Cameras = name, dev.Serial, hello.Cameras
	a.status.Transport = "USB"
	if a.opt.Addr != "" {
		a.status.Transport = "TCP " + a.opt.Addr
	}
	if a.ring != nil {
		a.ring.SetConnected(true)
	}
	a.mu.Unlock()
	host, _ := os.Hostname()
	ack, _ := json.Marshal(protocol.HelloAckMsg{PCName: host, Protocol: 1})
	if err := a.send(protocol.HelloAck, 0, ack); err != nil {
		return err
	}
	a.setPhase(PhaseStreaming, "Streaming from "+name)

	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	vch := make(chan protocol.Packet, 240)
	ach := make(chan protocol.Packet, 200)
	go func() {
		for {
			select {
			case <-sctx.Done():
				return
			case p := <-vch:
				if p.Type == protocol.VideoConfig {
					a.video.HandleConfig(p.Payload)
				} else {
					a.video.HandleFrame(p.Payload, p.Flags)
				}
			}
		}
	}()
	go func() {
		for {
			select {
			case <-sctx.Done():
				return
			case p := <-ach:
				if p.Type == protocol.AudioConfig {
					if err := a.audio.HandleConfig(p.Payload); err != nil {
						log.Printf("audio: %v", err)
					}
				} else {
					a.audio.HandleFrame(p.Payload)
				}
			}
		}
	}()
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-sctx.Done():
				return
			case <-t.C:
				a.send(protocol.Ping, 0, protocol.PingPayload(time.Now().UnixNano()))
			}
		}
	}()
	go func() {
		<-sctx.Done()
		if ctx.Err() != nil {
			a.send(protocol.Bye, 0, []byte(`{"reason":"pc closing"}`))
		}
		conn.SetReadDeadline(time.Now())
	}()

	for {
		conn.SetReadDeadline(time.Now().Add(10 * time.Second))
		p, err := protocol.Read(conn)
		if err != nil {
			return err
		}
		switch p.Type {
		case protocol.VideoConfig, protocol.VideoFrame:
			select {
			case vch <- p:
			default:
				// Decoder fell behind: flush, then resync on the next keyframe.
				for len(vch) > 0 {
					<-vch
				}
				a.video.Discontinuity()
				if p.Type == protocol.VideoConfig {
					vch <- p
				}
			}
		case protocol.AudioConfig:
			ach <- p
		case protocol.AudioFrame:
			select {
			case ach <- p:
			default:
				<-ach
				ach <- p
			}
		case protocol.ConfigState:
			var s config.Settings
			if err := json.Unmarshal(p.Payload, &s); err == nil {
				a.adopt(s)
			}
		case protocol.Stats:
			var st protocol.StatsMsg
			if json.Unmarshal(p.Payload, &st) == nil {
				a.mu.Lock()
				a.status.Phone = st
				a.mu.Unlock()
			}
		case protocol.Ping:
			if pong, err := protocol.PongPayload(p.Payload, time.Now().UnixNano()); err == nil {
				a.send(protocol.Pong, 0, pong)
			}
		case protocol.Pong:
			if sent, _, err := protocol.ParsePong(p.Payload); err == nil {
				a.mu.Lock()
				a.status.RTTms = float64(time.Now().UnixNano()-sent) / 1e6
				a.mu.Unlock()
			}
		case protocol.Bye:
			return errors.New("phone said bye: " + string(p.Payload))
		}
	}
}

func (a *App) send(t protocol.Type, flags uint8, payload []byte) error {
	a.mu.Lock()
	c := a.conn
	a.mu.Unlock()
	if c == nil {
		return errors.New("not connected")
	}
	a.wmu.Lock()
	defer a.wmu.Unlock()
	a.seq++
	c.SetWriteDeadline(time.Now().Add(3 * time.Second))
	return protocol.Write(c, protocol.Packet{Type: t, Flags: flags, Seq: a.seq, Payload: payload})
}

// adopt takes the phone's CONFIG_STATE as truth, keeping PC-only fields.
func (a *App) adopt(s config.Settings) {
	a.mu.Lock()
	s.Audio.GainDB = a.settings.Audio.GainDB
	s.Video.Mirror = a.settings.Video.Mirror
	if s.Video.Zoom == 0 {
		s.Video.Zoom = 1
	}
	a.settings = s
	a.mu.Unlock()
	if a.opt.SaveConfig {
		if err := config.Save(s); err != nil {
			log.Printf("config: %v", err)
		}
	}
}

func (a *App) codecFailed(codec string) {
	if codec != "hevc" {
		return
	}
	log.Printf("video: HEVC decoder unavailable (install “HEVC Video Extensions”); switching phone to H.264")
	s := a.Settings()
	s.Video.Codec = "h264"
	a.ApplySettings(s)
}

// ---------------------------------------------------------------------------
// API for the tray and the web page

func (a *App) Settings() config.Settings {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.settings
}

// ApplySettings validates, saves and pushes settings to the phone (live).
func (a *App) ApplySettings(s config.Settings) error {
	if err := s.Validate(); err != nil {
		return err
	}
	a.mu.Lock()
	a.settings = s
	a.mu.Unlock()
	if a.opt.SaveConfig {
		if err := config.Save(s); err != nil {
			log.Printf("config: %v", err)
		}
	}
	b, _ := json.Marshal(s)
	if err := a.send(protocol.SetConfig, 0, b); err != nil {
		log.Printf("settings saved; phone not connected (%v)", err)
	}
	return nil
}

// ApplyPreset switches to a preset, keeping lens, mirror and gain.
func (a *App) ApplyPreset(name string) error {
	p, ok := config.Presets[name]
	if !ok {
		return fmt.Errorf("unknown preset %q", name)
	}
	cur := a.Settings()
	p.Video.Lens, p.Video.Mirror, p.Audio.GainDB = cur.Video.Lens, cur.Video.Mirror, cur.Audio.GainDB
	return a.ApplySettings(p)
}

func (a *App) RequestKeyframe() { a.send(protocol.RequestKeyframe, 0, nil) }

func (a *App) Status() Status {
	v, au := a.video.Stats(), a.audio.Stats()
	a.mu.Lock()
	defer a.mu.Unlock()
	st := a.status
	st.Video, st.Audio = v, au
	return st
}

func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}
