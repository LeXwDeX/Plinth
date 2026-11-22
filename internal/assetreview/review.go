// Package assetreview builds the acceptance snapshot of a production-list item
// entering review (#329b). It resolves the purpose thresholds through the chat
// service (the same budget inspect_model and the plan card use), inspects the
// result's GLB and cover picture, and copies the persisted self-check and step
// summary. It never decides anything: the snapshot is a fact record the person
// reads before accepting or returning the item.
package assetreview

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/approvalpolicy"
	"github.com/LeXwDeX/Plinth/internal/assetlist"
	"github.com/LeXwDeX/Plinth/internal/budget"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/imagereview"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/modelcheck"
	"github.com/LeXwDeX/Plinth/internal/modelinspect"
)

// ModelInspector reads one saved GLB for its owner; the generation tool
// executor implements it with the same parse limits and cache as inspect_model.
type ModelInspector interface {
	ModelReport(ctx context.Context, owner generation.OwnerID,
		taskID generation.TaskID, assetID generation.AssetID) (modelinspect.Report, error)
}

// ImageInspector runs the delivery checklist over one saved picture; the chat
// image review service implements it.
type ImageInspector interface {
	InspectOwned(ctx context.Context, request imagereview.InspectRequest) (imagereview.Inspection, error)
}

// SelfChecks reads the persisted self-check records of one sub-session.
type SelfChecks interface {
	ListSelfChecks(ctx context.Context, ownerID, sessionID string) ([]chat.SelfCheckRecord, error)
}

// Checks freezes the item's 3D acceptance checks from the list brief. The chat
// service implements it with the purpose budget, so the review thresholds are
// never hardcoded here.
type Checks func(ctx context.Context, ownerID, sessionID string, brief json.RawMessage) []budget.PlanCheck

// StepUsage reads a session's 3D step standing under the person's setting
// (#330). nil falls back to counting the sub-session's terminal 3D tasks.
type StepUsage func(ctx context.Context, ownerID, sessionID string) (executiongrant.StepSummary, error)

// Config is the snapshot builder's sources.
type Config struct {
	Checks          Checks
	Models          ModelInspector
	Images          ImageInspector
	SelfChecks      SelfChecks
	SelfCheckSchema catalog.ChatSelfCheckSchema
	Steps           StepUsage
	Counted         map[generation.Capability]bool
	Clock           func() time.Time
}

// Service builds review snapshots.
type Service struct {
	checks          Checks
	models          ModelInspector
	images          ImageInspector
	selfChecks      SelfChecks
	selfCheckSchema catalog.ChatSelfCheckSchema
	steps           StepUsage
	counted         map[generation.Capability]bool
	clock           func() time.Time
}

// New builds the builder. Checks and Models may be nil: the snapshot then
// holds no 3D judgment, which reads as unknown rather than pass.
func New(config Config) *Service {
	clock := config.Clock
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	counted := make(map[generation.Capability]bool, len(config.Counted))
	for capability, value := range config.Counted {
		counted[capability] = value
	}
	return &Service{
		checks:          config.Checks,
		models:          config.Models,
		images:          config.Images,
		selfChecks:      config.SelfChecks,
		selfCheckSchema: config.SelfCheckSchema,
		steps:           config.Steps,
		counted:         counted,
		clock:           clock,
	}
}

// Snapshot builds the review_json document of one item entering review.
//
// The snapshot records the files the source Task has now, so a caller must not
// build it while that Task's delivery is still unstable: a 3D success can owe
// formats as post-success resources (#457), and the formats that arrive later
// (or that an automatic completion finishes, #461) would be missing from the
// snapshot and from the artifacts it lists. The completion tool defers such a
// report instead of entering review (#459); this builder takes the Task as it
// is and never decides when it is called.
func (service *Service) Snapshot(
	ctx context.Context,
	input assetlist.ReviewSnapshotInput,
) (json.RawMessage, error) {
	at := input.At
	if at.IsZero() {
		at = service.clock()
	}
	snapshot := assetlist.ReviewSnapshot{
		CapturedAt: at.UTC(),
		Round:      input.Round,
		TaskID:     string(input.Task.ID),
		Artifacts:  reviewArtifacts(input.Task.Assets),
		Steps:      service.stepSummary(ctx, input),
	}
	snapshot.CoverAssetID = coverAsset(input.Task.Assets)
	modelAsset := firstModelAsset(input.Task.Assets)
	imageAsset := firstImageAsset(input.Task.Assets)
	checks := service.acceptanceChecks(ctx, input)
	if modelAsset != "" {
		snapshot.Model3D = service.judgeModel(ctx, input, modelAsset, checks)
	} else if len(checks) > 0 {
		snapshot.Model3D = &assetlist.ReviewModel3D{
			Checks: unknownChecks(checks, "no_glb"),
		}
	}
	if imageAsset != "" {
		snapshot.Image2D = service.judgeImage(ctx, input, imageAsset)
	}
	snapshot.SelfCheck = service.selfCheck(ctx, input)
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return nil, fmt.Errorf("encode review snapshot: %w", err)
	}
	return encoded, nil
}

