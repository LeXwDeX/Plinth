package catalog_test

import (
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #632: the read-only 3D self-check tools (inspect_model, render_model_views,
// compare_models, compare_model_reference, record_self_check) hand their
// Schema copy to the Agent verbatim. In the default locale that copy is
// Chinese, so a Chinese session never reads an English sentence it could copy
// into its reply; the English copy lives in the en bundle, and the coverage
// entries keep every new Chinese sentence translated.
func TestSelfCheckToolCopyIsInTheDefaultLocale(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	chinese := regexp.MustCompile(`\p{Han}`)
	machineToken := regexp.MustCompile(`[A-Za-z0-9_@#./{}=<>%-]*[_@#{}0-9=<>%][A-Za-z0-9_@#./{}=<>%-]*`)
	english := regexp.MustCompile(`[A-Za-z][A-Za-z'’]*(?:[ ,;:()/-]+[A-Za-z][A-Za-z'’]*){2,}`)
	bundle := application.Localization.Bundles["en"].Fields
	groups := map[string]any{
		"chat.model_review.source": application.Chat.ModelReview.Source,
		"chat.model_review.texts":  application.Chat.ModelReview.Texts,
		"chat.self_check.texts":    application.Chat.SelfCheck.Texts,
	}
	for prefix, group := range groups {
		value := reflect.ValueOf(group)
		for index := 0; index < value.NumField(); index++ {
			field := value.Type().Field(index)
			text, ok := value.Field(index).Interface().(string)
			name := strings.Split(field.Tag.Get("json"), ",")[0]
			if !ok || strings.HasSuffix(name, "_argument") || text == "" {
				continue
			}
			path := prefix + "." + name
			if !chinese.MatchString(text) {
				t.Errorf("%s is not default-locale copy: %q", path, text)
			}
			if match := english.FindString(machineToken.ReplaceAllString(text, "|")); match != "" {
				t.Errorf("%s carries the English phrase %q", path, match)
			}
			translated := bundle[path]
			if strings.TrimSpace(translated) == "" || chinese.MatchString(translated) {
				t.Errorf("the en bundle lacks an English %s: %q", path, translated)
			}
		}
	}
	for prefix := range groups {
		declared := false
		for _, path := range application.Localization.Coverage {
			declared = declared || path == prefix
		}
		if !declared {
			t.Errorf("localization coverage does not declare %s", prefix)
		}
	}
	if err := catalog.ValidateApplicationSchema(application); err != nil {
		t.Fatalf("default schema invalid: %v", err)
	}
}
