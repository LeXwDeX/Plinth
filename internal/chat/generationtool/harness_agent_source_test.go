package generationtool_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/agents/openaicompatible"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/harness"
	"github.com/LeXwDeX/Plinth/internal/harness/resolve"
)

func agentHarness(t *testing.T) (*processingHarness, chat.ToolCommand) {
	t.Helper()
	h, command := activeHarness(t)
	h.gateway.tasks["model-2"] = modelTask("model-2", "owner-a", "session-a", h.meshy, generation.TextTo3D, "glb")
	for id, task := range h.gateway.tasks {
		for i := range task.Assets {
			if task.Assets[i].Role == "model" {
				task.Assets[i].SHA256 = strings.Repeat("a", 64)
			}
		}
		h.gateway.tasks[id] = task
	}
	resolver := resolve.Service{Store: h.gateway.repo, Schema: h.app.Chat.Harness, Tools: h.app.Chat.Tools, Catalog: h.gateway.product, Gateway: h.gateway}
	selection := command.Options.Harness.Selection
	selection.Sources = []harness.Source{{Kind: harness.SourceTaskAsset, SelectionMode: "agent", Parameter: "source_task"}}
	if _, err := resolver.Update(context.Background(), "owner-a", "session-a", selection.Revision, selection); err != nil {
		t.Fatal(err)
	}
	snapshot, err := resolver.Snapshot(context.Background(), "owner-a", "session-a")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Status != resolve.StatusReady || len(snapshot.SourceCandidates) != 2 || snapshot.DefaultSource != nil {
		t.Fatalf("unexpected candidate scope %+v", snapshot)
	}
	command.Options.Harness = &snapshot
	command.Call.Arguments = []byte(`{"source":{"task_id":"model-2","asset_id":"model-2-glb"},"parameters":{"prompt":"wood"}}`)
	return h, command
}

func TestAgentHarnessChoosesSecondRealCandidateAndRestoresFrozenScope(t *testing.T) {
	h, command := agentHarness(t)
	raw, _ := json.Marshal(command.Options.Harness)
	var frozen resolve.Snapshot
	if err := json.Unmarshal(raw, &frozen); err != nil {
		t.Fatal(err)
	}
	command.Options.Harness = &frozen
	originalBinding := frozen.Selection.Binding
	// A later model is not introduced into this turn's allowed scope.
	h.gateway.tasks["latest"] = modelTask("latest", "owner-a", "session-a", h.meshy, generation.TextTo3D, "glb")
	result, err := h.executor.Execute(context.Background(), command, nil)
	if err != nil || result.NoSubmission || len(h.gateway.frozen) != 1 || len(h.gateway.submitted) != 1 {
		t.Fatalf("explicit candidate failed %+v %v", result, err)
	}
	if h.frozenInput(0)["source_task"] != "model-2" {
		t.Fatal("Agent's explicit second model was replaced")
	}
	op, err := h.gateway.repo.LoadAgentOperation(context.Background(), "owner-a", "session-a", "turn-a", "active-call")
	if err != nil {
		t.Fatal(err)
	}
	grant, err := h.gateway.repo.GetExecutionGrant(context.Background(), "owner-a", op.GrantRequestID)
	if err != nil || grant.Harness != originalBinding || op.Harness != originalBinding {
		t.Fatal("scope binding/Grant changed")
	}
	// Bound recovery observes the already accepted Task even if its source has
	// since disappeared. It cannot search for or submit another model.
	delete(h.gateway.tasks, "model-2")
	if _, err = h.executor.Execute(context.Background(), command, nil); err != nil || len(h.gateway.submitted) != 1 {
		t.Fatalf("bound recovery changed source: %v", err)
	}
}

func TestAgentHarnessInvalidOrAmbiguousSourceNeverPrepares(t *testing.T) {
	for _, raw := range []string{
		`{"parameters":{"prompt":"wood"}}`,
		`{"source":"{\"task_id\":\"model-2\",\"asset_id\":\"model-2-glb\"}","parameters":{"prompt":"wood"}}`,
		`{"source":{},"parameters":{"prompt":"wood"}}`,
		`{"source":{"task_id":"other-owner","asset_id":"other-owner-glb"}}`,
		`{"source":{"task_id":"other-session","asset_id":"other-session-glb"}}`,
		`{"source":{"task_id":"model-1","asset_id":"model-2-glb"}}`,
		`{"source":{"task_id":"model-2","asset_id":"model-2-glb","other":true}}`,
		`{"source":{"task_id":"model-2","task_id":"model-1","asset_id":"model-2-glb"}}`,
		`{"source":{"task_id":"latest","asset_id":"latest-glb"}}`,
	} {
		t.Run(raw, func(t *testing.T) {
			h, c := agentHarness(t)
			c.Call.Arguments = []byte(raw)
			result, err := h.executor.Execute(context.Background(), c, nil)
			if err != nil {
				t.Fatal(err)
			}
			if !result.NoSubmission || len(h.gateway.frozen) > 0 || len(h.gateway.submitted) > 0 {
				t.Fatal("invalid source reached execution")
			}
		})
	}
}

