package chat_test

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/assetlist"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/project"
)

// memoryAssetLists is the workflow store of the proposal tool: it keeps the
// lists the tool asked to persist and answers the session's project.
type memoryAssetLists struct {
	mu                sync.Mutex
	projects          map[string]bool
	sessions          map[string]string
	lists             []assetlist.List
	sequence          int
	proposeErr        error
	sessionProjectErr error
}

func scopedKey(ownerID, id string) string { return ownerID + "\x00" + id }

func (repo *memoryAssetLists) ProjectExists(_ context.Context, ownerID, projectID string) (bool, error) {
	repo.mu.Lock()
	defer repo.mu.Unlock()
	return repo.projects[scopedKey(ownerID, projectID)], nil
}

func (repo *memoryAssetLists) SessionProject(_ context.Context, ownerID, sessionID string) (string, error) {
	repo.mu.Lock()
	defer repo.mu.Unlock()
	if repo.sessionProjectErr != nil {
		return "", repo.sessionProjectErr
	}
	return repo.sessions[scopedKey(ownerID, sessionID)], nil
}

func (repo *memoryAssetLists) FindByRequestKey(_ context.Context, ownerID, requestKey string) (assetlist.List, bool, error) {
	repo.mu.Lock()
	defer repo.mu.Unlock()
	for _, list := range repo.lists {
		if list.OwnerID == ownerID && list.RequestKey == requestKey {
			return list, true, nil
		}
	}
	return assetlist.List{}, false, nil
}

func (repo *memoryAssetLists) ListLists(_ context.Context, ownerID, projectID string) ([]assetlist.List, error) {
	repo.mu.Lock()
	defer repo.mu.Unlock()
	var lists []assetlist.List
	for _, list := range repo.lists {
		if list.OwnerID == ownerID && list.ProjectID == projectID {
			lists = append(lists, list)
		}
	}
	return lists, nil
}

func (repo *memoryAssetLists) GetList(_ context.Context, ownerID, projectID, listID string) (assetlist.List, error) {
	repo.mu.Lock()
	defer repo.mu.Unlock()
	for _, list := range repo.lists {
		if list.OwnerID == ownerID && list.ProjectID == projectID && list.ID == listID {
			return list, nil
		}
	}
	return assetlist.List{}, assetlist.ErrNotFound
}

func (repo *memoryAssetLists) CountLiveLists(_ context.Context, ownerID, projectID string) (int, error) {
	repo.mu.Lock()
	defer repo.mu.Unlock()
	count := 0
	for _, list := range repo.lists {
		if list.OwnerID == ownerID && list.ProjectID == projectID &&
			(list.Status == assetlist.StatusDraft || list.Status == assetlist.StatusConfirmed) {
			count++
		}
	}
	return count, nil
}

func (repo *memoryAssetLists) Propose(_ context.Context, list assetlist.List, items []assetlist.Item, _ int) (assetlist.List, error) {
	repo.mu.Lock()
	defer repo.mu.Unlock()
	if repo.proposeErr != nil {
		return assetlist.List{}, repo.proposeErr
	}
	for _, existing := range repo.lists {
		if existing.OwnerID == list.OwnerID && existing.RequestKey == list.RequestKey {
			return existing, nil
		}
	}
	list.Items = items
	list.ItemCount = len(items)
	repo.lists = append(repo.lists, list)
	return list, nil
}

func (repo *memoryAssetLists) EditList(context.Context, assetlist.List, []assetlist.Item, int64) (assetlist.List, error) {
	return assetlist.List{}, assetlist.ErrInvalid
}

func (repo *memoryAssetLists) Decide(context.Context, assetlist.Decision) (assetlist.List, error) {
	return assetlist.List{}, assetlist.ErrInvalid
}

// DecideItems is the #329b board decision write. The proposal/session tests
// never decide an item, so it stays a refusal.
func (repo *memoryAssetLists) DecideItems(context.Context, assetlist.DecisionBatch) (assetlist.List, error) {
	return assetlist.List{}, assetlist.ErrInvalid
}

