package hyper3d_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/modelinspect"
	"github.com/LeXwDeX/Plinth/internal/providers/hyper3d"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	hyper3dapiv2 "github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/hyper3d/api-v2"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/testkit"
)

// #303: the Hyper3D fake delivers the model its request implies (the face count
// and topology fix the triangles, the texture mode fixes the texture edges), so
// what the product reads from a delivered Rodin model (the purpose budgets of
// inspect_model and the plan checkpoints) is checked on the fake. These tests
// drive the real Adapter with the shipped Offerings and the shipped game
// purposes' own overrides, and read the delivered bytes the way Plinth does.

// rodinDelivery is what one Rodin step delivered through the Adapter.
type rodinDelivery struct {
	report   modelinspect.Report
	consumed float64
}

// textureEdge is the longest edge of the textures the model's slots carry.
func (d rodinDelivery) textureEdge() int {
	edge := 0
	for _, material := range d.report.Materials {
		for _, slot := range material.Slots {
			if slot.Present {
				edge = max(edge, slot.Width, slot.Height)
			}
		}
	}
	return edge
}

// routedRodin is the real Adapter against the Hyper3D fake, with the hosts the
// shipped Offerings allow (the API host and the file host) routed to the fake and
// every other host refused, so the model is fetched the way a real one is.
type routedRodin struct {
	t       *testing.T
	product *catalog.Catalog
	port    *hyper3d.Adapter
}

func newRoutedRodin(t *testing.T) *routedRodin {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	fixture := testkit.New(t, fakeupstream.Config{ArtifactOrigin: "https://" + rodinFileHost}, hyper3dapiv2.Version())
	for _, connection := range product.Application().Runtime.ProviderConnections {
		if connection.ID != "hyper3d" {
			continue
		}
		port, err := hyper3d.New(hyper3d.Config{
			BaseURL: "https://" + rodinAPIHost + hyper3dapiv2.Prefix, APIKey: "synthetic-key",
			AuthHeader: connection.AuthHeader, AuthPrefix: connection.AuthPrefix,
			StatusMapping: connection.StatusMapping, ErrorMapping: connection.ErrorMapping,
			HTTPClient:  &http.Client{Transport: fixture.RouteHosts(rodinAPIHost, rodinFileHost)},
			InputAssets: imageInputs{},
		})
		if err != nil {
			t.Fatal(err)
		}
		return &routedRodin{t: t, product: product, port: port}
	}
	t.Fatal("no hyper3d connection")
	return nil
}

// deliver runs one Rodin step to completion (Start, every Observe, the model
// claim's bytes) and reads the model back.
func (r *routedRodin) deliver(id string, extra map[string]any) rodinDelivery {
	r.t.Helper()
	offering, ok := r.product.Get(generation.OfferingID(id))
	if !ok {
		r.t.Fatalf("missing Offering %s", id)
	}
	_, frozen, err := r.product.Normalize(offering.Capability, offering.ID, rodinInputJSON(r.t, id, extra))
	if err != nil {
		r.t.Fatalf("%s: the Schema refused %v: %v", id, extra, err)
	}
	request := generation.StartRequest{
		OwnerID: "owner", TaskID: "task", AttemptID: "attempt", SubmissionKey: "once", Offering: offering, Input: frozen,
	}
	if offering.Capability == generation.ImageTo3D {
		request.InputAssets = map[string][]generation.InputAsset{"reference_assets": {
			{ID: "view-0", OwnerID: "owner", MediaType: "image/png", OriginalFilename: "view-0.png"},
		}}
	}
	ctx := context.Background()
	observation, err := r.port.Start(ctx, request)
	if err != nil || observation.Job == nil {
		r.t.Fatalf("%s: start = %+v, %v", id, observation, err)
	}
	var receipt struct {
		ConsumedCredit float64 `json:"consumed_credit"`
	}
	if err := json.Unmarshal(observation.Opaque, &receipt); err != nil {
		r.t.Fatalf("%s: checkpoint %s: %v", id, observation.Opaque, err)
	}
	job := *observation.Job
	for polls := 0; observation.Phase != generation.UpstreamCompleted; polls++ {
		if polls > 40 {
			r.t.Fatalf("%s: the fake never completed; last phase %s", id, observation.Phase)
		}
		observation, err = r.port.Observe(ctx, generation.ObserveRequest{
			TaskID: "task", AttemptID: "attempt", Offering: offering, Input: frozen, Job: job, Opaque: observation.Opaque,
		})
		if err != nil {
			r.t.Fatalf("%s: observe: %v", id, err)
		}
	}
	for _, claim := range observation.Artifacts {
		if claim.Role != "model" {
			continue
		}
		body, _, err := r.port.OpenAsset(ctx, generation.OpenAssetRequest{Offering: offering, Claim: claim})
		if err != nil {
			r.t.Fatalf("%s: open the model: %v", id, err)
		}
		data, readErr := io.ReadAll(body)
		_ = body.Close()
		if readErr != nil || len(data) == 0 {
			r.t.Fatalf("%s: the model has %d bytes: %v", id, len(data), readErr)
		}
		model, err := modelinspect.Parse(bytes.NewReader(data), int64(len(data)), modelinspect.DefaultLimits())
		if err != nil {
			r.t.Fatalf("%s: the delivered model cannot be read: %v", id, err)
		}
		return rodinDelivery{report: modelinspect.Inspect(model, modelinspect.DefaultInspectOptions()), consumed: receipt.ConsumedCredit}
	}
	r.t.Fatalf("%s: no model was claimed: %+v", id, observation.Artifacts)
	return rodinDelivery{}
}

