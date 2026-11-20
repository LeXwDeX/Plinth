package hyper3d_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/hyper3d"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// #376: Rodin Gen-2.5 takes image_label next to images, one mark per picture by
// position. The Schema declares the hidden parameter reference_image_labels; the
// Adapter sends it as one text part per mark, ahead of the pictures, and refuses
// before any request every mark list it cannot send as exactly one mark per
// picture. These tests run the shipped Offering against the Hyper3D fake, whose
// wire for the marks is the one two real requests of 2026-11-19 settled.

const (
	rodinImageOffering = "hyper3d/rodin-gen-2.5/image-to-3d"
	rodinLabelsName    = "reference_image_labels"
)

// rodinTemplateWire is the request the display profile sends, field by field, as
// the Offering's request_template and same-named parameters write it.
var rodinTemplateWire = []string{
	"geometry_file_format=glb", "material=PBR", "mesh_mode=Raw", "quality=high", "quality_override=2000000",
	"texture_mode=extreme-high", "tier=Gen-2.5-Extreme-High",
}

// pictureAssets are n owned pictures resolved for the reference_assets parameter.
func pictureAssets(n int) map[string][]generation.InputAsset {
	assets := make([]generation.InputAsset, 0, n)
	for index := 0; index < n; index++ {
		assets = append(assets, generation.InputAsset{
			ID: generation.AssetID(fmt.Sprintf("view-%d", index)), OwnerID: "owner",
			MediaType: "image/png", OriginalFilename: fmt.Sprintf("view-%d.png", index),
		})
	}
	return map[string][]generation.InputAsset{"reference_assets": assets}
}

// labelInput is the input of n pictures. marks is the value of the labels
// parameter as given (nil leaves the parameter out).
func labelInput(t *testing.T, n int, marks any) json.RawMessage {
	t.Helper()
	ids := make([]string, 0, n)
	for index := 0; index < n; index++ {
		ids = append(ids, fmt.Sprintf("view-%d", index))
	}
	input := map[string]any{"prompt": "a small robot", "authorize_extra_credits": true, "reference_assets": ids}
	if marks != nil {
		input[rodinLabelsName] = marks
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func (f *rodinFake) imageOffering() generation.Offering {
	f.t.Helper()
	offering, ok := f.product.Get(rodinImageOffering)
	if !ok {
		f.t.Fatalf("missing Offering %s", rodinImageOffering)
	}
	return offering
}

// startPictures submits input with n resolved pictures. A normalized input has
// passed the Schema; a raw one reaches the Adapter as given.
func (f *rodinFake) startPictures(input json.RawMessage, n int, normalize bool) (generation.Observation, error) {
	f.t.Helper()
	offering := f.imageOffering()
	if normalize {
		var err error
		if _, input, err = f.product.Normalize(offering.Capability, offering.ID, input); err != nil {
			f.t.Fatalf("the Schema refused the input: %v", err)
		}
	}
	return f.port().Start(context.Background(), generation.StartRequest{
		OwnerID: "owner", TaskID: "task", AttemptID: "attempt", SubmissionKey: "once",
		Offering: offering, Input: input, InputAssets: pictureAssets(n),
	})
}

func marksOnTheWire(parts []string) []string {
	var marks []string
	for _, part := range parts {
		if mark, ok := strings.CutPrefix(part, "image_label="); ok {
			marks = append(marks, mark)
		}
	}
	return marks
}

func repeated(part string, n int) []string {
	result := make([]string, n)
	for index := range result {
		result[index] = part
	}
	return result
}

// The marks are sent as the real requests sent them: one part per mark, in
// picture order, ahead of the pictures. The Offering declares the parameter
// before reference_assets, which is what puts them there; the template fields
// and the prompt keep their places.
func TestRodinImageLabelsAreSentBeforeThePictures(t *testing.T) {
	for name, test := range map[string]struct {
		pictures int
		marks    []string
	}{
		"three views":                {3, []string{"F", "L", "B"}},
		"every documented mark":      {5, []string{"F", "FL", "FR", "B", "BL"}},
		"the other documented marks": {5, []string{"BR", "L", "R", "U", "D"}},
		"unknown sides":              {2, []string{"?", "?"}},
		"a repeated mark":            {3, []string{"F", "F", "?"}},
		"one picture":                {1, []string{"F"}},
	} {
		t.Run(name, func(t *testing.T) {
			f := newRodinFake(t)
			started, err := f.startPictures(labelInput(t, test.pictures, test.marks), test.pictures, true)
			if err != nil || started.Job == nil {
				t.Fatalf("the fake refused marks the Schema lets through: %+v %v", started, err)
			}
			want := slices.Concat(rodinTemplateWire, prefixed("image_label=", test.marks),
				repeated("images=<file>", test.pictures), []string{"prompt=a small robot"})
			if got := partStrings(f.only()); !slices.Equal(got, want) {
				t.Fatalf("request = %v\nwant %v", got, want)
			}
		})
	}
}

func prefixed(prefix string, values []string) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = prefix + value
	}
	return result
}

