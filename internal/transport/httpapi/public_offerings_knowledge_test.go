package httpapi_test

import (
	"encoding/json"
	"testing"
)

type publicOfferingKnowledge struct {
	Strengths   []string `json:"strengths"`
	Weaknesses  []string `json:"weaknesses"`
	Limitations []string `json:"limitations"`
}

type publicOfferingsBody struct {
	Offerings []struct {
		ID        string                   `json:"id"`
		Knowledge *publicOfferingKnowledge `json:"knowledge"`
	} `json:"offerings"`
}

func offeringKnowledge(t *testing.T, body []byte, id string) *publicOfferingKnowledge {
	t.Helper()
	var parsed publicOfferingsBody
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatal(err)
	}
	for _, offering := range parsed.Offerings {
		if offering.ID == id {
			return offering.Knowledge
		}
	}
	t.Fatalf("%s is not public", id)
	return nil
}

func TestPublicOfferingsProjectOnlyTheThreeKnowledgeStatements(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarness(t)
	defaultBody := getWithLocale(t, handler, "/api/v1/offerings", "")
	englishBody := getWithLocale(t, handler, "/api/v1/offerings", "en")
	const id = "meshy/nano-banana-pro/text-to-image"

	chinese := offeringKnowledge(t, defaultBody, id)
	if chinese == nil || len(chinese.Strengths) == 0 || len(chinese.Weaknesses) == 0 || len(chinese.Limitations) == 0 {
		t.Fatalf("default knowledge = %#v", chinese)
	}
	translated := offeringKnowledge(t, englishBody, id)
	if translated == nil || len(translated.Strengths) == 0 {
		t.Fatalf("English knowledge = %#v", translated)
	}
	if translated.Strengths[0] == chinese.Strengths[0] {
		t.Fatal("English knowledge was not translated by offering_texts")
	}

	// Prompt craft, reference use and sources are Agent- or maintainer-facing.
	var generic struct {
		Offerings []map[string]any `json:"offerings"`
	}
	if err := json.Unmarshal(defaultBody, &generic); err != nil {
		t.Fatal(err)
	}
	for _, offering := range generic.Offerings {
		if offering["id"] != id {
			continue
		}
		knowledge, ok := offering["knowledge"].(map[string]any)
		if !ok {
			t.Fatal("knowledge is not an object")
		}
		for _, forbidden := range []string{"prompt", "reference_use", "sources"} {
			if _, present := knowledge[forbidden]; present {
				t.Fatalf("public knowledge leaked %q", forbidden)
			}
		}
	}
}
