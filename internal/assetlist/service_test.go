package assetlist_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/assetlist"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/project"
)

var testIDCounter atomic.Uint64

type sessionRecord struct {
	owner   string
	project string
}

type memoryRepository struct {
	projects map[string]string
	sessions map[string]sessionRecord
	lists    map[string]assetlist.List
	items    map[string][]assetlist.Item
	// assets records the project asset rows the decision transaction wrote;
	// decisionFail fails the next DecideItems like a lost CAS would.
	assets       map[string]project.Asset
	decisionFail error
}

func newMemoryRepository() *memoryRepository {
	return &memoryRepository{
		projects: map[string]string{"prj_1": "owner-a", "prj_2": "owner-a"},
		sessions: map[string]sessionRecord{
			"ses_1":   {owner: "owner-a", project: "prj_1"},
			"ses_2":   {owner: "owner-a", project: "prj_2"},
			"ses_own": {owner: "owner-b", project: "prj_1"},
		},
		lists:  map[string]assetlist.List{},
		items:  map[string][]assetlist.Item{},
		assets: map[string]project.Asset{},
	}
}

func (r *memoryRepository) ProjectExists(_ context.Context, ownerID, projectID string) (bool, error) {
	owner, exists := r.projects[projectID]
	return exists && owner == ownerID, nil
}

func (r *memoryRepository) SessionProject(_ context.Context, ownerID, sessionID string) (string, error) {
	session, exists := r.sessions[sessionID]
	if !exists || session.owner != ownerID {
		return "", assetlist.ErrNotFound
	}
	return session.project, nil
}

func (r *memoryRepository) FindByRequestKey(_ context.Context, ownerID, requestKey string) (assetlist.List, bool, error) {
	for _, list := range r.lists {
		if list.OwnerID == ownerID && list.RequestKey == requestKey {
			return r.withItems(list), true, nil
		}
	}
	return assetlist.List{}, false, nil
}

func (r *memoryRepository) ListLists(_ context.Context, ownerID, projectID string) ([]assetlist.List, error) {
	result := make([]assetlist.List, 0)
	for _, list := range r.lists {
		if list.OwnerID == ownerID && list.ProjectID == projectID {
			summary := list
			summary.Items = nil
			sumItems(&summary, r.items[list.ID])
			result = append(result, summary)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].UpdatedAt.Equal(result[j].UpdatedAt) {
			return result[i].ID > result[j].ID
		}
		return result[i].UpdatedAt.After(result[j].UpdatedAt)
	})
	return result, nil
}

func (r *memoryRepository) GetList(_ context.Context, ownerID, projectID, listID string) (assetlist.List, error) {
	list, exists := r.lists[listID]
	if !exists || list.OwnerID != ownerID || list.ProjectID != projectID {
		return assetlist.List{}, assetlist.ErrNotFound
	}
	return r.withItems(list), nil
}

func (r *memoryRepository) CountLiveLists(_ context.Context, ownerID, projectID string) (int, error) {
	count := 0
	for _, list := range r.lists {
		if list.OwnerID != ownerID || list.ProjectID != projectID {
			continue
		}
		if list.Status == assetlist.StatusDraft || list.Status == assetlist.StatusConfirmed {
			count++
		}
	}
	return count, nil
}

func (r *memoryRepository) Propose(_ context.Context, list assetlist.List, items []assetlist.Item, maximum int) (assetlist.List, error) {
	for _, existing := range r.lists {
		if existing.OwnerID == list.OwnerID && existing.RequestKey == list.RequestKey {
			return r.withItems(existing), nil
		}
	}
	if owner, exists := r.projects[list.ProjectID]; !exists || owner != list.OwnerID {
		return assetlist.List{}, assetlist.ErrProjectNotFound
	}
	if maximum > 0 {
		live, err := r.CountLiveLists(context.Background(), list.OwnerID, list.ProjectID)
		if err != nil {
			return assetlist.List{}, err
		}
		if live >= maximum {
			return assetlist.List{}, assetlist.ErrLimit
		}
	}
	for id, existing := range r.lists {
		if existing.OwnerID == list.OwnerID && existing.ProjectID == list.ProjectID &&
			existing.Status == assetlist.StatusDraft {
			existing.Status = assetlist.StatusSuperseded
			existing.SupersededBy = list.ID
			existing.UpdatedAt = list.UpdatedAt
			r.lists[id] = existing
		}
	}
	stored := list
	stored.Items = nil
	r.lists[list.ID] = stored
	r.items[list.ID] = cloneItems(items)
	return r.withItems(stored), nil
}

func (r *memoryRepository) EditList(_ context.Context, list assetlist.List, items []assetlist.Item, expectedVersion int64) (assetlist.List, error) {
	existing, exists := r.lists[list.ID]
	if !exists || existing.OwnerID != list.OwnerID || existing.ProjectID != list.ProjectID {
		return assetlist.List{}, assetlist.ErrNotFound
	}
	if existing.Status == assetlist.StatusExpired {
		return assetlist.List{}, assetlist.ErrExpired
	}
	if existing.Status == assetlist.StatusDraft && !list.UpdatedAt.Before(existing.ExpiresAt) {
		existing.Status = assetlist.StatusExpired
		existing.UpdatedAt = list.UpdatedAt
		r.lists[existing.ID] = existing
		return assetlist.List{}, assetlist.ErrExpired
	}
	if existing.Status != assetlist.StatusDraft || existing.Version != expectedVersion {
		return assetlist.List{}, assetlist.ErrConflict
	}
	existing.Title = list.Title
	existing.Brief = append(json.RawMessage(nil), list.Brief...)
	existing.Version++
	existing.UpdatedAt = list.UpdatedAt
	r.lists[existing.ID] = existing
	r.items[existing.ID] = cloneItems(items)
	return r.withItems(existing), nil
}

