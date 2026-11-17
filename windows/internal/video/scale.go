package video

import (
	"runtime"
	"sync"
)

// ScaleNV12 center-crops src (sw×sh) to the destination aspect ratio and bilinearly scales it
// into dst (dw×dh), optionally mirrored horizontally. Both buffers are tight NV12.
// When sizes match and mirror is off it is a plain copy.
func ScaleNV12(dst []byte, dw, dh int, src []byte, sw, sh int, mirror bool) {
	if dw == sw && dh == sh && !mirror {
		copy(dst[:dw*dh*3/2], src[:sw*sh*3/2])
		return
	}
	// Crop rectangle in source pixels (even-aligned for chroma).
	cx, cy, cw, ch := 0, 0, sw, sh
	if sw*dh > dw*sh { // source wider than destination
		cw = (sh * dw / dh) &^ 1
		cx = ((sw - cw) / 2) &^ 1
	} else if sw*dh < dw*sh { // source taller
		ch = (sw * dh / dw) &^ 1
		cy = ((sh - ch) / 2) &^ 1
	}

	scalePlane(dst[:dw*dh], dw, dh, dw, src, sw, cx, cy, cw, ch, 1, mirror)
	// Interleaved UV plane: half resolution, 2 bytes per sample.
	scalePlane(dst[dw*dh:], dw/2, dh/2, dw, src[sw*sh:], sw, cx/2, cy/2, cw/2, ch/2, 2, mirror)
}

// scalePlane resamples one plane with bilinear filtering (8-bit fixed-point weights).
// Coordinates are in samples; bpp is 1 (Y) or 2 (interleaved UV pairs).
func scalePlane(dst []byte, dw, dh, dstStride int, src []byte, srcStride, cx, cy, cw, ch, bpp int, mirror bool) {
	const fp = 16
	// Horizontal taps: byte offsets of the left/right neighbours and the right weight (0..256).
	x0 := make([]int32, dw)
	x1 := make([]int32, dw)
	wx := make([]int32, dw)
	for x := 0; x < dw; x++ {
		dx := x
		if mirror {
			dx = dw - 1 - x
		}
		pos := ((2*dx+1)*cw<<fp)/(2*dw) - (1 << (fp - 1))
		if pos < 0 {
			pos = 0
		}
		i := pos >> fp
		f := (pos & (1<<fp - 1)) >> (fp - 8)
		j := i + 1
		if i >= cw-1 {
			i, j, f = cw-1, cw-1, 0
		}
		x0[x] = int32((cx + i) * bpp)
		x1[x] = int32((cx + j) * bpp)
		wx[x] = int32(f)
	}
	rows := func(y0, y1 int) {
		for y := y0; y < y1; y++ {
			scaleRow(dst, dw, dh, dstStride, src, srcStride, cy, ch, bpp, y, x0, x1, wx)
		}
	}
	// Bands in parallel: frames are big and PCs have cores to spare.
	workers := runtime.NumCPU()
	if workers > 8 {
		workers = 8
	}
	if workers < 2 || dw*dh < 320*240 {
		rows(0, dh)
		return
	}
	var wg sync.WaitGroup
	band := (dh + workers - 1) / workers
	for y := 0; y < dh; y += band {
		end := y + band
		if end > dh {
			end = dh
		}
		wg.Add(1)
		go func(a, b int) { defer wg.Done(); rows(a, b) }(y, end)
	}
	wg.Wait()
}

func scaleRow(dst []byte, dw, dh, dstStride int, src []byte, srcStride, cy, ch, bpp, y int, x0, x1, wx []int32) {
	const fp = 16
	{
		pos := ((2*y+1)*ch<<fp)/(2*dh) - (1 << (fp - 1))
		if pos < 0 {
			pos = 0
		}
		j := pos >> fp
		fy := int32((pos & (1<<fp - 1)) >> (fp - 8))
		j1 := j + 1
		if j >= ch-1 {
			j, j1, fy = ch-1, ch-1, 0
		}
		r0 := src[(cy+j)*srcStride : (cy+j)*srcStride+srcStride]
		r1 := src[(cy+j1)*srcStride : (cy+j1)*srcStride+srcStride]
		out := dst[y*dstStride : y*dstStride+dw*bpp]
		iy := 256 - fy
		if bpp == 1 {
			for x := range out {
				a, b := int32(r0[x0[x]]), int32(r0[x1[x]])
				c, d := int32(r1[x0[x]]), int32(r1[x1[x]])
				f := wx[x]
				top := a*(256-f) + b*f
				bot := c*(256-f) + d*f
				out[x] = byte((top*iy + bot*fy + 32768) >> 16)
			}
			return
		}
		for x := 0; x < dw; x++ {
			s0, s1, f := x0[x], x1[x], wx[x]
			nf := 256 - f
			for k := int32(0); k < 2; k++ {
				top := int32(r0[s0+k])*nf + int32(r0[s1+k])*f
				bot := int32(r1[s0+k])*nf + int32(r1[s1+k])*f
				out[2*x+int(k)] = byte((top*iy + bot*fy + 32768) >> 16)
			}
		}
	}
}

// RotateNV12 rotates a tight NV12 frame clockwise by deg (0, 90, 180, 270) into dst and
// returns the new width and height. dst must hold w*h*3/2 bytes.
func RotateNV12(dst, src []byte, w, h, deg int) (int, int) {
	n := w * h
	switch deg {
	case 90, 270:
		nw, nh := h, w
		for y := 0; y < h; y++ {
			row := src[y*w:]
			for x := 0; x < w; x++ {
				var dx, dy int
				if deg == 90 {
					dx, dy = h-1-y, x
				} else {
					dx, dy = y, w-1-x
				}
				dst[dy*nw+dx] = row[x]
			}
		}
		cw, ch := w/2, h/2
		for y := 0; y < ch; y++ {
			row := src[n+y*w:]
			for x := 0; x < cw; x++ {
				var dx, dy int
				if deg == 90 {
					dx, dy = ch-1-y, x
				} else {
					dx, dy = y, cw-1-x
				}
				o := n + dy*nw + dx*2
				dst[o], dst[o+1] = row[2*x], row[2*x+1]
			}
		}
		return nw, nh
	case 180:
		for i := 0; i < n; i++ {
			dst[i] = src[n-1-i]
		}
		pairs := n / 4
		for i := 0; i < pairs; i++ {
			s := n + (pairs-1-i)*2
			dst[n+i*2], dst[n+i*2+1] = src[s], src[s+1]
		}
		return w, h
	}
	copy(dst[:n*3/2], src[:n*3/2])
	return w, h
}
