package protocol

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/jose00735/macawlink/windows/internal/config"
)

// Shared with android/app/src/test/.../GoldenTest.kt.
func TestGoldenPacket(t *testing.T) {
	want, _ := os.ReadFile("../../../proto-golden/video_frame_packet.hex")
	var buf bytes.Buffer
	Write(&buf, Packet{Type: VideoFrame, Flags: FlagKeyframe, Seq: 42, Payload: MediaPayload(123456, []byte{0, 0, 1})})
	if got := hex.EncodeToString(buf.Bytes()); got != strings.TrimSpace(string(want)) {
		t.Fatalf("wire bytes changed:\n got %s\nwant %s", got, want)
	}
}

func TestGoldenSettings(t *testing.T) {
	b, err := os.ReadFile("../../../proto-golden/settings.json")
	if err != nil {
		t.Fatal(err)
	}
	var s config.Settings
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil {
		t.Fatalf("Kotlin defaults don't parse in Go: %v", err)
	}
	if err := s.Validate(); err != nil {
		t.Fatalf("Kotlin defaults invalid for Go: %v", err)
	}
	if s != config.Default() {
		t.Fatalf("defaults differ:\n go %+v\n kt %+v", config.Default(), s)
	}
}
