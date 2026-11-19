package assetlist

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/project"
)

// The acceptance decisions of the board (#329b). accept settles one or more
// items and registers each as a project asset; return sends exactly one item
// back for another round of the same sub-session.
const (
	ReviewDecisionAccept = "accept"
	ReviewDecisionReturn = "return"
)

// ErrReworkLimit reports an item that used all its declared return rounds.
// It carries the item so transport layers can fill the Schema copy; it is not
// the project-asset limit, which is reported per accepted item.
var ErrReworkLimit = errors.New("assetlist: rework limit reached")

// ReworkLimitError is the refused return: the item used MaxReworkRounds
// return rounds. It carries the name and round the Schema copy interpolates.
type ReworkLimitError struct {
	Name    string
	Round   int
	Maximum int
}

func (e *ReworkLimitError) Error() string {
	return fmt.Sprintf("assetlist: item %q used its %d rework rounds at round %d", e.Name, e.Maximum, e.Round)
}

func (e *ReworkLimitError) Is(target error) bool { return target == ErrReworkLimit }

// ErrReviewAssetLimit reports that the project could not register the item's
// asset because the per-project asset bound is reached. It is per item: a
// batch accept keeps the other items' outcomes.
var ErrReviewAssetLimit = errors.New("assetlist: project asset limit reached")

// ReviewSnapshot is the review_json captured when an item enters review: the
// produced artifacts and the server's judgment at that instant. It is a fact
// record, never a decision: only the person accepts or returns the item.
type ReviewSnapshot struct {
	CapturedAt   time.Time        `json:"captured_at"`
	Round        int              `json:"round"`
	TaskID       string           `json:"task_id,omitempty"`
	CoverAssetID string           `json:"cover_asset_id,omitempty"`
	Artifacts    []ReviewArtifact `json:"artifacts"`
	// Model3D is present when the results contain a GLB; without one every
	// declared check is unknown, never pass.
	Model3D   *ReviewModel3D   `json:"model3d,omitempty"`
	Image2D   *ReviewImage2D   `json:"image2d,omitempty"`
	SelfCheck *ReviewSelfCheck `json:"self_check,omitempty"`
	Steps     ReviewSteps      `json:"steps"`
}

// ReviewArtifact is one stored result file.
type ReviewArtifact struct {
	AssetID   string `json:"asset_id"`
	Role      string `json:"role,omitempty"`
	Variant   string `json:"variant,omitempty"`
	MediaType string `json:"media_type,omitempty"`
	Size      int64  `json:"size,omitempty"`
	SHA256    string `json:"sha256,omitempty"`
}