func (r *memoryRepository) Decide(_ context.Context, decision assetlist.Decision) (assetlist.List, error) {
	existing, exists := r.lists[decision.ListID]
	if !exists || existing.OwnerID != decision.OwnerID || existing.ProjectID != decision.ProjectID {
		return assetlist.List{}, assetlist.ErrNotFound
	}
	if existing.Status == assetlist.StatusExpired {
		return assetlist.List{}, assetlist.ErrExpired
	}
	if existing.Status == assetlist.StatusDraft && !decision.At.Before(existing.ExpiresAt) {
		existing.Status = assetlist.StatusExpired
		existing.UpdatedAt = decision.At
		r.lists[existing.ID] = existing
		return assetlist.List{}, assetlist.ErrExpired
	}
	switch decision.Action {
	case assetlist.DecisionConfirm, assetlist.DecisionReplace:
		if existing.Status == assetlist.StatusConfirmed {
			return r.withItems(existing), nil
		}
	case assetlist.DecisionReject:
		if existing.Status == assetlist.StatusRejected {
			return r.withItems(existing), nil
		}
	}
	if existing.Status != assetlist.StatusDraft {
		return assetlist.List{}, assetlist.ErrConflict
	}
	if decision.Action == assetlist.DecisionReject {
		existing.Status = assetlist.StatusRejected
		existing.DecidedAt = &decision.At
		existing.DecidedBy = decision.OwnerID
		existing.DecisionReason = decision.Reason
		existing.Version++
		existing.UpdatedAt = decision.At
		r.lists[existing.ID] = existing
		return r.withItems(existing), nil
	}
	for id, other := range r.lists {
		if other.OwnerID != decision.OwnerID || other.ProjectID != decision.ProjectID ||
			other.Status != assetlist.StatusConfirmed || other.ID == decision.ListID {
			continue
		}
		if decision.Action == assetlist.DecisionConfirm {
			return assetlist.List{}, &assetlist.ErrActiveConflict{ListID: other.ID, Title: other.Title}
		}
		for _, item := range r.items[other.ID] {
			if item.State == assetlist.StateWorking {
				return assetlist.List{}, &assetlist.ErrActiveWorking{ListID: other.ID}
			}
		}
		other.Status = assetlist.StatusSuperseded
		other.SupersededBy = existing.ID
		other.Version++
		other.UpdatedAt = decision.At
		r.lists[id] = other
		break
	}
	existing.Status = assetlist.StatusConfirmed
	existing.DecidedAt = &decision.At
	existing.DecidedBy = decision.OwnerID
	existing.DecisionReason = decision.Reason
	existing.Version++
	existing.UpdatedAt = decision.At
	r.lists[existing.ID] = existing
	return r.withItems(existing), nil
}

func (r *memoryRepository) DecideItems(_ context.Context, batch assetlist.DecisionBatch) (assetlist.List, error) {
	existing, exists := r.lists[batch.ListID]
	if !exists || existing.OwnerID != batch.OwnerID || existing.ProjectID != batch.ProjectID {
		return assetlist.List{}, assetlist.ErrNotFound
	}
	if existing.Version != batch.ExpectedVersion {
		return assetlist.List{}, assetlist.ErrConflict
	}
	if r.decisionFail != nil {
		// The transaction wrote nothing, assets included.
		return assetlist.List{}, r.decisionFail
	}
	items := r.items[batch.ListID]
	for _, decision := range batch.Items {
		index := -1
		for position := range items {
			if items[position].ID == decision.ItemID {
				index = position
				break
			}
		}
		if index < 0 {
			return assetlist.List{}, assetlist.ErrNotFound
		}
		item := items[index]
		if item.State != assetlist.StateReview {
			return assetlist.List{}, assetlist.ErrItemState
		}
		switch decision.Verdict {
		case assetlist.ReviewVerdictApproved:
			item.State = assetlist.StateAccepted
			item.ProjectAssetID = decision.ProjectAssetID
			item.AcceptedAt = &batch.At
			item.ReturnReason, item.ReturnNote = "", ""
		case assetlist.ReviewVerdictReturned:
			item.State = assetlist.StateReturned
			item.Round++
			item.ReturnCount++
			item.WorkRound = 0
			item.ReturnReason = decision.Reason
			item.ReturnNote = decision.Note
		default:
			return assetlist.List{}, assetlist.ErrInvalid
		}
		item.Version++
		item.UpdatedAt = batch.At
		items[index] = item
	}
	for _, asset := range batch.Assets {
		if _, exists := r.assets[asset.ID]; !exists {
			r.assets[asset.ID] = asset
		}
	}
	existing.Version++
	existing.UpdatedAt = batch.At
	r.lists[existing.ID] = existing
	r.items[existing.ID] = items
	return r.withItems(existing), nil
}

func (r *memoryRepository) put(list assetlist.List, items []assetlist.Item) {
	list.Items = nil
	r.lists[list.ID] = list
	r.items[list.ID] = cloneItems(items)
}

