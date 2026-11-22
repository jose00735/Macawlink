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
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jose00735/macawlink/windows/internal/audio"
	"github.com/jose00735/macawlink/windows/internal/config"
	"github.com/jose00735/macawlink/windows/internal/native"
	"github.com/jose00735/macawlink/windows/internal/pairing"
	"github.com/jose00735/macawlink/windows/internal/protocol"
	"github.com/jose00735/macawlink/windows/internal/transport"
	"github.com/jose00735/macawlink/windows/internal/vcam"
	"github.com/jose00735/macawlink/windows/internal/video"
)

const Version = "0.4.0"

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
	Mode       string `json:"mode"` // "Media Foundation" (Win11) | "DirectShow" (Win10)
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
	WiFi      WiFiStatus            `json:"wifi"`
	Pairing   *PairingStatus        `json:"pairing,omitempty"`
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
	// RingSources open the camera rings: the Windows 11 Frame Server ring and/or the
	// session-local ring read by the Windows 10 DirectShow camera. Retried every second.
	RingSources []func() (*vcam.Ring, error)
	Camera      CameraStatus // filled by main after registering the virtual camera
	SaveConfig  bool         // persist settings to %APPDATA% (off in tests)

	// Wi-Fi (Phase 2). WiFiAddr "" disables it.
	WiFiAddr  string
	Identity  *pairing.Identity
	Pairs     *pairing.Store
	OnPairing func(phone, code string) // e.g. tray balloon + open the settings page
}

type App struct {
	opt Options

	mu       sync.Mutex
	settings config.Settings
	status   Status
	rings    []*vcam.Ring // indexed like opt.RingSources; nil until open
	conn     net.Conn
	autoAt   map[string]time.Time

	wmu sync.Mutex
	seq uint32

	active     atomic.Bool   // one phone session at a time (USB or Wi-Fi)
	usbBackoff time.Duration // between Wi-Fi → USB upgrade attempts
	pairing    *pairingState
	pairFail   map[string]time.Time // per-IP cool-down after a failed/declined pairing

	video *video.Pipeline
	audio *audio.Pipeline

	// teleprompter feed (TeleCyborg → phone screen); kept so a reconnecting phone gets it again
	prompterOK     bool   // the connected phone announced "prompter"
	prompterScript []byte // last PROMPTER_SCRIPT payload
	prompterState  []byte // last PROMPTER_STATE payload
	prompterKeys   []string
}

func New(opt Options, s config.Settings) *App {
	a := &App{opt: opt, settings: s, autoAt: map[string]time.Time{}, rings: make([]*vcam.Ring, len(opt.RingSources))}
	a.status = Status{Version: Version, Phase: PhaseStarting, Message: "Starting…", Camera: opt.Camera}
	a.video = &video.Pipeline{
		NewDecoder:      opt.NewDecoder,
		Sinks:           a.sinks,
		RequestKeyframe: func() { a.send(protocol.RequestKeyframe, 0, nil) },
		OnCodecFailed:   a.codecFailed,
		Mirror:          func() bool { a.mu.Lock(); defer a.mu.Unlock(); return a.settings.Video.Mirror },
		AudioDelay: func() time.Duration {
			st := a.audio.Stats()
			if st.Endpoint == "" {
				return 0
			}
			return time.Duration(st.QueuedMs) * time.Millisecond
		},
	}
	a.audio = &audio.Pipeline{
		Open:   opt.OpenAudio,
		GainDB: func() int { a.mu.Lock(); defer a.mu.Unlock(); return a.settings.Audio.GainDB },
	}
	return a
}

func (a *App) sinks() []video.Sink {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []video.Sink
	for _, r := range a.rings {
		if r != nil {
			out = append(out, r)
		}
	}
	return out
}

func (a *App) setConnectedLocked(on bool) {
	for _, r := range a.rings {
		if r != nil {
			r.SetConnected(on)
		}
	}
}

// Run blocks until ctx ends.
func (a *App) Run(ctx context.Context) {
	go a.ringLoop(ctx)
	if a.opt.WiFiAddr != "" && a.opt.Identity != nil && a.opt.Pairs != nil {
		go a.wifiLoop(ctx)
	}
	a.connLoop(ctx)
}

// ---------------------------------------------------------------------------
// camera ring

