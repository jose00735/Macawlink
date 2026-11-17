// Package fakephone emulates the Android app's side of the protocol: it is used by the
// end-to-end tests and by cmd/fakephone to exercise the receiver without a phone.
package fakephone

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net"
	"sync"
	"time"

	"github.com/jose00735/macawlink/windows/internal/config"
	"github.com/jose00735/macawlink/windows/internal/protocol"
)

type Options struct {
	PhoneID   string                 // Wi-Fi: phone identity
	Token     string                 // Wi-Fi: token from an earlier pairing
	Confirm   func(code string) bool // Wi-Fi pairing: phone user compares the code
	Model     string
	Settings  config.Settings
	H264      []byte // Annex-B elementary stream to loop; nil = no video
	Frames    int    // stop after N video frames (0 = forever)
	AudioOnly bool
	ToneHz    float64
}

// Received records what the PC sent to us.
type Received struct {
	Token      string // set after a successful pairing
	Code       string // pairing code shown on the "phone"
	mu         sync.Mutex
	Types      []protocol.Type
	SetConfigs []config.Settings
	Keyframes  int
}

func (r *Received) add(p protocol.Packet) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Types = append(r.Types, p.Type)
	switch p.Type {
	case protocol.SetConfig:
		var s config.Settings
		if json.Unmarshal(p.Payload, &s) == nil {
			r.SetConfigs = append(r.SetConfigs, s)
		}
	case protocol.RequestKeyframe:
		r.Keyframes++
	}
}

// Pairing returns the code shown and the token received (thread-safe).
func (r *Received) Pairing() (code, token string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.Code, r.Token
}

func (r *Received) Snapshot() ([]protocol.Type, []config.Settings, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]protocol.Type(nil), r.Types...), append([]config.Settings(nil), r.SetConfigs...), r.Keyframes
}

// Serve speaks the phone protocol on c (USB mode) until the PC disconnects or done closes.
func Serve(c net.Conn, opt Options, rec *Received, done <-chan struct{}) error {
	return serve(c, opt, rec, done, nil)
}

// DialWiFi connects like the app does over Wi-Fi: TLS 1.3 to the PC, pinning pinnedFP when
// given (a paired PC), otherwise accepting any certificate and pairing.
func DialWiFi(addr string, opt Options, rec *Received, done <-chan struct{}, pinnedFP []byte) error {
	var seen []byte
	cfg := &tls.Config{
		MinVersion:         tls.VersionTLS13,
		InsecureSkipVerify: true, // trust comes from pairing + pinning below
		VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
			sum := sha256.Sum256(raw[0])
			seen = sum[:]
			if pinnedFP != nil && !bytes.Equal(seen, pinnedFP) {
				return errors.New("PC certificate changed")
			}
			return nil
		},
	}
	c, err := tls.Dial("tcp", addr, cfg)
	if err != nil {
		return err
	}
	return serve(c, opt, rec, done, seen)
}

func awaitAck(c net.Conn, send func(protocol.Type, uint8, []byte) error, opt Options, rec *Received, certFP []byte) error {
	var commit string
	var myNonce []byte
	for {
		p, err := protocol.Read(c)
		if err != nil {
			return err
		}
		switch p.Type {
		case protocol.HelloAck:
			return nil
		case protocol.Bye:
			return errors.New("PC said bye: " + string(p.Payload))
		case protocol.PairBegin:
			var m protocol.PairBeginMsg
			json.Unmarshal(p.Payload, &m)
			commit = m.Commit
			myNonce = make([]byte, 32)
			rand.Read(myNonce)
			b, _ := json.Marshal(protocol.PairNonceMsg{Nonce: hex.EncodeToString(myNonce)})
			send(protocol.PairNonce, 0, b)
		case protocol.PairReveal:
			var m protocol.PairNonceMsg
			json.Unmarshal(p.Payload, &m)
			pcNonce, _ := hex.DecodeString(m.Nonce)
			ok := protocol.Commit(pcNonce) == commit
			code := protocol.SAS(certFP, pcNonce, myNonce)
			if rec != nil {
				rec.mu.Lock()
				rec.Code = code
				rec.mu.Unlock()
			}
			if ok && opt.Confirm != nil {
				ok = opt.Confirm(code)
			}
			b, _ := json.Marshal(protocol.PairConfirmMsg{OK: ok})
			send(protocol.PairConfirm, 0, b)
		case protocol.PairResult:
			var m protocol.PairResultMsg
			json.Unmarshal(p.Payload, &m)
			if !m.OK {
				return errors.New("pairing failed: " + m.Reason)
			}
			if rec != nil {
				rec.mu.Lock()
				rec.Token = m.Token
				rec.mu.Unlock()
			}
		}
	}
}