// acceptanceChecks resolves the declared thresholds; a deployment without the
// port has no 3D criteria, not a hidden default.
func (service *Service) acceptanceChecks(
	ctx context.Context,
	input assetlist.ReviewSnapshotInput,
) []budget.PlanCheck {
	if service.checks == nil {
		return nil
	}
	return service.checks(ctx, input.OwnerID, input.SessionID, input.Brief)
}

// judgeModel inspects the GLB and evaluates every declared check. A report
// that cannot be read leaves every check unknown: infrastructure trouble is
// visible, never a pass.
func (service *Service) judgeModel(
	ctx context.Context,
	input assetlist.ReviewSnapshotInput,
	assetID string,
	checks []budget.PlanCheck,
) *assetlist.ReviewModel3D {
	judgment := &assetlist.ReviewModel3D{AssetID: assetID}
	if service.models == nil || len(checks) == 0 {
		judgment.Checks = unknownChecks(checks, "model_inspection_unavailable")
		return judgment
	}
	report, err := service.models.ModelReport(ctx, generation.OwnerID(input.OwnerID),
		input.Task.ID, generation.AssetID(assetID))
	if err != nil {
		judgment.Checks = unknownChecks(checks, "unreadable")
		return judgment
	}
	results := modelcheck.Evaluate(report, checks)
	judgment.Checks = make([]assetlist.ReviewCheck, 0, len(results))
	for index, result := range results {
		budget := ""
		if index < len(checks) {
			budget = budgetText(checks[index])
		}
		judgment.Checks = append(judgment.Checks, assetlist.ReviewCheck{
			Check: result.Check, Status: result.Status, Actual: result.Actual,
			Budget: budget, Detail: result.Detail,
		})
	}
	return judgment
}

// judgeImage runs the delivery checklist over the cover picture. A read
// failure is one unknown check, not a failed delivery.
func (service *Service) judgeImage(
	ctx context.Context,
	input assetlist.ReviewSnapshotInput,
	assetID string,
) *assetlist.ReviewImage2D {
	judgment := &assetlist.ReviewImage2D{AssetID: assetID}
	if service.images == nil {
		judgment.Checks = []assetlist.ReviewCheck{{
			Check: "readable", Status: "unknown", Detail: "image_inspection_unavailable",
		}}
		return judgment
	}
	inspection, err := service.images.InspectOwned(ctx, imagereview.InspectRequest{
		Owner: generation.OwnerID(input.OwnerID),
		Source: imagereview.PictureSource{
			TaskID: input.Task.ID, TaskAsset: generation.AssetID(assetID),
		},
		Checklist: "delivery",
		Brief:     chat.ProductionBrief(productionBriefFields(input.Brief)),
	})
	if err != nil {
		judgment.Checks = []assetlist.ReviewCheck{{
			Check: "readable", Status: "unknown", Detail: "unreadable",
		}}
		return judgment
	}
	judgment.Width = inspection.Report.Width
	judgment.Height = inspection.Report.Height
	judgment.Checks = make([]assetlist.ReviewCheck, 0, len(inspection.Checks))
	for _, check := range inspection.Checks {
		judgment.Checks = append(judgment.Checks, assetlist.ReviewCheck{
			Check: check.Name, Status: check.Status, Actual: check.Actual,
			Budget: check.Expected, Detail: check.Hint,
		})
	}
	return judgment
}

// selfCheck copies the persisted conclusion of the reported Task. A record
// without a conclusion reads unchecked.
func (service *Service) selfCheck(
	ctx context.Context,
	input assetlist.ReviewSnapshotInput,
) *assetlist.ReviewSelfCheck {
	if service.selfChecks == nil {
		return nil
	}
	records, err := service.selfChecks.ListSelfChecks(ctx, input.OwnerID, input.SessionID)
	if err != nil {
		return nil
	}
	for _, record := range records {
		if record.TaskID != string(input.Task.ID) {
			continue
		}
		counts := chat.SelfCheckCounts(service.selfCheckSchema, record)
		result := &assetlist.ReviewSelfCheck{Status: counts.StatusRole}
		if record.Conclusion != nil {
			result.Summary = record.Conclusion.Summary
			result.Remaining = append([]string(nil), record.Conclusion.Remaining...)
		}
		return result
	}
	return nil
}

