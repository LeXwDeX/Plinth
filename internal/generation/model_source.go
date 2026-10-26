package generation

import (
	"encoding/hex"
	"errors"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/schema"
)

// ModelSourceBinding names the Offering parameter that carries a succeeded
// model Task into a processing step, and the platform ID it carries. It never
// holds an upstream job ID or URL: the kernel resolves the platform Task or
// Asset ID again at PrepareInput.
type ModelSourceBinding struct {
	Parameter string
	// Role is schema.SemanticSourceTask or schema.SemanticSourceAsset.
	Role  schema.ParameterSemantic
	Value string
	// AssetID is the exact saved model selected by an asset-bearing route.
	// A task-assets route still carries the public Task ID in Value.
	AssetID AssetID
}

// BindModelSource decides how a succeeded model Task feeds an Offering from
// the Offering's own input contract: a declared source-task parameter wins
// when its capability, Provider and source-kind restrictions match; otherwise a
// declared source-asset parameter takes one of the Task's saved model Assets.
// Plans and Agent tools share this rule so the
// panel and the chat cannot disagree about which sources are compatible.
//
// chosen restricts asset-bearing routes to one Asset (empty means any). prefer
// orders the candidate Asset variants when chosen is empty; an Asset whose
// variant is not listed is still acceptable after the listed ones.
// A source that satisfies no declared parameter returns an ErrInvalidInput
// error: callers reject the step before anything is frozen or paid.
//
// An upstream_job parameter whose declared age (UpstreamJobMaxAgeSeconds) has
// passed is skipped (#325): the Provider no longer keeps that job, so the
// saved model file is used when the Offering accepts one. When nothing else
// binds, the refusal wraps ErrSourceExpired so every caller can say why.
func BindModelSource(offering Offering, source Task, chosen AssetID, prefer []string) (ModelSourceBinding, error) {
	return BindModelSourceAt(offering, source, chosen, prefer, time.Now())
}

// BindModelSourceAt is BindModelSource judged at now.
func BindModelSourceAt(offering Offering, source Task, chosen AssetID, prefer []string, now time.Time) (ModelSourceBinding, error) {
	if source.State != TaskSucceeded {
		return ModelSourceBinding{}, incompatibleSource("来源任务尚未成功")
	}
	hasModel := false
	for _, asset := range source.Assets {
		if asset.Role == "model" && asset.StorageKey != "" && (chosen == "" || asset.ID == chosen) {
			hasModel = true
			break
		}
	}
	if !hasModel {
		return ModelSourceBinding{}, incompatibleSource("来源任务没有可用的已保存模型文件")
	}
	expired := false
	for _, parameter := range offering.Parameters {
		role, isSource := schema.SourceParameterRole(parameter)
		if !isSource || role != schema.SemanticSourceTask || parameter.Type != "task" {
			continue
		}
		if parameter.TaskSourceKind == schema.TaskSourceAssets {
			if parameter.SameProvider && source.Offering.ProviderID != offering.ProviderID {
				continue
			}
			asset, err := SelectTaskSourceAsset(parameter, source)
			if err != nil || (chosen != "" && chosen != asset.ID) {
				continue
			}
			return ModelSourceBinding{Parameter: parameter.Name, Role: schema.SemanticSourceTask, Value: string(source.ID), AssetID: asset.ID}, nil
		}
		// A local delivery completion has no Provider job; the kernel rejects
		// an upstream dependency on it, so only its saved files can bind.
		if source.DeliveryCompleted {
			continue
		}
		// The original Task reference carries the Provider's own job.
		if parameter.TaskSourceKind != "" && parameter.TaskSourceKind != schema.TaskSourceUpstreamJob {
			continue
		}
		if source.Offering.ProviderID != offering.ProviderID || !taskOfferingAllowed(parameter.TaskOfferings, source.Offering.ID) {
			continue
		}
		if !taskCapabilityAllowed(parameter.TaskCapabilities, source.Capability) {
			continue
		}
		if UpstreamJobExpired(parameter, source, now) {
			expired = true
			continue
		}
		return ModelSourceBinding{Parameter: parameter.Name, Role: schema.SemanticSourceTask, Value: string(source.ID)}, nil
	}
	for _, parameter := range offering.Parameters {
		role, isSource := schema.SourceParameterRole(parameter)
		if !isSource || role != schema.SemanticSourceAsset || parameter.Type != "asset" ||
			(parameter.UploadedModel != nil && parameter.UploadedModel.NativeOnly) {
			continue
		}
		var candidates []Asset
		for _, asset := range source.Assets {
			if chosen != "" && asset.ID != chosen {
				continue
			}
			if asset.Role != "model" || asset.StorageKey == "" ||
				(parameter.MaxBytes > 0 && asset.Size > parameter.MaxBytes) ||
				!mediaTypeAllowed(parameter.MediaTypes, asset.MediaType) {
				continue
			}
			candidates = append(candidates, asset)
		}
		if len(candidates) == 0 {
			continue
		}
		slices.SortStableFunc(candidates, CompareModelAssets(prefer))
		return ModelSourceBinding{Parameter: parameter.Name, Role: schema.SemanticSourceAsset, Value: string(candidates[0].ID), AssetID: candidates[0].ID}, nil
	}
	if expired {
		return ModelSourceBinding{}, &Error{Code: ErrInvalidInput, Operation: "bind_model_source",
			Detail: "来源模型在服务商处的原任务已超过保留期，且该供给项不接受本地模型文件", Cause: ErrSourceExpired}
	}
	return ModelSourceBinding{}, incompatibleSource("来源与该供给项的输入契约不兼容")
}

