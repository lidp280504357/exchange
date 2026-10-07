package avatars

import (
	"encoding/binary"
	"image"
)

// jpegOrientation reads the EXIF orientation (tag 0x0112, 1 to 8) of a
// JPEG: phones store photos as the sensor saw them and say how to turn
// them; the metadata is not kept, so the turn is made here. 1 (as is)
// when there is none or it cannot be read.
func jpegOrientation(b []byte) int {
	if len(b) < 4 || b[0] != 0xFF || b[1] != 0xD8 {
		return 1
	}
	for i := 2; i+4 <= len(b); {
		if b[i] != 0xFF {
			return 1
		}
		marker := b[i+1]
		if marker == 0xD8 || (marker >= 0xD0 && marker <= 0xD7) || marker == 0x01 {
			i += 2
			continue
		}
		if marker == 0xDA || marker == 0xD9 { // the image data starts: no EXIF before it
			return 1
		}
		size := int(binary.BigEndian.Uint16(b[i+2:]))
		if size < 2 || i+2+size > len(b) {
			return 1
		}
		seg := b[i+4 : i+2+size]
		if marker == 0xE1 && len(seg) >= 6 && string(seg[:6]) == "Exif\x00\x00" {
			return tiffOrientation(seg[6:])
		}
		i += 2 + size
	}
	return 1
}

// tiffOrientation finds the orientation in the first IFD of a TIFF
// header.
func tiffOrientation(t []byte) int {
	if len(t) < 8 {
		return 1
	}
	var order binary.ByteOrder
	switch string(t[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 1
	}
	ifd := int(order.Uint32(t[4:]))
	if ifd < 8 || ifd+2 > len(t) {
		return 1
	}
	n := int(order.Uint16(t[ifd:]))
	for k := range n {
		e := ifd + 2 + 12*k
		if e+12 > len(t) {
			return 1
		}
		if order.Uint16(t[e:]) == 0x0112 && order.Uint16(t[e+2:]) == 3 { // SHORT
			if v := int(order.Uint16(t[e+8:])); v >= 1 && v <= 8 {
				return v
			}
			return 1
		}
	}
	return 1
}

// orient turns a square image upright by its EXIF orientation: 2 mirror,
// 3 half turn, 4 flip, 5 transpose, 6 quarter turn clockwise, 7
// transverse, 8 quarter turn counter-clockwise.
func orient(src *image.NRGBA, orientation int) *image.NRGBA {
	if orientation <= 1 || orientation > 8 {
		return src
	}
	n := src.Bounds().Dx() // square
	dst := image.NewNRGBA(image.Rect(0, 0, n, n))
	for y := range n {
		for x := range n {
			// (sx, sy) is the source pixel that lands at (x, y).
			var sx, sy int
			switch orientation {
			case 2:
				sx, sy = n-1-x, y
			case 3:
				sx, sy = n-1-x, n-1-y
			case 4:
				sx, sy = x, n-1-y
			case 5:
				sx, sy = y, x
			case 6:
				sx, sy = y, n-1-x
			case 7:
				sx, sy = n-1-y, n-1-x
			case 8:
				sx, sy = n-1-y, x
			}
			si, di := src.PixOffset(sx, sy), dst.PixOffset(x, y)
			copy(dst.Pix[di:di+4], src.Pix[si:si+4])
		}
	}
	return dst
}
