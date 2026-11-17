package video

import "testing"

func solid(w, h int, y, u, v byte) []byte {
	b := make([]byte, w*h*3/2)
	for i := 0; i < w*h; i++ {
		b[i] = y
	}
	for i := w * h; i < len(b); i += 2 {
		b[i], b[i+1] = u, v
	}
	return b
}

func TestScaleSolidStaysSolid(t *testing.T) {
	src := solid(1920, 1080, 200, 50, 100)
	for _, d := range [][2]int{{1280, 720}, {640, 480}, {1920, 1080}} {
		dst := make([]byte, d[0]*d[1]*3/2)
		ScaleNV12(dst, d[0], d[1], src, 1920, 1080, true)
		for i := 0; i < d[0]*d[1]; i++ {
			if dst[i] != 200 {
				t.Fatalf("%v: Y[%d]=%d", d, i, dst[i])
			}
		}
		for i := d[0] * d[1]; i < len(dst); i += 2 {
			if dst[i] != 50 || dst[i+1] != 100 {
				t.Fatalf("%v: UV[%d]=%d,%d", d, i, dst[i], dst[i+1])
			}
		}
	}
}

func TestScaleMirrorAndCrop(t *testing.T) {
	// Left half dark, right half bright; 16:9 -> 4:3 crop keeps the centre.
	w, h := 64, 36
	src := solid(w, h, 0, 128, 128)
	for y := 0; y < h; y++ {
		for x := w / 2; x < w; x++ {
			src[y*w+x] = 255
		}
	}
	dw, dh := 32, 24
	dst := make([]byte, dw*dh*3/2)
	ScaleNV12(dst, dw, dh, src, w, h, false)
	if dst[0] != 0 || dst[dw-1] != 255 {
		t.Fatalf("no mirror: left=%d right=%d", dst[0], dst[dw-1])
	}
	ScaleNV12(dst, dw, dh, src, w, h, true)
	if dst[0] != 255 || dst[dw-1] != 0 {
		t.Fatalf("mirror: left=%d right=%d", dst[0], dst[dw-1])
	}
}

func BenchmarkScale1080To720(b *testing.B) {
	src := solid(1920, 1080, 1, 2, 3)
	dst := make([]byte, 1280*720*3/2)
	b.SetBytes(int64(len(dst)))
	for i := 0; i < b.N; i++ {
		ScaleNV12(dst, 1280, 720, src, 1920, 1080, false)
	}
}

func TestRotateNV12(t *testing.T) {
	// 4x2 luma with distinct values; chroma 2x1 pairs.
	w, h := 4, 2
	src := []byte{
		1, 2, 3, 4,
		5, 6, 7, 8,
		10, 11, 20, 21, // UV pairs: (10,11) (20,21)
	}
	dst := make([]byte, len(src))
	nw, nh := RotateNV12(dst, src, w, h, 90)
	// 90° clockwise: first row becomes last column.
	want := []byte{5, 1, 6, 2, 7, 3, 8, 4}
	if nw != 2 || nh != 4 || string(dst[:8]) != string(want) {
		t.Fatalf("90: %dx%d %v", nw, nh, dst[:8])
	}
	if dst[8] != 10 || dst[10] != 20 {
		t.Fatalf("90 chroma %v", dst[8:])
	}
	RotateNV12(dst, src, w, h, 180)
	if string(dst[:8]) != string([]byte{8, 7, 6, 5, 4, 3, 2, 1}) || dst[8] != 20 || dst[10] != 10 {
		t.Fatalf("180: %v", dst)
	}
	nw, nh = RotateNV12(dst, src, w, h, 270)
	if nw != 2 || nh != 4 || string(dst[:8]) != string([]byte{4, 8, 3, 7, 2, 6, 1, 5}) {
		t.Fatalf("270: %v", dst[:8])
	}
}
