package catalog

import (
	"strings"
	"testing"

	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
)

func TestDefaultCatalogGivesEveryEnabledGenerationOfferingKnowledge(t *testing.T) {
	product, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	capabilities := map[schemacontract.Capability]bool{
		schemacontract.TextToImage: true, schemacontract.ImageToImage: true,
		schemacontract.TextTo3D: true, schemacontract.ImageTo3D: true,
	}
	count := 0
	for _, offering := range product.List("") {
		if !capabilities[offering.Capability] {
			continue
		}
		count++
		if offering.Knowledge == nil {
			t.Fatalf("%s has no knowledge", offering.ID)
		}
		if offering.Knowledge.Public() == nil {
			t.Fatalf("%s has no public knowledge", offering.ID)
		}
		hasPrompt, hasNegative, hasReference := false, false, false
		for _, parameter := range offering.Parameters {
			switch parameter.Semantic {
			case schemacontract.SemanticPrompt:
				hasPrompt = true
			case schemacontract.SemanticNegativePrompt:
				hasNegative = true
			case schemacontract.SemanticReferenceImage:
				hasReference = true
			}
		}
		prompt := offering.Knowledge.Prompt
		if prompt == nil {
			t.Fatalf("%s declares no prompt craft", offering.ID)
		}
		if prompt.ReadsText == nil || *prompt.ReadsText != hasPrompt {
			t.Fatalf("%s reads_text disagrees with its prompt parameter", offering.ID)
		}
		if prompt.NegativePrompt == nil || *prompt.NegativePrompt != hasNegative {
			t.Fatalf("%s negative_prompt disagrees with its parameter", offering.ID)
		}
		hasNone := false
		for _, use := range offering.Knowledge.ReferenceUse {
			hasNone = hasNone || use == schemacontract.ReferenceUseNone
		}
		if hasNone != !hasReference {
			t.Fatalf("%s reference_use disagrees with its reference parameter", offering.ID)
		}
	}
	if count != 24 {
		t.Fatalf("enabled generation offerings = %d, want 24", count)
	}
}

func TestOfferingKnowledgePublicProjectionOmitsAgentAndMaintainerFields(t *testing.T) {
	knowledge := schemacontract.OfferingKnowledge{
		Strengths:    []string{"strong"},
		Weaknesses:   []string{"weak"},
		Limitations:  []string{"limited"},
		Prompt:       &schemacontract.OfferingPrompt{Language: "en", ReadsText: boolPointer(true)},
		ReferenceUse: []string{schemacontract.ReferenceUseGeometry},
		Sources:      []schemacontract.OfferingKnowledgeSource{{Title: "docs", URL: "https://example.test", CheckedAt: "2026-11-10"}},
	}
	public := knowledge.Public()
	if public == nil || len(public.Strengths) != 1 || len(public.Weaknesses) != 1 || len(public.Limitations) != 1 {
		t.Fatalf("public projection = %#v", public)
	}
	if (&schemacontract.OfferingKnowledge{}).Public() != nil {
		t.Fatal("empty knowledge must not project a public object")
	}
}

func TestOfferingKnowledgeInvalidDeclarationsFailLoading(t *testing.T) {
	for name, tc := range map[string]struct {
		change func(knowledge map[string]any)
		want   string
	}{
		"missing required knowledge": {
			change: func(knowledge map[string]any) {
				knowledge["strengths"] = nil
				knowledge["weaknesses"] = nil
				knowledge["limitations"] = nil
				knowledge["prompt"] = nil
				knowledge["reference_use"] = nil
				knowledge["sources"] = nil
			},
			want: "缺少 knowledge 自述",
		},
		"reads_text disagrees": {
			change: func(knowledge map[string]any) {
				knowledge["prompt"].(map[string]any)["reads_text"] = false
			},
			want: "reads_text",
		},
		"negative prompt disagrees": {
			change: func(knowledge map[string]any) {
				knowledge["prompt"].(map[string]any)["negative_prompt"] = true
			},
			want: "negative_prompt",
		},
		"reference use none with a reference parameter": {
			change: func(knowledge map[string]any) {
				knowledge["reference_use"] = []any{"identity", "none"}
			},
			want: "reference_use",
		},
		"invalid language": {
			change: func(knowledge map[string]any) {
				knowledge["prompt"].(map[string]any)["language"] = "fr"
			},
			want: "language",
		},
		"words minimum over maximum": {
			change: func(knowledge map[string]any) {
				prompt := knowledge["prompt"].(map[string]any)
				prompt["words_min"] = 200
				prompt["words_max"] = 100
			},
			want: "words_min",
		},
		"invalid checked date": {
			change: func(knowledge map[string]any) {
				knowledge["sources"].([]any)[0].(map[string]any)["checked_at"] = "November 2026"
			},
			want: "checked_at",
		},
		"internal parameter name in public copy": {
			change: func(knowledge map[string]any) {
				knowledge["strengths"] = []any{"Uses aspect_ratio for every output."}
			},
			want: "内部",
		},
	} {
		t.Run(name, func(t *testing.T) {
			err := mutateDefaultCatalog(t, func(document map[string]any) {
				for _, item := range document["offerings"].([]any) {
					offering := item.(map[string]any)
					if offering["id"] == "meshy/nano-banana-pro/text-to-image" {
						tc.change(offering["knowledge"].(map[string]any))
						return
					}
				}
				t.Fatal("fixture offering missing")
			})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestOfferingKnowledgeSchemaRejectsIncompleteTemplates(t *testing.T) {
	err := mutateDefaultCatalog(t, func(document map[string]any) {
		agent := document["application"].(map[string]any)["chat"].(map[string]any)["agent"].(map[string]any)
		knowledge := agent["offering_knowledge"].(map[string]any)
		delete(knowledge["language_texts"].(map[string]any), "unknown")
	})
	if err == nil || !strings.Contains(err.Error(), "language_texts") {
		t.Fatalf("error = %v", err)
	}
}

func TestOfferingKnowledgePromptGuidanceRendersUnknownAndUsesSharedVocabulary(t *testing.T) {
	product, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	schema := product.Application().Chat.Agent.OfferingKnowledge
	knowledge := &schemacontract.OfferingKnowledge{
		Prompt: &schemacontract.OfferingPrompt{ReadsText: boolPointer(false)},
	}
	text := schema.RenderPromptGuidance("A Model", knowledge)
	for _, unknown := range []string{"an unspecified language", "an unspecified prompt form", "aim for unknown", "unknown"} {
		if !strings.Contains(text, unknown) {
			t.Fatalf("guidance %q does not render %q", text, unknown)
		}
	}
	noText := schema.RenderNoTextGuidance("A Model", knowledge)
	if noText == "" || !strings.Contains(noText, "A Model") || !strings.Contains(noText, "not sent to this service") {
		t.Fatalf("no-text guidance = %q", noText)
	}
	if schema.RenderNoTextGuidance("A Model", &schemacontract.OfferingKnowledge{Prompt: &schemacontract.OfferingPrompt{ReadsText: boolPointer(true)}}) != "" {
		t.Fatal("a text-reading Offering rendered the no-text statement")
	}
}

func boolPointer(value bool) *bool { return &value }
