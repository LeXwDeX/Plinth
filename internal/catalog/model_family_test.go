package catalog_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/catalog/catalogtest"
)

// #380 step 1: an Offering may declare the model family its step shape belongs
// to. The declaration is a model-selection fact only, and the Catalog checks
// what it can check without knowing the resolver: the identifier reads like a
// model ID, and a family that could substitute for nothing is a typo.
func TestModelFamilyDeclarations(t *testing.T) {
	defaults := catalogtest.DefaultCatalogBytes(t)
	tests := []struct {
		name   string
		change func(offerings []map[string]json.RawMessage)
		want   string // the load error, "" when the Catalog must load
	}{
		{
			name:   "the shipped families load",
			change: func([]map[string]json.RawMessage) {},
		},
		{
			name: "one member is a typo",
			change: func(offerings []map[string]json.RawMessage) {
				for _, offering := range offerings {
					if idOf(t, offering) != "meshy/meshy-7.1-multiview/image-to-3d" {
						continue
					}
					offering["model_family"] = json.RawMessage(`"meshy-7.1-alone"`)
				}
			},
			want: `model_family "meshy-7.1-alone" 只有 Offering "meshy/meshy-7.1-multiview/image-to-3d"`,
		},
		{
			name: "a family ID is lower-case letters, digits, hyphens and dots",
			change: func(offerings []map[string]json.RawMessage) {
				for _, offering := range offerings {
					if idOf(t, offering) == "tripo/tripo-p2/text-to-3d" || idOf(t, offering) == "tripo/tripo-p2/image-to-3d" {
						offering["model_family"] = json.RawMessage(`"Tripo P2"`)
					}
				}
			},
			want: "不是合法的模型族标识",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var document map[string]json.RawMessage
			if err := json.Unmarshal(defaults, &document); err != nil {
				t.Fatal(err)
			}
			var offerings []map[string]json.RawMessage
			if err := json.Unmarshal(document["offerings"], &offerings); err != nil {
				t.Fatal(err)
			}
			test.change(offerings)
			encoded, err := json.Marshal(offerings)
			if err != nil {
				t.Fatal(err)
			}
			document["offerings"] = encoded
			raw, err := json.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			_, err = catalog.Load(bytes.NewReader(raw))
			switch {
			case test.want == "" && err != nil:
				t.Fatalf("Load refused the Catalog: %v", err)
			case test.want != "" && (err == nil || !strings.Contains(err.Error(), test.want)):
				t.Fatalf("Load = %v, want an error mentioning %q", err, test.want)
			}
		})
	}
}

func idOf(t *testing.T, offering map[string]json.RawMessage) string {
	t.Helper()
	var id string
	if err := json.Unmarshal(offering["id"], &id); err != nil {
		t.Fatal(err)
	}
	return id
}
