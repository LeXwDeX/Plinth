package harness

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestSelectionCanonicalDigest(t *testing.T) {
	a := DefaultSelection("owner", "session")
	a.EnabledOperations = []string{"texture", "mesh", "mesh"}
	a.FillableParameters = []string{"z", "a", "a"}
	a.FixedParameters = map[string]json.RawMessage{"x": json.RawMessage(`{"b":1.00,"a":[1e2,-0.0]}`)}
	b := a
	b.EnabledOperations = []string{"mesh", "texture"}
	b.FillableParameters = []string{"a", "z"}
	b.FixedParameters = map[string]json.RawMessage{"x": json.RawMessage(`{"a":[100,0],"b":1}`)}
	n, err := NormalizeSelection(a)
	if err != nil {
		t.Fatal(err)
	}
	m, err := NormalizeSelection(b)
	if err != nil {
		t.Fatal(err)
	}
	if n.Digest != m.Digest {
		t.Fatalf("equivalent JSON has different digests: %s %s", n.Digest, m.Digest)
	}
	if len(a.EnabledOperations) != 3 || a.EnabledOperations[0] != "texture" {
		t.Fatal("mutated caller")
	}
	n.Revision = 7
	n.UpdatedAt = n.UpdatedAt.AddDate(1, 0, 0)
	again, err := NormalizeSelection(n)
	if err != nil || again.Digest != m.Digest {
		t.Fatal("metadata changed digest", err)
	}
	if DefaultSelection("owner", "session").Digest == "" {
		t.Fatal("new default has legacy empty digest")
	}
}

func TestSelectionRejectsInvalidContentAndBinding(t *testing.T) {
	for _, raw := range []string{`{"x":`, `1 2`, `{"a":1,"a":2}`} {
		s := DefaultSelection("owner", "session")
		s.FixedParameters = map[string]json.RawMessage{"x": json.RawMessage(raw)}
		if _, err := NormalizeSelection(s); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted %s: %v", raw, err)
		}
	}
	s := DefaultSelection("owner", "session")
	s.Sources = []Source{{Parameter: "model", TaskID: "task"}, {Parameter: "model", TaskID: "task"}}
	if _, err := NormalizeSelection(s); !errors.Is(err, ErrInvalid) {
		t.Fatal("duplicate sources accepted")
	}
	for _, b := range []Binding{{Revision: -1}, {Revision: 1}, {Digest: "bad"}, {Revision: 1, Digest: "BAD"}} {
		if b.Valid() {
			t.Fatalf("accepted malformed binding %+v", b)
		}
	}
	if !(Binding{}).Valid() || !DefaultSelection("owner", "session").Binding.Valid() {
		t.Fatal("valid zero binding rejected")
	}
}

func TestSelectionCanonicalNumbersKeepExactPrecision(t *testing.T) {
	a := DefaultSelection("owner", "session")
	a.FixedParameters = map[string]json.RawMessage{"x": json.RawMessage(`9007199254740993`)}
	b := a
	b.FixedParameters = map[string]json.RawMessage{"x": json.RawMessage(`9007199254740992`)}
	n, err := NormalizeSelection(a)
	if err != nil {
		t.Fatal(err)
	}
	m, err := NormalizeSelection(b)
	if err != nil {
		t.Fatal(err)
	}
	if n.Digest == m.Digest {
		t.Fatal("distinct integers collapsed through floating point")
	}
	a.FixedParameters["x"] = json.RawMessage(`1e99999999999999999999`)
	if _, err := NormalizeSelection(a); err != nil {
		t.Fatal("valid exponent should not need a giant allocation", err)
	}
}
