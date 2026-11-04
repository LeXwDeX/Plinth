package modelinspect

import (
	"bytes"
	"image"
	"image/draw"
	"math"
)

// decodedTexture is a texture decoded to non-premultiplied 8-bit RGBA plus
// the sampler wrap modes. RGB samples are converted to linear light with a
// lookup table because the renderer works in linear space.
type decodedTexture struct {
	width, height int
	pixels        []uint8
	wrapS, wrapT  int
}

// decodeTexture decodes the base-color texture identified by textureIndex.
// A nil result with a nil error means the image exceeds MaxTexturePixels and
// the caller must fall back to material factors.
func (m *Model) decodeTexture(textureIndex int) (*decodedTexture, error) {
	m.textureCache.mu.Lock()
	defer m.textureCache.mu.Unlock()
	if texture, ok := m.textureCache.images[textureIndex]; ok {
		return texture, nil
	}
	texture := m.Textures[textureIndex]
	info := m.Images[texture.Source]
	if m.limits.MaxTexturePixels > 0 &&
		int64(info.Width)*int64(info.Height) > m.limits.MaxTexturePixels {
		m.textureCache.images[textureIndex] = nil
		return nil, nil
	}
	view := m.bufferViews[info.BufferView]
	data, err := readAt(m.src, view.offset, view.length)
	if err != nil {
		return nil, err
	}
	decoded, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, invalidf("image %d cannot be decoded", texture.Source)
	}
	if decoded.Bounds().Dx() != info.Width || decoded.Bounds().Dy() != info.Height {
		return nil, invalidf("image %d dimensions changed after decoding", texture.Source)
	}
	nrgba, ok := decoded.(*image.NRGBA)
	if !ok {
		nrgba = image.NewNRGBA(image.Rect(0, 0, info.Width, info.Height))
		draw.Draw(nrgba, nrgba.Bounds(), decoded, decoded.Bounds().Min, draw.Src)
	}
	wrapS, wrapT := 10497, 10497
	if texture.Sampler >= 0 {
		sampler := m.samplers[texture.Sampler]
		wrapS, wrapT = sampler.wrapS, sampler.wrapT
	}
	result := &decodedTexture{
		width: info.Width, height: info.Height, pixels: nrgba.Pix,
		wrapS: wrapS, wrapT: wrapT,
	}
	m.textureCache.images[textureIndex] = result
	return result, nil
}

// sample returns bilinearly interpolated linear RGBA at (u, v). glTF UVs use
// a top-left origin, which matches image.NRGBA's row order, so v is not
// flipped here.
func (t *decodedTexture) sample(u, v float64) [4]float64 {
	if !finite(u) || !finite(v) {
		return [4]float64{0, 0, 0, 0}
	}
	u = wrapCoordinate(u, t.wrapS)
	v = wrapCoordinate(v, t.wrapT)
	x := u*float64(t.width) - 0.5
	y := v*float64(t.height) - 0.5
	x0 := int(math.Floor(x))
	y0 := int(math.Floor(y))
	fx := x - float64(x0)
	fy := y - float64(y0)
	x0Index := t.index(x0, t.wrapS, t.width)
	x1Index := t.index(x0+1, t.wrapS, t.width)
	y0Index := t.index(y0, t.wrapT, t.height)
	y1Index := t.index(y0+1, t.wrapT, t.height)
	topLeft := t.texel(x0Index, y0Index)
	topRight := t.texel(x1Index, y0Index)
	bottomLeft := t.texel(x0Index, y1Index)
	bottomRight := t.texel(x1Index, y1Index)
	var out [4]float64
	for channel := 0; channel < 4; channel++ {
		top := topLeft[channel] + (topRight[channel]-topLeft[channel])*fx
		bottom := bottomLeft[channel] + (bottomRight[channel]-bottomLeft[channel])*fx
		out[channel] = top + (bottom-top)*fy
	}
	return out
}

func (t *decodedTexture) texel(x, y int) [4]float64 {
	offset := (y*t.width + x) * 4
	if offset < 0 || offset+4 > len(t.pixels) {
		return [4]float64{}
	}
	return [4]float64{
		srgbToLinear[t.pixels[offset]],
		srgbToLinear[t.pixels[offset+1]],
		srgbToLinear[t.pixels[offset+2]],
		float64(t.pixels[offset+3]) / 255,
	}
}

func wrapCoordinate(value float64, wrap int) float64 {
	switch wrap {
	case 33071: // CLAMP_TO_EDGE
		return math.Min(math.Max(value, 0), 1)
	case 33648: // MIRRORED_REPEAT
		value = math.Mod(value, 2)
		if value < 0 {
			value += 2
		}
		if value > 1 {
			value = 2 - value
		}
		return value
	default: // REPEAT
		value -= math.Floor(value)
		return value
	}
}

func (t *decodedTexture) index(value int, wrap, size int) int {
	if size <= 0 {
		return 0
	}
	switch wrap {
	case 10497: // REPEAT
		value %= size
		if value < 0 {
			value += size
		}
		return value
	default: // CLAMP_TO_EDGE, MIRRORED_REPEAT (coordinate already in [0,1])
		return min(max(value, 0), size-1)
	}
}

// srgbToLinear maps an 8-bit sRGB channel to linear light.
var srgbToLinear = func() (table [256]float64) {
	for i := range table {
		value := float64(i) / 255
		if value <= 0.04045 {
			table[i] = value / 12.92
		} else {
			table[i] = math.Pow((value+0.055)/1.055, 2.4)
		}
	}
	return table
}()

// linearToSRGB is a 12-bit lookup for the linear-to-sRGB transfer function.
var linearToSRGB = func() (table [4097]uint8) {
	for i := range table {
		value := float64(i) / 4096
		var encoded float64
		if value <= 0.0031308 {
			encoded = value * 12.92
		} else {
			encoded = 1.055*math.Pow(value, 1/2.4) - 0.055
		}
		table[i] = uint8(math.Round(math.Min(math.Max(encoded, 0), 1) * 255))
	}
	return table
}()

func linearToSRGBByte(value float64) uint8 {
	if !finite(value) || value <= 0 {
		return linearToSRGB[0]
	}
	if value >= 1 {
		return linearToSRGB[4096]
	}
	return linearToSRGB[int(value*4096+0.5)]
}