type sourceSchemaTransport func(*http.Request) (*http.Response, error)

func (f sourceSchemaTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestAgentHarnessFrozenToolSchemaDeclaresNestedSourceObject(t *testing.T) {
	_, command := agentHarness(t)
	var schema struct {
		Type       string                     `json:"type"`
		Required   []string                   `json:"required"`
		Properties map[string]json.RawMessage `json:"properties"`
	}
	var source struct {
		Type       string `json:"type"`
		Properties map[string]struct {
			Type  string `json:"type"`
			Const string `json:"const"`
		} `json:"properties"`
		Required []string `json:"required"`
		OneOf    []struct {
			Type       string `json:"type"`
			Properties map[string]struct {
				Type  string `json:"type"`
				Const string `json:"const"`
			} `json:"properties"`
			Required             []string `json:"required"`
			AdditionalProperties *bool    `json:"additionalProperties"`
		} `json:"oneOf"`
		AdditionalProperties *bool `json:"additionalProperties"`
	}
	// Exercise the actual OpenAI-compatible serializer, intercepting its HTTP
	// transport locally. This is wire-shape proof, not real-model acceptance.
	var transmitted json.RawMessage
	calls := 0
	transport := sourceSchemaTransport(func(request *http.Request) (*http.Response, error) {
		calls++
		if request.Method != http.MethodPost || request.URL.String() != "https://fixture.invalid/v1/chat/completions" {
			t.Fatalf("unexpected wire target %s %s", request.Method, request.URL)
		}
		var wire struct {
			Tools []struct {
				Function struct {
					Name       string          `json:"name"`
					Parameters json.RawMessage `json:"parameters"`
				} `json:"function"`
			} `json:"tools"`
		}
		if err := json.NewDecoder(request.Body).Decode(&wire); err != nil {
			t.Fatal(err)
		}
		if len(wire.Tools) != 1 || wire.Tools[0].Function.Name != command.Options.Harness.ToolName {
			t.Fatal("frozen tool missing from adapter wire")
		}
		transmitted = append(json.RawMessage(nil), wire.Tools[0].Function.Parameters...)
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: [DONE]\n\n"))}, nil
	})
	adapter, err := openaicompatible.New(openaicompatible.Config{BaseURL: "https://fixture.invalid/v1", EndpointPath: "chat/completions", AuthorizationName: "Authorization", AuthorizationType: "Bearer", RequestTimeout: time.Second, ScannerMaxBytes: 1 << 20, ErrorBodyMaxBytes: 4096, Client: &http.Client{Transport: transport}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = adapter.Stream(context.Background(), agents.Request{Model: "fixture", Messages: []agents.Message{{Role: "user", Content: "fixture"}}, Tools: []agents.Tool{{Name: command.Options.Harness.ToolName, Parameters: command.Options.Harness.ToolParameters}}}, nil)
	if err != nil || calls != 1 {
		t.Fatalf("adapter wire not verified: calls=%d err=%v", calls, err)
	}
	var original, wireSchema any
	if json.Unmarshal(command.Options.Harness.ToolParameters, &original) != nil || json.Unmarshal(transmitted, &wireSchema) != nil || !reflect.DeepEqual(original, wireSchema) {
		t.Fatal("adapter changed the frozen source schema")
	}
	if err := json.Unmarshal(transmitted, &schema); err != nil {
		t.Fatal(err)
	}
	// Other properties may legitimately use object-valued additionalProperties.
	sourceJSON, ok := schema.Properties["source"]
	if ok {
		if err := json.Unmarshal(sourceJSON, &source); err != nil {
			t.Fatal(err)
		}
	}
	if !ok || schema.Type != "object" || len(schema.Required) != 1 || schema.Required[0] != "source" || source.Type != "object" || source.AdditionalProperties == nil || *source.AdditionalProperties || len(source.Required) != 2 || source.Required[0] != "task_id" || source.Required[1] != "asset_id" || len(source.OneOf) != 2 {
		t.Fatalf("frozen nested source contract is incomplete: %s", command.Options.Harness.ToolParameters)
	}
	for _, field := range []string{"task_id", "asset_id"} {
		if source.Properties[field].Type != "string" {
			t.Fatalf("source property %s is not declared as a string: %+v", field, source.Properties[field])
		}
	}
	for _, candidate := range source.OneOf {
		if candidate.Type != "object" || candidate.AdditionalProperties == nil || *candidate.AdditionalProperties || len(candidate.Required) != 2 || candidate.Required[0] != "task_id" || candidate.Required[1] != "asset_id" {
			t.Fatalf("candidate is not a closed complete pair: %+v", candidate)
		}
		for _, field := range []string{"task_id", "asset_id"} {
			if candidate.Properties[field].Type != "string" || candidate.Properties[field].Const == "" {
				t.Fatalf("candidate %s is not constrained to a frozen string identity: %+v", field, candidate)
			}
		}
	}
}

func TestAgentHarnessCurrentSourceMutationNeverPrepares(t *testing.T) {
	for name, mutate := range map[string]func(*generation.Task){
		"owner":    func(t *generation.Task) { t.OwnerID = "other" },
		"session":  func(t *generation.Task) { t.SessionID = "other" },
		"pending":  func(t *generation.Task) { t.State = generation.TaskRunning },
		"provider": func(t *generation.Task) { t.Offering.ProviderID = "other" },
		"hash":     func(t *generation.Task) { t.Assets[0].SHA256 = strings.Repeat("b", 64) },
		"size":     func(t *generation.Task) { t.Assets[0].Size++ },
		"format":   func(t *generation.Task) { t.Assets[0].Variant = "other" },
	} {
		t.Run(name, func(t *testing.T) {
			h, c := agentHarness(t)
			source := h.gateway.tasks["model-2"]
			mutate(&source)
			h.gateway.tasks["model-2"] = source
			result, err := h.executor.Execute(context.Background(), c, nil)
			if err != nil {
				t.Fatal(err)
			}
			if !result.NoSubmission || len(h.gateway.frozen) > 0 || len(h.gateway.submitted) > 0 {
				t.Fatal("changed scoped source reached execution")
			}
		})
	}
}

func TestFixedHarnessRejectsEvenIdenticalTopLevelSource(t *testing.T) {
	h, c := activeHarness(t)
	c.Call.Arguments = []byte(`{"source":{"task_id":"model-1","asset_id":"model-1-glb"},"parameters":{"prompt":"wood"}}`)
	result, err := h.executor.Execute(context.Background(), c, nil)
	if err != nil || !result.NoSubmission || len(h.gateway.frozen) > 0 {
		t.Fatal("fixed source accepted an Agent override")
	}
}

func TestDetachedAgentSourceUsesOriginalTurnScopeAndPreparedIdentity(t *testing.T) {
	for _, kind := range []string{"valid", "missing-snapshot", "binding", "hash", "offering", "pair", "changed-source", "deleted"} {
		t.Run(kind, func(t *testing.T) {
			h, c := agentHarness(t)
			source := h.gateway.tasks["model-2"]
			prepared := generation.PreparedInput{ID: "prepared", OwnerID: "owner-a", SessionID: "session-a", InputHash: "hash", OfferingID: generation.OfferingID(c.Options.Harness.Selection.OfferingID), OfferingRevision: c.Options.Harness.OfferingRevision, Capability: c.Options.Harness.Capability, NormalizedInput: json.RawMessage(`{"source_task":"model-2","prompt":"wood"}`)}
			op := generationtool.Operation{OwnerID: "owner-a", SessionID: "session-a", AssistantMessageID: "turn-a", PreparedInputID: prepared.ID, PreparedInputHash: prepared.InputHash, OfferingID: prepared.OfferingID, OfferingRevision: prepared.OfferingRevision, Harness: c.Options.Harness.Selection.Binding}
			turn := chat.Turn{OwnerID: "owner-a", SessionID: "session-a", AssistantMessageID: "turn-a", Options: c.Options}
			switch kind {
			case "missing-snapshot":
				turn.Options.Harness = nil
			case "binding":
				op.Harness.Digest = strings.Repeat("b", 64)
			case "hash":
				prepared.InputHash = "other"
			case "offering":
				prepared.OfferingID = "other"
			case "pair":
				prepared.NormalizedInput = json.RawMessage(`{"source_task":"invented"}`)
			case "changed-source":
				source.SessionID = "foreign"
				h.gateway.tasks[source.ID] = source
			case "deleted":
				delete(h.gateway.tasks, source.ID)
			}
			err := h.executor.VerifyDetachedHarnessSource(context.Background(), turn, op, prepared)
			if (kind == "valid") != (err == nil) {
				t.Fatalf("detached source %s err %v", kind, err)
			}
			if len(h.gateway.frozen) > 0 || len(h.gateway.submitted) > 0 {
				t.Fatal("read-only verifier executed work")
			}
		})
	}
}

func wildcardAgentHarness(t *testing.T) (*processingHarness, chat.ToolCommand) {
	t.Helper()
	h, c := agentHarness(t)
	for id, task := range h.gateway.tasks {
		for i := range task.Assets {
			if task.Assets[i].Variant == "glb" {
				task.Assets[i].MediaType = "model/gltf-binary"
			}
		}
		h.gateway.tasks[id] = task
	}
	resolver := resolve.Service{Store: h.gateway.repo, Schema: h.app.Chat.Harness, Tools: h.app.Chat.Tools, Catalog: h.gateway.product, Gateway: h.gateway}
	selected := c.Options.Harness.Selection
	selected.Sources = append([]harness.Source(nil), selected.Sources...)
	selected.Sources[0].Parameter = "*"
	if _, err := resolver.Update(context.Background(), "owner-a", "session-a", selected.Revision, selected); err != nil {
		t.Fatal(err)
	}
	snapshot, err := resolver.Snapshot(context.Background(), "owner-a", "session-a")
	if err != nil {
		t.Fatal(err)
	}
	c.Options.Harness = &snapshot
	return h, c
}

func TestWildcardAgentUsesExistingNativeOrCrossProviderAssetRoute(t *testing.T) {
	for _, id := range []string{"model-1", "tripo-1"} {
		t.Run(id, func(t *testing.T) {
			h, c := wildcardAgentHarness(t)
			count := 0
			parameter := "source_task"
			value := id
			if id == "tripo-1" {
				parameter = "source_asset"
				value = id + "-glb"
			}
			for _, candidate := range c.Options.Harness.SourceCandidates {
				if candidate.Source.TaskID == id {
					count++
					if candidate.Source.Parameter != parameter || candidate.Value != value {
						t.Fatal("binder route was replaced")
					}
				}
			}
			if count != 1 {
				t.Fatalf("same model exposed ambiguous routes: %d", count)
			}
			c.Call.Arguments = []byte(`{"source":{"task_id":"` + id + `","asset_id":"` + id + `-glb"},"parameters":{"prompt":"wood"}}`)
			result, err := h.executor.Execute(context.Background(), c, nil)
			if err != nil || result.NoSubmission || len(h.gateway.frozen) != 1 {
				t.Fatalf("wildcard route failed %+v %v", result, err)
			}
			input := h.frozenInput(0)
			if input[parameter] != value || input["*"] != nil {
				t.Fatal("wildcard leaked instead of exact binder input")
			}
			prepared, err := h.gateway.repo.GetPreparedInput(context.Background(), "owner-a", "harness-prepared-1")
			if err != nil {
				t.Fatal(err)
			}
			op, err := h.gateway.repo.LoadAgentOperation(context.Background(), "owner-a", "session-a", "turn-a", "active-call")
			if err != nil {
				t.Fatal(err)
			}
			turn := chat.Turn{OwnerID: "owner-a", SessionID: "session-a", AssistantMessageID: "turn-a", Options: c.Options}
			if err = h.executor.VerifyDetachedHarnessSource(context.Background(), turn, op, prepared); err != nil {
				t.Fatal("wildcard prepared route cannot be rechecked", err)
			}
		})
	}
}

func TestWildcardAgentRejectsIncompatibleAssetAndChangedPermission(t *testing.T) {
	t.Run("format", func(t *testing.T) {
		h, c := wildcardAgentHarness(t)
		task := h.gateway.tasks["tripo-1"]
		task.Assets[0].MediaType = "model/stl"
		task.Assets[0].Variant = "stl"
		h.gateway.tasks[task.ID] = task
		c.Call.Arguments = []byte(`{"source":{"task_id":"tripo-1","asset_id":"tripo-1-glb"},"parameters":{"prompt":"wood"}}`)
		result, err := h.executor.Execute(context.Background(), c, nil)
		if err != nil || !result.NoSubmission || len(h.gateway.frozen) > 0 {
			t.Fatal("incompatible cross-provider bytes prepared")
		}
	})
	t.Run("scope", func(t *testing.T) {
		h, c := wildcardAgentHarness(t)
		resolver := resolve.Service{Store: h.gateway.repo, Schema: h.app.Chat.Harness, Tools: h.app.Chat.Tools, Catalog: h.gateway.product, Gateway: h.gateway}
		selected := c.Options.Harness.Selection
		selected.Sources = append([]harness.Source(nil), selected.Sources...)
		selected.Sources[0].Parameter = "source_task"
		if _, err := resolver.Update(context.Background(), "owner-a", "session-a", selected.Revision, selected); err != nil {
			t.Fatal(err)
		}
		result, err := h.executor.Execute(context.Background(), c, nil)
		if err != nil || !result.NoSubmission || len(h.gateway.frozen) > 0 {
			t.Fatal("old permission survived a scope revision")
		}
	})
}

func TestWildcardAgentApprovalRechecksScopeAndOriginalAsset(t *testing.T) {
	for _, change := range []string{"permission", "asset", "offering"} {
		t.Run(change, func(t *testing.T) {
			h, c := wildcardAgentHarness(t)
			p := generation.PreparedInput{ID: "prepared", OwnerID: "owner-a", SessionID: "session-a", InputHash: "hash", OfferingID: generation.OfferingID(c.Options.Harness.Selection.OfferingID), OfferingRevision: c.Options.Harness.OfferingRevision, Capability: c.Options.Harness.Capability, NormalizedInput: json.RawMessage(`{"source_asset":"tripo-1-glb","prompt":"wood"}`)}
			op := generationtool.Operation{OwnerID: "owner-a", SessionID: "session-a", AssistantMessageID: "turn-a", PreparedInputID: p.ID, PreparedInputHash: p.InputHash, OfferingID: p.OfferingID, OfferingRevision: p.OfferingRevision, Harness: c.Options.Harness.Selection.Binding}
			turn := chat.Turn{OwnerID: "owner-a", SessionID: "session-a", AssistantMessageID: "turn-a", Options: c.Options}
			if err := h.executor.VerifyDetachedHarnessSource(context.Background(), turn, op, p); err != nil {
				t.Fatal("baseline not recheckable", err)
			}
			switch change {
			case "permission":
				selected := c.Options.Harness.Selection
				selected.Sources = append([]harness.Source(nil), selected.Sources...)
				selected.Sources[0].Parameter = "source_task"
				resolver := resolve.Service{Store: h.gateway.repo, Schema: h.app.Chat.Harness, Tools: h.app.Chat.Tools, Catalog: h.gateway.product, Gateway: h.gateway}
				if _, err := resolver.Update(context.Background(), "owner-a", "session-a", selected.Revision, selected); err != nil {
					t.Fatal(err)
				}
			case "asset":
				task := h.gateway.tasks["tripo-1"]
				task.Assets[0].SHA256 = strings.Repeat("b", 64)
				h.gateway.tasks[task.ID] = task
			case "offering":
				p.OfferingRevision = "changed"
			}
			if err := h.executor.VerifyDetachedHarnessSource(context.Background(), turn, op, p); err == nil {
				t.Fatal("approval accepted changed frozen scope/asset/Offering")
			}
			if len(h.gateway.frozen) > 0 || len(h.gateway.submitted) > 0 {
				t.Fatal("source recheck executed work")
			}
		})
	}
}

func TestDetachedHistoricalZeroBindingRequiresNoHarnessSnapshot(t *testing.T) {
	h, _ := activeHarness(t)
	p := generation.PreparedInput{ID: "prepared", OwnerID: "owner-a", SessionID: "session-a", InputHash: "hash", OfferingID: "old", OfferingRevision: "old"}
	op := generationtool.Operation{OwnerID: "owner-a", SessionID: "session-a", AssistantMessageID: "turn-a", PreparedInputID: p.ID, PreparedInputHash: p.InputHash, OfferingID: p.OfferingID, OfferingRevision: p.OfferingRevision}
	turn := chat.Turn{OwnerID: "owner-a", SessionID: "session-a", AssistantMessageID: "turn-a"}
	if err := h.executor.VerifyDetachedHarnessSource(context.Background(), turn, op, p); err != nil {
		t.Fatal("explicit historical contract rejected", err)
	}
	turn.Options.Harness = &resolve.Snapshot{Selection: harness.DefaultSelection("owner-a", "session-a"), Status: resolve.StatusFree}
	if err := h.executor.VerifyDetachedHarnessSource(context.Background(), turn, op, p); err == nil {
		t.Fatal("new snapshot with missing operation binding treated as historical")
	}
}
