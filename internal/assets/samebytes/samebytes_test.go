package samebytes

import (
	"strings"
	"testing"
)

const digest = "0b9d1d4f9f2a5e0d7c8c3a1f4e6b2d9a8c7f5e3d1b0a9c8e7d6f5a4b3c2d1e0f"

func TestSameNeedsADigestAndASizeThatAgree(t *testing.T) {
	for name, test := range map[string]struct {
		left, right Record
		want        bool
	}{
		"same digest and size":         {Record{digest, 12}, Record{digest, 12}, true},
		"different digest":             {Record{digest, 12}, Record{strings.Repeat("a", 64), 12}, false},
		"same digest, different size":  {Record{digest, 12}, Record{digest, 13}, false},
		"neither record has a digest":  {Record{"", 12}, Record{"", 12}, false},
		"only one record has a digest": {Record{digest, 12}, Record{"", 12}, false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := Same(test.left, test.right); got != test.want {
				t.Fatalf("Same(%+v, %+v) = %v, want %v", test.left, test.right, got, test.want)
			}
			if got := Same(test.right, test.left); got != test.want {
				t.Fatalf("Same is not symmetric for %+v and %+v", test.left, test.right)
			}
		})
	}
}

func TestLabelNamesTheSlotByItsVariantOrItsRole(t *testing.T) {
	for _, test := range []struct{ role, variant, want string }{
		{"converted_model", "converted.gltf", "converted.gltf"},
		{"model", "", "model"},
		{"converted_model", "a b/c", "a_b_c"},
		{"", "", "asset"},
		{".hidden", "", "hidden"},
		{"model", strings.Repeat("v", 100), strings.Repeat("v", 80)},
	} {
		if got := Label(test.role, test.variant); got != test.want {
			t.Errorf("Label(%q, %q) = %q, want %q", test.role, test.variant, got, test.want)
		}
	}
}

func TestSectionNamesEveryRepeatUnderOneHeading(t *testing.T) {
	text := Text{Filename: "README.txt", Heading: "Identical files:", Line: "- {label} has the same content as {path}; only one copy is included"}
	if !text.Declared() {
		t.Fatal("a text with a heading and a line is declared")
	}
	if got := text.Section(nil); got != nil {
		t.Fatalf("no repeat, section = %q", got)
	}
	got := text.Section([]Repeat{{Label: "converted.gltf", Path: "model.glb"}, {Label: "model", Path: "model.glb"}})
	want := []string{
		"Identical files:",
		"- converted.gltf has the same content as model.glb; only one copy is included",
		"- model has the same content as model.glb; only one copy is included",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("section = %q, want %q", got, want)
	}
}

// Without both texts an archive never leaves a file out: the README could not say so.
func TestATextNeedsBothTheHeadingAndTheLineToBeDeclared(t *testing.T) {
	for name, text := range map[string]Text{
		"nothing":      {},
		"no heading":   {Filename: "README.txt", Line: "- {label} {path}"},
		"no line":      {Filename: "README.txt", Heading: "Identical files:"},
		"blank texts":  {Filename: "README.txt", Heading: "  ", Line: "\n"},
		"only a named": {Filename: "README.txt"},
	} {
		if text.Declared() {
			t.Errorf("%s: declared", name)
		}
	}
}
