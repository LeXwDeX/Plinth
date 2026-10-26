package mock

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"strconv"
	"strings"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

// 本文件只服务 PROVIDER_MODE=mock 的本地开发与验收：把一次请求画成一张
// 有尺寸、有颜色、有标签的程序化图片，让画板、版本条、比较和导出看到
// 真实尺寸的不同图片，而不是 64px 纯色块。不读取任何外部资源，同样的
// 种子永远得到同样的字节。

const (
	mockImageMinLongSide = 768
	mockImageMaxLongSide = 2048
)

// imageSpec 是画一张图所需的全部输入。
type imageSpec struct {
	Width, Height int
	// Seed 决定轮廓的变化（来自提交键与序号）；PromptHash 决定主色。
	Seed       [32]byte
	PromptHash [32]byte
	// Label 是左上角的小标签，必须是 ASCII。
	Label string
	// Cutout 为真时只画主体并保留透明背景（模拟去背景结果）。
	Cutout bool
}

// mockImageDimensions 从冻结输入里读出宽高比与尺寸档，得到不小于 768、
// 不大于 2048 的长边。aspect_ratio 优先；其次 size（WxH 或 1K/2K/4K 档）。
func mockImageDimensions(input map[string]any) (int, int) {
	ratioW, ratioH := 1.0, 1.0
	class := ""
	explicitEdge := 0
	if value, ok := input["image_size"].(string); ok {
		class = strings.ToUpper(strings.TrimSpace(value))
	}
	hasAspect := false
	if value, ok := input["aspect_ratio"].(string); ok {
		if w, h, ok := parseRatio(value, ":"); ok {
			ratioW, ratioH, hasAspect = w, h, true
		}
	}
	if value, ok := input["size"].(string); ok {
		size := strings.ToLower(strings.TrimSpace(value))
		switch {
		case size == "1k" || size == "2k" || size == "4k":
			class = strings.ToUpper(size)
		case strings.Contains(size, "x"):
			if w, h, ok := parseRatio(size, "x"); ok {
				if !hasAspect {
					ratioW, ratioH = w, h
				}
				explicitEdge = int(math.Max(w, h))
			}
		}
	}
	longSide := 1024
	switch {
	case class == "1K":
		longSide = 1024
	case class == "2K":
		longSide = 1536
	case class == "4K":
		longSide = mockImageMaxLongSide
	case explicitEdge > 0:
		longSide = max(mockImageMinLongSide, min(explicitEdge, 1536))
	}
	if ratioW >= ratioH {
		return roundTo(longSide, 8), roundTo(int(math.Round(float64(longSide)*ratioH/ratioW)), 8)
	}
	return roundTo(int(math.Round(float64(longSide)*ratioW/ratioH)), 8), roundTo(longSide, 8)
}

func parseRatio(value, separator string) (float64, float64, bool) {
	parts := strings.Split(strings.TrimSpace(value), separator)
	if len(parts) != 2 {
		return 0, 0, false
	}
	w, errW := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	h, errH := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	if errW != nil || errH != nil || w <= 0 || h <= 0 {
		return 0, 0, false
	}
	return w, h, true
}

func roundTo(value, multiple int) int {
	return max(multiple, (value+multiple/2)/multiple*multiple)
}

func renderImagePNG(spec imageSpec) ([]byte, error) {
	canvas := image.NewNRGBA(image.Rect(0, 0, spec.Width, spec.Height))
	hue := float64(binary.BigEndian.Uint16(spec.PromptHash[:2])) / 65535 * 360
	variant := int(spec.Seed[0])
	if !spec.Cutout {
		paintBackground(canvas, hue, variant)
	}
	subject := hsv(hue+180, 0.65, 0.92)
	shade := hsv(hue+210, 0.55, 0.45)
	if float64(spec.Height) >= float64(spec.Width)*0.9 {
		paintFigure(canvas, spec.Seed, subject, shade)
	} else {
		paintSkyline(canvas, spec.Seed, subject, shade, spec.Cutout)
	}
	if !spec.Cutout {
		paintFrame(canvas, hsv(hue+20, 0.4, 0.95))
	}
	paintLabel(canvas, spec.Label)
	var encoded bytes.Buffer
	encoder := png.Encoder{CompressionLevel: png.DefaultCompression}
	if err := encoder.Encode(&encoded, canvas); err != nil {
		return nil, fmt.Errorf("encode PNG artifact: %w", err)
	}
	return encoded.Bytes(), nil
}