func serve(c net.Conn, opt Options, rec *Received, done <-chan struct{}, certFP []byte) error {
	defer c.Close()
	var wmu sync.Mutex
	seq := uint32(0)
	send := func(t protocol.Type, flags uint8, payload []byte) error {
		wmu.Lock()
		defer wmu.Unlock()
		seq++
		return protocol.Write(c, protocol.Packet{Type: t, Flags: flags, Seq: seq, Payload: payload})
	}
	settings := opt.Settings
	var smu sync.Mutex

	hello, _ := json.Marshal(protocol.HelloMsg{
		Model: opt.Model, Manufacturer: "Fake", AppVersion: "test", Protocol: 1,
		Cameras: []protocol.CameraInfo{{ID: "0", Facing: "back", Sizes: [][2]int{{1280, 720}, {1920, 1080}}, FPS: []int{30, 60}}},
		PhoneID: opt.PhoneID, Token: opt.Token,
	})
	if err := send(protocol.Hello, 0, hello); err != nil {
		return err
	}
	// Wait for HELLO_ACK; on Wi-Fi the PC may run pairing first.
	if err := awaitAck(c, send, opt, rec, certFP); err != nil {
		return err
	}
	st, _ := json.Marshal(settings)
	send(protocol.ConfigState, 0, st)

	readErr := make(chan error, 1)
	go func() {
		for {
			p, err := protocol.Read(c)
			if err != nil {
				readErr <- err
				return
			}
			if rec != nil {
				rec.add(p)
			}
			switch p.Type {
			case protocol.Ping:
				if pong, err := protocol.PongPayload(p.Payload, time.Now().UnixNano()); err == nil {
					send(protocol.Pong, 0, pong)
				}
			case protocol.SetConfig:
				var s config.Settings
				if json.Unmarshal(p.Payload, &s) == nil {
					smu.Lock()
					settings = s
					smu.Unlock()
					b, _ := json.Marshal(s)
					send(protocol.ConfigState, 0, b)
				}
			case protocol.Bye:
				readErr <- io.EOF
				return
			}
		}
	}()

	// Audio
	smu.Lock()
	rate, ch, frameMs := settings.Audio.SampleRate, settings.Audio.Channels, settings.Audio.FrameMs
	smu.Unlock()
	send(protocol.AudioConfig, 0, protocol.JoinConfig(protocol.AudioConfigMsg{Codec: "pcm", SampleRate: rate, Channels: ch, FrameMs: frameMs}, nil))

	// Video
	var aus []AU
	var csd []byte
	if opt.H264 != nil && !opt.AudioOnly {
		aus, csd = SplitAnnexB(opt.H264)
		send(protocol.VideoConfig, protocol.FlagConfig, protocol.JoinConfig(protocol.VideoConfigMsg{
			Codec: "h264", Width: settings.Video.Width, Height: settings.Video.Height, FPS: settings.Video.FPS}, csd))
	}

	start := time.Now()
	tone := opt.ToneHz
	if tone == 0 {
		tone = 440
	}
	audioTick := time.NewTicker(time.Duration(frameMs) * time.Millisecond)
	defer audioTick.Stop()
	fps := settings.Video.FPS
	if fps == 0 {
		fps = 30
	}
	videoTick := time.NewTicker(time.Second / time.Duration(fps))
	defer videoTick.Stop()
	sampleN := 0
	vi, sent := 0, 0
	for {
		select {
		case <-done:
			send(protocol.Bye, 0, []byte(`{"reason":"test done"}`))
			return nil
		case err := <-readErr:
			return err
		case <-audioTick.C:
			n := rate * frameMs / 1000
			pcm := make([]byte, n*ch*2)
			for i := 0; i < n; i++ {
				v := int16(8000 * math.Sin(2*math.Pi*tone*float64(sampleN)/float64(rate)))
				sampleN++
				for c := 0; c < ch; c++ {
					binary.LittleEndian.PutUint16(pcm[(i*ch+c)*2:], uint16(v))
				}
			}
			if err := send(protocol.AudioFrame, 0, protocol.MediaPayload(time.Since(start).Microseconds(), pcm)); err != nil {
				return err
			}
		case <-videoTick.C:
			if len(aus) == 0 || (opt.Frames > 0 && sent >= opt.Frames) {
				continue
			}
			au := aus[vi%len(aus)]
			vi++
			sent++
			var flags uint8
			if au.Key {
				flags = protocol.FlagKeyframe
			}
			if err := send(protocol.VideoFrame, flags, protocol.MediaPayload(time.Since(start).Microseconds(), au.Data)); err != nil {
				return err
			}
		}
	}
}

// AU is one access unit (VCL NAL plus any SEI/AUD that preceded it).
type AU struct {
	Data []byte
	Key  bool
}

// SplitAnnexB splits an H.264 Annex-B stream into access units (one slice per picture
// assumed) and returns SPS+PPS separately as codec config, like MediaCodec does.
func SplitAnnexB(b []byte) ([]AU, []byte) {
	var nals [][]byte
	i := 0
	start := -1
	for i+3 <= len(b) {
		if b[i] == 0 && b[i+1] == 0 && (b[i+2] == 1 || (i+3 < len(b) && b[i+2] == 0 && b[i+3] == 1)) {
			if start >= 0 {
				nals = append(nals, b[start:i])
			}
			start = i
			if b[i+2] == 1 {
				i += 3
			} else {
				i += 4
			}
			continue
		}
		i++
	}
	if start >= 0 {
		nals = append(nals, b[start:])
	}
	var aus []AU
	var csd, pending bytes.Buffer
	seenCSD := false
	for _, n := range nals {
		h := bytes.IndexByte(n[2:], 1) + 3 // header byte after start code
		if h >= len(n) {
			continue
		}
		typ := n[h] & 0x1f
		switch {
		case typ == 7 || typ == 8:
			if !seenCSD || len(aus) == 0 {
				csd.Write(n)
			}
		case typ == 1 || typ == 5:
			seenCSD = csd.Len() > 0
			pending.Write(n)
			aus = append(aus, AU{Data: append([]byte(nil), pending.Bytes()...), Key: typ == 5})
			pending.Reset()
		default:
			pending.Write(n)
		}
	}
	return aus, csd.Bytes()
}