func (r *memoryRepository) withItems(list assetlist.List) assetlist.List {
	list.Items = cloneItems(r.items[list.ID])
	sumItems(&list, list.Items)
	return list
}

func sumItems(list *assetlist.List, items []assetlist.Item) {
	steps, model3dSteps := 0, 0
	for _, item := range items {
		steps += item.PlannedStepCount
		model3dSteps += item.PlannedModel3DStepCount
	}
	list.PlannedStepCount = steps
	list.PlannedModel3DStepCount = model3dSteps
}

func cloneItems(items []assetlist.Item) []assetlist.Item {
	result := make([]assetlist.Item, len(items))
	for index, item := range items {
		result[index] = item
		result[index].References = append([]project.Reference(nil), item.References...)
		result[index].Route = append([]assetlist.RouteStep(nil), item.Route...)
		result[index].Result = append(json.RawMessage(nil), item.Result...)
		result[index].Review = append(json.RawMessage(nil), item.Review...)
	}
	return result
}

func testSchema() catalog.AssetListSchema {
	return catalog.AssetListSchema{
		Limits: catalog.AssetListLimitsSchema{
			MaxListsPerProject:   20,
			DraftTTLSeconds:      604800,
			NameMaxLength:        40,
			DescriptionMaxLength: 200,
			MaxReferencesPerItem: 2,
		},
		Kinds: []catalog.AssetListKindSchema{
			{ID: "character", Label: "Character", ProjectKind: "character", DefaultRoute: "text-to-3d"},
			{ID: "image", Label: "Image", ProjectKind: "other", DefaultRoute: "text-to-image"},
		},
		Routes: []catalog.AssetListRouteSchema{
			{ID: "text-to-3d", Label: "Text to 3D", Kinds: []string{"character"},
				Steps: []catalog.AssetListStepSchema{{Capability: "text-to-3d"}}},
			{ID: "text-to-3d-rig", Label: "Text to 3D then rig", Kinds: []string{"character"},
				Steps: []catalog.AssetListStepSchema{{Capability: "text-to-3d"}, {Capability: "rigging"}}},
			{ID: "text-to-image", Label: "Text to image", Kinds: []string{"image"},
				Steps: []catalog.AssetListStepSchema{{Capability: "text-to-image"}}},
			{ID: "reference-to-3d", Label: "Reference to 3D", Kinds: []string{"character"},
				Steps: []catalog.AssetListStepSchema{
					{Capability: "image-to-image"},
					{Capability: "image-to-multiview"},
					{Capability: "image-to-3d"},
					{Capability: "remesh"},
				}},
		},
	}
}

type testClock struct {
	value time.Time
}

func (c *testClock) Now() time.Time { return c.value }

func (c *testClock) Advance(duration time.Duration) { c.value = c.value.Add(duration) }

func newService(t *testing.T, mutate func(*assetlist.Config)) (*assetlist.Service, *memoryRepository, *testClock) {
	t.Helper()
	repository := newMemoryRepository()
	clock := &testClock{value: time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)}
	config := assetlist.Config{
		Repository:      repository,
		Schema:          testSchema(),
		ProjectMaxItems: 3,
		Counted: map[generation.Capability]bool{
			"text-to-3d":  true,
			"rigging":     true,
			"image-to-3d": true,
			"remesh":      true,
		},
		Now: clock.Now,
		NewID: func(prefix string) (string, error) {
			return prefix + strconv.FormatUint(testIDCounter.Add(1), 10), nil
		},
	}
	if mutate != nil {
		mutate(&config)
	}
	service, err := assetlist.New(config)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	return service, repository, clock
}

func proposal() assetlist.ProposeInput {
	return assetlist.ProposeInput{
		Owner:           "owner-a",
		ProjectID:       "prj_1",
		SessionID:       "ses_1",
		OriginMessageID: "msg_1",
		RequestKey:      "req_1",
		Title:           "Chapter one",
		Items: []assetlist.ItemInput{
			{Kind: "character", Name: "Hero"},
		},
	}
}

func TestProposeValidatesInput(t *testing.T) {
	t.Parallel()
	reference := project.Reference{InputAssetID: "input_1"}
	cases := []struct {
		name   string
		mutate func(*assetlist.ProposeInput)
		want   error
	}{
		{"empty title", func(input *assetlist.ProposeInput) { input.Title = "  " }, assetlist.ErrInvalid},
		{"no items", func(input *assetlist.ProposeInput) { input.Items = nil }, assetlist.ErrInvalid},
		{"too many items", func(input *assetlist.ProposeInput) {
			input.Items = []assetlist.ItemInput{
				{Kind: "character", Name: "A"}, {Kind: "character", Name: "B"},
				{Kind: "character", Name: "C"}, {Kind: "character", Name: "D"},
			}
		}, assetlist.ErrLimit},
		{"duplicate name", func(input *assetlist.ProposeInput) {
			input.Items = []assetlist.ItemInput{
				{Kind: "character", Name: "Hero"}, {Kind: "image", Name: " hero "},
			}
		}, assetlist.ErrInvalid},
		{"empty name", func(input *assetlist.ProposeInput) { input.Items[0].Name = " " }, assetlist.ErrInvalid},
		{"unknown kind", func(input *assetlist.ProposeInput) { input.Items[0].Kind = "creature" }, assetlist.ErrInvalid},
		{"unknown route", func(input *assetlist.ProposeInput) { input.Items[0].RouteID = "missing" }, assetlist.ErrInvalid},
		{"route does not serve kind", func(input *assetlist.ProposeInput) {
			input.Items[0].Kind = "image"
			input.Items[0].RouteID = "text-to-3d"
		}, assetlist.ErrInvalid},
		{"too many references", func(input *assetlist.ProposeInput) {
			input.Items[0].References = []project.Reference{reference, reference, reference}
		}, assetlist.ErrLimit},
		{"invalid reference", func(input *assetlist.ProposeInput) {
			input.Items[0].References = []project.Reference{{TaskID: "task"}}
		}, assetlist.ErrInvalid},
	}
	for _, testCase := range cases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			service, _, _ := newService(t, nil)
			input := proposal()
			testCase.mutate(&input)
			if _, err := service.Propose(context.Background(), input); !errors.Is(err, testCase.want) {
				t.Fatalf("Propose error = %v; want %v", err, testCase.want)
			}
		})
	}
}

