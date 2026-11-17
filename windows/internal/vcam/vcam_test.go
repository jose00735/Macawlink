package vcam

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// Mirrors the reads windows/native/vcam_source.cpp performs, to pin the shared layout.
func TestPublishLayout(t *testing.T) {
	mem := make([]byte, RingBytes)
	r, err := NewRingFromMemory(mem, nil)
	if err != nil {
		t.Fatal(err)
	}
	if binary.LittleEndian.Uint32(mem[0:]) != Magic || binary.LittleEndian.Uint32(mem[4:]) != Version {
		t.Fatal("header not initialised")
	}
	// Consumer side writes the requested format.
	binary.LittleEndian.PutUint32(mem[96:], 1280)
	binary.LittleEndian.PutUint32(mem[100:], 720)
	binary.LittleEndian.PutUint32(mem[104:], 30)
	if w, h, f := r.Requested(); w != 1280 || h != 720 || f != 30 {
		t.Fatalf("requested = %d %d %d", w, h, f)
	}

	for i := 0; i < 4; i++ {
		frame := bytes.Repeat([]byte{byte(10 + i)}, 640*480*3/2)
		if err := r.Publish(frame, 640, 480, int64(i)*333333); err != nil {
			t.Fatal(err)
		}
		latest := binary.LittleEndian.Uint32(mem[8:])
		seq := binary.LittleEndian.Uint64(mem[16:])
		if latest != uint32(i%Slots) || seq != uint64(i+1) {
			t.Fatalf("frame %d: latest=%d seq=%d", i, latest, seq)
		}
		meta := 24 + int(latest)*24
		if binary.LittleEndian.Uint32(mem[meta+8:]) != 640 || binary.LittleEndian.Uint32(mem[meta+12:]) != 480 {
			t.Fatal("slot meta size wrong")
		}
		if mem[HeaderBytes+int(latest)*SlotBytes] != byte(10+i) {
			t.Fatal("slot data wrong")
		}
	}
	r.SetConnected(true)
	if binary.LittleEndian.Uint32(mem[12:]) != 1 {
		t.Fatal("connected flag")
	}
	if err := r.Publish(make([]byte, 10), 3840, 2160, 0); err == nil {
		t.Fatal("expected size error")
	}
}