// paintBackground 画竖向渐变和一组斜纹，斜纹间距由种子决定。
func paintBackground(canvas *image.NRGBA, hue float64, variant int) {
	bounds := canvas.Bounds()
	top, bottom := hsv(hue, 0.55, 0.30), hsv(hue+35, 0.45, 0.85)
	period := 56 + variant%48
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		t := float64(y) / float64(bounds.Dy())
		row := mix(top, bottom, t)
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			pixel := row
			if (x+y)%period < period/6 {
				pixel = mix(row, color.NRGBA{255, 255, 255, 255}, 0.10)
			}
			canvas.SetNRGBA(x, y, pixel)
		}
	}
}

func paintFrame(canvas *image.NRGBA, tone color.NRGBA) {
	bounds := canvas.Bounds()
	thickness := max(4, bounds.Dx()/160)
	fillRect(canvas, bounds.Min.X, bounds.Min.Y, bounds.Max.X, bounds.Min.Y+thickness, tone)
	fillRect(canvas, bounds.Min.X, bounds.Max.Y-thickness, bounds.Max.X, bounds.Max.Y, tone)
	fillRect(canvas, bounds.Min.X, bounds.Min.Y, bounds.Min.X+thickness, bounds.Max.Y, tone)
	fillRect(canvas, bounds.Max.X-thickness, bounds.Min.Y, bounds.Max.X, bounds.Max.Y, tone)
}

// paintFigure 画一个站姿角色剪影：头、躯干、手臂、裙或裤、鞋，细节随种子变化。
func paintFigure(canvas *image.NRGBA, seed [32]byte, body, shade color.NRGBA) {
	w, h := float64(canvas.Bounds().Dx()), float64(canvas.Bounds().Dy())
	cx := w / 2
	unit := h / 100
	skirt := seed[1]%2 == 0
	armLift := float64(seed[2]%20) - 4
	hat := seed[3] % 3
	skin := mix(body, color.NRGBA{255, 226, 200, 255}, 0.75)
	// 地面阴影
	fillEllipse(canvas, cx, 93*unit, 17*unit, 2.2*unit, color.NRGBA{0, 0, 0, 70})
	// 腿或裙
	if skirt {
		fillPolygon(canvas, []pt{{cx - 9*unit, 52 * unit}, {cx + 9*unit, 52 * unit}, {cx + 16*unit, 74 * unit}, {cx - 16*unit, 74 * unit}}, body)
		fillRect(canvas, int(cx-6*unit), int(74*unit), int(cx-2*unit), int(90*unit), skin)
		fillRect(canvas, int(cx+2*unit), int(74*unit), int(cx+6*unit), int(90*unit), skin)
	} else {
		fillRect(canvas, int(cx-8*unit), int(52*unit), int(cx-1*unit), int(90*unit), shade)
		fillRect(canvas, int(cx+1*unit), int(52*unit), int(cx+8*unit), int(90*unit), shade)
	}
	fillRect(canvas, int(cx-10*unit), int(90*unit), int(cx-1*unit), int(93*unit), shade)
	fillRect(canvas, int(cx+1*unit), int(90*unit), int(cx+10*unit), int(93*unit), shade)
	// 躯干
	fillPolygon(canvas, []pt{{cx - 10*unit, 26 * unit}, {cx + 10*unit, 26 * unit}, {cx + 8*unit, 54 * unit}, {cx - 8*unit, 54 * unit}}, body)
	fillRect(canvas, int(cx-9*unit), int(46*unit), int(cx+9*unit), int(49*unit), shade)
	// 手臂
	fillPolygon(canvas, []pt{{cx - 10*unit, 27 * unit}, {cx - 15*unit, (50 - armLift) * unit}, {cx - 12*unit, (51 - armLift) * unit}, {cx - 8*unit, 30 * unit}}, skin)
	fillPolygon(canvas, []pt{{cx + 10*unit, 27 * unit}, {cx + 15*unit, (50 + armLift/2) * unit}, {cx + 12*unit, (51 + armLift/2) * unit}, {cx + 8*unit, 30 * unit}}, skin)
	// 头与颈
	fillRect(canvas, int(cx-2.2*unit), int(21*unit), int(cx+2.2*unit), int(27*unit), skin)
	fillEllipse(canvas, cx, 14*unit, 6.5*unit, 8*unit, skin)
	switch hat {
	case 0:
		fillEllipse(canvas, cx, 9*unit, 7.4*unit, 5.4*unit, shade)
	case 1:
		fillRect(canvas, int(cx-8.5*unit), int(7*unit), int(cx+8.5*unit), int(9.2*unit), shade)
		fillRect(canvas, int(cx-5*unit), int(2*unit), int(cx+5*unit), int(8*unit), shade)
	default:
		fillPolygon(canvas, []pt{{cx - 7*unit, 12 * unit}, {cx, 1 * unit}, {cx + 7*unit, 12 * unit}}, shade)
	}
}

