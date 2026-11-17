package protocol

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	payload := make([]byte, 8+3)
	binary.BigEndian.PutUint64(payload, 123456)
	copy(payload[8:], []byte{0, 0, 1})

	in := Packet{Type: VideoFrame, Flags: FlagKeyframe, Seq: 42, Payload: payload}
	if err := Write(&buf, in); err != nil {
		t.Fatal(err)
	}
	out, err := Read(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if out.Type != in.Type || out.Flags != in.Flags || out.Seq != in.Seq || !bytes.Equal(out.Payload, in.Payload) {
		t.Fatalf("mismatch: %+v vs %+v", out, in)
	}
	pts, data, err := MediaPTS(out.Payload)
	if err != nil || pts != 123456 || len(data) != 3 {
		t.Fatalf("pts=%d len=%d err=%v", pts, len(data), err)
	}
}

func TestBadMagic(t *testing.T) {
	if _, err := Read(bytes.NewReader(make([]byte, HeaderSize))); err != ErrBadMagic {
		t.Fatalf("want ErrBadMagic, got %v", err)
	}
}