func TestProposeRequiresProjectAndSessionMembership(t *testing.T) {
	t.Parallel()
	service, _, _ := newService(t, nil)
	input := proposal()
	input.ProjectID = "prj_missing"
	if _, err := service.Propose(context.Background(), input); !errors.Is(err, assetlist.ErrProjectNotFound) {
		t.Fatalf("unknown project error = %v", err)
	}
	input = proposal()
	input.SessionID = "ses_missing"
	if _, err := service.Propose(context.Background(), input); !errors.Is(err, assetlist.ErrSessionNotInProject) {
		t.Fatalf("unknown session error = %v", err)
	}
	input = proposal()
	input.SessionID = "ses_2"
	if _, err := service.Propose(context.Background(), input); !errors.Is(err, assetlist.ErrSessionNotInProject) {
		t.Fatalf("session in another project error = %v", err)
	}
	input = proposal()
	input.SessionID = "ses_own"
	if _, err := service.Propose(context.Background(), input); !errors.Is(err, assetlist.ErrSessionNotInProject) {
		t.Fatalf("foreign session error = %v", err)
	}
}

func TestProposeReplayDoesNotCreateOrRevive(t *testing.T) {
	t.Parallel()
	service, repository, _ := newService(t, nil)
	ctx := context.Background()
	first, err := service.Propose(ctx, proposal())
	if err != nil {
		t.Fatalf("first propose: %v", err)
	}
	if _, err := service.Decide(ctx, assetlist.DecideInput{
		Owner: "owner-a", ProjectID: "prj_1", ListID: first.ID, Decision: assetlist.DecisionConfirm,
	}); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	replay := proposal()
	replay.Title = "A different title"
	second, err := service.Propose(ctx, replay)
	if err != nil {
		t.Fatalf("replayed propose: %v", err)
	}
	if second.ID != first.ID || second.Status != assetlist.StatusConfirmed || second.Title != "Chapter one" {
		t.Fatalf("replay = %+v", second)
	}
	if len(repository.lists) != 1 {
		t.Fatalf("replay created %d lists; want 1", len(repository.lists))
	}
}

func TestProposeSupersedesOtherDrafts(t *testing.T) {
	t.Parallel()
	service, repository, _ := newService(t, nil)
	ctx := context.Background()
	first, err := service.Propose(ctx, proposal())
	if err != nil {
		t.Fatalf("first propose: %v", err)
	}
	secondInput := proposal()
	secondInput.RequestKey = "req_2"
	secondInput.Title = "Chapter two"
	second, err := service.Propose(ctx, secondInput)
	if err != nil {
		t.Fatalf("second propose: %v", err)
	}
	stored := repository.lists[first.ID]
	if stored.Status != assetlist.StatusSuperseded || stored.SupersededBy != second.ID {
		t.Fatalf("old draft = %+v", stored)
	}
	if repository.lists[second.ID].Status != assetlist.StatusDraft {
		t.Fatalf("new draft status = %q", repository.lists[second.ID].Status)
	}
}

func TestProposeEnforcesProjectListLimit(t *testing.T) {
	t.Parallel()
	service, repository, clock := newService(t, func(config *assetlist.Config) {
		config.MaxListsPerProject = 20
	})
	ctx := context.Background()
	for index := 0; index < 19; index++ {
		seeded := assetlist.List{
			ID: fmt.Sprintf("alist_seed_%d", index), RequestKey: fmt.Sprintf("seed_%d", index),
			OwnerID: "owner-a", ProjectID: "prj_1", Title: "Seed",
			Status: assetlist.StatusDraft, CreatedAt: clock.Now(), UpdatedAt: clock.Now(),
			ExpiresAt: clock.Now().Add(time.Hour),
		}
		repository.put(seeded, nil)
	}
	repository.put(assetlist.List{
		ID: "alist_confirmed", RequestKey: "seed_confirmed", OwnerID: "owner-a",
		ProjectID: "prj_1", Title: "Confirmed", Status: assetlist.StatusConfirmed,
		CreatedAt: clock.Now(), UpdatedAt: clock.Now(), ExpiresAt: clock.Now().Add(time.Hour),
	}, nil)
	if live, err := repository.CountLiveLists(ctx, "owner-a", "prj_1"); err != nil || live != 20 {
		t.Fatalf("live lists = %d, %v", live, err)
	}
	if _, err := service.Propose(ctx, proposal()); !errors.Is(err, assetlist.ErrLimit) {
		t.Fatalf("proposal at the limit error = %v", err)
	}
	if live, err := repository.CountLiveLists(ctx, "owner-a", "prj_1"); err != nil || live != 20 {
		t.Fatalf("refused proposal changed live lists: %d, %v", live, err)
	}
	// Drafts that already reached a terminal status stop consuming the quota.
	for id, seeded := range repository.lists {
		if seeded.Status == assetlist.StatusDraft {
			seeded.Status = assetlist.StatusSuperseded
			repository.lists[id] = seeded
		}
	}
	if live, err := repository.CountLiveLists(ctx, "owner-a", "prj_1"); err != nil || live != 1 {
		t.Fatalf("live lists after supersede = %d, %v", live, err)
	}
	if _, err := service.Propose(ctx, proposal()); err != nil {
		t.Fatalf("proposal after supersede: %v", err)
	}
	if live, err := repository.CountLiveLists(ctx, "owner-a", "prj_1"); err != nil || live != 2 {
		t.Fatalf("live lists after proposal = %d, %v", live, err)
	}
}

