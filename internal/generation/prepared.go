package generation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"reflect"
	"sort"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/schema"
)

func preparedStoreOf(repository Repository) PreparedInputStore {
	store, _ := repository.(PreparedInputStore)
	return store
}

func (m *Module) PrepareInput(ctx context.Context, cmd PrepareInputCommand) (PreparedInput, error) {
	if m.preparedStore == nil {
		return PreparedInput{}, &Error{Code: ErrUnavailable, Operation: "prepare_input", Detail: "输入冻结存储未配置"}
	}
	if cmd.OwnerID == "" || len(cmd.SessionID) > 200 || !cmd.Capability.Valid() {
		return PreparedInput{}, &Error{Code: ErrInvalidInput, Operation: "prepare_input", Detail: "所有者、会话或能力无效"}
	}
	if cmd.SessionID != "" {
		sessions, ok := m.repository.(interface {
			OwnsSession(context.Context, string, string) (bool, error)
		})
		if !ok {
			return PreparedInput{}, &Error{Code: ErrUnavailable, Operation: "prepare_input", Detail: "会话所有权校验器未配置"}
		}
		owned, err := sessions.OwnsSession(ctx, string(cmd.OwnerID), cmd.SessionID)
		if err != nil {
			return PreparedInput{}, mapRepositoryError("prepare_input", err)
		}
		if !owned {
			return PreparedInput{}, &Error{Code: ErrNotFound, Operation: "prepare_input", Detail: "会话不存在"}
		}
	}
	if err := m.verifyPreparedContext(ctx, cmd.OwnerID, cmd.SessionID, cmd.SceneID, cmd.ManifestID, cmd.ManifestHash); err != nil {
		return PreparedInput{}, err
	}
	offeringID, err := m.availableOfferingID(cmd.Capability, cmd.OfferingID)
	if err != nil {
		return PreparedInput{}, err
	}
	input := cmd.Input
	var referenceLimit *ReferenceLimit
	if cmd.KeepFirstReferences {
		input, referenceLimit = m.keepFirstReferences(offeringID, input)
	}
	offering, normalized, err := m.catalog.Normalize(cmd.Capability, offeringID, input)
	if err != nil {
		return PreparedInput{}, err
	}
	offering, normalized, err = m.fitInputs(ctx, cmd.OwnerID, cmd.Capability, offering, normalized)
	if err != nil {
		return PreparedInput{}, err
	}
	quote, err := offering.EvaluateNativeQuote(normalized)
	if err != nil {
		return PreparedInput{}, &Error{Code: ErrInvalidInput, Operation: "prepare_input", Cause: err}
	}
	adjustments, err := freezeParameterAdjustments(offering, normalized, cmd.ParameterReasons)
	if err != nil {
		return PreparedInput{}, err
	}
	if err := rejectUnfrozenSourceURLs(offering, normalized); err != nil {
		return PreparedInput{}, err
	}
	sourceRepresentationID, err := m.resolvePreparedSceneObject(ctx, cmd.OwnerID, cmd.SessionID,
		cmd.SourceSceneObjectID, cmd.SourceSceneRevision, normalized)
	if err != nil {
		return PreparedInput{}, err
	}
	if _, ok := m.providers.Provider(offering); !ok {
		return PreparedInput{}, &Error{Code: ErrUnavailable, Operation: "prepare_input", Detail: "供给项适配器不可用"}
	}
	resolved, err := m.inputs.Resolve(ctx, cmd.OwnerID, offering, normalized)
	if err != nil {
		return PreparedInput{}, err
	}
	assets, err := m.snapshotPreparedAssets(ctx, cmd.OwnerID, resolved)
	if err != nil {
		return PreparedInput{}, err
	}
	if err := m.verifyPreparedOutputAsset(ctx, cmd.OwnerID, cmd.SessionID,
		cmd.SceneID, cmd.ManifestID, cmd.ManifestHash, assets); err != nil {
		return PreparedInput{}, err
	}
	dependencies, err := m.resolveTaskDependencies(ctx, cmd.OwnerID, offering, normalized, cmd.SessionID)
	if err != nil {
		return PreparedInput{}, err
	}
	if err := m.validateProviderInput(ctx, cmd.OwnerID, cmd.SessionID, offering, normalized, resolved, dependencies); err != nil {
		return PreparedInput{}, err
	}
	prepared := PreparedInput{
		ReferenceLimit:       referenceLimit,
		NativeQuote:          quote,
		ParameterAdjustments: adjustments,
		ID:                   PreparedInputID("prepared_" + m.ids.Token()), OwnerID: cmd.OwnerID,
		SessionID: cmd.SessionID, Capability: cmd.Capability, OfferingID: offering.ID,
		OfferingRevision: offering.Revision, SchemaVersion: m.schemaVersion,
		NormalizedInput: append(json.RawMessage(nil), normalized...),
		Assets:          assets, Dependencies: snapshotPreparedDependencies(dependencies),
		SceneID: cmd.SceneID, ManifestID: cmd.ManifestID, ManifestHash: cmd.ManifestHash,
		SourceSceneObjectID: cmd.SourceSceneObjectID, SourceSceneRevision: cmd.SourceSceneRevision,
		SourceSceneRepresentationID: sourceRepresentationID,
		ExtraCredits:                offering.ExtraCreditDetail(normalized).Credits,
		CreatedAt:                   m.clock.Now(),
	}
	if prepared.SchemaVersion == "" {
		prepared.SchemaVersion = "1"
	}
	if referenceLimit != nil {
		prepared.ReferenceNote = referenceLimit.Note(referenceLimit.Template)
	}
	prepared.InputHash, err = hashPreparedInput(prepared)
	if err != nil {
		return PreparedInput{}, err
	}
	if offering.Capability == schema.AssetRecovery {
		return m.stableRecoveryPrepared(ctx, prepared)
	}
	if err := m.preparedStore.CreatePreparedInput(ctx, prepared); err != nil {
		return PreparedInput{}, mapRepositoryError("prepare_input", err)
	}
	return prepared, nil
}

