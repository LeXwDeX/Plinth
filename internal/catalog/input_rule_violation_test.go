package catalog_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// Meshy retexture takes a text prompt or a style picture: the official API
// uses only image_style_url when both are sent, so the Offering's input rule
// gives the two one slot. A refusal keeps its diagnostic Detail for the logs
// and carries the broken rule as a structured cause, so the Agent tools can
// name it instead of a generic text.
func TestMeshyRetextureRefusesPromptAndStylePictureWithTheRule(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = taskCatalog.Normalize(generation.Retexture, "meshy/processing/retexture",
		json.RawMessage(`{"source_task":"task-1","prompt":"oak wood","style_asset":"asset-1"}`))
	var domainErr *generation.Error
	if !errors.As(err, &domainErr) || domainErr.Code != generation.ErrInvalidInput || domainErr.UserFacing {
		t.Fatalf("error = %#v", err)
	}
	if !strings.Contains(domainErr.Detail, "[prompt style_asset]") {
		t.Fatalf("diagnostic detail = %q", domainErr.Detail)
	}
	var violation *schema.InputRuleViolation
	if !errors.As(err, &violation) {
		t.Fatalf("no structured rule in %#v", err)
	}
	if violation.OfferingID != "meshy/processing/retexture" || violation.Capability != generation.Retexture ||
		!slices.Equal(violation.Parameters, []string{"prompt", "style_asset"}) ||
		!slices.Equal(violation.Present, []string{"prompt", "style_asset"}) ||
		violation.Minimum != 1 || violation.Maximum != 1 || violation.Message == "" {
		t.Fatalf("violation = %+v", violation)
	}
	if strings.Contains(violation.Error()+violation.Message, "oak wood") {
		t.Fatalf("violation echoed an input value: %+v", violation)
	}

	_, _, err = taskCatalog.Normalize(generation.Retexture, "meshy/processing/retexture",
		json.RawMessage(`{"source_task":"task-1"}`))
	if !errors.As(err, &violation) || len(violation.Present) != 0 || violation.Minimum != 1 {
		t.Fatalf("missing style input = %#v", err)
	}
	for _, input := range []string{
		`{"source_task":"task-1","prompt":"oak wood"}`,
		`{"source_task":"task-1","style_asset":"asset-1"}`,
	} {
		if _, _, err := taskCatalog.Normalize(generation.Retexture, "meshy/processing/retexture", json.RawMessage(input)); err != nil {
			t.Fatalf("%s refused: %v", input, err)
		}
	}
}

// A count input rule must name declared parameters.
func TestLoadRejectsInputRuleWithUnknownParameter(t *testing.T) {
	document := testProductDocument(`[
			{
				"id":"one","revision":"1","provider_id":"p","provider_name":"P","provider_short_label":"P",
				"model_id":"m","model_name":"M","capability":"text-to-image",
				"domain":"image","display_order":0,"capability_order":0,
				"adapter_key":"a","enabled":true,"timeout_seconds":1,"poll_interval_seconds":1,
				"retry":{"max_attempts":1},"materialize_attempts":1,
				"parameters":[{"name":"source_a","label":"来源 A","type":"text"}],
				"input_rules":[{"parameters":["source_a","ghost"],"minimum":1,"maximum":1}],
				"outputs":[{"role":"image","minimum":1}]
			}
		]`)
	_, err := catalog.Load(bytes.NewReader(document))
	if err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("load error = %v", err)
	}
}