func TestProposeNonPositiveListLimitIsUnlimited(t *testing.T) {
	t.Parallel()
	service, repository, clock := newService(t, nil)
	for index := 0; index < 20; index++ {
		repository.put(assetlist.List{
			ID: fmt.Sprintf("alist_seed_%d", index), RequestKey: fmt.Sprintf("seed_%d", index),
			OwnerID: "owner-a", ProjectID: "prj_1", Title: "Seed",
			Status: assetlist.StatusDraft, CreatedAt: clock.Now(), UpdatedAt: clock.Now(),
			ExpiresAt: clock.Now().Add(time.Hour),
		}, nil)
	}
	if _, err := service.Propose(context.Background(), proposal()); err != nil {
		t.Fatalf("proposal without a limit: %v", err)
	}
}

func TestProposePlansStepsFromCountedCapabilities(t *testing.T) {
	t.Parallel()
	service, _, _ := newService(t, nil)
	input := proposal()
	input.Items = []assetlist.ItemInput{
		{Kind: "character", Name: "Rigged", RouteID: "text-to-3d-rig"},
		{Kind: "image", Name: "Icon"},
	}
	list, err := service.Propose(context.Background(), input)
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	if len(list.Items) != 2 {
		t.Fatalf("items = %+v", list.Items)
	}
	if list.Items[0].PlannedStepCount != 2 || list.Items[0].PlannedModel3DStepCount != 2 {
		t.Fatalf("3D item plan = %+v", list.Items[0])
	}
	if list.Items[1].PlannedStepCount != 1 || list.Items[1].PlannedModel3DStepCount != 0 {
		t.Fatalf("2D item plan = %+v", list.Items[1])
	}
	if list.PlannedStepCount != 3 || list.PlannedModel3DStepCount != 2 {
		t.Fatalf("list plan = %d/%d", list.PlannedStepCount, list.PlannedModel3DStepCount)
	}
}

func TestProposeKeepsUnavailableSteps(t *testing.T) {
	t.Parallel()
	service, _, _ := newService(t, func(config *assetlist.Config) {
		config.ResolveStep = func(capability string) (assetlist.RouteStep, error) {
			if capability == "rigging" {
				return assetlist.RouteStep{}, errors.New("rigging provider is down")
			}
			return assetlist.RouteStep{Capability: capability, OfferingID: "off_1", Available: false,
				Reason: "model disabled"}, nil
		}
	})
	input := proposal()
	input.Items = []assetlist.ItemInput{{Kind: "character", Name: "Rigged", RouteID: "text-to-3d-rig"}}
	list, err := service.Propose(context.Background(), input)
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	route := list.Items[0].Route
	if len(route) != 2 {
		t.Fatalf("route = %+v", route)
	}
	if route[0].Available || route[0].OfferingID != "off_1" || route[0].Reason != "model disabled" {
		t.Fatalf("first step = %+v", route[0])
	}
	if route[1].Available || route[1].Reason != "rigging provider is down" {
		t.Fatalf("second step = %+v", route[1])
	}
}

func TestProposeValidatesBrief(t *testing.T) {
	t.Parallel()
	service, _, _ := newService(t, nil)
	input := proposal()
	input.Brief = json.RawMessage(`{`)
	if _, err := service.Propose(context.Background(), input); !errors.Is(err, assetlist.ErrInvalid) {
		t.Fatalf("invalid JSON brief error = %v", err)
	}
	input = proposal()
	input.Brief = json.RawMessage(`{"purpose":"icon"}`)
	stored, err := service.Propose(context.Background(), input)
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	if string(stored.Brief) != `{"purpose":"icon"}` {
		t.Fatalf("brief = %s", stored.Brief)
	}
	rejecting, _, _ := newService(t, func(config *assetlist.Config) {
		config.ValidateBrief = func(json.RawMessage) error { return errors.New("unknown field") }
	})
	input = proposal()
	if _, err := rejecting.Propose(context.Background(), input); !errors.Is(err, assetlist.ErrInvalid) {
		t.Fatalf("rejected brief error = %v", err)
	}
}

