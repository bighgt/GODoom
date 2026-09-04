package assets

// Pixel helpers for composing a ZDoom TEXTURES definition (texturesdef.go).

// orientRGBA returns src rotated by 0/90/180/270 degrees (clockwise) then
// mirrored per flipX / flipY. Returns src unchanged when there's nothing to
// do, else a fresh bitmap. OffsetX/OffsetY are carried but not transformed
// (ZDoom's UseOffsets on a rotated patch is a rare corner).
func orientRGBA(src *RGBA, rotate int, flipX, flipY bool) *RGBA {
	if rotate == 0 && !flipX && !flipY {
		return src
	}
	sw, sh := src.Width, src.Height
	dw, dh := sw, sh
	if rotate == 90 || rotate == 270 {
		dw, dh = sh, sw
	}
	out := &RGBA{Width: dw, Height: dh, Pix: make([]byte, dw*dh*4),
		OffsetX: src.OffsetX, OffsetY: src.OffsetY, TexelsPerUnit: src.TexelsPerUnit}
	for y := 0; y < sh; y++ {
		for x := 0; x < sw; x++ {
			var nx, ny int
			switch rotate {
			case 90:
				nx, ny = sh-1-y, x
			case 180:
				nx, ny = sw-1-x, sh-1-y
			case 270:
				nx, ny = y, sw-1-x
			default:
				nx, ny = x, y
			}
			if flipX {
				nx = dw - 1 - nx
			}
			if flipY {
				ny = dh - 1 - ny
			}
			si := (y*sw + x) * 4
			di := (ny*dw + nx) * 4
			copy(out.Pix[di:di+4], src.Pix[si:si+4])
		}
	}
	return out
}

// blendTintRGBA multiplies every opaque texel of src by tint.rgb and, when
// tint[3] < 1, lerps toward the tint colour by (1-tint[3])... actually
// ZDoom's `Blend r,g,b` is a straight multiply; `Blend r,g,b,a` lerps the
// texel toward the colour by a. Transparent texels are left alone.
func blendTintRGBA(src *RGBA, tint [4]float64) *RGBA {
	out := &RGBA{Width: src.Width, Height: src.Height, Pix: make([]byte, len(src.Pix)),
		OffsetX: src.OffsetX, OffsetY: src.OffsetY, TexelsPerUnit: src.TexelsPerUnit}
	copy(out.Pix, src.Pix)
	a := tint[3]
	hasLerp := a > 0 && a < 1
	for i := 0; i+4 <= len(out.Pix); i += 4 {
		if out.Pix[i+3] == 0 {
			continue
		}
		for c := 0; c < 3; c++ {
			v := float64(out.Pix[i+c])
			if hasLerp {
				v = v*(1-a) + tint[c]*255*a
			} else {
				v *= tint[c]
			}
			out.Pix[i+c] = clampByte(v)
		}
	}
	return out
}

// compositePatch stamps src onto dst at (ox, oy) per the ZDoom render
// style. Only "add" and the translucent family differ from a plain copy;
// the exotic styles (modulate, subtract, ...) fall back to translucent
// with a note left by the parser.
func compositePatch(dst, src *RGBA, ox, oy int, style string, alpha float64) {
	add := style == "add" || style == "overlay"
	translucent := style == "translucent" || style == "copyalpha" || style == "modulate" ||
		style == "subtract" || style == "reversesubtract" || alpha < 1

	for sy := 0; sy < src.Height; sy++ {
		dy := oy + sy
		if dy < 0 || dy >= dst.Height {
			continue
		}
		for sx := 0; sx < src.Width; sx++ {
			dx := ox + sx
			if dx < 0 || dx >= dst.Width {
				continue
			}
			si := (sy*src.Width + sx) * 4
			sa := src.Pix[si+3]
			if sa == 0 {
				continue
			}
			di := (dy*dst.Width + dx) * 4
			ca := alpha * float64(sa) / 255

			switch {
			case add:
				for c := 0; c < 3; c++ {
					dst.Pix[di+c] = clampByte(float64(dst.Pix[di+c]) + float64(src.Pix[si+c])*ca)
				}
				if dst.Pix[di+3] < 255 {
					dst.Pix[di+3] = clampByte(float64(dst.Pix[di+3]) + float64(sa)*alpha)
				}
			case translucent:
				for c := 0; c < 3; c++ {
					dst.Pix[di+c] = clampByte(float64(dst.Pix[di+c])*(1-ca) + float64(src.Pix[si+c])*ca)
				}
				if na := clampByte(float64(dst.Pix[di+3]) + float64(sa)*alpha); na > dst.Pix[di+3] {
					dst.Pix[di+3] = na
				}
			default: // copy
				copy(dst.Pix[di:di+4], src.Pix[si:si+4])
			}
		}
	}
}

func clampByte(v float64) byte {
	if v <= 0 {
		return 0
	}
	if v >= 255 {
		return 255
	}
	return byte(v + 0.5)
}
