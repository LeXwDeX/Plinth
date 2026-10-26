package catalog

import (
	"strings"
	"testing"
)

// The refusal text is Schema copy: changing it must keep the facts a person
// needs (the public label and both counts), like the reference-limit note.
func TestReferenceLimitExceededTemplateKeepsItsPlaceholders(t *testing.T) {
	err := mutateDefaultCatalog(t, func(document map[string]any) {
		runtime := document["application"].(map[string]any)["runtime"].(map[string]any)
		messages := runtime["generation_messages"].(map[string]any)
		messages["reference_limit_exceeded"] = "too many pictures"
	})
	if err == nil || !strings.Contains(err.Error(), "reference_limit_exceeded") {
		t.Fatalf("template without {label}/{max}/{actual} accepted: %v", err)
	}
}