func defaultAssetListSchema(t *testing.T) catalog.AssetListSchema {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	if application.AssetList == nil {
		t.Fatal("the default catalog must declare application.asset_list")
	}
	return *application.AssetList
}

func newAssetListProposalService(t *testing.T, repository *memoryAssetLists,
	schema catalog.AssetListSchema, adjust func(*assetlist.Config)) *chat.Service {
	t.Helper()
	config := assetlist.Config{
		Repository:         repository,
		Schema:             schema,
		ProjectMaxItems:    20,
		MaxListsPerProject: schema.Limits.MaxListsPerProject,
		Counted: map[generation.Capability]bool{
			"text-to-3d": true, "image-to-3d": true, "remesh": true, "rigging": true,
		},
		Now: func() time.Time { return time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC) },
		NewID: func(prefix string) (string, error) {
			repository.sequence++
			return prefix + "test-" + strconv.Itoa(repository.sequence), nil
		},
	}
	if adjust != nil {
		adjust(&config)
	}
	listService, err := assetlist.New(config)
	if err != nil {
		t.Fatal(err)
	}
	service := newChatServiceWith(t, newMemoryChatRepository(), func(config *chat.ServiceConfig) {
		config.Handles = chat.HandlePolicy{
			Prefixes:       map[string]string{chat.AttachmentKindImage: "@img"},
			SelectionAlias: "@selected",
		}
	})
	service.SetAssetLists(chat.AssetListPolicy{Service: listService, Schema: schema})
	return service
}

func assetListCommand(arguments string) chat.ToolCommand {
	return chat.ToolCommand{
		OwnerID: "owner-a", SessionID: "session-a", AssistantMessageID: "turn-a",
		Call: agents.ToolCall{ID: "call-a", Name: "propose_asset_list", Arguments: []byte(arguments)},
		Options: chat.TurnOptions{
			ProjectID: "project-a",
			ProjectContext: &chat.ProjectContext{ID: "project-a", Name: "Star Harbor",
				Assets: []chat.ProjectAssetContext{
					{Handle: "@project1", Kind: "character", Name: "Hero",
						Images: []string{"/api/v1/tasks/t1/assets/a1"},
						Models: []chat.ProjectModelContext{{TaskID: "task-9", AssetID: "model-1"}}},
					{Handle: "@project2", Kind: "prop", Name: "Sword",
						Images: []string{"/api/v1/input-assets/asset-9"}},
					{Handle: "@project3", Kind: "scene", Name: "Hall",
						Models: []chat.ProjectModelContext{
							{TaskID: "task-1", AssetID: "model-1"},
							{TaskID: "task-2", AssetID: "model-2"},
							{TaskID: "task-3", AssetID: "model-3"},
							{TaskID: "task-4", AssetID: "model-4"},
							{TaskID: "task-5", AssetID: "model-5"},
						}},
				}},
		},
		History: []chat.Message{{ID: "user-a", Role: "user", Attachments: []chat.Attachment{
			{Handle: "@img1", Kind: chat.AttachmentKindImage, AssetID: "asset-1"},
			{Handle: "@img2", Kind: chat.AttachmentKindImage, TaskID: "task-2", AssetID: "gen-1"},
			{Handle: "@img3", Kind: chat.AttachmentKindImage},
		}}},
	}
}

func newAssetListRepo() *memoryAssetLists {
	return &memoryAssetLists{
		projects: map[string]bool{
			scopedKey("owner-a", "project-a"): true,
			scopedKey("owner-a", "project-b"): true,
		},
		sessions: map[string]string{scopedKey("owner-a", "session-a"): "project-a"},
	}
}