// ReviewCheck is one judged acceptance criterion. Status is pass, fail or
// unknown; unknown is a fact (no readable GLB, an unusable threshold), never
// a pass.
type ReviewCheck struct {
	Check  string `json:"check"`
	Status string `json:"status"`
	Actual any    `json:"actual,omitempty"`
	Budget string `json:"budget,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// ReviewModel3D is the 3D judgment of one item. An empty AssetID means the
// results held no GLB and every declared check is unknown.
type ReviewModel3D struct {
	AssetID string        `json:"asset_id,omitempty"`
	Checks  []ReviewCheck `json:"checks"`
}

// ReviewImage2D is the 2D delivery judgment of the item's cover picture.
type ReviewImage2D struct {
	AssetID string        `json:"asset_id,omitempty"`
	Width   int           `json:"width,omitempty"`
	Height  int           `json:"height,omitempty"`
	Checks  []ReviewCheck `json:"checks"`
}

// ReviewSelfCheck is the Agent's persisted self-check conclusion of the
// reported Task, when one exists.
type ReviewSelfCheck struct {
	Status    string   `json:"status,omitempty"`
	Summary   string   `json:"summary,omitempty"`
	Remaining []string `json:"remaining,omitempty"`
}

// ReviewSteps is the step summary: every terminal Task of the sub-session is
// one step, and the 3D ones are counted separately (#330).
type ReviewSteps struct {
	Used    int    `json:"used"`
	Model3D int    `json:"model3d"`
	Cap     int    `json:"cap,omitempty"`
	Mode    string `json:"mode,omitempty"`
}

// ReviewSnapshotInput is one item entering review. The Task is the successful
// result the Agent reported; Tasks are the sub-session's terminal Tasks for
// the step summary. Brief is the frozen production brief the thresholds are
// resolved from.
type ReviewSnapshotInput struct {
	OwnerID   string
	SessionID string
	ProjectID string
	ListID    string
	ItemID    string
	Kind      string
	Brief     json.RawMessage
	Round     int
	Task      generation.Task
	Tasks     []generation.Task
	At        time.Time
}

// ReviewSnapshots builds the acceptance snapshot of an item entering review
// (#329b). The implementation owns the purpose/threshold resolution and the
// model/image inspection; this package hardcodes neither. nil stores an empty
// snapshot, which the board shows as "not judged".
type ReviewSnapshots interface {
	Snapshot(ctx context.Context, input ReviewSnapshotInput) (json.RawMessage, error)
}

// ProjectAssets registers an accepted item as a project asset. It is the
// project service's CreateAsset; a caller-supplied deterministic ID makes a
// retried registration idempotent.
type ProjectAssets interface {
	CreateAsset(ctx context.Context, ownerID, projectID string, input project.AssetInput) (project.Asset, error)
}

// ProjectAssetLimits are the project asset reference bounds
// (application.project.limits): an item result may hold more files than one
// asset accepts, so the accept keeps the first declared ones.
type ProjectAssetLimits struct {
	MaximumAssets int
	MaximumImages int
	MaximumModels int
}

// ReviewConfig installs the write side of the board (#329b). Application
// assembly calls it after the model/image inspection services exist.
type ReviewConfig struct {
	Snapshots     ReviewSnapshots
	ProjectAssets ProjectAssets
	// Limits bounds the project asset registration; zero values mean no
	// references are taken and the project's own 200 bound is unchanged.
	Limits ProjectAssetLimits
}

// SetReview installs the acceptance write ports (#329b). It is optional: a
// deployment without them can still read the board and keeps every item in
// review.
func (service *Service) SetReview(config ReviewConfig) {
	if config.Snapshots != nil {
		service.reviewSnapshots = config.Snapshots
	}
	if config.ProjectAssets != nil {
		service.projectAssets = config.ProjectAssets
	}
	if config.Limits != (ProjectAssetLimits{}) {
		service.projectAssetLimits = config.Limits
	}
}

// ReviewSnapshot builds the snapshot of one item entering review. A nil port
// answers empty, and a port failure is returned to the caller, which stores
// the report anyway.
func (service *Service) ReviewSnapshot(ctx context.Context, input ReviewSnapshotInput) (json.RawMessage, error) {
	if service.reviewSnapshots == nil {
		return nil, nil
	}
	return service.reviewSnapshots.Snapshot(ctx, input)
}

// ReviewInput is one board submission. ItemIDs carries one item for a return
// and one or more for an accept; ExpectedVersion is the list version the
// browser read.
type ReviewInput struct {
	OwnerID         string
	ProjectID       string
	ListID          string
	ReviewerID      string
	Decision        string
	ItemIDs         []string
	ReasonIDs       []string
	Note            string
	ExpectedVersion int64
}

// ReviewItemResult is one item's outcome of a batch. Error is set when the
// item alone could not be decided (asset limit, state); Verdict and
// ProjectAssetID describe a written decision.
type ReviewItemResult struct {
	ItemID         string `json:"item_id"`
	Verdict        string `json:"verdict,omitempty"`
	ProjectAssetID string `json:"project_asset_id,omitempty"`
	Error          string `json:"error,omitempty"`
}

// ReviewOutcome is the refreshed list plus one result per submitted item. A
// partial batch keeps the successful decisions written.
type ReviewOutcome struct {
	List    List
	Results []ReviewItemResult
}

// Review applies one person's accept or return decision (#329b). Only items
// awaiting review are decided; the list version the browser read gates the
// whole batch, so a double click or a stale board is ErrConflict. An accept
// registers each item's deterministic project asset before the decision
// write, so a retry never inserts a second row; a project at its asset bound
// reports that item and leaves it awaiting review. A return bumps the item's
// round, records every picked reason and the note, and reopens a completed
// run so the runner continues the same sub-session.
func (service *Service) Review(ctx context.Context, input ReviewInput) (ReviewOutcome, error) {
	ownerID := strings.TrimSpace(input.OwnerID)
	projectID := strings.TrimSpace(input.ProjectID)
	listID := strings.TrimSpace(input.ListID)
	if ownerID == "" || projectID == "" || listID == "" {
		return ReviewOutcome{}, ErrNotFound
	}
	itemIDs := uniqueStrings(input.ItemIDs)
	if len(itemIDs) == 0 || input.ExpectedVersion < 0 {
		return ReviewOutcome{}, fmt.Errorf("%w: review identity", ErrInvalid)
	}
	decision := strings.TrimSpace(input.Decision)
	switch decision {
	case ReviewDecisionAccept:
	case ReviewDecisionReturn:
		if len(itemIDs) != 1 {
			return ReviewOutcome{}, fmt.Errorf("%w: a return decides one item", ErrInvalid)
		}
	default:
		return ReviewOutcome{}, fmt.Errorf("%w: review decision %q", ErrInvalid, decision)
	}
	list, err := service.repository.GetList(ctx, ownerID, projectID, listID)
	if err != nil {
		return ReviewOutcome{}, err
	}
	if list.Version != input.ExpectedVersion {
		return ReviewOutcome{}, ErrConflict
	}
	byID := make(map[string]Item, len(list.Items))
	for _, item := range list.Items {
		byID[item.ID] = item
	}
	now := service.clock()
	reviewer := strings.TrimSpace(input.ReviewerID)
	if reviewer == "" {
		reviewer = ownerID
	}
	outcome := ReviewOutcome{Results: make([]ReviewItemResult, 0, len(itemIDs))}
	decisions := make([]ItemDecision, 0, len(itemIDs))
	// stateFailure remembers an item that is missing or not awaiting review:
	// a single-item request answers that as a state conflict, while a
	// resource failure (the asset limit) stays a per-item result.
	stateFailure := false
	var returnedContext returnContext
	if decision == ReviewDecisionReturn {
		item, found := byID[itemIDs[0]]
		if !found {
			return ReviewOutcome{}, ErrNotFound
		}
		reasons, labels, err := service.returnReasons(item, input.ReasonIDs)
		if err != nil {
			return ReviewOutcome{}, err
		}
		if err := service.checkReworkLimit(item); err != nil {
			return ReviewOutcome{}, err
		}
		returnedContext = returnContext{reasons: reasons, labels: labels}
	}
	for _, itemID := range itemIDs {
		item, found := byID[itemID]
		if !found {
			if decision == ReviewDecisionReturn {
				return ReviewOutcome{}, ErrNotFound
			}
			outcome.Results = append(outcome.Results, ReviewItemResult{
				ItemID: itemID, Error: "item not found",
			})
			stateFailure = true
			continue
		}
		if item.State != StateReview {
			if decision == ReviewDecisionReturn {
				return ReviewOutcome{}, ErrItemState
			}
			outcome.Results = append(outcome.Results, ReviewItemResult{
				ItemID: itemID, Error: "item is not awaiting review",
			})
			stateFailure = true
			continue
		}
		switch decision {
		case ReviewDecisionReturn:
			reviewID, err := service.newID("arev_")
			if err != nil {
				return ReviewOutcome{}, err
			}
			decisions = append(decisions, ItemDecision{
				ReviewID: reviewID, ItemID: item.ID, Verdict: ReviewVerdictReturned,
				Reasons: returnedContext.reasons, Reason: strings.Join(returnedContext.labels, "、"),
				Note: strings.TrimSpace(input.Note),
			})
		case ReviewDecisionAccept:
			assetID, err := service.registerReviewAsset(ctx, ownerID, projectID, item)
			if err != nil {
				if !errors.Is(err, ErrReviewAssetLimit) {
					return ReviewOutcome{}, err
				}
				outcome.Results = append(outcome.Results, ReviewItemResult{
					ItemID: item.ID, Error: service.assetLimitText(item),
				})
				continue
			}
			reviewID, err := service.newID("arev_")
			if err != nil {
				return ReviewOutcome{}, err
			}
			decisions = append(decisions, ItemDecision{
				ReviewID: reviewID, ItemID: item.ID, Verdict: ReviewVerdictApproved,
				ProjectAssetID: assetID,
			})
		}
	}
	if len(decisions) == 0 {
		// A single item in the wrong state refuses the request; an asset limit
		// and a batch report every item's own reason and write nothing.
		if len(itemIDs) == 1 && stateFailure {
			return ReviewOutcome{}, fmt.Errorf("%w: %s", ErrItemState,
				outcome.Results[0].Error)
		}
		outcome.List = list
		return outcome, nil
	}
	updated, err := service.repository.DecideItems(ctx, DecisionBatch{
		OwnerID: ownerID, ProjectID: projectID, ListID: listID,
		ExpectedVersion: input.ExpectedVersion, ReviewerID: reviewer,
		At: now, Items: decisions,
	})
	if err != nil {
		return ReviewOutcome{}, err
	}
	outcome.List = updated
	written := make(map[string]ItemDecision, len(decisions))
	for _, decided := range decisions {
		written[decided.ItemID] = decided
	}
	for _, itemID := range itemIDs {
		decided, ok := written[itemID]
		if !ok {
			continue
		}
		outcome.Results = append(outcome.Results, ReviewItemResult{
			ItemID: itemID, Verdict: decided.Verdict,
			ProjectAssetID: decided.ProjectAssetID,
		})
	}
	return outcome, nil
}

// returnContext is the validated return of the one item.
type returnContext struct {
	reasons []string
	labels  []string
}

// returnReasons validates the picked reason ids against the Schema: at least
// one, each declared, each visible for the item's kind. It returns the ids and
// their labels for the stored reason copy.
func (service *Service) returnReasons(item Item, raw []string) ([]string, []string, error) {
	if service.review == nil {
		return nil, nil, fmt.Errorf("%w: returning items is not declared", ErrInvalid)
	}
	ids := uniqueStrings(raw)
	if len(ids) == 0 {
		return nil, nil, fmt.Errorf("%w: a return needs a reason", ErrInvalid)
	}
	declared := make(map[string]catalog.AssetListReturnReasonSchema, len(service.review.ReturnReasons))
	for _, reason := range service.review.ReturnReasons {
		declared[reason.ID] = reason
	}
	labels := make([]string, 0, len(ids))
	for _, id := range ids {
		reason, found := declared[id]
		if !found {
			return nil, nil, fmt.Errorf("%w: return reason %q", ErrInvalid, id)
		}
		if len(reason.Kinds) > 0 && !containsString(reason.Kinds, item.Kind) {
			return nil, nil, fmt.Errorf("%w: return reason %q does not apply to %q", ErrInvalid, id, item.Kind)
		}
		labels = append(labels, reason.Label)
	}
	return ids, labels, nil
}

// checkReworkLimit refuses a return once the item used its declared rework
// rounds. Round 1 is the first make, so round N already means N-1 reworks.
func (service *Service) checkReworkLimit(item Item) error {
	if service.review == nil {
		return nil
	}
	maximum := service.review.MaxReworkRounds
	if maximum <= 0 || item.Round <= maximum {
		return nil
	}
	return &ReworkLimitError{Name: item.Name, Round: item.Round, Maximum: maximum}
}

// registerReviewAsset creates the item's deterministic project asset from the
// snapshot's pictures and models. The id is passet_ + sha256(item id, round),
// so an accept retried after a failed decision write returns the existing row.
// A deployment without the port accepts the item without registering.
func (service *Service) registerReviewAsset(
	ctx context.Context,
	ownerID, projectID string,
	item Item,
) (string, error) {
	if service.projectAssets == nil {
		return "", nil
	}
	kind := ""
	if declared, found := service.kinds[item.Kind]; found {
		kind = declared.ProjectKind
	}
	if kind == "" {
		return "", fmt.Errorf("%w: item kind %q has no project asset kind", ErrInvalid, item.Kind)
	}
	snapshot := ReviewSnapshot{}
	if len(item.Review) > 0 {
		_ = json.Unmarshal(item.Review, &snapshot)
	}
	images, models := reviewAssetReferences(snapshot, service.projectAssetLimits)
	id := reviewProjectAssetID(item.ID, item.Round)
	_, err := service.projectAssets.CreateAsset(ctx, ownerID, projectID, project.AssetInput{
		ID:     id,
		Kind:   kind,
		Name:   item.Name,
		Images: images,
		Models: models,
	})
	if err != nil {
		var limitMessage error
		switch {
		case errors.Is(err, project.ErrLimit):
			limitMessage = ErrReviewAssetLimit
		case errors.Is(err, project.ErrInvalid), errors.Is(err, project.ErrReferenceInvalid),
			errors.Is(err, project.ErrReferenceLimit):
			limitMessage = ErrInvalid
		case errors.Is(err, project.ErrNotFound):
			return "", ErrNotFound
		default:
			return "", err
		}
		return "", fmt.Errorf("%w: %v", limitMessage, err)
	}
	return id, nil
}

// assetLimitText is the Schema copy of a project at its asset bound, with the
// item name and the bound filled in.
func (service *Service) assetLimitText(item Item) string {
	text := ""
	if service.review != nil {
		text = service.review.Messages.AssetLimit
	}
	if text == "" {
		text = "the project asset limit was reached"
	}
	return strings.NewReplacer(
		"{name}", item.Name,
		"{maximum}", strconv.Itoa(service.projectAssetLimits.MaximumAssets),
	).Replace(text)
}

// reviewProjectAssetID is the deterministic project asset identity of one
// item version: passet_ + sha256(item id, round).
func reviewProjectAssetID(itemID string, round int) string {
	sum := sha256.Sum256([]byte(itemID + "\x00" + strconv.Itoa(round)))
	return "passet_" + hex.EncodeToString(sum[:])
}

// reviewAssetReferences takes the snapshot's pictures and models in result
// order, bounded by application.project.limits. A snapshot without artifacts
// (an item reported before snapshots existed) registers an asset with no
// references instead of guessing.
func reviewAssetReferences(snapshot ReviewSnapshot, limits ProjectAssetLimits) (images, models []project.Reference) {
	seen := make(map[string]bool, len(snapshot.Artifacts))
	for _, artifact := range snapshot.Artifacts {
		if artifact.AssetID == "" || snapshot.TaskID == "" || seen[artifact.AssetID] {
			continue
		}
		seen[artifact.AssetID] = true
		reference := project.Reference{TaskID: snapshot.TaskID, AssetID: artifact.AssetID}
		switch {
		case strings.HasPrefix(artifact.MediaType, "model/"):
			if limits.MaximumModels <= 0 || len(models) < limits.MaximumModels {
				models = append(models, reference)
			}
		case strings.HasPrefix(artifact.MediaType, "image/"):
			if limits.MaximumImages <= 0 || len(images) < limits.MaximumImages {
				images = append(images, reference)
			}
		}
	}
	return images, models
}

func uniqueStrings(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