// ErrSourceExpired marks a refusal whose cause is that the Provider no longer
// keeps the job a task reference names (#325).
var ErrSourceExpired = errors.New("source upstream job expired")

// UpstreamJobExpired reports whether an upstream_job task parameter can no
// longer reach source's upstream job at now: a step naming that job already
// failed because the Provider no longer keeps it (UpstreamJobGone, #326), or
// the parameter's declared age has passed. The age counts from the creation
// of the Task that holds the job (UpstreamJobCreatedAt for a recovered
// result, #326; otherwise the source Task's own), which precedes the job's
// creation and completion, so the rule errs toward the saved file. A Task
// without a creation time, a stored-bytes (assets) dependency and an
// undeclared age never expire by age.
func UpstreamJobExpired(parameter Parameter, source Task, now time.Time) bool {
	if parameter.Type != "task" || (parameter.TaskSourceKind != "" && parameter.TaskSourceKind != schema.TaskSourceUpstreamJob) {
		return false
	}
	if source.UpstreamJobGone {
		return true
	}
	created := source.CreatedAt
	if !source.UpstreamJobCreatedAt.IsZero() {
		created = source.UpstreamJobCreatedAt
	}
	if parameter.UpstreamJobMaxAgeSeconds <= 0 || created.IsZero() {
		return false
	}
	return now.Sub(created) >= time.Duration(parameter.UpstreamJobMaxAgeSeconds)*time.Second
}

