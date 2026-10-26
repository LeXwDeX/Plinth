package catalog

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCanvasCreatedFieldMustBeConfiguredAndUnique(t *testing.T) {
	var doc document
	if err := json.Unmarshal(defaultCatalog, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Application == nil {
		t.Fatal("default catalog is missing its application schema")
	}

	tests := []struct {
		name    string
		value   string
		wantErr string
	}{
		{name: "configured", value: "created"},
		{name: "empty", wantErr: "api.fields.created 不能为空"},
		{name: "duplicate revision", value: "revision", wantErr: "重复"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			schema := doc.Application.Canvas
			schema.API.Fields.Created = test.value
			err := validateCanvas(schema, doc.Application.Runtime, doc.Application.Content)
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("validateCanvas() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("validateCanvas() error = %v, want substring %q", err, test.wantErr)
			}
		})
	}
}
