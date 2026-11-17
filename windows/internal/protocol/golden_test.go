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

func TestGoldenSAS(t *testing.T) {
	b, err := os.ReadFile("../../../proto-golden/pairing.json")
	if err != nil {
		t.Fatal(err)
	}
	var g struct{ CertFP, PCNonce, PhoneNonce, SAS, Commit string }
	var raw map[string]string
	json.Unmarshal(b, &raw)
	g.CertFP, g.PCNonce, g.PhoneNonce, g.SAS, g.Commit = raw["cert_fp"], raw["pc_nonce"], raw["phone_nonce"], raw["sas"], raw["commit"]
	fp, _ := hex.DecodeString(g.CertFP)
	pn, _ := hex.DecodeString(g.PCNonce)
	ph, _ := hex.DecodeString(g.PhoneNonce)
	sas, commit := SAS(fp, pn, ph), Commit(pn)
	if len(sas) != 6 {
		t.Fatalf("sas %q", sas)
	}
	if g.SAS == "" { // first run: record the expected values for the Kotlin test
		raw["sas"], raw["commit"] = sas, commit
		out, _ := json.Marshal(raw)
		os.WriteFile("../../../proto-golden/pairing.json", append(out, '\n'), 0o644)
		return
	}
	if sas != g.SAS || commit != g.Commit {
		t.Fatalf("pairing derivation changed: sas %s want %s, commit %s want %s", sas, g.SAS, commit, g.Commit)
	}
}