func TestEditRequiresDraftAndVersion(t *testing.T) {
	t.Parallel()
	service, repository, clock := newService(t, nil)
	ctx := context.Background()
	list, err := service.Propose(ctx, proposal())
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	edited, err := service.Edit(ctx, assetlist.EditInput{
		Owner: "owner-a", ProjectID: "prj_1", ListID: list.ID, ExpectedVersion: 0,
		Title: "Edited",
		Items: []assetlist.ItemInput{{Kind: "image", Name: "Icon"}},
		Brief: json.RawMessage(`{"purpose":"icon"}`),
	})
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	if edited.Version != 1 || edited.Title != "Edited" || len(edited.Items) != 1 ||
		edited.Items[0].Name != "Icon" || string(edited.Brief) != `{"purpose":"icon"}` {
		t.Fatalf("edited = %+v", edited)
	}
	if _, err := service.Edit(ctx, assetlist.EditInput{
		Owner: "owner-a", ProjectID: "prj_1", ListID: list.ID, ExpectedVersion: 0,
		Title: "Stale", Items: []assetlist.ItemInput{{Kind: "image", Name: "Icon"}},
	}); !errors.Is(err, assetlist.ErrConflict) {
		t.Fatalf("stale edit error = %v", err)
	}
	if _, err := service.Edit(ctx, assetlist.EditInput{
		Owner: "owner-b", ProjectID: "prj_1", ListID: list.ID, ExpectedVersion: 1,
		Title: "Foreign", Items: []assetlist.ItemInput{{Kind: "image", Name: "Icon"}},
	}); !errors.Is(err, assetlist.ErrNotFound) {
		t.Fatalf("foreign edit error = %v", err)
	}
	if _, err := service.Edit(ctx, assetlist.EditInput{
		Owner: "owner-a", ProjectID: "prj_1", ListID: "alist_missing", ExpectedVersion: 0,
		Title: "Missing", Items: []assetlist.ItemInput{{Kind: "image", Name: "Icon"}},
	}); !errors.Is(err, assetlist.ErrNotFound) {
		t.Fatalf("missing edit error = %v", err)
	}
	if _, err := service.Decide(ctx, assetlist.DecideInput{
		Owner: "owner-a", ProjectID: "prj_1", ListID: list.ID, Decision: assetlist.DecisionConfirm,
	}); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if _, err := service.Edit(ctx, assetlist.EditInput{
		Owner: "owner-a", ProjectID: "prj_1", ListID: list.ID, ExpectedVersion: 2,
		Title: "Confirmed", Items: []assetlist.ItemInput{{Kind: "image", Name: "Icon"}},
	}); !errors.Is(err, assetlist.ErrConflict) {
		t.Fatalf("confirmed edit error = %v", err)
	}
	expiring, err := service.Propose(ctx, func() assetlist.ProposeInput {
		input := proposal()
		input.RequestKey = "req_expiring"
		return input
	}())
	if err != nil {
		t.Fatalf("propose expiring: %v", err)
	}
	clock.Advance(8 * 24 * time.Hour)
	if _, err := service.Edit(ctx, assetlist.EditInput{
		Owner: "owner-a", ProjectID: "prj_1", ListID: expiring.ID, ExpectedVersion: 0,
		Title: "Too late", Items: []assetlist.ItemInput{{Kind: "image", Name: "Icon"}},
	}); !errors.Is(err, assetlist.ErrExpired) {
		t.Fatalf("expired edit error = %v", err)
	}
	if repository.lists[expiring.ID].Status != assetlist.StatusExpired {
		t.Fatalf("expired edit did not store the status: %+v", repository.lists[expiring.ID])
	}
}

func TestDecideConfirmRejectAndReplace(t *testing.T) {
	t.Parallel()
	service, repository, clock := newService(t, nil)
	ctx := context.Background()
	first, err := service.Propose(ctx, proposal())
	if err != nil {
		t.Fatalf("propose first: %v", err)
	}
	confirmed, err := service.Decide(ctx, assetlist.DecideInput{
		Owner: "owner-a", ProjectID: "prj_1", ListID: first.ID, Decision: assetlist.DecisionConfirm,
	})
	if err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if confirmed.Status != assetlist.StatusConfirmed || confirmed.EffectiveStatus != assetlist.StatusConfirmed ||
		confirmed.DecidedBy != "owner-a" || confirmed.DecidedAt == nil {
		t.Fatalf("confirmed = %+v", confirmed)
	}
	if again, err := service.Decide(ctx, assetlist.DecideInput{
		Owner: "owner-a", ProjectID: "prj_1", ListID: first.ID, Decision: assetlist.DecisionConfirm,
	}); err != nil || again.ID != first.ID || again.Version != confirmed.Version {
		t.Fatalf("repeat confirm = %+v, %v", again, err)
	}

	secondInput := proposal()
	secondInput.RequestKey = "req_2"
	secondInput.Title = "Chapter two"
	second, err := service.Propose(ctx, secondInput)
	if err != nil {
		t.Fatalf("propose second: %v", err)
	}
	_, err = service.Decide(ctx, assetlist.DecideInput{
		Owner: "owner-a", ProjectID: "prj_1", ListID: second.ID, Decision: assetlist.DecisionConfirm,
	})
	var active *assetlist.ErrActiveConflict
	if !errors.As(err, &active) || active.ListID != first.ID || active.Title != "Chapter one" {
		t.Fatalf("active conflict = %+v (%v)", active, err)
	}
	if !errors.Is(err, &assetlist.ErrActiveConflict{}) {
		t.Fatalf("active conflict is not errors.Is-compatible: %v", err)
	}
	replaced, err := service.Decide(ctx, assetlist.DecideInput{
		Owner: "owner-a", ProjectID: "prj_1", ListID: second.ID, Decision: assetlist.DecisionReplace,
	})
	if err != nil {
		t.Fatalf("replace: %v", err)
	}
	if replaced.Status != assetlist.StatusConfirmed {
		t.Fatalf("replacement status = %q", replaced.Status)
	}
	old := repository.lists[first.ID]
	if old.Status != assetlist.StatusSuperseded || old.SupersededBy != second.ID {
		t.Fatalf("old confirmed = %+v", old)
	}

	thirdInput := proposal()
	thirdInput.RequestKey = "req_3"
	thirdInput.Title = "Chapter three"
	third, err := service.Propose(ctx, thirdInput)
	if err != nil {
		t.Fatalf("propose third: %v", err)
	}
	rejected, err := service.Decide(ctx, assetlist.DecideInput{
		Owner: "owner-a", ProjectID: "prj_1", ListID: third.ID,
		Decision: assetlist.DecisionReject, Reason: "not this quarter",
	})
	if err != nil {
		t.Fatalf("reject: %v", err)
	}
	if rejected.Status != assetlist.StatusRejected || rejected.DecisionReason != "not this quarter" {
		t.Fatalf("rejected = %+v", rejected)
	}
	if again, err := service.Decide(ctx, assetlist.DecideInput{
		Owner: "owner-a", ProjectID: "prj_1", ListID: third.ID, Decision: assetlist.DecisionReject,
	}); err != nil || again.ID != third.ID {
		t.Fatalf("repeat reject = %+v, %v", again, err)
	}
	if _, err := service.Decide(ctx, assetlist.DecideInput{
		Owner: "owner-a", ProjectID: "prj_1", ListID: third.ID, Decision: "approve",
	}); !errors.Is(err, assetlist.ErrInvalid) {
		t.Fatalf("invalid decision error = %v", err)
	}
	clock.Advance(time.Minute)
}