func (a *App) ringLoop(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		for i, open := range a.opt.RingSources {
			a.mu.Lock()
			have := a.rings[i] != nil
			a.mu.Unlock()
			if have {
				continue
			}
			if r, err := open(); err == nil {
				a.mu.Lock()
				a.rings[i] = r
				r.SetConnected(a.status.Phase == PhaseStreaming)
				a.mu.Unlock()
				log.Printf("camera: frame ring %d open", i)
			}
		}
		a.mu.Lock()
		wasInUse := a.status.Camera.InUse
		cs := &a.status.Camera
		cs.RingOpen, cs.InUse, cs.AppWidth, cs.AppHeight, cs.AppFPS, cs.Delivered = false, false, 0, 0, 0, 0
		for _, r := range a.rings {
			if r == nil {
				continue
			}
			cs.RingOpen = true
			cs.Delivered += r.Delivered()
			if w, h, fps := r.Requested(); w > 0 && !cs.InUse {
				cs.InUse, cs.AppWidth, cs.AppHeight, cs.AppFPS = true, w, h, fps
			}
		}
		nowInUse, w, h, fps := cs.InUse, cs.AppWidth, cs.AppHeight, cs.AppFPS
		a.mu.Unlock()
		if !wasInUse && nowInUse {
			log.Printf("camera: app opened %dx%d@%d", w, h, fps)
			a.send(protocol.RequestKeyframe, 0, nil) // first picture right away
		}
		select {
		case <-ctx.Done():
			a.mu.Lock()
			for i, r := range a.rings {
				if r != nil {
					r.Close()
					a.rings[i] = nil
				}
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
	var usbRetryAt time.Time
	for ctx.Err() == nil {
		if a.active.Load() {
			// On Wi-Fi? Plugging in the cable upgrades the link to USB (lower latency, charging).
			// The phone agrees only in its Auto link mode; otherwise it says bye and we back off.
			if a.currentTransport() == "Wi-Fi" && time.Now().After(usbRetryAt) {
				if a.upgradeToUSB(ctx) {
					continue
				}
				usbRetryAt = time.Now().Add(a.usbBackoff)
			}
			sleep(ctx, time.Second)
			continue
		}
		conn, hello, dev, err := a.connect(ctx, false)
		if err != nil {
			sleep(ctx, 2*time.Second)
			continue
		}
		if !a.active.CompareAndSwap(false, true) {
			conn.Close()
			continue
		}
		a.runSession(ctx, conn, hello, dev, a.usbName())
		sleep(ctx, 1500*time.Millisecond)
	}
}

func (a *App) usbName() string {
	if a.opt.Addr != "" {
		return "TCP " + a.opt.Addr
	}
	return "USB"
}

func (a *App) currentTransport() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.status.Transport
}

// upgradeToUSB dials the phone over USB while a Wi-Fi session runs. A phone in Auto mode
// ends its Wi-Fi session and answers HELLO here; we then take over once Wi-Fi has wound down.
func (a *App) upgradeToUSB(ctx context.Context) bool {
	conn, hello, dev, err := a.connect(ctx, true)
	if err != nil {
		if strings.Contains(err.Error(), "bye") { // phone is in Wi-Fi-only mode (or an old version)
			a.usbBackoff = 30 * time.Second
		} else {
			a.usbBackoff = 2 * time.Second
		}
		return false
	}
	deadline := time.Now().Add(6 * time.Second)
	for a.active.Load() && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if !a.active.CompareAndSwap(false, true) {
		conn.Close()
		return false
	}
	log.Printf("link: switched from Wi-Fi to %s", a.usbName())
	a.runSession(ctx, conn, hello, dev, a.usbName())
	return true
}

// runSession streams until the phone leaves, then resets everything. a.active must be set.
func (a *App) runSession(ctx context.Context, conn net.Conn, hello protocol.Packet, dev transport.Device, transportName string) {
	defer a.active.Store(false)
	err := a.session(ctx, conn, hello, dev, transportName)
	if err != nil && ctx.Err() == nil {
		log.Printf("session ended: %v", err)
	}
	a.video.Reset()
	a.audio.Reset()
	a.mu.Lock()
	a.conn = nil
	a.setConnectedLocked(false)
	a.status.Video, a.status.Phone, a.status.RTTms, a.status.Cameras = video.Stats{}, protocol.StatsMsg{}, 0, nil
	a.status.Transport = ""
	a.mu.Unlock()
	a.setPhase(PhaseWaitingApp, "Phone disconnected — waiting for it to reconnect…")
}

// connect finds and dials the USB phone. quiet: don't touch the status or launch the app
// (used for the Wi-Fi → USB upgrade probe while streaming).
func (a *App) connect(ctx context.Context, quiet bool) (net.Conn, protocol.Packet, transport.Device, error) {
	setPhase := a.setPhase
	if quiet {
		setPhase = func(string, string) {}
	}
	if a.opt.Addr != "" {
		c, h, err := transport.Dial(ctx, a.opt.Addr)
		if err != nil {
			setPhase(PhaseWaitingApp, "Waiting for phone at "+a.opt.Addr)
			return nil, h, transport.Device{}, err
		}
		return c, h, transport.Device{Serial: a.opt.Addr}, nil
	}
	path, err := transport.FindADB(a.opt.ExeDir)
	if err != nil {
		setPhase(PhaseNoADB, "adb.exe not found — keep the bundled platform-tools next to macawlink.exe")
		return nil, protocol.Packet{}, transport.Device{}, err
	}
	adb := transport.ADB{Path: path}
	devs, err := adb.Devices(ctx)
	if err != nil {
		setPhase(PhaseNoADB, "adb is not responding: "+err.Error())
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
			setPhase(PhaseUnauthorized, "Tap “Allow USB debugging” on your phone")
		} else {
			setPhase(PhaseNoDevice, a.idleHint())
		}
		return nil, protocol.Packet{}, dev, errors.New("no device")
	}
	if err := adb.Forward(ctx, dev.Serial, protocol.DefaultPort); err != nil {
		setPhase(PhaseNoDevice, "adb forward failed: "+err.Error())
		return nil, protocol.Packet{}, dev, err
	}
	c, h, err := transport.Dial(ctx, fmt.Sprintf("127.0.0.1:%d", protocol.DefaultPort))
	if err != nil {
		name := dev.Model
		if name == "" {
			name = dev.Serial
		}
		setPhase(PhaseWaitingApp, "Found "+name+" — open Macawlink on the phone and tap Start")
		// Open the app on the phone once per plug-in (not repeatedly, so Stop on the phone sticks).
		a.mu.Lock()
		_, done := a.autoAt[dev.Serial]
		a.autoAt[dev.Serial] = time.Now()
		a.mu.Unlock()
		if !done && !quiet {
			if err := adb.StartApp(ctx, dev.Serial); err == nil {
				log.Printf("asked %s to open Macawlink", dev.Serial)
			}
		}
		return nil, h, dev, err
	}
	return c, h, dev, nil
}

