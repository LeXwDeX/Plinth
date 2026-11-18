package assetlist

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/project"
)

// Config is the Schema-derived policy and the runtime ports of the service.
// ProjectMaxItems is application.project.limits.maximum_list_items, the
// existing per-list item bound (the asset_list section deliberately declares
// no second name for it).
type Config struct {
	Repository Repository
	// Runs reads the execution state, including which item a sub-session
	// belongs to (#328). nil leaves ItemBySession reporting "no item", which is
	// the safe answer for a deployment without the runner.
	Runs RunStore
	// Presence reads the project's unexpired heartbeats (#328). nil, or an
	// execution section without presence limits, leaves the submission gate
	// without a presence check.
	Presence PresenceStore
	// Reviews reads the recorded acceptance decisions of the board (#329a).
	// nil leaves every item with an empty latest review.
	Reviews ReviewStore
	// References resolves input-asset references against the owner's stored
	// assets and crop provenance (#331), so proposals and edits share one
	// validation. nil keeps the #327 shape checks only.
	References      ReferenceAssets
	Schema          catalog.AssetListSchema
	ProjectMaxItems int
	// MaxListsPerProject is asset_list.limits.max_lists_per_project: the
	// draft+confirmed lists one project keeps. Terminal lists (rejected,
	// expired, superseded) do not count. <= 0 means unlimited.
	MaxListsPerProject int
	// Counted names the capabilities whose planned Tasks count as 3D steps.
	// StepPolicy is a convenience: when Counted is nil its
	// count_capabilities list is used. Both empty means every step is 2D.
	Counted    map[generation.Capability]bool
	StepPolicy *catalog.StepPolicySchema
	// ResolveStep snapshots one capability's runtime offering. nil marks every
	// step available without an offering id, which is what tests and catalogs
	// without runtime offerings use. An error marks the step unavailable with
	// the error text as its reason; it never blocks the proposal.
	ResolveStep func(capability string) (RouteStep, error)
	// ResolveStepFor resolves one capability for a known owner and session and
	// takes precedence over ResolveStep. The session is the list's proposal
	// session: its explicit model picks shape the offering the step snapshots.
	ResolveStepFor func(ctx context.Context, ownerID, sessionID, capability string) (RouteStep, error)
	// ValidateBrief checks the shared production brief against
	// chat.model3d_usage.brief.fields. nil only checks that it is JSON.
	ValidateBrief func(json.RawMessage) error
	Now           func() time.Time
	NewID         func(prefix string) (string, error)
}

// Service implements the list workflow over a Repository.
type Service struct {
	repository      Repository
	runs            RunStore
	presence        PresenceStore
	reviews         ReviewStore
	referenceAssets ReferenceAssets
	// imageItemLimit is application.asset_list.decompose.maximum_items_per_image
	// (#331); 0 leaves the proposal-time per-image bound unwired.
	imageItemLimit     int
	presenceGrace      time.Duration
	executionEnabled   bool
	limits             catalog.AssetListLimitsSchema
	projectMaxItems    int
	maxListsPerProject int
	kinds              map[string]catalog.AssetListKindSchema
	routes             map[string]catalog.AssetListRouteSchema
	counted            map[generation.Capability]bool
	resolveStep        func(capability string) (RouteStep, error)
	resolveStepFor     func(ctx context.Context, ownerID, sessionID, capability string) (RouteStep, error)
	validateBrief      func(json.RawMessage) error
	clock              func() time.Time
	newID              func(prefix string) (string, error)
}

