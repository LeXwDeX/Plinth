package catalog_test

import (
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// #187: the direct prepare surface is declared in the Schema and can only
// name distinct image-domain capabilities.
func TestChatStyleLockDirectTaskCapabilitiesValidation(t *testing.T) {
	source, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name         string
		capabilities []schema.Capability
		want         string
	}{
		{name: "empty", capabilities: nil, want: "style_lock 的上限、字段与错误文案必须完整"},
		{name: "duplicate", capabilities: []schema.Capability{schema.TextToImage, schema.TextToImage}, want: "不能重复"},
		{name: "processing domain", capabilities: []schema.Capability{schema.BackgroundRemove}, want: "只能声明图片域能力"},
		{name: "3d domain", capabilities: []schema.Capability{schema.ImageTo3D}, want: "只能声明图片域能力"},
		{name: "unknown", capabilities: []schema.Capability{"not-a-capability"}, want: "只能声明图片域能力"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			application := source.Application()
			application.Chat.StyleLock.DirectTaskCapabilities = test.capabilities
			err := catalog.ValidateApplicationSchema(application)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validation error = %v, want %q", err, test.want)
			}
		})
	}
}