func TestDecideReplaceRefusesWorkingItems(t *testing.T) {
	t.Parallel()
	service, repository, clock := newService(t, nil)
	ctx := context.Background()
	confirmed := assetlist.List{
		ID: "alist_confirmed", RequestKey: "req_confirmed", OwnerID: "owner-a",
		ProjectID: "prj_1", Title: "Running", Status: assetlist.StatusConfirmed,
		CreatedAt: clock.Now(), UpdatedAt: clock.Now(), ExpiresAt: clock.Now().Add(time.Hour),
	}
	repository.put(confirmed, []assetlist.Item{{
		ID: "aitem_running", OwnerID: "owner-a", Kind: "character", Name: "Hero",
		State: assetlist.StateWorking, PlannedStepCount: 1, PlannedModel3DStepCount: 1,
	}})
	draft, err := service.Propose(ctx, proposal())
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	_, err = service.Decide(ctx, assetlist.DecideInput{
		Owner: "owner-a", ProjectID: "prj_1", ListID: draft.ID, Decision: assetlist.DecisionReplace,
	})
	var working *assetlist.ErrActiveWorking
	if !errors.As(err, &working) || working.ListID != confirmed.ID {
		t.Fatalf("working guard = %+v (%v)", working, err)
	}
	if repository.lists[draft.ID].Status != assetlist.StatusDraft {
		t.Fatalf("refused replace changed the draft: %+v", repository.lists[draft.ID])
	}
	repository.items[confirmed.ID][0].State = assetlist.StateTodo
	if _, err := service.Decide(ctx, assetlist.DecideInput{
		Owner: "owner-a", ProjectID: "prj_1", ListID: draft.ID, Decision: assetlist.DecisionReplace,
	}); err != nil {
		t.Fatalf("replace after resolving the item: %v", err)
	}
}

func TestDecideExpiredDraftIsRecorded(t *testing.T) {
	t.Parallel()
	service, repository, clock := newService(t, nil)
	ctx := context.Background()
	list, err := service.Propose(ctx, proposal())
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	clock.Advance(8 * 24 * time.Hour)
	if _, err := service.Decide(ctx, assetlist.DecideInput{
		Owner: "owner-a", ProjectID: "prj_1", ListID: list.ID, Decision: assetlist.DecisionConfirm,
	}); !errors.Is(err, assetlist.ErrExpired) {
		t.Fatalf("expired decision error = %v", err)
	}
	if repository.lists[list.ID].Status != assetlist.StatusExpired {
		t.Fatalf("expiry was not stored: %+v", repository.lists[list.ID])
	}
	read, err := service.Get(ctx, "owner-a", "prj_1", list.ID)
	if err != nil || read.Status != assetlist.StatusExpired || read.EffectiveStatus != assetlist.StatusExpired {
		t.Fatalf("expired read = %+v, %v", read, err)
	}
}