// Without marks nothing about the request changes: no part, whether the
// parameter is absent, null or empty. The recorded wire of the display request
// is pinned by TestRodinDisplayRequestIsTheRecordedWireOfTheOldTemplate; this is
// the same request with several pictures.
func TestRodinWithoutImageLabelsTheRequestIsUnchanged(t *testing.T) {
	for name, marks := range map[string]any{"absent": nil, "an empty list": []string{}} {
		t.Run(name, func(t *testing.T) {
			f := newRodinFake(t)
			if _, err := f.startPictures(labelInput(t, 3, marks), 3, true); err != nil {
				t.Fatal(err)
			}
			want := slices.Concat(rodinTemplateWire, repeated("images=<file>", 3), []string{"prompt=a small robot"})
			if got := partStrings(f.only()); !slices.Equal(got, want) {
				t.Fatalf("request = %v\nwant %v", got, want)
			}
		})
	}
	t.Run("null", func(t *testing.T) {
		f := newRodinFake(t)
		input := json.RawMessage(`{"prompt":"a small robot","authorize_extra_credits":true,"reference_assets":["view-0","view-1"],"reference_image_labels":null}`)
		if _, err := f.startPictures(input, 2, false); err != nil {
			t.Fatal(err)
		}
		if marks := marksOnTheWire(partStrings(f.only())); len(marks) != 0 {
			t.Fatalf("a null list wrote marks: %v", marks)
		}
	})
}

// Every list the Adapter cannot send as one mark per picture is refused before
// the request: more marks than pictures (what Rodin refuses with
// IMAGE_LABEL_LENGTH_TOO_LONG), fewer marks (whose result was not observed, so it
// is never sent), a mark that is not on the page's list, and values that are not
// a list of text. The requester reads the Schema's copy with the parameter's
// public label; neither the parameter nor the wire field is named. Nothing
// reaches the upstream, and ValidateInput says the same before the step is
// prepared.
func TestRodinImageLabelsAreRefusedLocallyBeforeAnyRequest(t *testing.T) {
	for name, test := range map[string]struct {
		pictures int
		marks    any
	}{
		"more marks than pictures":  {2, []string{"F", "L", "B"}},
		"fewer marks than pictures": {3, []string{"F", "L"}},
		"one mark for two pictures": {2, []string{"F"}},
		"a mark that is not listed": {3, []string{"F", "X", "B"}},
		"a mark in lowercase":       {1, []string{"f"}},
		"an empty mark":             {1, []string{""}},
		"a word for a mark":         {1, []string{"front"}},
		"not a list":                {1, "F"},
		"a mark that is not text":   {1, []any{1}},
	} {
		t.Run(name, func(t *testing.T) {
			f := newRodinFake(t)
			input := labelInput(t, test.pictures, test.marks)
			started, err := f.startPictures(input, test.pictures, false)
			if started.Job != nil {
				t.Fatalf("marks that cannot be sent started a job: %+v", started)
			}
			assertLabelRefusal(t, err)
			f.mu.Lock()
			exchanges := len(f.exchanges)
			f.mu.Unlock()
			if exchanges != 0 || len(f.submissions()) != 0 {
				t.Fatalf("the upstream was reached: %d exchanges, %d submissions", exchanges, len(f.submissions()))
			}
			validated := f.port().ValidateInput(context.Background(), generation.InputValidationRequest{
				OwnerID: "owner", Offering: f.imageOffering(), Input: input, InputAssets: pictureAssets(test.pictures),
			})
			assertLabelRefusal(t, validated)
		})
	}
}

