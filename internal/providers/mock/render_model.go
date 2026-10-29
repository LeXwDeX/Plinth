package mock

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"

	"github.com/LeXwDeX/Plinth/internal/providers/mock/modelfiles"
)

type modelSpec struct {
	// GeometrySeed 决定身材比例；TextureSeed 与 Hue 决定贴图。
	GeometrySeed [32]byte
	TextureSeed  [32]byte
	Hue          float64
	// Subdivision 是每个方块面每边的分段数：重拓扑结果更少，原始结果更多。
	Subdivision int
	TextureSize int
	Label       string
}

// renderModelTexture 画 3x2 图集：肤色、上衣、袖子、裤子，格内有条纹。
func renderModelTexture(spec modelSpec) ([]byte, error) {
	size := spec.TextureSize
	if size <= 0 {
		size = 512
	}
	canvas := image.NewNRGBA(image.Rect(0, 0, size, size))
	tones := atlasTones(spec.Hue)
	cellW, cellH := size/3, size/2
	period := 20 + int(spec.TextureSeed[0])%24
	for cell, tone := range tones {
		x0, y0 := (cell%3)*cellW, (cell/3)*cellH
		for y := y0; y < y0+cellH; y++ {
			for x := x0; x < x0+cellW; x++ {
				pixel := tone
				if (x+y)%period < period/4 {
					pixel = mix(tone, color.NRGBA{255, 255, 255, 255}, 0.16)
				}
				canvas.SetNRGBA(x, y, pixel)
			}
		}
	}
	if spec.Label != "" {
		paintLabel(canvas, spec.Label)
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, canvas); err != nil {
		return nil, fmt.Errorf("encode texture PNG: %w", err)
	}
	return encoded.Bytes(), nil
}

func atlasTones(hue float64) []color.NRGBA {
	return []color.NRGBA{
		mix(hsv(hue, 0.4, 0.95), color.NRGBA{255, 226, 200, 255}, 0.7),
		hsv(hue+180, 0.7, 0.85),
		hsv(hue+170, 0.6, 0.7),
		hsv(hue+190, 0.6, 0.7),
		hsv(hue+30, 0.55, 0.45),
		hsv(hue+40, 0.55, 0.4),
	}
}

// renderModelThumbnail 画 512 见方的模型预览：渐变底加这个模型的正面投影
// （同一组方块、同一套颜色），预览和 3D 舞台里看到的是同一个小人。
func renderModelThumbnail(spec modelSpec) ([]byte, error) {
	canvas := image.NewNRGBA(image.Rect(0, 0, 512, 512))
	paintBackground(canvas, spec.Hue, int(spec.GeometrySeed[0]))
	tones := atlasTones(spec.Hue)
	parts := modelfiles.HumanoidParts(spec.GeometrySeed)
	top := 0.0
	for _, part := range parts {
		top = math.Max(top, part.CY+part.SY/2)
	}
	scale := 400 / top
	baseline := 470.0
	fillEllipse(canvas, 256, baseline+4, 90, 10, color.NRGBA{0, 0, 0, 70})
	for _, part := range parts {
		x0, x1 := 256+(part.CX-part.SX/2)*scale, 256+(part.CX+part.SX/2)*scale
		y0, y1 := baseline-(part.CY+part.SY/2)*scale, baseline-(part.CY-part.SY/2)*scale
		fillRect(canvas, int(x0), int(y0), int(x1), int(y1), tones[part.Cell])
	}
	paintLabel(canvas, spec.Label)
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, canvas); err != nil {
		return nil, fmt.Errorf("encode thumbnail PNG: %w", err)
	}
	return encoded.Bytes(), nil
}
