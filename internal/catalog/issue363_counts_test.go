package catalog

// #363: three user-visible sentences put a plural noun after a count that can
// be one. Approval time uses the existing count pair; the two Go-rendered
// refusals keep their placeholders and word the count without a plural noun.
import (
	"strings"
	"testing"
)

func TestDefaultCatalogIssue363SingularCounts(t *testing.T) {
	application := defaultApplicationForLocalization(t)
	english := application.Localization.Bundles["en"]

	t.Run("approval validity has a singular form", func(t *testing.T) {
		for _, testCase := range []struct {
			key, placeholder, want string
		}{
			{"ui.studio.approval.card.valid_hours", "{hours}", "Awaiting approval · valid for {hours} hour"},
			{"ui.studio.approval.card.valid_minutes", "{minutes}", "Awaiting approval · valid for {minutes} minute"},
		} {
			base := application.Content.Messages[testCase.key]
			if base == "" || !strings.Contains(base, testCase.placeholder) {
				t.Fatalf("%s general form %q does not name %s", testCase.key, base, testCase.placeholder)
			}
			if one := application.Content.Messages[testCase.key+"_one"]; one != base {
				t.Fatalf("%s Chinese singular = %q, want the same count-neutral text %q", testCase.key, one, base)
			}
			if got := english.Messages[testCase.key+"_one"]; got != testCase.want {
				t.Fatalf("%s English singular = %q, want %q", testCase.key, got, testCase.want)
			}
		}
	})

	t.Run("reference index names one image without a plural", func(t *testing.T) {
		if got := application.Chat.Presentation.Messages.ReferenceIndexOutOfRangeTemplate; got != "参考图序号 {index} 无效；当前共有 {count} 张图片" {
			t.Fatalf("Chinese reference text = %q", got)
		}
		template := english.Fields["chat.presentation.messages.reference_index_out_of_range_template"]
		for _, placeholder := range []string{"{index}", "{count}"} {
			if !strings.Contains(template, placeholder) {
				t.Fatalf("English reference text %q lacks %s", template, placeholder)
			}
		}
		one := strings.NewReplacer("{index}", "2", "{count}", "1").Replace(template)
		if strings.Contains(one, "1 images") || !strings.Contains(one, "total images: 1") {
			t.Fatalf("English reference text for one image = %q", one)
		}
	})

	t.Run("total encoded refusal names the count without a plural", func(t *testing.T) {
		chinese := application.Runtime.InputAssets.Messages.TotalTooLargeTemplate
		if !strings.Contains(chinese, "{count} 张图片") {
			t.Fatalf("Chinese total refusal = %q", chinese)
		}
		template := english.Fields["runtime.input_assets.messages.total_too_large_template"]
		for _, placeholder := range []string{"{label}", "{count}", "{size}", "{limit}"} {
			if !strings.Contains(template, placeholder) {
				t.Fatalf("English total refusal %q lacks %s", template, placeholder)
			}
		}
		one := strings.NewReplacer("{label}", "Reference image", "{count}", "1", "{size}", "9 MB", "{limit}", "8 MB").Replace(template)
		if !strings.Contains(one, `Encoded image data for "Reference image" totals 9 MB (image count: 1)`) {
			t.Fatalf("English total refusal for one image = %q", one)
		}
		for _, pluralPhrase := range []string{"1 images", "The images", "images themselves"} {
			if strings.Contains(one, pluralPhrase) {
				t.Fatalf("English total refusal for one image contains %q: %q", pluralPhrase, one)
			}
		}
	})
}
