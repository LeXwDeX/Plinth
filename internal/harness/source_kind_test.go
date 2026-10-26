package harness

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestHistoricalTaskSourceKeepsFrozenDigest(t *testing.T) {
	const oldSource = `{"parameter":"source_task","task_id":"task-original","asset_id":"asset-original"}`
	var source Source
	if err := json.Unmarshal([]byte(oldSource), &source); err != nil {
		t.Fatal(err)
	}
	selection := DefaultSelection("owner", "session")
	selection.OperationID, selection.OfferingID, selection.OfferingRevision = "remesh", "meshy/processing/remesh", "2026-10-13.1"
	selection.EnabledOperations = []string{"remesh"}
	selection.Sources = []Source{source}
	selection.FixedParameters = map[string]json.RawMessage{"target_polycount": json.RawMessage(`1500`)}
	normalized, err := NormalizeSelection(selection)
	if err != nil {
		t.Fatal(err)
	}
	if normalized.Digest != "ad03e5204bfd89017e873776f4f14ea01daafd98397dfae4131a26da9021105c" {
		t.Fatalf("historical digest changed: %s", normalized.Digest)
	}
	encoded, err := json.Marshal(normalized.Sources[0])
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != oldSource {
		t.Fatalf("historical source changed: %s", encoded)
	}
}

func TestSourceKindMustSurviveAndConstrainIdentity(t *testing.T) {
	cases := []struct {
		name, source string
		valid        bool
	}{
		{"native upload", `{"kind":"input_asset","parameter":"source_asset","asset_id":"upload-one"}`, true},
		{"explicit task", `{"kind":"task_asset","parameter":"source_task","task_id":"task-one"}`, true},
		{"task output", `{"kind":"task_asset","parameter":"source_asset","task_id":"task-one","asset_id":"output-one"}`, true},
		{"upload relabels task", `{"kind":"input_asset","parameter":"source_asset","task_id":"task-one","asset_id":"output-one"}`, false},
		{"upload without identity", `{"kind":"input_asset","parameter":"source_asset","task_id":"task-one"}`, false},
		{"task without task identity", `{"kind":"task_asset","parameter":"source_asset","asset_id":"output-one"}`, false},
		{"unknown kind", `{"kind":"remote_url","parameter":"source_asset","asset_id":"upload-one"}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var source Source
			if err := json.Unmarshal([]byte(tc.source), &source); err != nil {
				t.Fatal(err)
			}
			selection := DefaultSelection("owner", "session")
			selection.Sources = []Source{source}
			normalized, err := NormalizeSelection(selection)
			if !tc.valid {
				if !errors.Is(err, ErrInvalid) {
					t.Fatalf("ambiguous source accepted: %s (%v)", tc.source, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(normalized.Sources[0])
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(encoded), `"kind":`) {
				t.Fatalf("source identity discriminator was lost: %s", encoded)
			}
			var decoded map[string]any
			if err := json.Unmarshal(encoded, &decoded); err != nil {
				t.Fatal(err)
			}
			delete(decoded, "kind")
			raw, _ := json.Marshal(decoded)
			var legacy Source
			if err := json.Unmarshal(raw, &legacy); err != nil {
				t.Fatal(err)
			}
			selection.Sources = []Source{legacy}
			old, err := NormalizeSelection(selection)
			if err != nil {
				t.Fatal(err)
			}
			if old.Digest == normalized.Digest {
				t.Fatal("explicit source kind did not bind frozen identity")
			}
		})
	}
}

func TestAgentTaskSourceIsExplicitlyFillableAndNeverAnEmptyFixedSource(t *testing.T) {
	for _, tc := range []struct {
		raw   string
		valid bool
	}{
		{`{"kind":"task_asset","selection_mode":"agent","parameter":"source_task"}`, true},
		{`{"kind":"task_asset","selection_mode":"agent","parameter":"source_task","task_id":"task-one"}`, false},
		{`{"kind":"input_asset","selection_mode":"agent","parameter":"source_asset","asset_id":"upload-one"}`, false},
		{`{"kind":"task_asset","selection_mode":"invented","parameter":"source_task","task_id":"task-one"}`, false},
	} {
		var source Source
		json.Unmarshal([]byte(tc.raw), &source)
		selection := DefaultSelection("owner", "session")
		selection.Sources = []Source{source}
		normalized, err := NormalizeSelection(selection)
		if tc.valid {
			if err != nil {
				t.Fatal(err)
			}
			b, _ := json.Marshal(normalized.Sources[0])
			if !strings.Contains(string(b), `"selection_mode":"agent"`) {
				t.Fatal("fillable scope lost")
			}
		} else if err == nil {
			t.Fatal("ambiguous source accepted")
		}
	}
}

func TestWildcardModelSourcePermissionIsAgentTaskOnly(t *testing.T) {
	for _, s := range []Source{{Kind: SourceTaskAsset, SelectionMode: "agent", Parameter: "*"}, {Kind: SourceTaskAsset, Parameter: "*", TaskID: "fixed"}, {Kind: SourceInputAsset, Parameter: "*", AssetID: "upload"}, {Kind: SourceTaskAsset, SelectionMode: "fixed", Parameter: "*", TaskID: "fixed"}} {
		selection := DefaultSelection("owner", "session")
		selection.Sources = []Source{s}
		_, err := NormalizeSelection(selection)
		if (s.SelectionMode == "agent") != (err == nil) {
			t.Fatalf("wildcard permission accepted wrong mode: %+v %v", s, err)
		}
	}
}