// paintSkyline 画远景天际线和一个太阳（或月亮），适合横幅与方图。
func paintSkyline(canvas *image.NRGBA, seed [32]byte, body, shade color.NRGBA, cutout bool) {
	w, h := float64(canvas.Bounds().Dx()), float64(canvas.Bounds().Dy())
	if !cutout {
		fillEllipse(canvas, w*(0.2+float64(seed[4]%60)/100), h*0.28, h*0.11, h*0.11, mix(body, color.NRGBA{255, 244, 200, 255}, 0.6))
	}
	ground := h * 0.78
	x := 0.0
	index := 5
	for x < w {
		width := w * (0.05 + float64(seed[index%32]%7)/100)
		height := h * (0.18 + float64(seed[(index+7)%32]%40)/100)
		index++
		tone := mix(shade, body, float64(seed[(index+3)%32]%100)/260)
		fillRect(canvas, int(x), int(ground-height), int(x+width-2), int(ground), tone)
		for wy := ground - height + h*0.03; wy < ground-h*0.04; wy += h * 0.05 {
			for wx := x + width*0.15; wx < x+width*0.8; wx += width * 0.3 {
				if seed[(int(wx+wy))%32]%3 != 0 {
					fillRect(canvas, int(wx), int(wy), int(wx+width*0.12), int(wy+h*0.025), mix(body, color.NRGBA{255, 240, 180, 255}, 0.7))
				}
			}
		}
		x += width + 2
	}
	fillRect(canvas, 0, int(ground), canvas.Bounds().Dx(), canvas.Bounds().Dy(), shade)
	fillRect(canvas, 0, int(ground), canvas.Bounds().Dx(), int(ground)+max(3, int(h*0.006)), body)
}

// paintLabel 在左上角画深色标签条和 ASCII 文字。basicfont 只有 7x13，
// 先画到小图再整数倍放大，大图上才看得清。
func paintLabel(canvas *image.NRGBA, label string) { paintLabelAt(canvas, label, false) }

// paintLabelAt 的 bottom 为真时标签贴左下角：改过色的参考图左上角还留着上一张图的标签，
// 新标签放在另一个角，不会和旧标签叠成一团。
func paintLabelAt(canvas *image.NRGBA, label string, bottom bool) {
	if label == "" {
		return
	}
	face := basicfont.Face7x13
	textWidth := font.MeasureString(face, label).Ceil()
	small := image.NewNRGBA(image.Rect(0, 0, textWidth+8, 19))
	draw.Draw(small, small.Bounds(), &image.Uniform{C: color.NRGBA{12, 14, 22, 215}}, image.Point{}, draw.Src)
	drawer := font.Drawer{
		Dst:  small,
		Src:  image.NewUniform(color.NRGBA{255, 255, 255, 255}),
		Face: face,
		Dot:  fixed.P(4, 14),
	}
	drawer.DrawString(label)
	scale := max(2, canvas.Bounds().Dx()/380)
	offset := max(8, canvas.Bounds().Dx()/90)
	top := offset
	if bottom {
		top = canvas.Bounds().Dy() - offset - small.Bounds().Dy()*scale
	}
	for y := 0; y < small.Bounds().Dy()*scale; y++ {
		for x := 0; x < small.Bounds().Dx()*scale; x++ {
			target := image.Pt(offset+x, top+y)
			if !target.In(canvas.Bounds()) {
				continue
			}
			canvas.SetNRGBA(target.X, target.Y, over(canvas.NRGBAAt(target.X, target.Y), small.NRGBAAt(x/scale, y/scale)))
		}
	}
}

type pt struct{ x, y float64 }

func fillRect(canvas *image.NRGBA, x0, y0, x1, y1 int, tone color.NRGBA) {
	bounds := canvas.Bounds()
	x0, y0 = max(x0, bounds.Min.X), max(y0, bounds.Min.Y)
	x1, y1 = min(x1, bounds.Max.X), min(y1, bounds.Max.Y)
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			canvas.SetNRGBA(x, y, over(canvas.NRGBAAt(x, y), tone))
		}
	}
}

