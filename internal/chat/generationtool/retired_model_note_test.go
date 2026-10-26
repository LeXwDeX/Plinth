package generationtool_test

import (
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #329 follow-up C: when the turn's pick was a retired model that the
// service moved to its successor, the generation step runs on the successor
// and its result says so in the Schema's words, so the Agent tells the person.
func TestStepOnTheSuccessorOfARetiredPickSaysSo(t *testing.T) {
	h := newCrossDomainHarness(t,
		generation.Offering{ID: "meshy/meshy-7.1/text-to-3d", ModelID: "meshy-7.1", ModelName: "Meshy 7.1", Default: true},
	)
	h.expectOffering("meshy/meshy-7.1/text-to-3d")
	if h.tool.ResultFields.ModelNote == "" || !strings.Contains(h.chat.Presentation.Messages.RetiredModelFallback, "{from}") {
		t.Fatalf("the 3D tool declares no model note: %+v / %q", h.tool.ResultFields, h.chat.Presentation.Messages.RetiredModelFallback)
	}
	_, payload := h.run(t, chat.TurnOptions{
		CreativeDomain: "model-3d", AIModel: "meshy-7.1",
		RetiredModels: []chat.RetiredModelSwitch{{Domain: "model-3d", From: "meshy-7", FromName: "Meshy 7", To: "meshy-7.1", ToName: "Meshy 7.1"}},
	})
	if h.gateway.submitted.OfferingID != "meshy/meshy-7.1/text-to-3d" {
		t.Fatalf("submitted = %#v", h.gateway.submitted)
	}
	want := strings.NewReplacer("{from}", "Meshy 7", "{to}", "Meshy 7.1").Replace(h.chat.Presentation.Messages.RetiredModelFallback)
	if payload[h.tool.ResultFields.ModelNote] != want {
		t.Fatalf("model note = %v, want %q (payload %v)", payload[h.tool.ResultFields.ModelNote], want, payload)
	}

	// A turn without a switch (or a switch for the other domain) has no note.
	for name, options := range map[string]chat.TurnOptions{
		"no switch": {CreativeDomain: "model-3d", AIModel: "meshy-7.1"},
		"other domain": {CreativeDomain: "model-3d", AIModel: "meshy-7.1",
			RetiredModels: []chat.RetiredModelSwitch{{Domain: "image", From: "old", FromName: "Old", To: "new", ToName: "New"}}},
	} {
		h := newCrossDomainHarness(t,
			generation.Offering{ID: "meshy/meshy-7.1/text-to-3d", ModelID: "meshy-7.1", ModelName: "Meshy 7.1", Default: true},
		)
		h.expectOffering("meshy/meshy-7.1/text-to-3d")
		_, payload := h.run(t, options)
		if _, noted := payload[h.tool.ResultFields.ModelNote]; noted {
			t.Fatalf("%s: got a note %v", name, payload)
		}
	}
}

// #358: the retired-model note follows the turn locale. Only Schema names are
// translated; a person's or provider's own string stays as it is.
func TestRetiredModelNoteTranslatesOnlySchemaNames(t *testing.T) {
	fromZH := "生成失败"
	toZH := "生成异常"
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	texts, err := catalog.NewTextLocalizer(application)
	if err != nil {
		t.Fatal(err)
	}
	fromZH = application.Chat.Presentation.Messages.GenerationFailed
	toZH = application.Chat.Presentation.Messages.GenerationError
	fromEN, toEN := texts.Text("en", fromZH), texts.Text("en", toZH)
	if fromZH == "" || toZH == "" || fromEN == fromZH || toEN == toZH {
		t.Fatalf("need distinct default and English Schema names: %q/%q %q/%q", fromZH, fromEN, toZH, toEN)
	}

	h := newCrossDomainHarness(t,
		generation.Offering{ID: "meshy/meshy-7.1/text-to-3d", ModelID: "meshy-7.1", Default: true},
	)
	h.expectOffering("meshy/meshy-7.1/text-to-3d")
	_, payload := h.run(t, chat.TurnOptions{
		CreativeDomain: "model-3d", AIModel: "meshy-7.1", Locale: "en",
		RetiredModels: []chat.RetiredModelSwitch{{Domain: "model-3d", From: "meshy-7", FromName: fromZH,
			To: "meshy-7.1", ToName: toZH}},
	})
	want := strings.NewReplacer("{from}", fromEN, "{to}", toEN).Replace(
		texts.Text("en", h.chat.Presentation.Messages.RetiredModelFallback))
	if payload[h.tool.ResultFields.ModelNote] != want {
		t.Fatalf("model note = %v, want %q", payload[h.tool.ResultFields.ModelNote], want)
	}

	// A name that is not Schema copy is not translated.
	h = newCrossDomainHarness(t,
		generation.Offering{ID: "meshy/meshy-7.1/text-to-3d", ModelID: "meshy-7.1", Default: true},
	)
	h.expectOffering("meshy/meshy-7.1/text-to-3d")
	_, payload = h.run(t, chat.TurnOptions{
		CreativeDomain: "model-3d", AIModel: "meshy-7.1", Locale: "en",
		RetiredModels: []chat.RetiredModelSwitch{{Domain: "model-3d", From: "meshy-7", FromName: "user named model",
			To: "meshy-7.1", ToName: "user successor"}},
	})
	want = strings.NewReplacer("{from}", "user named model", "{to}", "user successor").Replace(
		texts.Text("en", h.chat.Presentation.Messages.RetiredModelFallback))
	if payload[h.tool.ResultFields.ModelNote] != want {
		t.Fatalf("user names changed: %v, want %q", payload[h.tool.ResultFields.ModelNote], want)
	}
}