func (m *Module) GetPreparedInput(ctx context.Context, owner OwnerID, id PreparedInputID) (PreparedInput, error) {
	if m.preparedStore == nil {
		return PreparedInput{}, &Error{Code: ErrUnavailable, Operation: "get_prepared_input"}
	}
	if owner == "" || id == "" {
		return PreparedInput{}, &Error{Code: ErrInvalidInput, Operation: "get_prepared_input"}
	}
	prepared, err := m.preparedStore.GetPreparedInput(ctx, owner, id)
	if err != nil {
		return PreparedInput{}, mapRepositoryError("get_prepared_input", err)
	}
	if prepared.ID != id || prepared.OwnerID != owner {
		return PreparedInput{}, &Error{Code: ErrConflict, Operation: "get_prepared_input", Detail: "输入冻结记录身份不匹配"}
	}
	if err := VerifyPreparedPricingHash(prepared); err != nil {
		return PreparedInput{}, &Error{Code: ErrConflict, Operation: "get_prepared_input", Cause: err}
	}
	digest, err := hashPreparedInput(prepared)
	if err != nil || digest != prepared.InputHash {
		return PreparedInput{}, &Error{Code: ErrConflict, Operation: "get_prepared_input", Detail: "输入冻结记录校验失败", Cause: err}
	}
	return prepared, nil
}

// VerifyPreparedTaskReplay verifies an already accepted Task without submitting
// or creating anything. Reconciliation must never fall through into admission.
// It uses the frozen input and bound grant, independent of today's Catalog.
func (m *Module) VerifyPreparedTaskReplay(ctx context.Context, cmd SubmitPreparedCommand) (Task, error) {
	if !cmd.Harness.Valid() || cmd.OwnerID == "" || cmd.PreparedInputID == "" || cmd.IdempotencyKey == "" || len(cmd.IdempotencyKey) > 200 {
		return Task{}, &Error{Code: ErrInvalidInput, Operation: "verify_prepared_replay"}
	}
	if cmd.AuthorizationRef == "" || cmd.OperationID == "" {
		return Task{}, &Error{Code: ErrForbidden, Operation: "verify_prepared_replay"}
	}
	if _, ok := m.repository.(interface {
		VerifyTaskGrantReplay(context.Context, Task, *ExecutionAdmission) error
	}); !ok {
		return Task{}, &Error{Code: ErrUnavailable, Operation: "verify_prepared_replay"}
	}
	prepared, err := m.GetPreparedInput(ctx, cmd.OwnerID, cmd.PreparedInputID)
	if err != nil {
		return Task{}, err
	}
	if prepared.SessionID != cmd.SessionID {
		return Task{}, &Error{Code: ErrConflict, Operation: "verify_prepared_replay"}
	}
	existing, err := m.repository.GetByIdempotency(ctx, cmd.OwnerID, cmd.IdempotencyKey)
	if err != nil {
		return Task{}, mapRepositoryError("verify_prepared_replay", err)
	}
	if err := m.verifyPreparedTaskReplay(ctx, cmd, prepared, existing); err != nil {
		return Task{}, err
	}
	return existing, nil
}

