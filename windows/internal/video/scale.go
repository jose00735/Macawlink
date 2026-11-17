package video

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

// scalePlane resamples one plane. Coordinates are in samples; bpp is 1 (Y) or 2 (UV pairs).
func scalePlane(dst []byte, dw, dh, dstStride int, src []byte, srcStride, cx, cy, cw, ch, bpp int, mirror bool) {
	const fp = 16
	// Precompute horizontal taps.
	x0 := make([]int, dw)
	wx := make([]int, dw)
	for x := 0; x < dw; x++ {
		dx := x
		if mirror {
			dx = dw - 1 - x
		}
		// Sample centre mapping: (dx+0.5)*cw/dw - 0.5
		pos := ((2*dx+1)*cw<<fp)/(2*dw) - (1 << (fp - 1))
		if pos < 0 {
			pos = 0
		}
		i := pos >> fp
		f := pos & (1<<fp - 1)
		if i >= cw-1 {
			i, f = cw-1, 0
		}
		x0[x] = (cx + i) * bpp
		wx[x] = f
	}
	for y := 0; y < dh; y++ {
		pos := ((2*y+1)*ch<<fp)/(2*dh) - (1 << (fp - 1))
		if pos < 0 {
			pos = 0
		}
		j := pos >> fp
		fy := pos & (1<<fp - 1)
		if j >= ch-1 {
			j, fy = ch-1, 0
		}
		r0 := src[(cy+j)*srcStride:]
		r1 := r0
		if fy != 0 {
			r1 = src[(cy+j+1)*srcStride:]
		}
		out := dst[y*dstStride : y*dstStride+dw*bpp]
		for x := 0; x < dw; x++ {
			s := x0[x]
			fx := wx[x]
			for c := 0; c < bpp; c++ {
				a := int(r0[s+c])
				b := a
				if fx != 0 {
					b = int(r0[s+bpp+c])
				}
				top := a + ((b-a)*fx)>>fp
				v := top
				if fy != 0 {
					a2 := int(r1[s+c])
					b2 := a2
					if fx != 0 {
						b2 = int(r1[s+bpp+c])
					}
					bot := a2 + ((b2-a2)*fx)>>fp
					v = top + ((bot-top)*fy)>>fp
				}
				out[x*bpp+c] = byte(v)
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