func (a *App) session(ctx context.Context, conn net.Conn, helloPkt protocol.Packet, dev transport.Device, transportName string) error {
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
	a.status.Transport = transportName
	a.setConnectedLocked(true)
	a.prompterOK = hello.HasFeature(protocol.FeaturePrompter)
	script, pstate := a.prompterScript, a.prompterState
	a.mu.Unlock()
	defer func() { a.mu.Lock(); a.prompterOK = false; a.mu.Unlock() }()
	host, _ := os.Hostname()
	ack, _ := json.Marshal(protocol.HelloAckMsg{PCName: host, Protocol: 1})
	if err := a.send(protocol.HelloAck, 0, ack); err != nil {
		return err
	}
	if hello.HasFeature(protocol.FeaturePrompter) && script != nil {
		a.send(protocol.PrompterScript, 0, script)
		if pstate != nil {
			a.send(protocol.PrompterState, 0, pstate)
		}
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
		case protocol.PrompterKey:
			var k protocol.PrompterKeyMsg
			if json.Unmarshal(p.Payload, &k) == nil && k.Key != "" {
				a.mu.Lock()
				if len(a.prompterKeys) < 32 {
					a.prompterKeys = append(a.prompterKeys, k.Key)
				}
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
	s.Normalize() // older phone apps don't send the Phase 3 fields
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

// PrompterInfo says whether a phone that can show the teleprompter is streaming.
type PrompterInfo struct {
	Available bool   `json:"available"`
	Phone     bool   `json:"phone"` // a connected phone supports the prompter
	Device    string `json:"device,omitempty"`
	Active    bool   `json:"active"` // a script is being shown
}

// Prompter reports the teleprompter feed state.
func (a *App) Prompter() PrompterInfo {
	a.mu.Lock()
	defer a.mu.Unlock()
	return PrompterInfo{Available: true, Phone: a.prompterOK, Device: a.status.Device, Active: a.prompterScript != nil}
}

// SetPrompterScript forwards teleprompter text (JSON from TeleCyborg) to the phone.
func (a *App) SetPrompterScript(payload []byte) {
	a.mu.Lock()
	a.prompterScript, a.prompterState = payload, nil
	ok := a.prompterOK
	a.mu.Unlock()
	if ok {
		a.send(protocol.PrompterScript, 0, payload)
	}
}

// SetPrompterState forwards the reading position.
func (a *App) SetPrompterState(payload []byte) {
	a.mu.Lock()
	a.prompterState = payload
	ok := a.prompterOK && a.prompterScript != nil
	a.mu.Unlock()
	if ok {
		a.send(protocol.PrompterState, 0, payload)
	}
}

// ClearPrompter hides the teleprompter on the phone.
func (a *App) ClearPrompter() {
	a.mu.Lock()
	a.prompterScript, a.prompterState, a.prompterKeys = nil, nil, nil
	ok := a.prompterOK
	a.mu.Unlock()
	if ok {
		a.send(protocol.PrompterClear, 0, nil)
	}
}

// PrompterKeys returns and clears the taps made on the phone.
func (a *App) PrompterKeys() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	k := a.prompterKeys
	a.prompterKeys = nil
	if k == nil {
		k = []string{}
	}
	return k
}

func (a *App) Status() Status {
	v, au := a.video.Stats(), a.audio.Stats()
	a.mu.Lock()
	defer a.mu.Unlock()
	st := a.status
	st.Video, st.Audio = v, au
	if a.pairing != nil && a.pairing.status.Code != "" {
		ps := a.pairing.status
		st.Pairing = &ps
	}
	return st
}

func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}