func (m *Module) verifyPreparedTaskReplay(ctx context.Context, cmd SubmitPreparedCommand, prepared PreparedInput, existing Task) error {
	if existing.SessionID != prepared.SessionID || existing.Capability != prepared.Capability ||
		existing.Offering.ID != prepared.OfferingID || existing.Offering.Revision != prepared.OfferingRevision ||
		existing.RequestHash != hashRequest(Offering{ID: prepared.OfferingID}, prepared.NormalizedInput) || !reflect.DeepEqual(existing.NativeQuote, prepared.NativeQuote) {
		return &Error{Code: ErrConflict, Operation: "submit_prepared", Detail: "幂等键已属于其他输入"}
	}
	if verifier, ok := m.repository.(interface {
		VerifyTaskGrantReplay(context.Context, Task, *ExecutionAdmission) error
	}); ok {
		admission := &ExecutionAdmission{RequestID: cmd.AuthorizationRef,
			PreparedInputID: prepared.ID, PreparedInputHash: prepared.InputHash,
			OperationID: cmd.OperationID, PlanStepID: cmd.PlanStepID, Harness: cmd.Harness}
		if err := verifier.VerifyTaskGrantReplay(ctx, existing, admission); err != nil {
			return mapRepositoryError("submit_prepared", err)
		}
	}
	return nil
}

