package modeldelivery

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #330 final-review P1: the formats projection must fail closed when it
// cannot read the session's direct conversions, and it must read only the
// declared conversion Offerings instead of walking every Task of a session.

func TestSchemaConversionRuleNamesSourceAndFormats330(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	rule := NewSchemaConversionRule(product.Application().ModelDelivery)

	convert, exists := product.Get("meshy/processing/convert")
	if !exists {
		t.Fatal("Meshy convert offering missing")
	}
	intent, ok := rule.ConversionIntent(convert, json.RawMessage(`{"source_task":"source-1"}`))
	if !ok || intent.SourceTaskID != "source-1" || intent.SourceAssetID != "" {
		t.Fatalf("task source intent = %#v %v", intent, ok)
	}
	if want := []string{"glb", "fbx", "obj", "usdz", "stl", "3mf"}; !reflect.DeepEqual(intent.Formats, want) {
		t.Fatalf("whole-run formats = %v, want %v", intent.Formats, want)
	}
	asset, ok := rule.ConversionIntent(convert, json.RawMessage(`{"source_asset":"asset-1"}`))
	if !ok || asset.SourceAssetID != "asset-1" || asset.SourceTaskID != "" {
		t.Fatalf("asset source intent = %#v %v", asset, ok)
	}

	tripo, exists := product.Get("tripo/tripo-v3.1/format-conversion")
	if !exists {
		t.Fatal("Tripo conversion offering missing")
	}
	perFormat, ok := rule.ConversionIntent(tripo, json.RawMessage(`{"source_task":"source-1","format":"FBX"}`))
	if !ok || perFormat.SourceTaskID != "source-1" || !reflect.DeepEqual(perFormat.Formats, []string{"fbx"}) {
		t.Fatalf("per-format intent = %#v %v", perFormat, ok)
	}

	retired, exists := product.Get("meshy/processing/remesh")
	if !exists {
		t.Fatal("retired conversion offering missing")
	}
	if intent, ok := rule.ConversionIntent(retired, json.RawMessage(`{"source_task":"source-1","convert_format_only":true}`)); !ok || len(intent.Formats) == 0 {
		t.Fatalf("retired conversion intent = %#v %v", intent, ok)
	}

	retexture, exists := product.Get("meshy/processing/retexture")
	if !exists {
		t.Fatal("retexture offering missing")
	}
	if _, ok := rule.ConversionIntent(retexture, json.RawMessage(`{"source_task":"source-1"}`)); ok {
		t.Fatal("a non-conversion Offering was reported as a conversion")
	}
}

func TestCoverageFailsClosedWhenDirectConversionsCannotBeListed330(t *testing.T) {
	f := newConversionFixture(t)
	source := f.meshyGlbOnly("meshy-task", "owner")
	readErr := errors.New("conversion listing unavailable")
	f.listErr = readErr

	if _, err := f.service.Coverage(context.Background(), generation.OwnTasks("owner"), source.ID); !errors.Is(err, readErr) {
		t.Fatalf("Coverage hid a failed conversion listing: %v", err)
	}
	if _, err := f.service.Complete(context.Background(), "owner", source.ID); !errors.Is(err, readErr) {
		t.Fatalf("Complete hid a failed conversion listing: %v", err)
	}
	if f.submitted != 0 || len(f.store.plans) != 0 {
		t.Fatalf("a failed listing started paid work: submitted=%d plans=%d", f.submitted, len(f.store.plans))
	}
}

func TestCoverageFailsClosedWhenAnActiveConversionCannotBeRead330(t *testing.T) {
	f := newConversionFixture(t)
	source := f.meshyGlbOnly("meshy-task", "owner")
	active := f.addConversionTask("active-conversion", "owner", source.SessionID, "meshy/processing/convert",
		`{"source_task":"meshy-task"}`, generation.TaskRunning, conversionAt(1))
	readErr := errors.New("conversion read unavailable")
	f.getErrs[active.ID] = readErr

	if _, err := f.service.Coverage(context.Background(), generation.OwnTasks("owner"), source.ID); !errors.Is(err, readErr) {
		t.Fatalf("Coverage hid an unreadable active conversion: %v", err)
	}
	if _, err := f.service.Complete(context.Background(), "owner", source.ID, "fbx"); !errors.Is(err, readErr) {
		t.Fatalf("Complete hid an unreadable active conversion: %v", err)
	}
	if f.submitted != 0 || len(f.store.plans) != 0 {
		t.Fatalf("an unreadable active conversion started paid work: submitted=%d plans=%d", f.submitted, len(f.store.plans))
	}
}

func TestCoverageIgnoresOnlyANotFoundConversionTask330(t *testing.T) {
	f := newConversionFixture(t)
	source := f.meshyGlbOnly("meshy-task", "owner")
	active := f.addConversionTask("gone-conversion", "owner", source.SessionID, "meshy/processing/convert",
		`{"source_task":"meshy-task"}`, generation.TaskRunning, conversionAt(1))
	f.getErrs[active.ID] = &generation.Error{Code: generation.ErrNotFound, Operation: "get"}

	if _, err := f.service.Coverage(context.Background(), generation.OwnTasks("owner"), source.ID); err != nil {
		t.Fatalf("NotFound conversion hid the projection: %v", err)
	}
}

func TestDirectConversionListingAsksOnlyForConversionOfferings330(t *testing.T) {
	f := newConversionFixture(t)
	source := f.meshyGlbOnly("meshy-task", "owner")
	f.addConversionTask("active-conversion", "owner", source.SessionID, "meshy/processing/convert",
		`{"source_task":"meshy-task"}`, generation.TaskRunning, conversionAt(1))
	f.addTask("unrelated", "owner", "meshy", generation.Retexture,
		assetSpec{role: "model", variant: "glb", mediaType: "model/gltf-binary"})

	if _, err := f.service.Coverage(context.Background(), generation.OwnTasks("owner"), source.ID); err != nil {
		t.Fatal(err)
	}
	wanted := map[generation.OfferingID]bool{}
	for _, id := range f.lastListQuery.OfferingIDs {
		wanted[id] = true
	}
	for _, id := range []generation.OfferingID{"meshy/processing/convert", "tripo/tripo-v3.1/format-conversion"} {
		if !wanted[id] {
			t.Fatalf("conversion listing omits %s: %v", id, f.lastListQuery.OfferingIDs)
		}
	}
	if wanted["meshy/processing/retexture"] {
		t.Fatalf("conversion listing reads an unrelated Offering: %v", f.lastListQuery.OfferingIDs)
	}
}