// New builds the service. A Catalog that declares application.asset_list is
// fully validated by the Catalog layer, so missing limits, kinds or routes
// here mean the application was wired with the wrong section.
func New(config Config) (*Service, error) {
	if config.Repository == nil {
		return nil, fmt.Errorf("%w: repository", ErrInvalid)
	}
	if config.ProjectMaxItems <= 0 {
		return nil, fmt.Errorf("%w: project item limit", ErrInvalid)
	}
	limits := config.Schema.Limits
	if limits.NameMaxLength <= 0 || limits.DescriptionMaxLength <= 0 ||
		limits.MaxReferencesPerItem <= 0 || limits.DraftTTLSeconds <= 0 {
		return nil, fmt.Errorf("%w: asset list limits", ErrInvalid)
	}
	kinds := make(map[string]catalog.AssetListKindSchema, len(config.Schema.Kinds))
	for _, kind := range config.Schema.Kinds {
		kinds[kind.ID] = kind
	}
	if len(kinds) == 0 {
		return nil, fmt.Errorf("%w: no asset list kinds", ErrInvalid)
	}
	routes := make(map[string]catalog.AssetListRouteSchema, len(config.Schema.Routes))
	for _, route := range config.Schema.Routes {
		routes[route.ID] = route
	}
	if len(routes) == 0 {
		return nil, fmt.Errorf("%w: no asset list routes", ErrInvalid)
	}
	counted := make(map[generation.Capability]bool, len(config.Counted))
	for capability, value := range config.Counted {
		counted[capability] = value
	}
	if config.Counted == nil && config.StepPolicy != nil {
		for _, capability := range config.StepPolicy.CountedCapabilities {
			counted[capability] = true
		}
	}
	clock := config.Now
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	newID := config.NewID
	if newID == nil {
		newID = randomID
	}
	var presenceGrace time.Duration
	executionEnabled := config.Schema.Execution != nil && config.Schema.Execution.Enabled
	if config.Schema.Execution != nil {
		presenceGrace = time.Duration(config.Schema.Execution.Presence.GraceSeconds) * time.Second
	}
	var imageItemLimit int
	if config.Schema.Decompose != nil {
		imageItemLimit = config.Schema.Decompose.MaximumItemsPerImage
	}
	return &Service{
		repository:         config.Repository,
		runs:               config.Runs,
		presence:           config.Presence,
		reviews:            config.Reviews,
		referenceAssets:    config.References,
		imageItemLimit:     imageItemLimit,
		presenceGrace:      presenceGrace,
		executionEnabled:   executionEnabled,
		limits:             limits,
		projectMaxItems:    config.ProjectMaxItems,
		maxListsPerProject: config.MaxListsPerProject,
		kinds:              kinds,
		routes:             routes,
		counted:            counted,
		resolveStep:        config.ResolveStep,
		resolveStepFor:     config.ResolveStepFor,
		validateBrief:      config.ValidateBrief,
		clock:              clock,
		newID:              newID,
	}, nil
}

func randomID(prefix string) (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(value[:]), nil
}

// Propose validates and stores one draft list for the Agent's session. The
// session must currently belong to the project: the proposal tool has no
// project_id parameter, so a session outside the project is refused instead of
// guessing. A replayed request_key returns the stored list untouched
// (whatever state it reached meanwhile); it never creates or revives a list.
// A project already at MaxListsPerProject live (draft+confirmed) lists is
// refused with ErrLimit before any draft is superseded.
func (service *Service) Propose(ctx context.Context, input ProposeInput) (List, error) {
	ownerID := strings.TrimSpace(input.Owner)
	projectID := strings.TrimSpace(input.ProjectID)
	sessionID := strings.TrimSpace(input.SessionID)
	requestKey := strings.TrimSpace(input.RequestKey)
	if ownerID == "" || projectID == "" || sessionID == "" || requestKey == "" {
		return List{}, fmt.Errorf("%w: owner, project, session and request_key are required", ErrInvalid)
	}
	existing, found, err := service.repository.FindByRequestKey(ctx, ownerID, requestKey)
	if err != nil {
		return List{}, err
	}
	if found {
		return service.decorate(existing), nil
	}
	exists, err := service.repository.ProjectExists(ctx, ownerID, projectID)
	if err != nil {
		return List{}, err
	}
	if !exists {
		return List{}, ErrProjectNotFound
	}
	sessionProject, err := service.repository.SessionProject(ctx, ownerID, sessionID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return List{}, ErrSessionNotInProject
		}
		return List{}, err
	}
	if sessionProject != projectID {
		return List{}, ErrSessionNotInProject
	}
	title, err := service.title(input.Title)
	if err != nil {
		return List{}, err
	}
	now := service.clock()
	items, err := service.buildItems(ctx, ownerID, sessionID, input.Items, now)
	if err != nil {
		return List{}, err
	}
	brief, err := service.brief(input.Brief)
	if err != nil {
		return List{}, err
	}
	id, err := service.newID("alist_")
	if err != nil {
		return List{}, err
	}
	list := List{
		ID:              id,
		RequestKey:      requestKey,
		OwnerID:         ownerID,
		ProjectID:       projectID,
		SourceSessionID: sessionID,
		OriginMessageID: strings.TrimSpace(input.OriginMessageID),
		Title:           title,
		Status:          StatusDraft,
		Brief:           brief,
		Version:         0,
		ExpiresAt:       now.Add(time.Duration(service.limits.DraftTTLSeconds) * time.Second),
		CreatedAt:       now,
		UpdatedAt:       now,
		Items:           items,
	}
	stored, err := service.repository.Propose(ctx, list, items, service.maxListsPerProject)
	if err != nil {
		return List{}, err
	}
	return service.decorate(stored), nil
}