func (m *Module) SubmitPrepared(ctx context.Context, cmd SubmitPreparedCommand) (Accepted, error) {
	if !cmd.Harness.Valid() || cmd.OwnerID == "" || cmd.PreparedInputID == "" ||
		cmd.IdempotencyKey == "" || len(cmd.IdempotencyKey) > 200 {
		return Accepted{}, &Error{Code: ErrInvalidInput, Operation: "submit_prepared", Detail: "缺少冻结输入或幂等身份"}
	}
	prepared, err := m.GetPreparedInput(ctx, cmd.OwnerID, cmd.PreparedInputID)
	if err != nil {
		return Accepted{}, err
	}
	if prepared.SessionID != cmd.SessionID {
		return Accepted{}, &Error{Code: ErrConflict, Operation: "submit_prepared", Detail: "冻结输入不属于该会话"}
	}
	// A committed paid Task is the first fact to consult. It remains replayable
	// after Catalog, assets or Provider availability have changed.
	existing, err := m.repository.GetByIdempotency(ctx, cmd.OwnerID, cmd.IdempotencyKey)
	switch {
	case err == nil:
		if err := m.verifyPreparedTaskReplay(ctx, cmd, prepared, existing); err != nil {
			return Accepted{}, err
		}
		return Accepted{TaskID: existing.ID, Replayed: true}, nil
	case errors.Is(err, RepositoryNotFound):
		// New admission continues below.
	case err != nil:
		return Accepted{}, mapRepositoryError("submit_prepared", err)
	}
	if cmd.AuthorizationRef == "" || cmd.OperationID == "" {
		return Accepted{}, &Error{Code: ErrForbidden, Operation: "submit_prepared", Detail: "当前操作需要单独确认"}
	}
	if err := m.verifyPreparedContext(ctx, prepared.OwnerID, prepared.SessionID,
		prepared.SceneID, prepared.ManifestID, prepared.ManifestHash); err != nil {
		return Accepted{}, err
	}
	if m.schemaVersion != "" && prepared.SchemaVersion != m.schemaVersion {
		return Accepted{}, &Error{Code: ErrConflict, Operation: "submit_prepared", Detail: "Product Schema 已变化，请重新准备输入"}
	}
	offering, normalized, err := m.catalog.Normalize(prepared.Capability, prepared.OfferingID, prepared.NormalizedInput)
	if err != nil {
		return Accepted{}, err
	}
	currentQuote, quoteErr := offering.EvaluateNativeQuote(normalized)
	if quoteErr != nil || !reflect.DeepEqual(currentQuote, prepared.NativeQuote) {
		return Accepted{}, &Error{Code: ErrConflict, Operation: "submit_prepared", Cause: quoteErr}
	}
	if offering.Revision != prepared.OfferingRevision || !reflect.DeepEqual(normalized, prepared.NormalizedInput) {
		return Accepted{}, &Error{Code: ErrConflict, Operation: "submit_prepared", Detail: "供给项修订或规范化输入已变化"}
	}
	if err := rejectUnfrozenSourceURLs(offering, normalized); err != nil {
		return Accepted{}, err
	}
	sourceRepresentationID, err := m.resolvePreparedSceneObject(ctx, prepared.OwnerID, prepared.SessionID,
		prepared.SourceSceneObjectID, prepared.SourceSceneRevision, normalized)
	if err != nil {
		return Accepted{}, err
	}
	if sourceRepresentationID != prepared.SourceSceneRepresentationID {
		return Accepted{}, &Error{Code: ErrConflict, Operation: "submit_prepared", Detail: "来源场景表示已变化，请重新准备输入"}
	}
	if _, ok := m.providers.Provider(offering); !ok {
		return Accepted{}, &Error{Code: ErrUnavailable, Operation: "submit_prepared", Detail: "供给项适配器不可用"}
	}
	resolved, err := m.inputs.Resolve(ctx, prepared.OwnerID, offering, prepared.NormalizedInput)
	if err != nil {
		return Accepted{}, err
	}
	assets, err := m.snapshotPreparedAssets(ctx, prepared.OwnerID, resolved)
	if err != nil {
		return Accepted{}, err
	}
	if !reflect.DeepEqual(assets, prepared.Assets) {
		return Accepted{}, &Error{Code: ErrConflict, Operation: "submit_prepared", Detail: "输入资产已变化，请重新准备输入"}
	}
	if err := m.verifyPreparedOutputAsset(ctx, prepared.OwnerID, prepared.SessionID,
		prepared.SceneID, prepared.ManifestID, prepared.ManifestHash, assets); err != nil {
		return Accepted{}, err
	}
	dependencies, err := m.resolveTaskDependencies(ctx, prepared.OwnerID, offering, prepared.NormalizedInput, prepared.SessionID)
	if err != nil {
		return Accepted{}, err
	}
	if !reflect.DeepEqual(snapshotPreparedDependencies(dependencies), prepared.Dependencies) {
		return Accepted{}, &Error{Code: ErrConflict, Operation: "submit_prepared", Detail: "任务依赖已变化，请重新准备输入"}
	}
	// Create atomically checks the separate human decision against this exact
	// frozen input and binds its single-use grant to the accepted Task.
	return m.Submit(ctx, SubmitCommand{OwnerID: prepared.OwnerID, SessionID: prepared.SessionID,
		Capability: prepared.Capability, OfferingID: prepared.OfferingID,
		Input: prepared.NormalizedInput, IdempotencyKey: cmd.IdempotencyKey,
		Admission: &ExecutionAdmission{RequestID: cmd.AuthorizationRef,
			PreparedInputID: prepared.ID, PreparedInputHash: prepared.InputHash,
			OperationID: cmd.OperationID, PlanStepID: cmd.PlanStepID, Harness: cmd.Harness}})
}

// A remote source URL is only a mutable address. Until it is imported as an
// owned platform Asset, neither PrepareInput nor a human decision can attest
// to the model bytes the Provider will later read.
func rejectUnfrozenSourceURLs(offering Offering, normalized json.RawMessage) error {
	var values map[string]json.RawMessage
	if err := json.Unmarshal(normalized, &values); err != nil {
		return &Error{Code: ErrInvalidInput, Operation: "prepare_input", Detail: "规范化输入无效", Cause: err}
	}
	for _, parameter := range offering.Parameters {
		if parameter.Semantic != SemanticSourceURL {
			continue
		}
		raw, present := values[parameter.Name]
		if !present || string(raw) == "null" {
			continue
		}
		var address string
		if json.Unmarshal(raw, &address) == nil && strings.TrimSpace(address) == "" {
			continue
		}
		return &Error{Code: ErrInvalidInput, Operation: "prepare_input", Detail: "远程模型 URL 的内容无法冻结，请先导入平台资产"}
	}
	return nil
}

func (m *Module) verifyPreparedContext(ctx context.Context, owner OwnerID, session, scene, manifest, digest string) error {
	if scene == "" && manifest == "" && digest == "" {
		return nil
	}
	if m.preparedContext == nil {
		return &Error{Code: ErrUnavailable, Operation: "prepare_input", Detail: "场景或渲染清单校验器未配置"}
	}
	return m.preparedContext.VerifyPreparedContext(ctx, owner, session, scene, manifest, digest)
}