// The shipped game purposes pick Quad face counts and the 2K texture so that the
// Rodin model fits the budget of every platform. The fake delivers what those
// numbers imply (2.18 triangles per Quad face, three 2048 textures), so the
// delivered model is inside the budget the purpose resolves to, measured on the
// bytes and not on the Catalog's arithmetic. Every platform of the Catalog is
// covered, and a session without a platform.
func TestRodinGamePurposeDeliveriesFitTheirBudgetsOnTheFake(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	usage := product.Application().Chat.Model3DUsage
	platforms := []string{""}
	for _, field := range usage.Brief.Fields {
		if field.Name == usage.PlatformField {
			for _, option := range field.Options {
				platforms = append(platforms, option.ID)
			}
		}
	}
	if len(platforms) < 3 {
		t.Fatalf("the Catalog declares platforms %v", platforms)
	}
	f := newRoutedRodin(t)
	delivered := map[string]rodinDelivery{} // by Offering and overrides: equal requests deliver equal models
	for _, profile := range usage.Profiles {
		if profile.ID != "game_character" && profile.ID != "game_prop" {
			continue
		}
		for _, platform := range platforms {
			resolved, _ := usage.ForPlatform(profile, platform)
			for _, id := range rodinOfferings {
				extra := map[string]any{}
				for name, raw := range resolved.ParameterOverrides[id] {
					var value any
					if err := json.Unmarshal(raw, &value); err != nil {
						t.Fatal(err)
					}
					extra[name] = value
				}
				if len(extra) == 0 {
					t.Fatalf("%s on %q has no override for %s", profile.ID, platform, id)
				}
				encoded, err := json.Marshal(extra)
				if err != nil {
					t.Fatal(err)
				}
				key := id + " " + string(encoded)
				delivery, done := delivered[key]
				if !done {
					delivery = f.deliver(id, extra)
					delivered[key] = delivery
				}
				budget := resolved.Budget
				label := profile.ID + " on " + platform + " (" + key + ")"
				minimum := int64(0)
				if budget.MinTriangles != nil {
					minimum = int64(*budget.MinTriangles)
				}
				if delivery.report.Triangles < minimum || delivery.report.Triangles > int64(budget.MaxTriangles) {
					t.Errorf("%s delivered %d triangles outside the %d-%d budget", label, delivery.report.Triangles, minimum, budget.MaxTriangles)
				}
				if budget.MaxTextureSize == nil || delivery.textureEdge() == 0 || delivery.textureEdge() > *budget.MaxTextureSize {
					t.Errorf("%s delivered a %d texture against the budget %v", label, delivery.textureEdge(), budget.MaxTextureSize)
				}
				if budget.RequireUV && !delivery.report.UV.AllHaveUV {
					t.Errorf("%s delivered a model without UVs", label)
				}
				if delivery.report.Triangles == 0 || delivery.report.Meshes != 1 || len(delivery.report.Skins) != 0 {
					t.Errorf("%s delivered %d triangles in %d meshes with %d skins", label, delivery.report.Triangles, delivery.report.Meshes, len(delivery.report.Skins))
				}
			}
		}
	}
	// Equal requests are run once; every purpose and Offering still ran at least once.
	if len(delivered) < 4 {
		t.Errorf("only %d distinct requests ran, want at least the character and the prop on both Offerings (4)", len(delivered))
	}
}

// The display request of the shipped Offering, which is what a user who names
// Rodin without a purpose gets, delivers the 6144 base color of texture_mode
// extreme-high and the fixture's ceiling of triangles. That is what makes it fail
// the game purposes' texture budget, and why they override texture_mode: the
// override is what the previous test shows fitting.
func TestRodinDisplayRequestDeliversASixKTextureThatTheGameBudgetsRefuse(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	usage := product.Application().Chat.Model3DUsage
	f := newRoutedRodin(t)
	for _, id := range rodinOfferings {
		delivery := f.deliver(id, nil)
		if delivery.consumed != 3 {
			t.Errorf("%s: the display request was charged %v, want the Catalog's 3", id, delivery.consumed)
		}
		if delivery.report.Triangles != fakeupstream.MaxSizedModelTriangles {
			t.Errorf("%s: %d triangles, want the fixture's ceiling %d (the request targets 2,000,000 faces)", id, delivery.report.Triangles, fakeupstream.MaxSizedModelTriangles)
		}
		if delivery.textureEdge() != 6144 {
			t.Errorf("%s: texture edge %d, want the 6K base color of extreme-high", id, delivery.textureEdge())
		}
		for _, profile := range usage.Profiles {
			if profile.ID != "game_character" && profile.ID != "game_prop" {
				continue
			}
			resolved, _ := usage.ForPlatform(profile, "")
			if resolved.Budget.MaxTextureSize == nil || delivery.textureEdge() <= *resolved.Budget.MaxTextureSize {
				t.Errorf("%s: a %d texture is inside the %s budget %v; the game purposes would not need their texture override",
					id, delivery.textureEdge(), profile.ID, resolved.Budget.MaxTextureSize)
			}
		}
	}
}
