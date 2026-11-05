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

// decodedImage is the decoded pixel data of one glTF image, shared by every
// texture that references that image.
type decodedImage struct {
	width, height int
	pixels        []uint8
}

// decodeTexture decodes the base-color texture identified by textureIndex.
// A nil result with a nil error means the image exceeds MaxTexturePixels and
// the caller must fall back to material factors. Exceeding either cumulative
// budget is an ErrTooLarge error so decoding stops before more pixels are
// allocated.
func (m *Model) decodeTexture(textureIndex int) (*decodedTexture, error) {
	m.textureCache.mu.Lock()
	defer m.textureCache.mu.Unlock()
	if texture, ok := m.textureCache.images[textureIndex]; ok {
		return texture, nil
	}
	if textureIndex < 0 || textureIndex >= len(m.Textures) {
		return nil, invalidf("texture index %d is out of range", textureIndex)
	}
	texture := m.Textures[textureIndex]
	if texture.Source < 0 || texture.Source >= len(m.Images) {
		return nil, invalidf("texture %d references an invalid image", textureIndex)
	}
	if m.textureCache.decoded == nil {
		m.textureCache.decoded = make(map[int]*decodedImage)
	}
	decoded, ok := m.textureCache.decoded[texture.Source]
	if !ok {
		var err error
		decoded, err = m.decodeImage(texture.Source)
		if err != nil {
			return nil, err
		}
		m.textureCache.decoded[texture.Source] = decoded
	}
	if decoded == nil {
		m.textureCache.images[textureIndex] = nil
		return nil, nil
	}
	wrapS, wrapT := 10497, 10497
	if texture.Sampler >= 0 {
		if texture.Sampler >= len(m.samplers) {
			return nil, invalidf("texture %d references an invalid sampler", textureIndex)
		}
		sampler := m.samplers[texture.Sampler]
		wrapS, wrapT = sampler.wrapS, sampler.wrapT
	}
	result := &decodedTexture{
		width: decoded.width, height: decoded.height, pixels: decoded.pixels,
		wrapS: wrapS, wrapT: wrapT,
	}
	m.textureCache.images[textureIndex] = result
	return result, nil
}

// decodeImage decodes one image and charges the cumulative pixel and byte
// budgets once per distinct image. It returns nil without an error when the
// image exceeds MaxTexturePixels, which the caller reports as a factor
// fallback.
func (m *Model) decodeImage(imageIndex int) (*decodedImage, error) {
	info := m.Images[imageIndex]
	width, height := int64(info.Width), int64(info.Height)
	if width <= 0 || height <= 0 || width > math.MaxInt64/height {
		return nil, invalidf("image %d has invalid dimensions", imageIndex)
	}
	pixels := width * height
	if m.limits.MaxTexturePixels > 0 && pixels > m.limits.MaxTexturePixels {
		return nil, nil
	}
	if pixels > math.MaxInt64/4 {
		return nil, toolargef("image %d is too large to decode", imageIndex)
	}
	decodedBytes := pixels * 4
	if !withinBudget(m.textureCache.pixels, pixels, m.limits.MaxTotalTexturePixels) {
		return nil, toolargef("decoded texture pixels exceed MaxTotalTexturePixels")
	}
	if !withinBudget(m.textureCache.bytes, decodedBytes, m.limits.MaxTextureBytes) {
		return nil, toolargef("decoded texture bytes exceed MaxTextureBytes")
	}
	view := m.bufferViews[info.BufferView]
	data, err := readAt(m.src, view.offset, view.length)
	if err != nil {
		return nil, err
	}
	decoded, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, invalidf("image %d cannot be decoded", imageIndex)
	}
	if decoded.Bounds().Dx() != info.Width || decoded.Bounds().Dy() != info.Height {
		return nil, invalidf("image %d dimensions changed after decoding", imageIndex)
	}
	nrgba, ok := decoded.(*image.NRGBA)
	if !ok {
		nrgba = image.NewNRGBA(image.Rect(0, 0, info.Width, info.Height))
		draw.Draw(nrgba, nrgba.Bounds(), decoded, decoded.Bounds().Min, draw.Src)
	}
	m.textureCache.pixels += pixels
	m.textureCache.bytes += decodedBytes
	m.decodedBytes.Add(int64(cap(nrgba.Pix)))
	return &decodedImage{width: info.Width, height: info.Height, pixels: nrgba.Pix}, nil
}

// apply maps a texture coordinate as KHR_texture_transform defines: scale,
// then rotate, then offset.
func (t *TextureTransform) apply(u, v float64) (float64, float64) {
	u, v = u*t.Scale[0], v*t.Scale[1]
	cos, sin := math.Cos(t.Rotation), math.Sin(t.Rotation)
	return cos*u + sin*v + t.Offset[0], -sin*u + cos*v + t.Offset[1]
}

// withinBudget reports whether add still fits under limit. A non-positive
// limit disables the cap.
func withinBudget(used, add, limit int64) bool {
	if limit <= 0 {
		return true
	}
	if add < 0 || add > limit {
		return false
	}
	return used <= limit-add
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