// The proposal resolves both handle families to project references, stores one
// draft list and reports the server's counts; nothing is submitted.
func TestProposeAssetListResolvesReferencesAndReportsThePlan(t *testing.T) {
	repository := newAssetListRepo()
	schema := defaultAssetListSchema(t)
	service := newAssetListProposalService(t, repository, schema, nil)
	result, err := service.ProposeAssetList(context.Background(), assetListCommand(`{
		"title":"骑士与剑",
		"items":[
			{"kind":"character","name":"Knight","description":"red coat",
			 "reference_handles":["@img1","@img2","@project1"]},
			{"kind":"prop","name":"Sword","description":"blade"}
		]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !result.NoSubmission || result.Error != "" || result.Suspend {
		t.Fatalf("proposal result = %+v", result)
	}
	if len(repository.lists) != 1 {
		t.Fatalf("stored lists = %d", len(repository.lists))
	}
	stored := repository.lists[0]
	if len(stored.Items) != 2 || stored.ProjectID != "project-a" ||
		stored.SourceSessionID != "session-a" || stored.OriginMessageID != "turn-a" {
		t.Fatalf("stored list = %+v", stored)
	}
	wantReferences := []string{"input:asset-1", "task:task-2/gen-1", "task:task-9/model-1"}
	gotReferences := make([]string, 0, len(stored.Items[0].References))
	for _, reference := range stored.Items[0].References {
		gotReferences = append(gotReferences, referenceKey(reference))
	}
	if strings.Join(gotReferences, ",") != strings.Join(wantReferences, ",") {
		t.Fatalf("item references = %v, want %v", gotReferences, wantReferences)
	}
	if len(stored.Items[1].References) != 0 {
		t.Fatalf("item without handles carries references: %+v", stored.Items[1].References)
	}

	var content map[string]any
	if err := json.Unmarshal([]byte(result.AgentContent), &content); err != nil {
		t.Fatalf("agent content %q: %v", result.AgentContent, err)
	}
	if content["list_id"] != stored.ID || content["status"] != assetlist.StatusDraft ||
		content["version"].(float64) != 0 || content["item_count"].(float64) != 2 ||
		content["planned_step_count"].(float64) != 2 ||
		content["planned_model3d_step_count"].(float64) != 2 {
		t.Fatalf("proposal content = %v", content)
	}
	if items, ok := content["unavailable_items"].([]any); !ok || len(items) != 0 {
		t.Fatalf("unavailable items = %v", content["unavailable_items"])
	}
	wantMessage := strings.NewReplacer("{count}", "2", "{steps}", "2").Replace(schema.Messages.Proposed)
	if content["message"] != wantMessage {
		t.Fatalf("proposal message = %q, want %q", content["message"], wantMessage)
	}
	var summary map[string]string
	if err := json.Unmarshal(result.Summary, &summary); err != nil || summary["asset_list_id"] != stored.ID {
		t.Fatalf("summary = %s err=%v", result.Summary, err)
	}
}

func referenceKey(reference project.Reference) string {
	if reference.InputAssetID != "" {
		return "input:" + reference.InputAssetID
	}
	return "task:" + reference.TaskID + "/" + reference.AssetID
}

// The request key is stable within a turn (a replay returns the stored list
// untouched) and includes the turn, so the same call id in a later turn is a
// new proposal.
func TestProposeAssetListReplaysInsideTheTurnAndNotAcrossTurns(t *testing.T) {
	repository := newAssetListRepo()
	schema := defaultAssetListSchema(t)
	service := newAssetListProposalService(t, repository, schema, nil)
	command := assetListCommand(`{"title":"清单","items":[{"kind":"prop","name":"Sword","description":"blade"}]}`)
	first, err := service.ProposeAssetList(context.Background(), command)
	if err != nil || first.Error != "" {
		t.Fatalf("first proposal = %+v err=%v", first, err)
	}
	second, err := service.ProposeAssetList(context.Background(), command)
	if err != nil || second.Error != "" {
		t.Fatalf("replay = %+v err=%v", second, err)
	}
	if first.AgentContent != second.AgentContent {
		t.Fatalf("replay changed the result:\n%s\n%s", first.AgentContent, second.AgentContent)
	}
	if len(repository.lists) != 1 {
		t.Fatalf("replay created %d lists", len(repository.lists))
	}

	nextTurn := command
	nextTurn.AssistantMessageID = "turn-b"
	third, err := service.ProposeAssetList(context.Background(), nextTurn)
	if err != nil || third.Error != "" {
		t.Fatalf("later turn = %+v err=%v", third, err)
	}
	if len(repository.lists) != 2 || third.AgentContent == first.AgentContent {
		t.Fatalf("later turn did not propose a new list: stored=%d", len(repository.lists))
	}
}

func TestProposeAssetListRefusesASessionOutsideTheProject(t *testing.T) {
	schema := defaultAssetListSchema(t)
	for name, adjust := range map[string]func(*chat.ToolCommand){
		"no project":      func(command *chat.ToolCommand) { command.Options.ProjectID = "" },
		"another project": func(command *chat.ToolCommand) { command.Options.ProjectID = "project-b" },
	} {
		t.Run(name, func(t *testing.T) {
			repository := newAssetListRepo()
			service := newAssetListProposalService(t, repository, schema, nil)
			command := assetListCommand(`{"title":"清单","items":[{"kind":"prop","name":"Sword","description":"blade"}]}`)
			adjust(&command)
			result, err := service.ProposeAssetList(context.Background(), command)
			if err != nil || !result.NoSubmission || result.Error != schema.Messages.ProposeSessionNotInProject {
				t.Fatalf("refusal = %+v err=%v", result, err)
			}
			if len(repository.lists) != 0 {
				t.Fatalf("refused proposal stored %d lists", len(repository.lists))
			}
		})
	}
}

func TestProposeAssetListRefusesInvalidArguments(t *testing.T) {
	schema := defaultAssetListSchema(t)
	cases := map[string]string{
		"empty object":     `{}`,
		"empty items":      `{"title":"清单","items":[]}`,
		"missing title":    `{"items":[{"kind":"prop","name":"Sword","description":"blade"}]}`,
		"missing required": `{"title":"清单","items":[{"kind":"prop","name":"Sword"}]}`,
		"unknown field":    `{"title":"清单","items":[{"kind":"prop","name":"Sword","description":"blade"}],"extra":1}`,
		"unknown kind":     `{"title":"清单","items":[{"kind":"dragon","name":"D","description":"d"}]}`,
		"trailing json":    `{"title":"清单","items":[{"kind":"prop","name":"Sword","description":"blade"}]}{}`,
	}
	for name, arguments := range cases {
		t.Run(name, func(t *testing.T) {
			repository := newAssetListRepo()
			service := newAssetListProposalService(t, repository, schema, nil)
			result, err := service.ProposeAssetList(context.Background(), assetListCommand(arguments))
			if err != nil || !result.NoSubmission || result.Error != schema.Messages.ProposeInvalidItems {
				t.Fatalf("refusal = %+v err=%v", result, err)
			}
			if len(repository.lists) != 0 {
				t.Fatalf("refused proposal stored %d lists", len(repository.lists))
			}
		})
	}
}

func TestProposeAssetListRefusesUnresolvedReferences(t *testing.T) {
	schema := defaultAssetListSchema(t)
	cases := map[string]string{
		"unknown image":        `"@img9"`,
		"image without id":     `"@img3"`,
		"unknown project":      `"@project9"`,
		"picture-only project": `"@project2"`,
		"too many references":  `"@project3"`,
	}
	for name, handles := range cases {
		t.Run(name, func(t *testing.T) {
			repository := newAssetListRepo()
			service := newAssetListProposalService(t, repository, schema, nil)
			arguments := `{"title":"清单","items":[{"kind":"prop","name":"Sword","description":"blade","reference_handles":[` + handles + `]}]}`
			result, err := service.ProposeAssetList(context.Background(), assetListCommand(arguments))
			if err != nil || !result.NoSubmission || result.Error != schema.Messages.ProposeReferenceUnresolved {
				t.Fatalf("refusal = %+v err=%v", result, err)
			}
			if len(repository.lists) != 0 {
				t.Fatalf("refused proposal stored %d lists", len(repository.lists))
			}
		})
	}
}

func TestProposeAssetListMapsServiceFailures(t *testing.T) {
	schema := defaultAssetListSchema(t)
	t.Run("session lookup", func(t *testing.T) {
		repository := newAssetListRepo()
		repository.sessionProjectErr = assetlist.ErrNotFound
		service := newAssetListProposalService(t, repository, schema, nil)
		result, err := service.ProposeAssetList(context.Background(),
			assetListCommand(`{"title":"清单","items":[{"kind":"prop","name":"Sword","description":"blade"}]}`))
		if err != nil || !result.NoSubmission || result.Error != schema.Messages.ProposeSessionNotInProject {
			t.Fatalf("refusal = %+v err=%v", result, err)
		}
	})
	t.Run("store failure", func(t *testing.T) {
		repository := newAssetListRepo()
		repository.proposeErr = errors.New("internal store exploded")
		service := newAssetListProposalService(t, repository, schema, nil)
		result, err := service.ProposeAssetList(context.Background(),
			assetListCommand(`{"title":"清单","items":[{"kind":"prop","name":"Sword","description":"blade"}]}`))
		if err != nil || !result.NoSubmission || result.Error != schema.Messages.ProposeFailed {
			t.Fatalf("refusal = %+v err=%v", result, err)
		}
		if strings.Contains(result.AgentContent, "exploded") || strings.Contains(result.Error, "exploded") {
			t.Fatalf("the internal error leaked: %+v", result)
		}
	})
}

// An unavailable route step is kept and reported with its position and name;
// the proposal still succeeds because the person decides the card.
func TestProposeAssetListReportsUnavailableItems(t *testing.T) {
	repository := newAssetListRepo()
	schema := defaultAssetListSchema(t)
	service := newAssetListProposalService(t, repository, schema, func(config *assetlist.Config) {
		config.ResolveStepFor = func(_ context.Context, _, _, capability string) (assetlist.RouteStep, error) {
			if capability == "remesh" {
				return assetlist.RouteStep{}, errors.New("remesh is not available now")
			}
			return assetlist.RouteStep{Capability: capability, OfferingID: capability + "-default",
				Available: true}, nil
		}
	})
	result, err := service.ProposeAssetList(context.Background(), assetListCommand(`{
		"title":"清单",
		"items":[{"kind":"character","name":"Knight","description":"red coat","route_id":"text-to-3d-remesh"}]}`))
	if err != nil || result.Error != "" || !result.NoSubmission {
		t.Fatalf("proposal = %+v err=%v", result, err)
	}
	var content struct {
		PlannedStepCount int `json:"planned_step_count"`
		UnavailableItems []struct {
			Position int    `json:"position"`
			Name     string `json:"name"`
			Reason   string `json:"reason"`
		} `json:"unavailable_items"`
	}
	if err := json.Unmarshal([]byte(result.AgentContent), &content); err != nil {
		t.Fatal(err)
	}
	if content.PlannedStepCount != 2 || len(content.UnavailableItems) != 1 ||
		content.UnavailableItems[0].Position != 1 || content.UnavailableItems[0].Name != "Knight" ||
		content.UnavailableItems[0].Reason != "remesh is not available now" {
		t.Fatalf("content = %+v", content)
	}
}

// memoryCropReferences is the #331 reference port of the proposal tests. Every
// crop names the source picture recorded here; ids absent from the map do not
// exist.
type memoryCropReferences map[generation.AssetID]assetlist.InputAssetReference

func (port memoryCropReferences) ResolveInputAsset(_ context.Context,
	_ generation.OwnerID, id generation.AssetID,
) (assetlist.InputAssetReference, bool, error) {
	asset, found := port[id]
	return asset, found, nil
}

// #331: a directly named input asset (a crop) merges into the same reference
// set as the resolved handles, keeps its order after them and is deduplicated.
func TestProposeAssetListMergesInputAssetReferences(t *testing.T) {
	repository := newAssetListRepo()
	schema := defaultAssetListSchema(t)
	service := newAssetListProposalService(t, repository, schema, nil)
	result, err := service.ProposeAssetList(context.Background(), assetListCommand(`{
		"title":"骑士与剑",
		"items":[
			{"kind":"character","name":"Knight","description":"red coat",
			 "reference_handles":["@img1"],
			 "reference_input_asset_ids":["crop-1","asset-1"]},
			{"kind":"prop","name":"Sword","description":"blade",
			 "reference_input_asset_ids":["crop-1"]}
		]}`))
	if err != nil || result.Error != "" || !result.NoSubmission {
		t.Fatalf("proposal = %+v err=%v", result, err)
	}
	if len(repository.lists) != 1 {
		t.Fatalf("stored lists = %d", len(repository.lists))
	}
	stored := repository.lists[0]
	keys := make([]string, 0, len(stored.Items[0].References))
	for _, reference := range stored.Items[0].References {
		keys = append(keys, referenceKey(reference))
	}
	// @img1 resolves to input:asset-1; the duplicate id collapses.
	if got, want := strings.Join(keys, ","), "input:asset-1,input:crop-1"; got != want {
		t.Fatalf("item references = %v, want %v", got, want)
	}
	if len(stored.Items[1].References) != 1 ||
		stored.Items[1].References[0] != (project.Reference{InputAssetID: "crop-1"}) {
		t.Fatalf("second item references = %+v", stored.Items[1].References)
	}
}

// #331: the assetlist service owns the crop rules; a violation reaches the
// Agent as the decompose section's reference copy, and an empty id is an
// invalid argument.
func TestProposeAssetListRefusesInvalidInputAssetReferences(t *testing.T) {
	cases := []struct {
		name      string
		arguments string
		mutate    func(*assetlist.Config)
		wantCopy  func(catalog.AssetListSchema) string
	}{
		{
			name: "two crops in one item",
			arguments: `{"title":"清单","items":[{"kind":"character","name":"Knight",
				"description":"red coat","reference_input_asset_ids":["crop-1","crop-2"]}]}`,
			wantCopy: func(schema catalog.AssetListSchema) string {
				return schema.Decompose.Messages.ReferenceInvalid
			},
		},
		{
			name: "crops from different sources",
			arguments: `{"title":"清单","items":[
				{"kind":"character","name":"Knight","description":"red coat",
				 "reference_input_asset_ids":["crop-1"]},
				{"kind":"prop","name":"Sword","description":"blade",
				 "reference_input_asset_ids":["crop-3"]}]}`,
			wantCopy: func(schema catalog.AssetListSchema) string {
				return schema.Decompose.Messages.ReferenceInvalid
			},
		},
		{
			name: "crop without provenance",
			arguments: `{"title":"清单","items":[{"kind":"character","name":"Knight",
				"description":"red coat","reference_input_asset_ids":["crop-broken"]}]}`,
			mutate: func(config *assetlist.Config) {
				port := memoryCropReferences{
					"crop-broken": {MediaType: "image/png", IsCrop: true},
				}
				config.References = port
			},
			wantCopy: func(schema catalog.AssetListSchema) string {
				return schema.Decompose.Messages.ReferenceInvalid
			},
		},
		{
			name: "empty id",
			arguments: `{"title":"清单","items":[{"kind":"character","name":"Knight",
				"description":"red coat","reference_input_asset_ids":["  "]}]}`,
			wantCopy: func(schema catalog.AssetListSchema) string {
				return schema.Messages.ProposeInvalidItems
			},
		},
	}
	for _, testCase := range cases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			repository := newAssetListRepo()
			schema := defaultAssetListSchema(t)
			service := newAssetListProposalService(t, repository, schema, func(config *assetlist.Config) {
				// The default port for these cases: two good crops from
				// different sources plus one valid crop.
				config.References = memoryCropReferences{
					"crop-1": {MediaType: "image/png", IsCrop: true, CropSourceAssetID: "scene-1"},
					"crop-2": {MediaType: "image/png", IsCrop: true, CropSourceAssetID: "scene-1"},
					"crop-3": {MediaType: "image/png", IsCrop: true, CropSourceAssetID: "scene-2"},
				}
				if testCase.mutate != nil {
					testCase.mutate(config)
				}
			})
			result, err := service.ProposeAssetList(context.Background(), assetListCommand(testCase.arguments))
			if err != nil {
				t.Fatal(err)
			}
			if want := testCase.wantCopy(schema); result.Error != want {
				t.Fatalf("refusal = %q, want %q", result.Error, want)
			}
			if len(repository.lists) != 0 {
				t.Fatalf("stored lists = %d", len(repository.lists))
			}
		})
	}
}

// #331: a proposal that takes more items from one source picture than
// maximum_items_per_image allows is refused with the decompose limit copy,
// its {maximum} filled in; a proposal at the bound passes.
func TestProposeAssetListRefusesTooManyItemsForOneImage(t *testing.T) {
	repository := newAssetListRepo()
	schema := defaultAssetListSchema(t)
	schema.Decompose.MaximumItemsPerImage = 1
	service := newAssetListProposalService(t, repository, schema, func(config *assetlist.Config) {
		config.References = memoryCropReferences{
			"crop-1": {MediaType: "image/png", IsCrop: true, CropSourceAssetID: "scene-1"},
			"crop-2": {MediaType: "image/png", IsCrop: true, CropSourceAssetID: "scene-1"},
		}
	})
	result, err := service.ProposeAssetList(context.Background(), assetListCommand(
		`{"title":"清单","items":[
			{"kind":"character","name":"Knight","description":"red coat",
			 "reference_input_asset_ids":["crop-1"]},
			{"kind":"prop","name":"Sword","description":"blade",
			 "reference_input_asset_ids":["crop-2"]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	want := strings.ReplaceAll(schema.Decompose.Messages.ImageItemLimit, "{maximum}", "1")
	if result.Error != want {
		t.Fatalf("refusal = %q, want %q", result.Error, want)
	}
	if len(repository.lists) != 0 {
		t.Fatalf("stored lists = %d", len(repository.lists))
	}

	// One item from the source is at the bound and is accepted.
	result, err = service.ProposeAssetList(context.Background(), assetListCommand(
		`{"title":"清单","items":[{"kind":"character","name":"Knight",
		  "description":"red coat","reference_input_asset_ids":["crop-1"]}]}`))
	if err != nil || result.Error != "" || !result.NoSubmission {
		t.Fatalf("proposal at the bound = %+v err=%v", result, err)
	}
	if len(repository.lists) != 1 {
		t.Fatalf("stored lists = %d", len(repository.lists))
	}
}

// A Catalog without the decompose section keeps the generic invalid-items
// copy for a refused reference (#327 behavior).
func TestProposeAssetListReferenceRefusalFallsBackWithoutDecompose(t *testing.T) {
	repository := newAssetListRepo()
	schema := defaultAssetListSchema(t)
	schema.Decompose = nil
	service := newAssetListProposalService(t, repository, schema, func(config *assetlist.Config) {
		config.References = memoryCropReferences{
			"crop-1": {MediaType: "image/png", IsCrop: true, CropSourceAssetID: "scene-1"},
			"crop-2": {MediaType: "image/png", IsCrop: true, CropSourceAssetID: "scene-1"},
		}
	})
	result, err := service.ProposeAssetList(context.Background(), assetListCommand(
		`{"title":"清单","items":[{"kind":"character","name":"Knight","description":"red coat",
		  "reference_input_asset_ids":["crop-1","crop-2"]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.Error != schema.Messages.ProposeInvalidItems {
		t.Fatalf("refusal = %q, want the generic invalid-items copy", result.Error)
	}
}
