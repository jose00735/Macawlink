// Package protocol implements the Macawlink wire format described in docs/PROTOCOL.md.
// Keep it byte-for-byte in sync with android/.../protocol/Packet.kt.
package protocol

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const (
	Magic       uint16 = 0x4D4B // "MK"
	Version     uint8  = 1
	HeaderSize         = 16
	MaxPayload         = 4 << 20 // 4 MiB
	DefaultPort        = 47800   // USB: phone listens on loopback, PC dials through adb forward
	WiFiPort           = 47802   // Wi-Fi: PC listens (TLS 1.3), phone dials
	MDNSService        = "_macawlink._tcp"
)

// Type identifies a message.
type Type uint8

const (
	Hello           Type = 0x01
	HelloAck        Type = 0x02
	VideoConfig     Type = 0x10
	VideoFrame      Type = 0x11
	AudioConfig     Type = 0x20
	AudioFrame      Type = 0x21
	SetConfig       Type = 0x30
	ConfigState     Type = 0x31
	RequestKeyframe Type = 0x32
	CameraControl   Type = 0x33
	Ping            Type = 0x40
	Pong            Type = 0x41
	Stats           Type = 0x42
	PairBegin       Type = 0x50 // PC→phone {commit, pc_name}
	PairNonce       Type = 0x51 // phone→PC {nonce}
	PairReveal      Type = 0x52 // PC→phone {nonce}
	PairConfirm     Type = 0x53 // phone→PC {ok}
	PairResult      Type = 0x54 // PC→phone {ok, token, reason}
	PrompterScript  Type = 0x60 // PC→phone teleprompter text (phones that announce "prompter")
	PrompterState   Type = 0x61 // PC→phone position, several times a second
	PrompterClear   Type = 0x62 // PC→phone hide the teleprompter
	PrompterKey     Type = 0x63 // phone→PC {key}: a tap on the phone
	Bye             Type = 0x7F
)

// Flags on a packet.
const (
	FlagKeyframe   uint8 = 1 << 0
	FlagConfig     uint8 = 1 << 1
	FlagEndOfFrame uint8 = 1 << 2
)

// Packet is one framed message.
type Packet struct {
	Type    Type
	Flags   uint8
	Seq     uint32
	Payload []byte
}

var (
	ErrBadMagic   = errors.New("protocol: bad magic")
	ErrBadVersion = errors.New("protocol: unsupported version")
	ErrTooLarge   = errors.New("protocol: payload too large")
)

// Write encodes p to w.
func Write(w io.Writer, p Packet) error {
	if len(p.Payload) > MaxPayload {
		return ErrTooLarge
	}
	var h [HeaderSize]byte
	binary.BigEndian.PutUint16(h[0:2], Magic)
	h[2] = Version
	h[3] = byte(p.Type)
	h[4] = p.Flags
	binary.BigEndian.PutUint32(h[8:12], p.Seq)
	binary.BigEndian.PutUint32(h[12:16], uint32(len(p.Payload)))
	if _, err := w.Write(h[:]); err != nil {
		return err
	}
	_, err := w.Write(p.Payload)
	return err
}

// Read decodes the next packet from r.
func Read(r io.Reader) (Packet, error) {
	var h [HeaderSize]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return Packet{}, err
	}
	if binary.BigEndian.Uint16(h[0:2]) != Magic {
		return Packet{}, ErrBadMagic
	}
	if h[2] != Version {
		return Packet{}, fmt.Errorf("%w: %d", ErrBadVersion, h[2])
	}
	n := binary.BigEndian.Uint32(h[12:16])
	if n > MaxPayload {
		return Packet{}, ErrTooLarge
	}
	p := Packet{
		Type:    Type(h[3]),
		Flags:   h[4],
		Seq:     binary.BigEndian.Uint32(h[8:12]),
		Payload: make([]byte, n),
	}
	_, err := io.ReadFull(r, p.Payload)
	return p, err
}

// MediaPTS splits a media payload into its capture timestamp (µs) and data.
func MediaPTS(payload []byte) (ptsUs int64, data []byte, err error) {
	if len(payload) < 8 {
		return 0, nil, errors.New("protocol: media payload shorter than pts")
	}
	return int64(binary.BigEndian.Uint64(payload[:8])), payload[8:], nil
}