// SelectTaskSourceAsset chooses one persisted model under an explicit assets
// dependency contract. Ownership and the successful Attempt are checked by the
// kernel; callers with a target Offering also enforce its same-provider rule.
// A chosen UI asset cannot resolve ambiguity: Task input freezes only TaskID,
// so preparation and execution must independently select the same unique file.
func SelectTaskSourceAsset(parameter Parameter, source Task) (Asset, error) {
	if parameter.Type != "task" || parameter.TaskSourceKind != schema.TaskSourceAssets ||
		source.ID == "" || source.State != TaskSucceeded || source.Offering.ID == "" ||
		!taskCapabilityAllowed(parameter.TaskCapabilities, source.Capability) ||
		!taskOfferingAllowed(parameter.TaskOfferings, source.Offering.ID) ||
		parameter.MaxBytes <= 0 || len(parameter.MediaTypes) == 0 || len(parameter.Extensions) == 0 {
		return Asset{}, incompatibleSource("来源任务与资产输入契约不兼容")
	}
	var selected Asset
	for _, asset := range source.Assets {
		if asset.Role != "model" || asset.ID == "" || asset.TaskID != source.ID ||
			asset.StorageKey == "" || asset.Size <= 0 || asset.Size == math.MaxInt64 || asset.Size > parameter.MaxBytes ||
			len(asset.SHA256) != 64 {
			continue
		}
		if _, err := hex.DecodeString(asset.SHA256); err != nil {
			continue
		}
		if !slices.Contains(parameter.MediaTypes, asset.MediaType) || !slices.ContainsFunc(parameter.Extensions, func(extension string) bool {
			return strings.EqualFold(strings.TrimPrefix(extension, "."), asset.Variant)
		}) {
			continue
		}
		if selected.ID != "" {
			return Asset{}, incompatibleSource("来源任务有多个匹配的模型文件")
		}
		selected = asset
	}
	if selected.ID == "" {
		return Asset{}, incompatibleSource("来源任务没有符合输入契约的已保存模型文件")
	}
	return selected, nil
}

func taskOfferingAllowed(allowed []OfferingID, actual OfferingID) bool {
	return len(allowed) == 0 || slices.Contains(allowed, actual)
}

// viewerModelMediaType is the one model format the 3D stage shows (GLB). A
// file of that type ranks as the "glb" entry of a variant preference even when
// its variant names a Provider field (rigged_character_glb_url).
const viewerModelMediaType = "model/gltf-binary"

// PrimaryModelAsset is the main model of a task (#326): the saved model file a
// step binds, a chat handle names and the stage shows when only the model is
// named. Required outputs come before attachments (a Meshy Rigging result
// lists its basic-animation skeletons first); among them the variant
// preference decides (a GLB, by variant or media type, first in the shipped
// Schema), then the ordinal. The 加工 panel mirrors this rule
// (src/lib/workspace/task-projection.ts viewableModelAsset) and the 格式 tab
// classifies files in the same order.
func PrimaryModelAsset(task Task, prefer []string) (Asset, bool) {
	var candidates []Asset
	for _, asset := range task.Assets {
		if asset.Role == "model" && asset.StorageKey != "" {
			candidates = append(candidates, asset)
		}
	}
	if len(candidates) == 0 {
		return Asset{}, false
	}
	slices.SortStableFunc(candidates, CompareModelAssets(prefer))
	return candidates[0], true
}

// CompareModelAssets orders saved model files by PrimaryModelAsset's rule
// (main model first).
func CompareModelAssets(prefer []string) func(left, right Asset) int {
	return func(left, right Asset) int {
		if left.Required != right.Required {
			if left.Required {
				return -1
			}
			return 1
		}
		if rank := modelFormatRank(prefer, left) - modelFormatRank(prefer, right); rank != 0 {
			return rank
		}
		if left.Ordinal != right.Ordinal {
			return left.Ordinal - right.Ordinal
		}
		return strings.Compare(string(left.ID), string(right.ID))
	}
}

func modelFormatRank(prefer []string, asset Asset) int {
	rank := variantRank(prefer, asset.Variant)
	if rank == len(prefer) && strings.EqualFold(asset.MediaType, viewerModelMediaType) {
		rank = variantRank(prefer, "glb")
	}
	return rank
}

func variantRank(prefer []string, variant string) int {
	for index, candidate := range prefer {
		if strings.EqualFold(candidate, variant) {
			return index
		}
	}
	return len(prefer)
}

func incompatibleSource(detail string) error {
	return &Error{Code: ErrInvalidInput, Operation: "bind_model_source", Detail: detail}
}