func (m *Module) verifyPreparedOutputAsset(ctx context.Context, owner OwnerID, session, scene,
	manifest, digest string, assets []PreparedInputAsset) error {
	if manifest == "" {
		return nil
	}
	verifier, ok := m.preparedContext.(PreparedContextOutputVerifier)
	if !ok {
		return &Error{Code: ErrUnavailable, Operation: "prepare_input", Detail: "画布输出校验器未配置"}
	}
	output, err := verifier.VerifiedPreparedOutput(ctx, owner, session, scene, manifest, digest)
	if err != nil {
		return &Error{Code: ErrConflict, Operation: "prepare_input", Detail: "画布输出证明已失效", Cause: err}
	}
	if len(assets) != 1 || !strings.HasPrefix(assets[0].MediaType, "image/") ||
		assets[0].ID != output.AssetID || assets[0].Size != output.Size ||
		!strings.EqualFold(assets[0].SHA256, output.SHA256) {
		return &Error{Code: ErrConflict, Operation: "prepare_input", Detail: "生成输入与画布渲染内容不一致"}
	}
	return nil
}

func (m *Module) resolvePreparedSceneObject(ctx context.Context, owner OwnerID, session, object string,
	revision int64, input json.RawMessage,
) (string, error) {
	if object == "" && revision == 0 {
		return "", nil
	}
	if session == "" || object == "" || revision < 1 || m.preparedContext == nil {
		return "", &Error{Code: ErrInvalidInput, Operation: "prepare_input", Detail: "来源场景对象身份或版本无效"}
	}
	verifier, ok := m.preparedContext.(PreparedSceneObjectVerifier)
	if !ok {
		return "", &Error{Code: ErrUnavailable, Operation: "prepare_input", Detail: "来源场景对象校验器未配置"}
	}
	return verifier.ResolvePreparedSource(ctx, owner, session, object, revision, input)
}

func (m *Module) snapshotPreparedAssets(ctx context.Context, owner OwnerID, resolved map[string][]InputAsset) ([]PreparedInputAsset, error) {
	result := []PreparedInputAsset{}
	parameters := make([]string, 0, len(resolved))
	for name := range resolved {
		parameters = append(parameters, name)
	}
	sort.Strings(parameters)
	for _, parameter := range parameters {
		for index, asset := range resolved[parameter] {
			if asset.OwnerID != owner || asset.ID == "" || asset.Size <= 0 || asset.Size == math.MaxInt64 || len(asset.SHA256) != 64 {
				return nil, &Error{Code: ErrInvalidInput, Operation: "prepare_input", Detail: "输入资产未就绪或归属不符"}
			}
			body, err := m.inputs.Open(ctx, asset)
			if err != nil {
				return nil, &Error{Code: ErrUnavailable, Operation: "prepare_input", Detail: "输入资产暂不可读取", Cause: err}
			}
			hasher := sha256.New()
			size, readErr := io.Copy(hasher, io.LimitReader(body, asset.Size+1))
			closeErr := body.Close()
			if readErr != nil || closeErr != nil || size != asset.Size || !strings.EqualFold(hex.EncodeToString(hasher.Sum(nil)), asset.SHA256) {
				return nil, &Error{Code: ErrConflict, Operation: "prepare_input", Detail: "输入资产内容或摘要已变化", Cause: errors.Join(readErr, closeErr)}
			}
			role := asset.AssetType
			if role == "" {
				role = parameter
			}
			result = append(result, PreparedInputAsset{Parameter: parameter, Index: index, ID: asset.ID,
				SHA256: strings.ToLower(asset.SHA256), Size: asset.Size, MediaType: asset.MediaType, Role: role})
		}
	}
	return result, nil
}

