package generationtool_test

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/approvalpolicy"
	inputassets "github.com/LeXwDeX/Plinth/internal/assets/input"
	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/chat/references"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// The gateway records Agent submissions without running any provider. Its
// PrepareInput uses the real Catalog normalization of the candidate declaration.
type splatGrantGateway struct {
	*grantGateway
	catalog   *catalog.Catalog
	lastInput json.RawMessage
}

func (g *splatGrantGateway) PrepareInput(ctx context.Context, cmd generation.PrepareInputCommand) (generation.PreparedInput, error) {
	g.lastInput = append(json.RawMessage(nil), cmd.Input...)
	_, normalized, err := g.catalog.Normalize(cmd.Capability, cmd.OfferingID, cmd.Input)
	if err != nil {
		return generation.PreparedInput{}, err
	}
	cmd.Input = normalized
	return g.grantGateway.PrepareInput(ctx, cmd)
}

func splatAgentCatalog(t *testing.T) *catalog.Catalog {
	t.Helper()
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func splatAgentHarness(t *testing.T, mode string) (*generationtool.Executor, chat.ToolCommand, *splatGrantGateway, *sqlite.Repository) {
	t.Helper()
	_, command, gateway, repo := policyHarness(t, sessionMode{mode: mode}, "create_gaussian_splat", "image-to-splat")
	c := splatAgentCatalog(t)
	o, _ := c.Get("tripo/image-to-splat")
	gateway.offerings = []generation.Offering{o}
	app := c.Application()
	// Build from the real Schema, including the new manual capability.
	var manual, known []string
	for _, cap := range app.Chat.ApprovalPolicy.ManualApprovalCapabilities {
		manual = append(manual, string(cap))
	}
	for _, cap := range app.Capabilities {
		known = append(known, string(cap.ID))
	}
	service := &executiongrant.Service{Grants: repo, Prepared: repo, Modes: sessionMode{mode: mode}, Policy: approvalpolicy.New(app.Chat.ApprovalPolicy.DefaultMode, app.Chat.ApprovalPolicy.Modes, manual, known)}
	wrapped := &splatGrantGateway{grantGateway: gateway, catalog: c}
	blobs, err := local.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	inputs, err := inputassets.New(repo, blobs)
	if err != nil {
		t.Fatal(err)
	}
	var raw bytes.Buffer
	if err := png.Encode(&raw, image.NewNRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	asset, err := inputs.UploadAsset(context.Background(), "owner-a", generation.InputAssetUpload{Body: &raw, MediaType: "image/png", AssetType: "image", MaxBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := references.New(references.Config{Inputs: inputs, Tasks: struct{ references.TaskGateway }{}, Legacy: struct{ references.LegacyStore }{}, InputAssetRoutes: []string{"/inputs/{assetID}"}, TaskAssetRoute: "/tasks/{taskID}/assets/{assetID}", GeneratedImageRoute: "/generated/{filename}", UploadMaxBytes: 1024, GeneratedReferenceMaxBytes: 4096, GeneratedReferenceTooLargeTemplate: "too large {size} {limit}", DefaultMediaType: "image/png", DefaultAssetType: "image", ReferenceFilename: "reference.png"})
	if err != nil {
		t.Fatal(err)
	}
	executor, err := generationtool.New(generationtool.Config{Gateway: wrapped, References: resolver, Operations: repo, Grants: service, Chat: app.Chat,
		StopRequested: func(context.Context, string, string, string) (bool, error) { return false, nil }})
	if err != nil {
		t.Fatal(err)
	}
	// The dedicated tool must not read or mutate the workspace's mesh choice.
	command.Options.AIModel = "existing-mesh-model"
	command.History = []chat.Message{{Role: app.Chat.Roles.User, ImageURLs: []string{"/inputs/" + string(asset.ID)}}}
	command.Call.Arguments = json.RawMessage(`{"model_seed":42}`)
	return executor, command, wrapped, repo
}

func TestSplatAgentScalarInputApprovalAndPublicReceipt(t *testing.T) {
	for _, mode := range []string{"manual", "auto"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			executor, command, gateway, repo := splatAgentHarness(t, mode)
			var observations []chat.ToolObservation
			result, err := executor.Execute(ctx, command, func(o chat.ToolObservation) error { observations = append(observations, o); return nil })
			if err != nil {
				t.Fatal(err)
			}
			op, err := repo.LoadAgentOperation(ctx, command.OwnerID, command.SessionID, command.AssistantMessageID, command.Call.ID)
			if err != nil {
				t.Fatal(err)
			}
			prepared, err := repo.GetPreparedInput(ctx, "owner-a", op.PreparedInputID)
			if err != nil {
				t.Fatal(err)
			}
			if prepared.OfferingID != "tripo/image-to-splat" || command.Options.AIModel != "existing-mesh-model" {
				t.Fatalf("dedicated selection changed workspace model: %+v", prepared)
			}
			var input map[string]any
			if err := json.Unmarshal(prepared.NormalizedInput, &input); err != nil {
				t.Fatal(err)
			}
			if input["image"] != strings.TrimPrefix(command.History[0].ImageURLs[0], "/inputs/") || input["model_seed"] != float64(42) || len(input) != 2 {
				t.Fatalf("frozen input=%s", prepared.NormalizedInput)
			}
			grant, err := repo.GetExecutionGrant(ctx, "owner-a", op.GrantRequestID)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "manual" {
				if gateway.submitCount != 0 || !result.NoSubmission || grant.Status != executiongrant.Pending || !op.Detached {
					t.Fatalf("manual result=%+v grant=%+v submits=%d", result, grant, gateway.submitCount)
				}
			} else {
				if gateway.submitCount != 1 || result.TaskID != "accepted-agent-task" || len(result.ImageURLs) != 0 || len(observations) == 0 || observations[0].ApprovalSource != "policy" {
					t.Fatalf("auto result=%+v observations=%+v submits=%d", result, observations, gateway.submitCount)
				}
				if strings.Contains(result.AgentContent, "model_url") || strings.Contains(result.AgentContent, "remote") {
					t.Fatalf("private receipt=%s", result.AgentContent)
				}
			}
			// A detached pending operation keeps its frozen image after restart.
			// The fake gateway does not persist Task/Grant binding; automatic
			// submission idempotency belongs to the real kernel integration tests.
			if mode == "manual" {
				command.History = nil
				if _, err := executor.Execute(ctx, command, nil); err != nil {
					t.Fatal(err)
				}
				if gateway.preparedCount != 1 || gateway.submitCount != 0 {
					t.Fatal("pending replay prepared/submitted again")
				}
			}
		})
	}
}

func TestSplatAgentRejectsUnavailableAndInvalidSourcesBeforeSubmission(t *testing.T) {
	for name, mutate := range map[string]func(*chat.ToolCommand, *splatGrantGateway){
		"disabled": func(_ *chat.ToolCommand, g *splatGrantGateway) { g.offerings[0].Enabled = false },
		"unconfigured": func(_ *chat.ToolCommand, g *splatGrantGateway) {
			g.unavailable = map[generation.OfferingID]bool{g.offerings[0].ID: true}
		},
		"ambiguous providers": func(_ *chat.ToolCommand, g *splatGrantGateway) {
			other := g.offerings[0]
			other.ID = "other/splat"
			other.ModelID = "other-splat"
			g.offerings = append(g.offerings, other)
		},
		"missing image": func(c *chat.ToolCommand, _ *splatGrantGateway) { c.History = nil },
		"unknown asset": func(c *chat.ToolCommand, _ *splatGrantGateway) {
			c.History[0].ImageURLs = []string{"/inputs/missing-asset"}
		},
		"multiple images": func(c *chat.ToolCommand, _ *splatGrantGateway) {
			c.History[0].ImageURLs = append(c.History[0].ImageURLs, "/another")
		},
		"another session handle": func(c *chat.ToolCommand, _ *splatGrantGateway) {
			c.Call.Arguments = json.RawMessage(`{"reference_handles":["@img9"]}`)
		},
		"mesh handle": func(c *chat.ToolCommand, _ *splatGrantGateway) {
			c.History[0].Attachments = []chat.Attachment{{Handle: "@model1", Kind: chat.AttachmentKindModel, ImageSlot: 1}}
			c.Call.Arguments = json.RawMessage(`{"reference_handles":["@model1"]}`)
		},
		"fractional seed": func(c *chat.ToolCommand, _ *splatGrantGateway) {
			c.Call.Arguments = json.RawMessage(`{"model_seed":1.5}`)
		},
		"wrong owner operation":   func(c *chat.ToolCommand, _ *splatGrantGateway) { c.OwnerID = "owner-b" },
		"wrong session operation": func(c *chat.ToolCommand, _ *splatGrantGateway) { c.SessionID = "session-b" },
	} {
		t.Run(name, func(t *testing.T) {
			executor, command, gateway, _ := splatAgentHarness(t, "auto")
			mutate(&command, gateway)
			result, err := executor.Execute(context.Background(), command, nil)
			if gateway.submitCount != 0 || (err == nil && !result.NoSubmission) {
				t.Fatalf("invalid call submitted: %+v err=%v submits=%d", result, err, gateway.submitCount)
			}
		})
	}
}
