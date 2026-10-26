package imagewire

import (
	"bytes"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"path"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/assets/imagefit"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"
)

// flattenAlpha applies a parameter's alpha_flatten declaration (#271) to one source image. The board
// uploads PNG with alpha, where empty canvas is transparent; an upstream that is declared not to take
// transparent pixels gets the source composited over the declared colour as an opaque PNG instead. A
// source without any non-opaque pixel (every JPEG, an opaque PNG) keeps its stored bytes unchanged,
// and so does every source when the input matches one of the declaration's keep_when conditions.
func flattenAlpha(data []byte, asset generation.InputAsset, p generation.Parameter, input map[string]any) ([]byte, generation.InputAsset, error) {
	policy := p.AlphaFlatten
	if policy == nil {
		return data, asset, nil
	}
	for _, condition := range policy.KeepWhen {
		if schema.ConditionMatches(condition, input) {
			return data, asset, nil
		}
	}
	background, err := imagefit.ParseHexColor(policy.BackgroundColor)
	if err != nil || policy.MaxPixels <= 0 {
		return nil, asset, fmt.Errorf("参数 %s 的 alpha_flatten 声明无效", p.Name)
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, asset, fmt.Errorf("参数 %s 图片无法解码，不能铺底", p.Name)
	}
	if format == "jpeg" {
		return data, asset, nil
	}
	if config.Width <= 0 || config.Height <= 0 || int64(config.Width) > policy.MaxPixels/int64(config.Height) {
		return nil, asset, fmt.Errorf("参数 %s 图片像素超过铺底上限", p.Name)
	}
	source, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, asset, fmt.Errorf("参数 %s 图片无法解码，不能铺底", p.Name)
	}
	if opaque, ok := source.(interface{ Opaque() bool }); ok && opaque.Opaque() {
		return data, asset, nil
	}
	flat := imagefit.Flatten(source, background)
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, flat); err != nil {
		return nil, asset, err
	}
	if int64(encoded.Len()) > p.MaxBytes {
		return nil, asset, fmt.Errorf("参数 %s 铺底后的图片超过大小限制", p.Name)
	}
	// The wire carries a new picture: say what it is, and claim no digest of the stored one.
	asset.MediaType = "image/png"
	asset.Size = int64(encoded.Len())
	asset.SHA256 = ""
	if name := strings.TrimSpace(asset.OriginalFilename); name != "" {
		asset.OriginalFilename = strings.TrimSuffix(name, path.Ext(name)) + ".png"
	}
	return encoded.Bytes(), asset, nil
}