// Edit replaces a draft's title, brief and items under an expected_version
// CAS. Confirmed and other terminal lists are never edited; an expired draft
// reports ErrExpired and the adapter records the expiry.
func (service *Service) Edit(ctx context.Context, input EditInput) (List, error) {
	ownerID := strings.TrimSpace(input.Owner)
	projectID := strings.TrimSpace(input.ProjectID)
	listID := strings.TrimSpace(input.ListID)
	if ownerID == "" || projectID == "" || listID == "" {
		return List{}, ErrNotFound
	}
	if input.ExpectedVersion < 0 {
		return List{}, fmt.Errorf("%w: expected_version", ErrInvalid)
	}
	title, err := service.title(input.Title)
	if err != nil {
		return List{}, err
	}
	now := service.clock()
	items, err := service.buildItems(ctx, ownerID, strings.TrimSpace(input.SessionID), input.Items, now)
	if err != nil {
		return List{}, err
	}
	brief, err := service.brief(input.Brief)
	if err != nil {
		return List{}, err
	}
	stored, err := service.repository.EditList(ctx, List{
		ID:        listID,
		OwnerID:   ownerID,
		ProjectID: projectID,
		Title:     title,
		Brief:     brief,
		UpdatedAt: now,
	}, items, input.ExpectedVersion)
	if err != nil {
		return List{}, err
	}
	return service.decorate(stored), nil
}

// Decide confirms, rejects or replaces a draft. Confirming while another list
// is confirmed is ErrActiveConflict: the person must explicitly replace it.
// Repeating the same decision returns the current list (a double click decides
// once); any other status is ErrConflict. An expired draft is stored as
// expired and reports ErrExpired.
func (service *Service) Decide(ctx context.Context, input DecideInput) (List, error) {
	ownerID := strings.TrimSpace(input.Owner)
	projectID := strings.TrimSpace(input.ProjectID)
	listID := strings.TrimSpace(input.ListID)
	action := strings.TrimSpace(input.Decision)
	if ownerID == "" || projectID == "" || listID == "" {
		return List{}, ErrNotFound
	}
	switch action {
	case DecisionConfirm, DecisionReject, DecisionReplace:
	default:
		return List{}, fmt.Errorf("%w: decision %q", ErrInvalid, action)
	}
	stored, err := service.repository.Decide(ctx, Decision{
		OwnerID:   ownerID,
		ProjectID: projectID,
		ListID:    listID,
		Action:    action,
		Reason:    strings.TrimSpace(input.Reason),
		At:        service.clock(),
	})
	if err != nil {
		return List{}, err
	}
	// A confirmed list gets its run here, best-effort: failing to store the run
	// must not undo the person's decision. The runner creates it lazily anyway
	// (#328), so a lost write only delays automatic production.
	if stored.Status == StatusConfirmed && service.executionEnabled {
		if _, err := service.EnsureRun(ctx, ownerID, projectID, stored.ID); err != nil {
			slog.Warn("asset list run was not created after confirm",
				"owner_id", ownerID, "project_id", projectID, "list_id", stored.ID, "error", err)
		}
	}
	return service.decorate(stored), nil
}

