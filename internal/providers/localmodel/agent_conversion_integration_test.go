package localmodel_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/approvalpolicy"
	inputassets "github.com/LeXwDeX/Plinth/internal/assets/input"
	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/assets/modelconvert/modelconverttest"
	"github.com/LeXwDeX/Plinth/internal/assets/modelinput/modelinputtest"
	"github.com/LeXwDeX/Plinth/internal/assets/modelsource"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers"
	"github.com/LeXwDeX/Plinth/internal/providers/localmodel"
)

// agentKernel is the real kernel; only the Offering listing comes from the
// default Product Schema, so the Agent sees every format-conversion Offering
// in the order production lists them.
type agentKernel struct {
	*generation.Module
	product *catalog.Catalog
}

func (k agentKernel) Offerings(capability generation.Capability) []generation.Offering {
	return k.product.List(capability)
}

type autoApproval struct{}

type noReferences struct{}

func (noReferences) Resolve(context.Context, generation.OwnerID, []string) ([]generation.AssetID, error) {
	return nil, nil
}

func (autoApproval) SessionApprovalMode(context.Context, string, string) (string, error) {
	return "auto", nil
}

// runAgentConversion stores raw as the model of a successful Task of the chat
// session, asks convert_model_format for glTF through the real executor,
// kernel, grants, CAS and SQLite, and runs the accepted Task to its end.
func runAgentConversion(t *testing.T, raw []byte) (generation.Task, *local.Store) {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	repo, err := sqlite.Open(filepath.Join(root, "generation.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	blobs, err := local.New(filepath.Join(root, "cas"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = blobs.Close() })
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	app := product.Application()
	upload := uploadPolicyFrom(t, product)
	profile := app.Runtime.InputAssets.ModelProfile
	native, err := inputassets.NewWithConfig(repo, blobs, inputassets.Config{UploadValidator: modelsource.UploadValidator(upload, profile.AssetType, profile.Extensions, profile.MediaTypes)})
	if err != nil {
		t.Fatal(err)
	}
	models, err := modelsource.New(native, upload)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := repo.CreateSession(ctx, chat.Session{ID: "local-session", OwnerID: "owner", OwnerName: "Owner", Title: "agent conversion", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.StartTurn(ctx, "owner",
		chat.Message{ID: "user-a", SessionID: "local-session", Role: "user", Content: "convert", CreatedAt: now},
		chat.Message{ID: "turn-a", SessionID: "local-session", Role: "assistant", Content: "pending", CreatedAt: now},
		chat.Event{SessionID: "local-session", Data: map[string]any{"type": "message.created"}, CreatedAt: now},
		chat.Stream{SessionID: "local-session", Status: "active"},
		chat.Turn{OwnerID: "owner", SessionID: "local-session", AssistantMessageID: "turn-a", State: "running", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	generated, _ := product.Get(generatedOfferingID)
	static, _ := product.Get("local/static-glb/to-gltf")
	one := 1
	sourceOffering := generation.Offering{ID: "legacy/generated-model", Revision: "legacy-v1", Enabled: true, ProviderID: "legacy", AdapterKey: "legacy", Capability: generation.FormatConversion, Domain: generation.DomainProcessing, Outputs: []generation.AssetRequirement{{Role: "model", Variant: "glb", MediaTypes: []string{"model/gltf-binary"}, MaxBytes: 128 << 20, Minimum: &one}}}
	fixture := fixtureCatalog{offerings: map[generation.OfferingID]generation.Offering{generated.ID: generated, static.ID: static, sourceOffering.ID: sourceOffering}}
	port, err := localmodel.New(localmodel.Config{Inputs: models, Assets: blobs})
	if err != nil {
		t.Fatal(err)
	}
	registry := providers.NewRegistry()
	if err := registry.Register(generated.AdapterKey, port); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register("legacy", historicalModelPort{body: raw}); err != nil {
		t.Fatal(err)
	}
	module, err := generation.New(generation.Dependencies{Repository: repo, Catalog: fixture, Providers: registry, Assets: blobs, Inputs: models})
	if err != nil {
		t.Fatal(err)
	}
	source := runLocalTask(t, module, submitApprovedLocal(t, module, repo, sourceOffering, json.RawMessage(`{}`), "generated-source").TaskID)

	var known, manual []string
	for _, capability := range app.Capabilities {
		known = append(known, string(capability.ID))
	}
	for _, capability := range app.Chat.ApprovalPolicy.ManualApprovalCapabilities {
		manual = append(manual, string(capability))
	}
	policy := approvalpolicy.New(app.Chat.ApprovalPolicy.DefaultMode, app.Chat.ApprovalPolicy.Modes, manual, known)
	executor, err := generationtool.New(generationtool.Config{
		Gateway: agentKernel{Module: module, product: product}, References: noReferences{}, Operations: repo,
		Grants: &executiongrant.Service{Grants: repo, Prepared: repo, Policy: policy, Modes: autoApproval{}},
		Chat:   app.Chat, ModelDelivery: app.ModelDelivery, TaskStates: app.TaskStates,
		StopRequested: repo.TurnStopRequested,
	})
	if err != nil {
		t.Fatal(err)
	}
	var tool catalog.ChatToolSchema
	for _, candidate := range app.Chat.Tools {
		if candidate.Name == "convert_model_format" {
			tool = candidate
		}
	}
	command := chat.ToolCommand{OwnerID: "owner", SessionID: "local-session", AssistantMessageID: "turn-a",
		Call: agents.ToolCall{ID: "call-1", Name: tool.Name, Arguments: json.RawMessage(fmt.Sprintf(`{"source_task_id":%q,"format":"gltf"}`, source.ID))},
		Tool: tool}
	result, err := executor.Execute(ctx, command, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.NoSubmission || result.TaskID == "" {
		t.Fatalf("Agent conversion was not submitted: %+v", result)
	}
	return runLocalTask(t, module, generation.TaskID(result.TaskID)), blobs
}

// #264 follow-up: in chat, a trusted platform-generated GLB is converted to
// glTF by the same Schema-ordered route as the format coverage, under the same
// kernel trust and profile checks.
func TestAgentConvertsGeneratedGLBToGLTFLikeTheFormatCoverage(t *testing.T) {
	cases := map[string]struct {
		raw []byte
		min int64
	}{
		// Above the 10 MiB upload profile.
		"larger than the upload profile": {raw: modelinputtest.Large(modelinputtest.LargeOptions{Grid: 722, Images: []modelinputtest.LargeImage{{Width: 8192, Height: 8192, RGBA: true}, {Width: 512, Height: 512, Noise: true}}}), min: 10<<20 + 1},
		// Below 10 MiB but with an 8192² texture the upload profile refuses.
		"small but beyond the upload profile": {raw: modelinputtest.Large(modelinputtest.LargeOptions{Grid: 64, Images: []modelinputtest.LargeImage{{Width: 8192, Height: 8192}}})},
		// Within every upload limit: still the package route, as in the coverage.
		"within the upload profile": {raw: modelinputtest.Triangle(modelinputtest.Options{Indexed: true, Textured: true})},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			if int64(len(test.raw)) < test.min {
				t.Fatalf("fixture has %d bytes; want at least %d", len(test.raw), test.min)
			}
			task, blobs := runAgentConversion(t, test.raw)
			if task.Offering.ID != generatedOfferingID || len(task.Assets) != 1 {
				t.Fatalf("Agent conversion ran %s with assets %#v", task.Offering.ID, task.Assets)
			}
			asset := task.Assets[0]
			if asset.Role != "model" || asset.Variant != "gltf" || asset.MediaType != "application/zip" {
				t.Fatalf("package asset = %#v", asset)
			}
			reader, err := blobs.Open(context.Background(), asset)
			if err != nil {
				t.Fatal(err)
			}
			archive, err := io.ReadAll(reader)
			_ = reader.Close()
			if err != nil {
				t.Fatal(err)
			}
			if err := modelconverttest.VerifyPackage(archive, test.raw); err != nil {
				t.Fatal(err)
			}
		})
	}
}
