package generationtool_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite/sqlitetest"
	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/approvalpolicy"
	inputassets "github.com/LeXwDeX/Plinth/internal/assets/input"
	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers"
	"github.com/LeXwDeX/Plinth/internal/providers/mock"
)

// Use the shipped declaration so enabled discovery and the frozen contract stay aligned.
func qualifiedMultiviewCatalog(t *testing.T) *catalog.Catalog {
	t.Helper()
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestMultiviewRealPreparedInputFreezesOrderBytesAndGrantAcrossRestart(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	dbpath := filepath.Join(root, "state.sqlite")
	repo, err := sqlitetest.Open(dbpath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = repo.Close() }()
	blobs, err := local.New(filepath.Join(root, "assets"))
	if err != nil {
		t.Fatal(err)
	}
	inputs, err := inputassets.New(repo, blobs)
	if err != nil {
		t.Fatal(err)
	}
	c := qualifiedMultiviewCatalog(t)
	registry := providers.NewRegistry()
	if err = registry.Register("meshy.v2", mock.New()); err != nil {
		t.Fatal(err)
	}
	moduleFor := func(version string) *generation.Module {
		m, e := generation.New(generation.Dependencies{Repository: repo, Catalog: c, Inputs: inputs, Assets: blobs, Providers: registry, SchemaVersion: version})
		if e != nil {
			t.Fatal(e)
		}
		return m
	}
	m := moduleFor("multiview-fixture-v1")
	now := time.Now().UTC()
	if _, err = repo.CreateSession(ctx, chat.Session{ID: "session", OwnerID: "owner", OwnerName: "Fixture", Title: "Views", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	ids := []generation.AssetID{}
	original := [][]byte{}
	for i := 0; i < 4; i++ {
		im := image.NewNRGBA(image.Rect(0, 0, 2, 2))
		im.SetNRGBA(0, 0, color.NRGBA{R: uint8(40 + i), G: 50, B: 90, A: 255})
		var data bytes.Buffer
		if err = png.Encode(&data, im); err != nil {
			t.Fatal(err)
		}
		raw := append([]byte(nil), data.Bytes()...)
		original = append(original, raw)
		a, e := inputs.UploadAsset(ctx, "owner", generation.InputAssetUpload{Body: bytes.NewReader(raw), MediaType: "image/png", AssetType: "image", MaxBytes: 20 << 20})
		if e != nil {
			t.Fatal(e)
		}
		ids = append(ids, a.ID)
	}
	rawFor := func(values []generation.AssetID) json.RawMessage {
		raw, e := json.Marshal(map[string]any{"reference_assets": values, "authorize_extra_credits": true})
		if e != nil {
			t.Fatal(e)
		}
		return raw
	}
	command := generation.PrepareInputCommand{OwnerID: "owner", SessionID: "session", Capability: generation.ImageTo3D, OfferingID: "meshy/meshy-7.1-multiview/image-to-3d", Input: rawFor(ids)}
	// Invalid calls must not create a Task or a Grant; owner identity is verified by the real input service.
	foreign, err := inputs.UploadAsset(ctx, "foreign", generation.InputAssetUpload{Body: bytes.NewReader(original[0]), MediaType: "image/png", AssetType: "image", MaxBytes: 20 << 20})
	if err != nil {
		t.Fatal(err)
	}
	for _, values := range [][]generation.AssetID{append(append([]generation.AssetID(nil), ids...), ids[0]), {foreign.ID}, {}} {
		bad := command
		bad.Input = rawFor(values)
		if _, e := m.PrepareInput(ctx, bad); e == nil {
			t.Fatalf("unsafe prepare accepted %v", values)
		}
	}
	readDB, e := sql.Open("sqlite", dbpath)
	if e != nil {
		t.Fatal(e)
	}
	defer readDB.Close()
	for _, table := range []string{"generation_tasks", "execution_grants"} {
		var count int
		if e := readDB.QueryRow("SELECT count(*) FROM " + table).Scan(&count); e != nil || count != 0 {
			t.Fatalf("invalid preparation wrote %s count=%d err=%v", table, count, e)
		}
	}

	var draftInput map[string]any
	if err := json.Unmarshal(command.Input, &draftInput); err != nil {
		t.Fatal(err)
	}
	draftInput["geometry_resolution"] = "standard"
	command.Input, _ = json.Marshal(draftInput)
	if _, err := m.PrepareInput(ctx, command); !generation.IsCode(err, generation.ErrInvalidInput) {
		t.Fatalf("unexplained multiview downgrade accepted: %v", err)
	}
	command.ParameterReasons = map[string]string{"geometry_resolution": "先检查多视角草稿"}
	prepared, err := m.PrepareInput(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.NativeQuote == nil || prepared.NativeQuote.TotalAmount != "35" || len(prepared.ParameterAdjustments) != 1 || prepared.ParameterAdjustments[0].Reason != "先检查多视角草稿" {
		t.Fatalf("quote and reason not frozen: %+v", prepared)
	}
	if len(prepared.Assets) != 4 {
		t.Fatalf("manifest=%+v", prepared.Assets)
	}
	for i, a := range prepared.Assets {
		if a.Index != i || a.ID != ids[i] || a.Size != int64(len(original[i])) {
			t.Fatalf("order changed at %d: %+v", i, a)
		}
	}
	service := executiongrant.Service{Grants: repo, Prepared: repo}
	principal := executiongrant.Principal{OwnerID: "owner", ActorID: "fixture-human", Source: executiongrant.HumanBrowser}
	g, err := service.Prepare(ctx, executiongrant.PrepareCommand{Principal: principal, SessionID: "session", OfferingID: string(prepared.OfferingID), OfferingRevision: prepared.OfferingRevision, PreparedInputID: string(prepared.ID), PreparedInputHash: prepared.InputHash, IdempotencyKey: "original", OperationID: "original-op"})
	if err != nil {
		t.Fatal(err)
	}
	if g.Status != executiongrant.Approved {
		g, err = service.Decide(ctx, principal, g.RequestID, executiongrant.Approve)
		if err != nil {
			t.Fatal(err)
		}
	}
	submit := generation.SubmitPreparedCommand{OwnerID: "owner", SessionID: "session", PreparedInputID: prepared.ID, IdempotencyKey: "original", OperationID: "original-op", AuthorizationRef: g.RequestID}
	if _, err = moduleFor("changed-schema").SubmitPrepared(ctx, submit); !generation.IsCode(err, generation.ErrConflict) {
		t.Fatalf("old Grant bypassed Schema change: %v", err)
	}
	if err = repo.Close(); err != nil {
		t.Fatal(err)
	}
	repo, err = sqlite.Open(dbpath)
	if err != nil {
		t.Fatal(err)
	}
	inputs, err = inputassets.New(repo, blobs)
	if err != nil {
		t.Fatal(err)
	}
	m = moduleFor("multiview-fixture-v1")
	restored, err := m.GetPreparedInput(ctx, "owner", prepared.ID)
	if err != nil || !reflect.DeepEqual(restored.Assets, prepared.Assets) || !bytes.Equal(restored.NormalizedInput, prepared.NormalizedInput) || restored.InputHash != prepared.InputHash || !reflect.DeepEqual(restored.NativeQuote, prepared.NativeQuote) || !reflect.DeepEqual(restored.ParameterAdjustments, prepared.ParameterAdjustments) {
		t.Fatalf("restart changed frozen order: %+v err=%v", restored, err)
	}
	accepted, err := m.SubmitPrepared(ctx, submit)
	if err != nil {
		t.Fatal(err)
	}
	task, err := m.Get(ctx, "owner", accepted.TaskID)
	if err != nil || task.Offering.ReferenceSelectionPolicy != "preserve" || !bytes.Equal(task.Input, prepared.NormalizedInput) || !reflect.DeepEqual(task.NativeQuote, prepared.NativeQuote) {
		t.Fatalf("admitted contract drifted %+v err=%v", task, err)
	}
	// Reordered input has a different hash; the existing approved Grant cannot authorize it.
	reversed := append([]generation.AssetID(nil), ids...)
	reversed[0], reversed[3] = reversed[3], reversed[0]
	draftInput["reference_assets"] = reversed
	command.Input, _ = json.Marshal(draftInput)
	other, err := m.PrepareInput(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	if other.InputHash == prepared.InputHash {
		t.Fatal("order not bound to input hash")
	}
	changed := submit
	changed.PreparedInputID = other.ID
	changed.IdempotencyKey = "changed"
	if _, err = m.SubmitPrepared(ctx, changed); err == nil {
		t.Fatal("original Grant authorized reordered views")
	}
	for i, id := range ids {
		a, body, e := inputs.OpenOwned(ctx, "owner", id)
		if e != nil {
			t.Fatal(e)
		}
		got, e := io.ReadAll(body)
		_ = body.Close()
		if e != nil || !bytes.Equal(got, original[i]) || a.SHA256 != prepared.Assets[i].SHA256 {
			t.Fatal("owned source bytes changed")
		}
	}
}

type multiviewReferenceFixture []generation.AssetID

func (ids multiviewReferenceFixture) Resolve(context.Context, generation.OwnerID, []string) ([]generation.AssetID, error) {
	return ids, nil
}

func TestMultiviewAgentDefaultQuoteUsesManualOrAutomaticGrant(t *testing.T) {
	for _, mode := range []string{"manual", "auto"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			repo, err := sqlitetest.Open(filepath.Join(t.TempDir(), "agent.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer repo.Close()
			blobs, err := local.New(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			inputs, err := inputassets.New(repo, blobs)
			if err != nil {
				t.Fatal(err)
			}
			c := qualifiedMultiviewCatalog(t)
			app := c.Application()
			registry := providers.NewRegistry()
			if err := registry.Register("meshy.v2", mock.New()); err != nil {
				t.Fatal(err)
			}
			m, err := generation.New(generation.Dependencies{Repository: repo, Catalog: c, Providers: registry, Inputs: inputs, Assets: blobs})
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			if _, err := repo.CreateSession(ctx, chat.Session{ID: "session", OwnerID: "owner", OwnerName: "Owner", Title: "Views", CreatedAt: now, UpdatedAt: now}); err != nil {
				t.Fatal(err)
			}
			if _, _, err := repo.StartTurn(ctx, "owner", chat.Message{ID: "user", SessionID: "session", Role: "user", Content: "views", CreatedAt: now}, chat.Message{ID: "turn", SessionID: "session", Role: "assistant", Content: "pending", CreatedAt: now}, chat.Event{SessionID: "session", Data: map[string]any{"type": "message.created"}, CreatedAt: now}, chat.Stream{SessionID: "session", Status: "active"}, chat.Turn{OwnerID: "owner", SessionID: "session", AssistantMessageID: "turn", State: "running", CreatedAt: now, UpdatedAt: now}); err != nil {
				t.Fatal(err)
			}
			var imageBytes bytes.Buffer
			if err := png.Encode(&imageBytes, image.NewNRGBA(image.Rect(0, 0, 2, 2))); err != nil {
				t.Fatal(err)
			}
			asset, err := inputs.UploadAsset(ctx, "owner", generation.InputAssetUpload{Body: bytes.NewReader(imageBytes.Bytes()), MediaType: "image/png", AssetType: "image", MaxBytes: 20 << 20})
			if err != nil {
				t.Fatal(err)
			}
			var manual, known []string
			for _, v := range app.Chat.ApprovalPolicy.ManualApprovalCapabilities {
				manual = append(manual, string(v))
			}
			for _, v := range app.Capabilities {
				known = append(known, string(v.ID))
			}
			grants := &executiongrant.Service{Grants: repo, Prepared: repo, Policy: approvalpolicy.New(app.Chat.ApprovalPolicy.DefaultMode, app.Chat.ApprovalPolicy.Modes, manual, known), Modes: sessionMode{mode: mode}}
			executor, err := generationtool.New(generationtool.Config{Gateway: m, References: multiviewReferenceFixture{asset.ID}, Operations: repo, Grants: grants, Chat: app.Chat, TaskStates: app.TaskStates, StopRequested: func(context.Context, string, string, string) (bool, error) { return false, nil }})
			if err != nil {
				t.Fatal(err)
			}
			var tool catalog.ChatToolSchema
			for _, v := range app.Chat.Tools {
				if v.Name == "create_model_3d_from_image" {
					tool = v
				}
			}
			command := chat.ToolCommand{OwnerID: "owner", SessionID: "session", AssistantMessageID: "turn", Tool: tool, Call: agents.ToolCall{ID: "call", Name: tool.Name, Arguments: json.RawMessage(`{}`)}, Options: chat.TurnOptions{AIModel: "meshy-7.1-multiview", CreativeDomain: "model-3d"}, History: []chat.Message{{Role: app.Chat.Roles.User, ImageURLs: []string{"/api/v1/assets/" + string(asset.ID)}}}}
			result, err := executor.Execute(ctx, command, nil)
			if err != nil {
				t.Fatal(err)
			}
			op, err := repo.LoadAgentOperation(ctx, "owner", "session", "turn", "call")
			if err != nil {
				t.Fatalf("operation missing: result=%+v err=%v", result, err)
			}
			prepared, err := m.GetPreparedInput(ctx, "owner", op.PreparedInputID)
			if err != nil {
				t.Fatal(err)
			}
			if prepared.NativeQuote == nil || prepared.NativeQuote.TotalAmount != "40" || prepared.NativeQuote.ExtraAmount != "20" || len(prepared.ParameterAdjustments) != 0 {
				t.Fatalf("default native quote=%+v", prepared)
			}
			grant, err := repo.GetExecutionGrant(ctx, "owner", op.GrantRequestID)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "manual" {
				if !result.NoSubmission || !op.Detached || grant.Status != executiongrant.Pending || result.TaskID != "" {
					t.Fatalf("manual bypass %+v grant=%+v", result, grant)
				}
			} else {
				if result.NoSubmission || result.TaskID == "" || grant.Status != executiongrant.Bound || grant.DecidedBy != "policy:auto" {
					t.Fatalf("auto blocked %+v grant=%+v", result, grant)
				}
			}
		})
	}
}