// Get reads one list with its items, owner and project filtered. The stored
// status is unchanged when a draft's TTL passed; EffectiveStatus says expired.
func (service *Service) Get(ctx context.Context, ownerID, projectID, listID string) (List, error) {
	ownerID = strings.TrimSpace(ownerID)
	projectID = strings.TrimSpace(projectID)
	listID = strings.TrimSpace(listID)
	if ownerID == "" || projectID == "" || listID == "" {
		return List{}, ErrNotFound
	}
	list, err := service.repository.GetList(ctx, ownerID, projectID, listID)
	if err != nil {
		return List{}, err
	}
	summaries, err := service.runSummaries(ctx, ownerID, projectID)
	if err != nil {
		return List{}, err
	}
	return service.decorateWithRun(list, summaries), nil
}

// List reads the project's lists in update order as summaries: status, counts
// and effective status, without items.
func (service *Service) List(ctx context.Context, ownerID, projectID string) ([]List, error) {
	ownerID = strings.TrimSpace(ownerID)
	projectID = strings.TrimSpace(projectID)
	if ownerID == "" || projectID == "" {
		return nil, ErrNotFound
	}
	lists, err := service.repository.ListLists(ctx, ownerID, projectID)
	if err != nil {
		return nil, err
	}
	summaries, err := service.runSummaries(ctx, ownerID, projectID)
	if err != nil {
		return nil, err
	}
	for index := range lists {
		lists[index] = service.decorateWithRun(lists[index], summaries)
	}
	return lists, nil
}

// ProjectExists reports whether the owner has the project. The transport layer
// uses it to answer a missing project with 404 instead of an empty list.
func (service *Service) ProjectExists(ctx context.Context, ownerID, projectID string) (bool, error) {
	ownerID = strings.TrimSpace(ownerID)
	projectID = strings.TrimSpace(projectID)
	if ownerID == "" || projectID == "" {
		return false, nil
	}
	return service.repository.ProjectExists(ctx, ownerID, projectID)
}

// ListSession reads the lists one session proposed as summaries. The session
// must be the owner's; a session outside a project has no lists.
func (service *Service) ListSession(ctx context.Context, ownerID, sessionID string) ([]List, error) {
	ownerID = strings.TrimSpace(ownerID)
	sessionID = strings.TrimSpace(sessionID)
	if ownerID == "" || sessionID == "" {
		return nil, ErrNotFound
	}
	projectID, err := service.repository.SessionProject(ctx, ownerID, sessionID)
	if err != nil {
		return nil, err
	}
	if projectID == "" {
		return nil, nil
	}
	lists, err := service.repository.ListLists(ctx, ownerID, projectID)
	if err != nil {
		return nil, err
	}
	summaries, err := service.runSummaries(ctx, ownerID, projectID)
	if err != nil {
		return nil, err
	}
	result := make([]List, 0, len(lists))
	for _, list := range lists {
		if list.SourceSessionID != sessionID {
			continue
		}
		result = append(result, service.decorateWithRun(list, summaries))
	}
	return result, nil
}

// ItemBySession reports the item whose sub-session is sessionID, owner-scoped
// (#328). false is an ordinary session, not an error; a read failure is
// returned so callers never guess a session's role.
func (service *Service) ItemBySession(
	ctx context.Context,
	ownerID string,
	sessionID string,
) (Item, bool, error) {
	ownerID = strings.TrimSpace(ownerID)
	sessionID = strings.TrimSpace(sessionID)
	if ownerID == "" || sessionID == "" || service.runs == nil {
		return Item{}, false, nil
	}
	return service.runs.ItemBySession(ctx, ownerID, sessionID)
}

// ItemByID reads one item of the owner's list (#328). A missing, foreign or
// mismatched list/item pair is ErrNotFound; a read failure is returned. A
// deployment without a run store has no items.
func (service *Service) ItemByID(
	ctx context.Context,
	ownerID string,
	listID string,
	itemID string,
) (Item, error) {
	ownerID = strings.TrimSpace(ownerID)
	listID = strings.TrimSpace(listID)
	itemID = strings.TrimSpace(itemID)
	if ownerID == "" || listID == "" || itemID == "" || service.runs == nil {
		return Item{}, ErrNotFound
	}
	return service.runs.ItemByID(ctx, ownerID, listID, itemID)
}

