package catalog

import (
	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"

	"fmt"
	"strconv"
	"strings"
)

func validateImageSize(c schemacontract.ImageSizeConstraint, value string) error {
	parts := strings.Split(value, "x")
	if len(parts) != 2 {
		return fmt.Errorf("尺寸必须为宽x高或已声明预设")
	}
	w, e1 := strconv.ParseInt(parts[0], 10, 32)
	h, e2 := strconv.ParseInt(parts[1], 10, 32)
	if e1 != nil || e2 != nil {
		return fmt.Errorf("尺寸必须为正整数宽x高")
	}
	return c.Check(w, h)
}