func TestGetAndListDeriveExpiryAndFilterOwner(t *testing.T) {
	t.Parallel()
	service, _, clock := newService(t, nil)
	ctx := context.Background()
	list, err := service.Propose(ctx, proposal())
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	clock.Advance(8 * 24 * time.Hour)
	read, err := service.Get(ctx, "owner-a", "prj_1", list.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if read.Status != assetlist.StatusDraft || read.EffectiveStatus != assetlist.StatusExpired {
		t.Fatalf("derived expiry = %+v", read)
	}
	summaries, err := service.List(ctx, "owner-a", "prj_1")
	if err != nil || len(summaries) != 1 {
		t.Fatalf("list = %+v, %v", summaries, err)
	}
	if summaries[0].ID != list.ID || summaries[0].EffectiveStatus != assetlist.StatusExpired ||
		summaries[0].PlannedStepCount != 1 || summaries[0].PlannedModel3DStepCount != 1 {
		t.Fatalf("summary = %+v", summaries[0])
	}
	if _, err := service.Get(ctx, "owner-b", "prj_1", list.ID); !errors.Is(err, assetlist.ErrNotFound) {
		t.Fatalf("foreign get error = %v", err)
	}
	if _, err := service.List(ctx, "owner-b", "prj_1"); err != nil {
		t.Fatalf("foreign list error = %v", err)
	}
	if result, err := service.List(ctx, "owner-b", "prj_1"); err != nil || len(result) != 0 {
		t.Fatalf("foreign list = %+v, %v", result, err)
	}
	if _, err := service.Decide(ctx, assetlist.DecideInput{
		Owner: "owner-b", ProjectID: "prj_1", ListID: list.ID, Decision: assetlist.DecisionReject,
	}); !errors.Is(err, assetlist.ErrNotFound) {
		t.Fatalf("foreign decide error = %v", err)
	}
}

func TestNewRejectsIncompleteConfig(t *testing.T) {
	t.Parallel()
	base := assetlist.Config{
		Repository:      newMemoryRepository(),
		Schema:          testSchema(),
		ProjectMaxItems: 3,
	}
	if _, err := assetlist.New(assetlist.Config{}); !errors.Is(err, assetlist.ErrInvalid) {
		t.Fatalf("empty config error = %v", err)
	}
	broken := base
	broken.ProjectMaxItems = 0
	if _, err := assetlist.New(broken); !errors.Is(err, assetlist.ErrInvalid) {
		t.Fatalf("missing item limit error = %v", err)
	}
	broken = base
	broken.Schema.Kinds = nil
	if _, err := assetlist.New(broken); !errors.Is(err, assetlist.ErrInvalid) {
		t.Fatalf("missing kinds error = %v", err)
	}
	stepPolicy := assetlist.Config{
		Repository:      newMemoryRepository(),
		Schema:          testSchema(),
		ProjectMaxItems: 3,
		StepPolicy: &catalog.StepPolicySchema{
			CountedCapabilities: []generation.Capability{"text-to-3d"},
		},
	}
	service, err := assetlist.New(stepPolicy)
	if err != nil {
		t.Fatalf("step policy config: %v", err)
	}
	list, err := service.Propose(context.Background(), proposal())
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	if list.PlannedModel3DStepCount != 1 {
		t.Fatalf("step policy counted = %d", list.PlannedModel3DStepCount)
	}
}

// stubRunStore answers only the item-session lookup; the embedded nil
// interface keeps the rest of the run port out of these tests.
type stubRunStore struct {
	assetlist.RunStore
	item      assetlist.Item
	found     bool
	err       error
	ownerID   string
	sessionID string
}

func (s *stubRunStore) ItemBySession(_ context.Context, ownerID, sessionID string) (assetlist.Item, bool, error) {
	s.ownerID, s.sessionID = ownerID, sessionID
	if s.err != nil {
		return assetlist.Item{}, false, s.err
	}
	if !s.found {
		return assetlist.Item{}, false, nil
	}
	return s.item, true, nil
}

// RunSummaries keeps list reads undecorated in this fixture.
func (s *stubRunStore) RunSummaries(_ context.Context, _, _ string) (map[string]assetlist.RunSummary, error) {
	return nil, nil
}

// ItemBySession is the item-session lookup the chat derivation uses: it trims
// the ids, forwards to the run store and never turns a missing row into an
// error.
func TestServiceItemBySessionForwardsToTheRunStore(t *testing.T) {
	runs := &stubRunStore{item: assetlist.Item{ID: "aitem_1", SessionID: "ses_item"}, found: true}
	service, _, _ := newService(t, func(config *assetlist.Config) { config.Runs = runs })
	item, found, err := service.ItemBySession(context.Background(), " owner-a ", " ses_item ")
	if err != nil || !found || item.ID != "aitem_1" || runs.ownerID != "owner-a" || runs.sessionID != "ses_item" {
		t.Fatalf("item = %+v, %v, %v; forwarded %q/%q", item, found, err, runs.ownerID, runs.sessionID)
	}

	runs.found = false
	if item, found, err := service.ItemBySession(context.Background(), "owner-a", "ses_plain"); err != nil || found || item.ID != "" {
		t.Fatalf("ordinary session = %+v, %v, %v; want no item", item, found, err)
	}

	runs.err = errors.New("database is locked")
	if _, _, err := service.ItemBySession(context.Background(), "owner-a", "ses_item"); !errors.Is(err, runs.err) {
		t.Fatalf("read failure = %v; want the store error", err)
	}

	// No run store (no runner assembled) and blank ids are a miss, not a read.
	plain, _, _ := newService(t, nil)
	if _, found, err := plain.ItemBySession(context.Background(), "owner-a", "ses_item"); err != nil || found {
		t.Fatalf("no run store = %v, %v; want no item", found, err)
	}
	runs.err, runs.found = nil, true
	runs.ownerID, runs.sessionID = "unchanged", "unchanged"
	if _, found, err := service.ItemBySession(context.Background(), "", "ses_item"); err != nil || found {
		t.Fatalf("blank owner = %v, %v; want no item", found, err)
	}
	if runs.ownerID != "unchanged" {
		t.Fatalf("blank owner reached the store: %q", runs.ownerID)
	}
}