// RunByList reads the execution state of the owner's confirmed list (#328).
// A missing run, list or project is ErrNotFound.
func (service *Service) RunByList(
	ctx context.Context,
	ownerID string,
	projectID string,
	listID string,
) (Run, error) {
	ownerID = strings.TrimSpace(ownerID)
	projectID = strings.TrimSpace(projectID)
	listID = strings.TrimSpace(listID)
	if ownerID == "" || projectID == "" || listID == "" || service.runs == nil {
		return Run{}, ErrNotFound
	}
	return service.runs.GetRunByList(ctx, ownerID, projectID, listID)
}

// FinishItem applies one terminal item transition (#328): working→review,
// failed or skipped. A missing item or a state conflict is reported by the run
// store; the completion tool maps it to its Schema refusal.
func (service *Service) FinishItem(
	ctx context.Context,
	input FinishItemInput,
) (Item, error) {
	if service.runs == nil {
		return Item{}, ErrNotFound
	}
	return service.runs.FinishItem(ctx, input)
}

// decorateWithRun derives the read-time list fields and attaches the list's
// run summary when it has one (#328). A list without a run keeps Run nil.
func (service *Service) decorateWithRun(list List, summaries map[string]RunSummary) List {
	list = service.decorate(list)
	if summary, found := summaries[list.ID]; found {
		list.Run = &summary
	}
	return list
}

// decorate derives the read-time effective status and, when the items are
// loaded, the item count and plan totals. A draft at exactly its expiry
// instant is expired.
func (service *Service) decorate(list List) List {
	list.EffectiveStatus = list.Status
	if list.Status == StatusDraft && !list.ExpiresAt.IsZero() && !service.clock().Before(list.ExpiresAt) {
		list.EffectiveStatus = StatusExpired
	}
	if list.Items != nil {
		list.ItemCount = len(list.Items)
		steps, model3dSteps := 0, 0
		for _, item := range list.Items {
			steps += item.PlannedStepCount
			model3dSteps += item.PlannedModel3DStepCount
		}
		list.PlannedStepCount = steps
		list.PlannedModel3DStepCount = model3dSteps
	}
	return list
}

func (service *Service) title(raw string) (string, error) {
	title := strings.TrimSpace(raw)
	if title == "" {
		return "", fmt.Errorf("%w: title is required", ErrInvalid)
	}
	if utf8.RuneCountInString(title) > service.limits.NameMaxLength {
		return "", fmt.Errorf("%w: title is too long", ErrInvalid)
	}
	return title, nil
}

func (service *Service) brief(raw json.RawMessage) (json.RawMessage, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = json.RawMessage("{}")
	}
	if !json.Valid(raw) {
		return nil, fmt.Errorf("%w: brief is not JSON", ErrInvalid)
	}
	if service.validateBrief != nil {
		if err := service.validateBrief(raw); err != nil {
			return nil, fmt.Errorf("%w: brief: %v", ErrInvalid, err)
		}
	}
	return append(json.RawMessage(nil), raw...), nil
}