func snapshotPreparedDependencies(values map[string]TaskDependency) []PreparedInputDependency {
	result := make([]PreparedInputDependency, 0, len(values))
	for parameter, dependency := range values {
		assets := make([]PreparedDependencyAsset, 0, len(dependency.Assets))
		for _, asset := range dependency.Assets {
			assets = append(assets, PreparedDependencyAsset{ID: asset.ID, Role: asset.Role,
				Variant: asset.Variant, Ordinal: asset.Ordinal, SHA256: asset.SHA256,
				Size: asset.Size, MediaType: asset.MediaType})
		}
		sort.Slice(assets, func(i, j int) bool {
			if assets[i].Role != assets[j].Role {
				return assets[i].Role < assets[j].Role
			}
			if assets[i].Variant != assets[j].Variant {
				return assets[i].Variant < assets[j].Variant
			}
			if assets[i].Ordinal != assets[j].Ordinal {
				return assets[i].Ordinal < assets[j].Ordinal
			}
			return assets[i].ID < assets[j].ID
		})
		result = append(result, PreparedInputDependency{SourceSessionID: dependency.SessionID, ProviderBinding: append(json.RawMessage(nil), dependency.ProviderBinding...), Parameter: parameter, TaskID: dependency.TaskID,
			Capability: dependency.Capability, OfferingID: dependency.OfferingID,
			ProviderID: dependency.ProviderID, RemoteID: dependency.UpstreamJob.RemoteID,
			UpstreamProviderID: dependency.UpstreamJob.ProviderID, OutputAssets: assets})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Parameter < result[j].Parameter })
	return result
}

func hashPreparedInput(prepared PreparedInput) (string, error) {
	value := struct {
		OwnerID                     OwnerID                   `json:"owner_id"`
		SessionID                   string                    `json:"session_id"`
		Capability                  Capability                `json:"capability"`
		OfferingID                  OfferingID                `json:"offering_id"`
		OfferingRevision            string                    `json:"offering_revision"`
		SchemaVersion               string                    `json:"schema_version"`
		NormalizedInput             json.RawMessage           `json:"normalized_input"`
		Assets                      []PreparedInputAsset      `json:"assets"`
		Dependencies                []PreparedInputDependency `json:"dependencies"`
		SceneID                     string                    `json:"scene_id"`
		ManifestID                  string                    `json:"manifest_id"`
		ManifestHash                string                    `json:"manifest_hash"`
		SourceSceneObjectID         string                    `json:"source_scene_object_id,omitempty"`
		SourceSceneRevision         int64                     `json:"source_scene_revision,omitempty"`
		SourceSceneRepresentationID string                    `json:"source_scene_representation_id,omitempty"`
		ExtraCredits                int                       `json:"extra_credits,omitempty"`
		ParameterAdjustments        []ParameterAdjustment     `json:"parameter_adjustments,omitempty"`
		NativeQuote                 *NativeQuote              `json:"native_quote,omitempty"`
	}{prepared.OwnerID, prepared.SessionID, prepared.Capability, prepared.OfferingID,
		prepared.OfferingRevision, prepared.SchemaVersion, prepared.NormalizedInput,
		prepared.Assets, prepared.Dependencies, prepared.SceneID, prepared.ManifestID, prepared.ManifestHash,
		prepared.SourceSceneObjectID, prepared.SourceSceneRevision, prepared.SourceSceneRepresentationID,
		prepared.ExtraCredits, prepared.ParameterAdjustments, prepared.NativeQuote}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode prepared input: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

// keepFirstReferences cuts a reference-image list longer than the Offering's
// max_items to its first pictures, as the Agent's generation tools do, and
// reports what it kept (#329 follow-up). An Offering that preserves its
// picture sequence, or a Schema without the note, keeps the input whole so
// normalization refuses it.
func (m *Module) keepFirstReferences(offeringID OfferingID, input json.RawMessage) (json.RawMessage, *ReferenceLimit) {
	offering, ok := m.catalog.Get(offeringID)
	if !ok || offering.ExecutionMessages.ReferenceLimit == "" || offering.ReferenceSelectionPolicy == "preserve" {
		return input, nil
	}
	var values map[string]json.RawMessage
	if json.Unmarshal(input, &values) != nil {
		return input, nil
	}
	for _, parameter := range offering.Parameters {
		if parameter.Semantic != SemanticReferenceImage || parameter.Type != "asset-array" || parameter.MaxItems <= 0 {
			continue
		}
		var pictures []json.RawMessage
		if json.Unmarshal(values[parameter.Name], &pictures) != nil || len(pictures) <= parameter.MaxItems {
			continue
		}
		kept, err := json.Marshal(pictures[:parameter.MaxItems])
		if err != nil {
			return input, nil
		}
		values[parameter.Name] = kept
		trimmed, err := json.Marshal(values)
		if err != nil {
			return input, nil
		}
		model := offering.ModelName
		if model == "" {
			model = offering.ModelID
		}
		return trimmed, &ReferenceLimit{Template: offering.ExecutionMessages.ReferenceLimitText(parameter.MaxItems),
			Model: model, Count: parameter.MaxItems, Total: len(pictures)}
	}
	return input, nil
}
