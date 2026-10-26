package schema

import "fmt"

// Check reports whether a custom width×height output size is within the
// constraint. Input normalization and the canvas output-size rule (#272) use
// the same check, so a size the canvas rule picks is never refused later.
func (c ImageSizeConstraint) Check(width, height int64) error {
	if width <= 0 || height <= 0 {
		return fmt.Errorf("尺寸必须为正整数宽x高")
	}
	if c.Multiple > 0 && (width%c.Multiple != 0 || height%c.Multiple != 0) {
		return fmt.Errorf("宽高必须为 %d 的倍数", c.Multiple)
	}
	if c.MaxEdge > 0 && (width > c.MaxEdge || height > c.MaxEdge) {
		return fmt.Errorf("边长不能超过 %d", c.MaxEdge)
	}
	if width*height < c.MinPixels || width*height > c.MaxPixels {
		return fmt.Errorf("总像素必须在 %d 到 %d 之间", c.MinPixels, c.MaxPixels)
	}
	if float64(width)/float64(height) > c.MaxRatio || float64(height)/float64(width) > c.MaxRatio {
		return fmt.Errorf("长短边比例不能超过 %g", c.MaxRatio)
	}
	return nil
}
