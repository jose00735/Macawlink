// Package session runs one connected phone: demux packets, drive the video and audio
// pipelines, answer control messages and push setting changes from the tray.
package session

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"

	"github.com/jose00735/macawlink/windows/internal/config"
	"github.com/jose00735/macawlink/windows/internal/protocol"
)

type Session struct {
	conn     io.ReadWriteCloser
	settings config.Settings
	seq      uint32

	Video chan protocol.Packet // drop-oldest consumer: video pipeline
	Audio chan protocol.Packet // audio pipeline
}

func New(conn io.ReadWriteCloser, s config.Settings) *Session {
	return &Session{
		conn:     conn,
		settings: s,
		Video:    make(chan protocol.Packet, 8),
		Audio:    make(chan protocol.Packet, 32),
	}
}

// Run reads packets until the context ends or the phone says BYE.
func (s *Session) Run(ctx context.Context) error {
	defer s.conn.Close()
	// Push the PC's saved preferences so the phone starts with them.
	if err := s.PushSettings(s.settings); err != nil {
		return err
	}
	for ctx.Err() == nil {
		p, err := protocol.Read(s.conn)
		if err != nil {
			return err
		}
		switch p.Type {
		case protocol.VideoConfig, protocol.VideoFrame:
			offerDropOldest(s.Video, p)
		case protocol.AudioConfig, protocol.AudioFrame:
			offerDropOldest(s.Audio, p)
		case protocol.ConfigState:
			if err := json.Unmarshal(p.Payload, &s.settings); err != nil {
				log.Printf("bad CONFIG_STATE: %v", err)
			}
		case protocol.Ping:
			// TODO: reply PONG with receive timestamp for clock-offset estimation.
		case protocol.Bye:
			return nil
		}
	}
	return ctx.Err()
}

// PushSettings sends a SET_CONFIG; the phone answers with CONFIG_STATE and new *_CONFIG packets.
func (s *Session) PushSettings(next config.Settings) error {
	if err := next.Validate(); err != nil {
		return fmt.Errorf("refusing invalid settings: %w", err)
	}
	b, _ := json.Marshal(next)
	s.seq++
	return protocol.Write(s.conn, protocol.Packet{Type: protocol.SetConfig, Seq: s.seq, Payload: b})
}

// RequestKeyframe is sent when an app opens the camera mid-stream.
func (s *Session) RequestKeyframe() error {
	s.seq++
	return protocol.Write(s.conn, protocol.Packet{Type: protocol.RequestKeyframe, Seq: s.seq})
}

func offerDropOldest(ch chan protocol.Packet, p protocol.Packet) {
	for {
		select {
		case ch <- p:
			return
		default:
			select {
			case <-ch:
			default:
			}
		}
	}
}
