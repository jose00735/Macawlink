package audio

import (
	"errors"
	"testing"

	"github.com/jose00735/macawlink/windows/internal/native"
	"github.com/jose00735/macawlink/windows/internal/protocol"
)

// The level meter must reflect the phone's mic even when no virtual mic is installed,
// and must read silent (-90 dB) before any audio arrives.
func TestLevelWithoutOutput(t *testing.T) {
	p := &Pipeline{Open: func(int, int) (native.AudioOut, error) { return nil, errors.New("not found") }}
	if err := p.HandleConfig(protocol.JoinConfig(protocol.AudioConfigMsg{Codec: "pcm", SampleRate: 48000, Channels: 1, FrameMs: 10}, nil)); err != nil {
		t.Fatal(err)
	}
	if st := p.Stats(); st.LevelDB != -90 || st.LastError == "" || st.Endpoint != "" {
		t.Fatalf("before audio: %+v", st)
	}
	pcm := make([]byte, 960)
	pcm[0], pcm[1] = 0x00, 0x40 // 16384 = -6 dBFS
	p.HandleFrame(protocol.MediaPayload(0, pcm))
	if st := p.Stats(); st.LevelDB < -7 || st.LevelDB > -5 {
		t.Fatalf("level = %v dB, want about -6", st.LevelDB)
	}
}