// stepSummary counts the sub-session's terminal Tasks as steps and reads the
// 3D standing from the execution grant when the port exists.
func (service *Service) stepSummary(
	ctx context.Context,
	input assetlist.ReviewSnapshotInput,
) assetlist.ReviewSteps {
	summary := assetlist.ReviewSteps{Used: len(input.Tasks)}
	for _, task := range input.Tasks {
		if service.counted[task.Capability] {
			summary.Model3D++
		}
	}
	if service.steps == nil {
		return summary
	}
	standing, err := service.steps(ctx, input.OwnerID, input.SessionID)
	if err != nil {
		return summary
	}
	summary.Model3D = standing.Used
	summary.Mode = standing.Mode
	if standing.Mode == approvalpolicy.StepModeCap {
		// Only the cap mode has a limit; the agent and unlimited modes keep a
		// remembered cap number in the setting, which is not applied.
		summary.Cap = standing.Cap
		summary.Extra = standing.Extra
	}
	return summary
}

// reviewArtifacts copies the stored result files. The snapshot records only
// references and public facts, never bytes.
func reviewArtifacts(assets []generation.Asset) []assetlist.ReviewArtifact {
	artifacts := make([]assetlist.ReviewArtifact, 0, len(assets))
	for _, asset := range assets {
		if asset.StorageKey == "" {
			continue
		}
		artifacts = append(artifacts, assetlist.ReviewArtifact{
			AssetID: string(asset.ID), Role: asset.Role, Variant: asset.Variant,
			MediaType: asset.MediaType, Size: asset.Size, SHA256: asset.SHA256,
		})
	}
	return artifacts
}

func coverAsset(assets []generation.Asset) string {
	if id := firstImageAsset(assets); id != "" {
		return id
	}
	return firstModelAsset(assets)
}

// firstModelAsset picks the model the board judges and previews. Stored assets
// are ordered by role, which puts converted_model (FBX/OBJ) before model, so
// the order is explicit: the GLB of the task's own model role first, then any
// GLB, then the model role's other files. The GLB is the file the inspector
// can actually read; a conversion output is a delivery copy, not the judged
// result.
func firstModelAsset(assets []generation.Asset) string {
	candidates := []func(generation.Asset) bool{
		func(asset generation.Asset) bool {
			return asset.Role == "model" && asset.MediaType == "model/gltf-binary"
		},
		func(asset generation.Asset) bool {
			return asset.MediaType == "model/gltf-binary"
		},
		func(asset generation.Asset) bool {
			return asset.Role == "model" && strings.HasPrefix(asset.MediaType, "model/")
		},
		func(asset generation.Asset) bool {
			return strings.HasPrefix(asset.MediaType, "model/")
		},
	}
	for _, matches := range candidates {
		for _, asset := range assets {
			if asset.StorageKey != "" && matches(asset) {
				return string(asset.ID)
			}
		}
	}
	return ""
}

func firstImageAsset(assets []generation.Asset) string {
	for _, asset := range assets {
		if asset.StorageKey != "" && strings.HasPrefix(asset.MediaType, "image/") {
			return string(asset.ID)
		}
	}
	return ""
}

// unknownChecks states every declared criterion as unknown with a stable
// reason. An empty declaration yields an empty list.
func unknownChecks(checks []budget.PlanCheck, detail string) []assetlist.ReviewCheck {
	result := make([]assetlist.ReviewCheck, 0, len(checks))
	for _, check := range checks {
		result = append(result, assetlist.ReviewCheck{
			Check: check.Check, Status: modelcheck.StatusUnknown,
			Budget: budgetText(check), Detail: detail,
		})
	}
	return result
}

// budgetText words one frozen threshold for the board's detail view.
func budgetText(check budget.PlanCheck) string {
	parts := make([]string, 0, 3)
	if check.Minimum != nil {
		parts = append(parts, fmt.Sprintf("min %g", *check.Minimum))
	}
	if check.Maximum != nil {
		parts = append(parts, fmt.Sprintf("max %g", *check.Maximum))
	}
	if check.MaximumRatio != nil {
		parts = append(parts, fmt.Sprintf("ratio max %g", *check.MaximumRatio))
	}
	if check.Required {
		parts = append(parts, "required")
	}
	return strings.Join(parts, ", ")
}

// productionBriefFields reads a stored brief object for the image purpose
// lookup; an unreadable value is an empty brief.
func productionBriefFields(raw json.RawMessage) map[string]json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return nil
	}
	return fields
}
