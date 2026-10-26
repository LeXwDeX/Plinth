package mock

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	_ "image/jpeg" // 参考图可能是 JPEG。
	"image/png"
	"io"
	"math"
	"sort"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// 本文件让 mock 的“图改图”像图改图：
//   - 带蒙版的局部重绘：输出是参考图的拷贝，只有蒙版里透明的区域被换成新图案，
//     其余像素与参考图逐字节相同（标签也画在被改的区域里），验收时可以对比；
//   - 不带蒙版、且参考图与输出同一宽高比：输出是参考图改色后的版本，构图不变；
//   - 其他情况（参考图是草图或比例不同、读不出来）仍走程序化出图。
//
// 参考图和蒙版经 InputAssetResolver 读取，没有解析器（单元测试）时整段跳过。

const maxEditSourceBytes = 32 << 20

// editSource 是一次图改图请求读到的参考图和蒙版。
type editSource struct {
	reference *image.NRGBA
	mask      image.Image
}

// loadEdit 读第一张能解码成图片的参考图（参考图、风格图、待处理图）和可选的蒙版；
// 任何一步失败都返回 nil，不影响出图。
func (a *Adapter) loadEdit(ctx context.Context, request generation.StartRequest) *editSource {
	if a.options.Inputs == nil || len(request.InputAssets) == 0 {
		return nil
	}
	names := make([]string, 0, len(request.InputAssets))
	for name := range request.InputAssets {
		if name != "mask" && (strings.Contains(name, "reference") || strings.Contains(name, "style") || strings.Contains(name, "source")) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	var source *editSource
	for _, name := range names {
		assets := request.InputAssets[name]
		if len(assets) == 0 {
			continue
		}
		reference, err := a.decodeInput(ctx, assets[0])
		if err != nil {
			continue
		}
		if looksLikeSketch(reference) {
			// 草图只提供构图，不能当成要改色的底图：交给程序化出图画一张新的。
			return nil
		}
		source = &editSource{reference: toNRGBA(reference)}
		break
	}
	if source == nil {
		return nil
	}
	if masks := request.InputAssets["mask"]; len(masks) > 0 {
		if mask, err := a.decodeInput(ctx, masks[0]); err == nil {
			source.mask = mask
		}
	}
	return source
}

func (a *Adapter) decodeInput(ctx context.Context, asset generation.InputAsset) (image.Image, error) {
	body, err := a.options.Inputs.Open(ctx, asset)
	if err != nil {
		return nil, err
	}
	defer func() { _ = body.Close() }()
	decoded, _, err := image.Decode(io.LimitReader(body, maxEditSourceBytes))
	return decoded, err
}

// looksLikeSketch 判断一张图是不是白底线稿：绝大多数采样点又亮又不带颜色。
func looksLikeSketch(source image.Image) bool {
	bounds := source.Bounds()
	step := max(1, min(bounds.Dx(), bounds.Dy())/64)
	pale, total := 0, 0
	for y := bounds.Min.Y; y < bounds.Max.Y; y += step {
		for x := bounds.Min.X; x < bounds.Max.X; x += step {
			pixel := color.NRGBAModel.Convert(source.At(x, y)).(color.NRGBA)
			high := max(pixel.R, pixel.G, pixel.B)
			low := min(pixel.R, pixel.G, pixel.B)
			total++
			if low > 200 && int(high)-int(low) < 24 {
				pale++
			}
		}
	}
	return total > 0 && float64(pale)/float64(total) > 0.7
}

func toNRGBA(source image.Image) *image.NRGBA {
	bounds := source.Bounds()
	out := image.NewNRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	for y := 0; y < bounds.Dy(); y++ {
		for x := 0; x < bounds.Dx(); x++ {
			out.SetNRGBA(x, y, color.NRGBAModel.Convert(source.At(bounds.Min.X+x, bounds.Min.Y+y)).(color.NRGBA))
		}
	}
	return out
}

// renderEdit 返回 (字节, 是否适用, 错误)。
func renderEdit(stored *job, plan claimPlan, seed [32]byte) ([]byte, bool, error) {
	if stored.edit == nil || stored.edit.reference == nil {
		return nil, false, nil
	}
	reference := stored.edit.reference
	if stored.request.Offering.Capability == "background-removal" {
		encoded, err := encodePNG(cutOutBackground(reference, capabilityLabel(stored.request.Offering.Capability), plan, seed))
		return encoded, true, err
	}
	promptHash := digest("prompt", promptOf(stored.input))
	hue := float64(binary.BigEndian.Uint16(promptHash[:2])) / 65535 * 360
	if stored.edit.mask != nil {
		out, ok := paintMaskedRegion(reference, stored.edit.mask, hue, seed, plan, stored)
		if !ok {
			return nil, false, nil
		}
		encoded, err := encodePNG(out)
		return encoded, true, err
	}
	width, height := mockImageDimensions(stored.input)
	sourceRatio := float64(reference.Bounds().Dx()) / float64(reference.Bounds().Dy())
	if math.Abs(sourceRatio/(float64(width)/float64(height))-1) > 0.02 {
		return nil, false, nil
	}
	out := image.NewNRGBA(image.Rect(0, 0, width, height))
	shift := 50 + float64(seed[1])/255*250
	value := 0.8 + float64(seed[2])/255*0.3
	for y := 0; y < height; y++ {
		sy := y * reference.Bounds().Dy() / height
		for x := 0; x < width; x++ {
			sx := x * reference.Bounds().Dx() / width
			out.SetNRGBA(x, y, recolor(reference.NRGBAAt(sx, sy), shift, value))
		}
	}
	label := fmt.Sprintf("%s EDIT %d/%d #%s %dx%d", capabilityLabel(stored.request.Offering.Capability), plan.index+1, plan.total, shortHex(seed), width, height)
	paintLabelAt(out, label, true)
	encoded, err := encodePNG(out)
	return encoded, true, err
}

// paintMaskedRegion 只改蒙版里透明（alpha<128）的像素。没有任何透明像素时
// 视为不适用，让调用方走普通出图。
func paintMaskedRegion(reference *image.NRGBA, mask image.Image, hue float64, seed [32]byte, plan claimPlan, stored *job) (*image.NRGBA, bool) {
	width, height := reference.Bounds().Dx(), reference.Bounds().Dy()
	maskBounds := mask.Bounds()
	out := image.NewNRGBA(reference.Bounds())
	copy(out.Pix, reference.Pix)
	minX, minY, maxX, maxY := width, height, -1, -1
	period := 14 + int(seed[0])%10
	dark, light := hsv(hue, 0.85, 0.55), hsv(hue+20, 0.65, 0.98)
	for y := 0; y < height; y++ {
		my := maskBounds.Min.Y + y*maskBounds.Dy()/height
		for x := 0; x < width; x++ {
			mx := maskBounds.Min.X + x*maskBounds.Dx()/width
			_, _, _, alpha := mask.At(mx, my).RGBA()
			if alpha>>8 >= 128 {
				continue
			}
			tone := light
			if ((x+y)/period)%2 == 0 {
				tone = dark
			}
			out.SetNRGBA(x, y, mix(reference.NRGBAAt(x, y), tone, 0.88))
			minX, minY = min(minX, x), min(minY, y)
			maxX, maxY = max(maxX, x), max(maxY, y)
		}
	}
	if maxX < 0 {
		return nil, false
	}
	// 标签只画在被改的区域里：画到小画布上再贴回，且只贴蒙版内的像素。
	label := fmt.Sprintf("INPAINT %d/%d #%s", plan.index+1, plan.total, shortHex(seed))
	badge := image.NewNRGBA(image.Rect(0, 0, maxX-minX+1, maxY-minY+1))
	paintLabel(badge, label)
	for y := 0; y < badge.Bounds().Dy(); y++ {
		my := maskBounds.Min.Y + (minY+y)*maskBounds.Dy()/height
		for x := 0; x < badge.Bounds().Dx(); x++ {
			pixel := badge.NRGBAAt(x, y)
			if pixel.A == 0 {
				continue
			}
			mx := maskBounds.Min.X + (minX+x)*maskBounds.Dx()/width
			if _, _, _, alpha := mask.At(mx, my).RGBA(); alpha>>8 >= 128 {
				continue
			}
			out.SetNRGBA(minX+x, minY+y, over(out.NRGBAAt(minX+x, minY+y), pixel))
		}
	}
	return out, true
}

// recolor 转色相并缩放明度，保留饱和度与明暗层次。
func recolor(pixel color.NRGBA, shift, value float64) color.NRGBA {
	r, g, b := float64(pixel.R)/255, float64(pixel.G)/255, float64(pixel.B)/255
	maxC, minC := math.Max(r, math.Max(g, b)), math.Min(r, math.Min(g, b))
	delta := maxC - minC
	var hue float64
	switch {
	case delta == 0:
	case maxC == r:
		hue = 60 * math.Mod((g-b)/delta, 6)
	case maxC == g:
		hue = 60 * ((b-r)/delta + 2)
	default:
		hue = 60 * ((r-g)/delta + 4)
	}
	saturation := 0.0
	if maxC > 0 {
		saturation = delta / maxC
	}
	out := hsv(hue+shift, saturation, math.Min(1, maxC*value))
	out.A = pixel.A
	return out
}

func shortHex(seed [32]byte) string {
	return fmt.Sprintf("%02x%02x%02x", seed[0], seed[1], seed[2])
}

func encodePNG(canvas image.Image) ([]byte, error) {
	var encoded bytes.Buffer
	encoder := png.Encoder{CompressionLevel: png.DefaultCompression}
	if err := encoder.Encode(&encoded, canvas); err != nil {
		return nil, fmt.Errorf("encode PNG artifact: %w", err)
	}
	return encoded.Bytes(), nil
}

// cutOutBackground 去掉 mock 图片的背景：背景是竖向渐变加斜纹，同一行里最左边
// 靠近画框的那个点就是这一行的底色，与底色（或被斜纹提亮的底色）接近的点算背景。
// 画框、左上角的旧标签一并清掉，留下主体和脚下的影子。
func cutOutBackground(reference *image.NRGBA, capability string, plan claimPlan, seed [32]byte) *image.NRGBA {
	bounds := reference.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	out := image.NewNRGBA(bounds)
	copy(out.Pix, reference.Pix)
	frame := max(4, width/160)
	sampleX := frame + 3
	scale := max(2, width/380)
	labelBottom := max(8, width/90) + 19*scale
	near := func(a, b color.NRGBA, tolerance int) bool {
		distance := absInt(int(a.R)-int(b.R)) + absInt(int(a.G)-int(b.G)) + absInt(int(a.B)-int(b.B))
		return distance <= tolerance
	}
	for y := 0; y < height; y++ {
		base := reference.NRGBAAt(sampleX, y)
		lighter := mix(base, color.NRGBA{255, 255, 255, 255}, 0.10)
		for x := 0; x < width; x++ {
			pixel := reference.NRGBAAt(x, y)
			background := near(pixel, base, 36) || near(pixel, lighter, 36)
			border := x < frame+1 || y < frame+1 || x >= width-frame-1 || y >= height-frame-1
			oldLabel := y < labelBottom && x < width*3/5
			if background || border || oldLabel {
				out.SetNRGBA(x, y, color.NRGBA{})
			}
		}
	}
	paintLabelAt(out, fmt.Sprintf("%s %d/%d #%s", capability, plan.index+1, plan.total, shortHex(seed)), true)
	return out
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

// dominantHue 取参考图中部（人物躯干的位置）的平均颜色的色相；颜色太灰时返回 false。
func dominantHue(reference *image.NRGBA) (float64, bool) {
	bounds := reference.Bounds()
	x0, x1 := bounds.Dx()*40/100, bounds.Dx()*60/100
	y0, y1 := bounds.Dy()*28/100, bounds.Dy()*50/100
	var r, g, b, count float64
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			pixel := reference.NRGBAAt(x, y)
			if pixel.A < 128 {
				continue
			}
			r += float64(pixel.R)
			g += float64(pixel.G)
			b += float64(pixel.B)
			count++
		}
	}
	if count == 0 {
		return 0, false
	}
	r, g, b = r/count/255, g/count/255, b/count/255
	maxC, minC := math.Max(r, math.Max(g, b)), math.Min(r, math.Min(g, b))
	delta := maxC - minC
	if maxC == 0 || delta/maxC < 0.2 {
		return 0, false
	}
	var hue float64
	switch maxC {
	case r:
		hue = 60 * math.Mod((g-b)/delta, 6)
	case g:
		hue = 60 * ((b-r)/delta + 2)
	default:
		hue = 60 * ((r-g)/delta + 4)
	}
	return math.Mod(hue+360, 360), true
}