func fillEllipse(canvas *image.NRGBA, cx, cy, rx, ry float64, tone color.NRGBA) {
	bounds := canvas.Bounds()
	for y := max(bounds.Min.Y, int(cy-ry)); y <= min(bounds.Max.Y-1, int(cy+ry)); y++ {
		for x := max(bounds.Min.X, int(cx-rx)); x <= min(bounds.Max.X-1, int(cx+rx)); x++ {
			dx, dy := (float64(x)-cx)/rx, (float64(y)-cy)/ry
			if dx*dx+dy*dy <= 1 {
				canvas.SetNRGBA(x, y, over(canvas.NRGBAAt(x, y), tone))
			}
		}
	}
}

// fillPolygon 用扫描线填充凸或简单多边形（偶奇规则）。
func fillPolygon(canvas *image.NRGBA, points []pt, tone color.NRGBA) {
	bounds := canvas.Bounds()
	minY, maxY := math.MaxFloat64, -math.MaxFloat64
	for _, p := range points {
		minY, maxY = math.Min(minY, p.y), math.Max(maxY, p.y)
	}
	for y := max(bounds.Min.Y, int(minY)); y <= min(bounds.Max.Y-1, int(maxY)); y++ {
		scan := float64(y) + 0.5
		var crossings []float64
		for i := range points {
			a, b := points[i], points[(i+1)%len(points)]
			if (a.y <= scan && b.y > scan) || (b.y <= scan && a.y > scan) {
				crossings = append(crossings, a.x+(scan-a.y)/(b.y-a.y)*(b.x-a.x))
			}
		}
		if len(crossings) < 2 {
			continue
		}
		for i := 0; i+1 < len(crossings); i += 2 {
			left, right := crossings[i], crossings[i+1]
			if left > right {
				left, right = right, left
			}
			for x := max(bounds.Min.X, int(left)); x <= min(bounds.Max.X-1, int(right)); x++ {
				canvas.SetNRGBA(x, y, over(canvas.NRGBAAt(x, y), tone))
			}
		}
	}
}

// over 把 src 叠在 dst 上（非预乘 alpha）。
func over(dst, src color.NRGBA) color.NRGBA {
	if src.A == 255 {
		return src
	}
	if src.A == 0 {
		return dst
	}
	sa, da := float64(src.A)/255, float64(dst.A)/255
	outA := sa + da*(1-sa)
	if outA == 0 {
		return color.NRGBA{}
	}
	blend := func(s, d uint8) uint8 {
		return uint8(math.Round((float64(s)*sa + float64(d)*da*(1-sa)) / outA))
	}
	return color.NRGBA{blend(src.R, dst.R), blend(src.G, dst.G), blend(src.B, dst.B), uint8(math.Round(outA * 255))}
}

func mix(a, b color.NRGBA, t float64) color.NRGBA {
	t = math.Max(0, math.Min(1, t))
	lerp := func(x, y uint8) uint8 { return uint8(math.Round(float64(x) + (float64(y)-float64(x))*t)) }
	return color.NRGBA{lerp(a.R, b.R), lerp(a.G, b.G), lerp(a.B, b.B), 255}
}

func hsv(hue, saturation, value float64) color.NRGBA {
	hue = math.Mod(math.Mod(hue, 360)+360, 360)
	c := value * saturation
	x := c * (1 - math.Abs(math.Mod(hue/60, 2)-1))
	m := value - c
	var r, g, b float64
	switch {
	case hue < 60:
		r, g, b = c, x, 0
	case hue < 120:
		r, g, b = x, c, 0
	case hue < 180:
		r, g, b = 0, c, x
	case hue < 240:
		r, g, b = 0, x, c
	case hue < 300:
		r, g, b = x, 0, c
	default:
		r, g, b = c, 0, x
	}
	return color.NRGBA{uint8(math.Round((r + m) * 255)), uint8(math.Round((g + m) * 255)), uint8(math.Round((b + m) * 255)), 255}
}

func digest(parts ...string) [32]byte {
	hasher := sha256.New()
	for _, part := range parts {
		hasher.Write([]byte(part))
		hasher.Write([]byte{0})
	}
	var out [32]byte
	copy(out[:], hasher.Sum(nil))
	return out
}