func assertLabelRefusal(t *testing.T, err error) {
	t.Helper()
	var provider *generation.ProviderError
	if !errors.As(err, &provider) || provider.Failure == nil ||
		provider.Failure.Class != generation.FailureInvalidInput || provider.Failure.Retryable || provider.Failure.Ambiguous {
		t.Fatalf("error = %v, want a non-retryable invalid-input refusal", err)
	}
	refusal := generation.UserFacingError(err)
	if refusal == nil || refusal.Code != generation.ErrInvalidInput || !strings.Contains(refusal.Detail, "参考图方位标记") {
		t.Fatalf("refusal = %+v, want the Schema text with the parameter's label", refusal)
	}
	for _, internal := range []string{"image_label", rodinLabelsName, "reference_assets"} {
		if strings.Contains(refusal.Detail, internal) || strings.Contains(provider.Failure.SafeDetail, internal) {
			t.Fatalf("the refusal names %q: %q / %q", internal, refusal.Detail, provider.Failure.SafeDetail)
		}
	}
}

// ValidateInput passes a request the Adapter can send, and says nothing about
// an Offering that declares no marks, whatever its input holds.
func TestRodinValidateInputAcceptsSendableMarksAndIgnoresOfferingsWithoutThem(t *testing.T) {
	f := newRodinFake(t)
	port := f.port()
	validate := func(offering generation.Offering, input json.RawMessage, pictures int) error {
		return port.ValidateInput(context.Background(), generation.InputValidationRequest{
			OwnerID: "owner", Offering: offering, Input: input, InputAssets: pictureAssets(pictures),
		})
	}
	image := f.imageOffering()
	for name, test := range map[string]struct {
		pictures int
		marks    any
	}{
		"one mark per picture": {3, []string{"F", "L", "B"}},
		"repeated marks":       {2, []string{"?", "?"}},
		"no marks":             {3, nil},
		"an empty list":        {3, []string{}},
	} {
		if err := validate(image, labelInput(t, test.pictures, test.marks), test.pictures); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	for _, id := range []string{"hyper3d/rodin-gen-2.5/text-to-3d", "hyper3d/texture/local-model"} {
		other, ok := f.product.Get(generation.OfferingID(id))
		if !ok {
			t.Fatalf("missing %s", id)
		}
		junk := json.RawMessage(`{"prompt":"x","reference_image_labels":["X","Y","Z"]}`)
		if err := validate(other, junk, 1); err != nil {
			t.Errorf("%s declares no marks, ValidateInput = %v", id, err)
		}
	}
	// An Offering whose image parameter carries no marks at all (the plain shape
	// of adapter_test.go) takes the pictures as before.
	if err := validate(offering(generation.ImageTo3D), json.RawMessage(`{"reference_assets":["a","b"]}`), 2); err != nil {
		t.Errorf("an Offering without marks: %v", err)
	}
	// The input must be an object, as everywhere in the Adapter.
	if err := validate(image, json.RawMessage(`[1]`), 1); err == nil {
		t.Error("an input that is not an object was accepted")
	}
}

// The Schema's wire shape is checked where the Offerings are wired, so a
// Catalog that would send the marks another way cannot start: the field, the
// verbatim text, the place before the pictures, the lack of a default, the
// repeated values and the one-mark-per-picture capacity.
func TestRodinImageLabelDeclarationIsChecked(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	base, ok := product.Get(rodinImageOffering)
	if !ok {
		t.Fatalf("missing %s", rodinImageOffering)
	}
	if err := hyper3d.ValidateOffering(base); err != nil {
		t.Fatalf("the shipped Offering is refused: %v", err)
	}
	change := func(edit func(offering *generation.Offering, labels, images *generation.Parameter)) error {
		offering := base
		offering.Parameters = append([]generation.Parameter(nil), base.Parameters...)
		labels, images := -1, -1
		for index, parameter := range offering.Parameters {
			switch {
			case parameter.Semantic == schema.SemanticReferenceImageLabels:
				labels = index
			case parameter.Name == "reference_assets":
				images = index
			}
		}
		if labels < 0 || images < 0 {
			t.Fatal("the Offering lacks the labels or the pictures parameter")
		}
		edit(&offering, &offering.Parameters[labels], &offering.Parameters[images])
		return hyper3d.ValidateOffering(offering)
	}
	for name, test := range map[string]struct {
		edit func(*generation.Offering, *generation.Parameter, *generation.Parameter)
		want string
	}{
		"a default": {func(_ *generation.Offering, labels, _ *generation.Parameter) {
			labels.Default = json.RawMessage(`["F"]`)
		}, "默认值"},
		"required": {func(_ *generation.Offering, labels, _ *generation.Parameter) {
			labels.Required = true
		}, "必填"},
		"a minimum count": {func(_ *generation.Offering, labels, _ *generation.Parameter) {
			labels.MinItems = 1
		}, "必填"},
		"fixed": {func(_ *generation.Offering, labels, _ *generation.Parameter) {
			labels.Fixed = true
		}, "固定"},
		"values that cannot repeat": {func(_ *generation.Offering, labels, _ *generation.Parameter) {
			labels.AllowDuplicateValues = false
		}, "allow_duplicate_values"},
		"a remapped value": {func(_ *generation.Offering, labels, _ *generation.Parameter) {
			labels.WireValues = map[string]json.RawMessage{"F": json.RawMessage(`"front"`)}
		}, "wire_values"},
		"a templated value": {func(_ *generation.Offering, labels, _ *generation.Parameter) {
			labels.WireTemplate = json.RawMessage(`"$value"`)
		}, "wire_template"},
		"another field": {func(_ *generation.Offering, labels, _ *generation.Parameter) {
			labels.WireName = "labels"
		}, "image_label"},
		"not a list": {func(_ *generation.Offering, labels, _ *generation.Parameter) {
			labels.Type = "text"
		}, "string-array"},
		"room for fewer marks than pictures": {func(_ *generation.Offering, labels, _ *generation.Parameter) {
			labels.MaxItems = 4
		}, "max_items"},
		"no declared marks": {func(_ *generation.Offering, labels, _ *generation.Parameter) {
			labels.Options = nil
		}, "可选标记"},
		"a mark the page does not list": {func(_ *generation.Offering, labels, _ *generation.Parameter) {
			labels.Options = append(append([]generation.ParameterOption(nil), labels.Options...), generation.ParameterOption{Value: "X", Label: "x"})
		}, `"X"`},
		"declared after the pictures": {func(offering *generation.Offering, _, _ *generation.Parameter) {
			parameters := offering.Parameters
			first, second := slices.IndexFunc(parameters, func(p generation.Parameter) bool { return p.Name == rodinLabelsName }),
				slices.IndexFunc(parameters, func(p generation.Parameter) bool { return p.Name == "reference_assets" })
			parameters[first], parameters[second] = parameters[second], parameters[first]
		}, "之前"},
		"a second labels parameter": {func(offering *generation.Offering, labels, _ *generation.Parameter) {
			second := *labels
			second.Name = "other_labels"
			offering.Parameters = append(offering.Parameters, second)
		}, "一个"},
		"a text-to-3D Offering": {func(offering *generation.Offering, _, _ *generation.Parameter) {
			offering.Capability = generation.TextTo3D
		}, "图生 3D"},
	} {
		if err := change(test.edit); err == nil || !strings.Contains(err.Error(), test.want) {
			t.Errorf("%s: error = %v, want one naming %q", name, err, test.want)
		}
	}
	// The pictures parameter has to be there for the marks to match anything.
	noPictures := base
	noPictures.Parameters = slices.DeleteFunc(append([]generation.Parameter(nil), base.Parameters...), func(p generation.Parameter) bool {
		return p.Name == "reference_assets"
	})
	if err := hyper3d.ValidateOffering(noPictures); err == nil {
		t.Error("an Offering with marks and without pictures was accepted")
	}
}