// buildItems validates the whole item set before any row is written: the
// project's item bound, unique names (trimmed, case-insensitive), kinds and
// routes from the Schema, the per-item reference bound, shape and (when the
// port is wired) the owner's stored assets and crop provenance, and the
// availability snapshot of every route step.
func (service *Service) buildItems(ctx context.Context, ownerID, sessionID string, inputs []ItemInput, now time.Time) ([]Item, error) {
	if len(inputs) == 0 {
		return nil, fmt.Errorf("%w: at least one item is required", ErrInvalid)
	}
	if len(inputs) > service.projectMaxItems {
		return nil, fmt.Errorf("%w: at most %d items per list", ErrLimit, service.projectMaxItems)
	}
	seen := make(map[string]bool, len(inputs))
	items := make([]Item, 0, len(inputs))
	// The crop rules are list-wide: one crop per item, one source picture per
	// list and at most maximum_items_per_image items cut from one source. The
	// scope carries the source across the item loop.
	crops := &cropReferenceScope{maximum: service.imageItemLimit}
	for index, input := range inputs {
		crops.resetItem()
		kindID := strings.TrimSpace(input.Kind)
		kind, exists := service.kinds[kindID]
		if !exists {
			return nil, fmt.Errorf("%w: item %d has unknown kind %q", ErrInvalid, index+1, kindID)
		}
		name := strings.TrimSpace(input.Name)
		if name == "" {
			return nil, fmt.Errorf("%w: item %d needs a name", ErrInvalid, index+1)
		}
		if utf8.RuneCountInString(name) > service.limits.NameMaxLength {
			return nil, fmt.Errorf("%w: item %d name is too long", ErrInvalid, index+1)
		}
		key := strings.ToLower(name)
		if seen[key] {
			return nil, fmt.Errorf("%w: duplicate item name %q", ErrInvalid, name)
		}
		seen[key] = true
		description := strings.TrimSpace(input.Description)
		if utf8.RuneCountInString(description) > service.limits.DescriptionMaxLength {
			return nil, fmt.Errorf("%w: item %d description is too long", ErrInvalid, index+1)
		}
		routeID := strings.TrimSpace(input.RouteID)
		if routeID == "" {
			routeID = kind.DefaultRoute
		}
		route, exists := service.routes[routeID]
		if !exists {
			return nil, fmt.Errorf("%w: item %d has unknown route %q", ErrInvalid, index+1, routeID)
		}
		if !slices.Contains(route.Kinds, kindID) {
			return nil, fmt.Errorf("%w: route %q does not serve kind %q", ErrInvalid, routeID, kindID)
		}
		references, err := service.references(ctx, ownerID, input.References, crops)
		if err != nil {
			return nil, err
		}
		steps := make([]RouteStep, 0, len(route.Steps))
		model3dSteps := 0
		for _, step := range route.Steps {
			capability := string(step.Capability)
			resolved := RouteStep{Capability: capability, Available: true}
			switch {
			case service.resolveStepFor != nil && sessionID != "":
				if value, err := service.resolveStepFor(ctx, ownerID, sessionID, capability); err != nil {
					resolved.Available = false
					resolved.Reason = err.Error()
				} else {
					resolved = value
					resolved.Capability = capability
				}
			case service.resolveStep != nil:
				if value, err := service.resolveStep(capability); err != nil {
					resolved.Available = false
					resolved.Reason = err.Error()
				} else {
					resolved = value
					resolved.Capability = capability
				}
			}
			if service.counted[step.Capability] {
				model3dSteps++
			}
			steps = append(steps, resolved)
		}
		id, err := service.newID("aitem_")
		if err != nil {
			return nil, err
		}
		items = append(items, Item{
			ID:                      id,
			OwnerID:                 ownerID,
			Kind:                    kindID,
			Name:                    name,
			Description:             description,
			References:              references,
			RouteID:                 routeID,
			Route:                   steps,
			PlannedStepCount:        len(steps),
			PlannedModel3DStepCount: model3dSteps,
			State:                   StateTodo,
			Version:                 0,
			Result:                  json.RawMessage("{}"),
			Review:                  json.RawMessage("{}"),
			CreatedAt:               now,
			UpdatedAt:               now,
		})
	}
	return items, nil
}

// references bounds and shape-checks the item's references and, when the
// ReferenceAssets port is wired, validates every input-asset reference against
// the owner's stored assets and crop provenance (#331). Task-shaped references
// keep the #327 contract. Proposals and edits share this implementation, so a
// bare references list on an edit PUT cannot bypass the crop rules.
func (service *Service) references(ctx context.Context, ownerID string,
	values []project.Reference, crops *cropReferenceScope,
) ([]project.Reference, error) {
	if len(values) > service.limits.MaxReferencesPerItem {
		return nil, fmt.Errorf("%w: at most %d references per item", ErrLimit, service.limits.MaxReferencesPerItem)
	}
	result := make([]project.Reference, 0, len(values))
	for _, reference := range values {
		if !reference.Valid() {
			return nil, invalidReference("a reference must name exactly one source")
		}
		if reference.InputAssetID != "" {
			if err := service.validateInputReference(ctx, ownerID, reference, crops); err != nil {
				return nil, err
			}
		}
		result = append(result, reference)
	}
	return result, nil
}
