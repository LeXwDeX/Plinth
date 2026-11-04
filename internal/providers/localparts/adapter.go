package localparts

import (
	"bytes"
	"container/list"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/LeXwDeX/Plinth/internal/assets/modelinput"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

const verdictCacheEntries = 128

// Config wires the two storage ports the free local adapter needs.
type Config struct {
	Inputs generation.InputAssetResolver
	Assets generation.AssetStore
}

type Adapter struct {
	inputs   generation.InputAssetResolver
	assets   generation.AssetStore
	verdicts *verdictCache
	slots    sync.Map // Offering ID@revision -> chan struct{}
}

func New(config Config) (*Adapter, error) {
	if config.Inputs == nil || config.Assets == nil {
		return nil, errors.New("local model parts storage ports missing")
	}
	return &Adapter{inputs: config.Inputs, assets: config.Assets, verdicts: newVerdictCache(verdictCacheEntries)}, nil
}

var _ generation.ProviderPort = (*Adapter)(nil)
var _ generation.ProviderInputValidator = (*Adapter)(nil)

func digest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func validDigest(value string) bool {
	raw, err := hex.DecodeString(value)
	return err == nil && len(raw) == 32
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func domainError(err error) error {
	if err == nil {
		return nil
	}
	var input *modelinput.Error
	if errors.As(err, &input) {
		switch input.Kind {
		case modelinput.Canceled:
			return context.Canceled
		case modelinput.InvalidPolicy, modelinput.IO:
			return unavailableError("本地模型校验暂不可用", err)
		case modelinput.Unsupported:
			return &generation.Error{Code: generation.ErrUnsupportedModelInput, Operation: operation, Detail: "模型包含暂不支持的扩展或特性", Cause: err}
		case modelinput.Limit:
			return &generation.Error{Code: generation.ErrModelInputLimit, Operation: operation, Detail: "模型超出本地拆分或合并的资源上限", Cause: err}
		default:
			return &generation.Error{Code: generation.ErrInvalidModelInput, Operation: operation, Detail: "模型内容不符合本地拆分或合并约束", Cause: err}
		}
	}
	return err
}

func providerError(err error) error {
	class := generation.FailureInvalidInput
	retryable := false
	var domain *generation.Error
	if errors.As(err, &domain) {
		if domain.Code == generation.ErrUnavailable {
			class = generation.FailureTransport
			retryable = true
		}
	} else {
		class = generation.FailureTransport
		retryable = true
	}
	return &generation.ProviderError{Failure: &generation.Failure{Class: class, Retryable: retryable, SafeDetail: "本地拆分或合并未完成"}, Cause: err}
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(body []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(body)
}

// checkedRead allocates the frozen size once and hashes while reading. It is
// the same contract localmodel uses for generated model sources.
func checkedRead(ctx context.Context, reader io.ReadCloser, size, maximum int64, expected string) ([]byte, error) {
	if reader == nil {
		return nil, sourceError()
	}
	if err := ctx.Err(); err != nil {
		_ = reader.Close()
		return nil, err
	}
	if size <= 0 || size > maximum || !validDigest(expected) {
		_ = reader.Close()
		return nil, sourceError()
	}
	body := make([]byte, size)
	hasher := sha256.New()
	source := io.TeeReader(contextReader{ctx: ctx, reader: reader}, hasher)
	_, readErr := io.ReadFull(source, body)
	var extra [1]byte
	trailing := 0
	if readErr == nil {
		trailing, readErr = io.ReadFull(source, extra[:])
		if readErr == io.EOF {
			readErr = nil
		}
	}
	closeErr := reader.Close()
	if errors.Is(readErr, io.ErrUnexpectedEOF) || (readErr == io.EOF && trailing == 0) {
		return nil, sourceError()
	}
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if trailing != 0 || !strings.EqualFold(hex.EncodeToString(hasher.Sum(nil)), expected) {
		return nil, sourceError()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return body, nil
}

type splitInput struct {
	source   string
	mode     string
	minRatio float64
	maxParts int
}

type mergeInput struct {
	sources []string
	name    string
}

func splitInputFrom(c contract, raw json.RawMessage) (splitInput, error) {
	var input splitInput
	values, err := decodeInput(raw)
	if err != nil || len(values) != 4 {
		return input, sourceError()
	}
	source, ok := rawString(values[c.source.Name])
	if !ok {
		return input, sourceError()
	}
	if json.Unmarshal(values[c.mode.Name], &input.mode) != nil || !validSplitMode(input.mode) {
		return input, sourceError()
	}
	var ratio float64
	if json.Unmarshal(values[c.minRatio.Name], &ratio) != nil || math.IsNaN(ratio) ||
		c.minRatio.Minimum == nil || c.minRatio.Maximum == nil || ratio < *c.minRatio.Minimum || ratio > *c.minRatio.Maximum {
		return input, sourceError()
	}
	var maxParts int
	if json.Unmarshal(values[c.maxParts.Name], &maxParts) != nil || c.maxParts.Minimum == nil || c.maxParts.Maximum == nil ||
		float64(maxParts) < *c.maxParts.Minimum || float64(maxParts) > *c.maxParts.Maximum {
		return input, sourceError()
	}
	input.source, input.minRatio, input.maxParts = source, ratio, maxParts
	return input, nil
}

func mergeInputFrom(c contract, raw json.RawMessage) (mergeInput, error) {
	var input mergeInput
	values, err := decodeInput(raw)
	if err != nil || len(values) < 1 || len(values) > 2 {
		return input, sourceError()
	}
	var sources []string
	if json.Unmarshal(values[c.source.Name], &sources) != nil || len(sources) < c.source.MinItems || len(sources) > c.source.MaxItems {
		return input, sourceError()
	}
	for _, source := range sources {
		if source == "" || strings.TrimSpace(source) != source {
			return input, sourceError()
		}
	}
	if rawName, ok := values[c.name.Name]; ok {
		var name string
		if json.Unmarshal(rawName, &name) != nil {
			return input, sourceError()
		}
		if name != "" {
			if uint64(len([]rune(name))) > uint64(c.name.MaxLength) {
				return input, sourceError()
			}
			input.name = name
		}
	}
	input.sources = sources
	return input, nil
}

// hasSkeletonOrAnimation looks only at the framed JSON chunk. A malformed
// container returns false and is left to modelinput to describe.
func hasSkeletonOrAnimation(raw []byte) bool {
	if len(raw) < 20 {
		return false
	}
	length := int(binary.LittleEndian.Uint32(raw[12:16]))
	if length <= 0 || 20+length > len(raw) {
		return false
	}
	var document map[string]json.RawMessage
	if json.Unmarshal(raw[20:20+length], &document) != nil {
		return false
	}
	for _, key := range []string{"skins", "animations"} {
		value, ok := document[key]
		if !ok {
			continue
		}
		trimmed := bytes.TrimSpace(value)
		if len(trimmed) > 2 && !bytes.Equal(trimmed, []byte("null")) && !bytes.Equal(trimmed, []byte("[]")) && !bytes.Equal(trimmed, []byte("{}")) {
			return true
		}
	}
	return false
}

// readSources validates every frozen source identity, bounded the same way
// localmodel reads a Task model output, and parses the static GLB subset.
func (a *Adapter) readSources(ctx context.Context, request generation.InputValidationRequest, c contract, ids []string, budget *workBudget) ([]*sourceModel, error) {
	if request.OwnerID == "" || len(request.TaskDependencies) != 0 || len(request.InputAssets) != 1 {
		return nil, sourceError()
	}
	assets := request.InputAssets[c.source.Name]
	if len(assets) != len(ids) || len(assets) == 0 {
		return nil, sourceError()
	}
	totalEncoded := int64(0)
	sources := make([]*sourceModel, 0, len(ids))
	for index, id := range ids {
		asset := assets[index]
		if string(asset.ID) != id || asset.OwnerID != request.OwnerID || asset.StorageKey == "" ||
			!contains(c.source.MediaTypes, asset.MediaType) || asset.Size <= 0 ||
			asset.Size > c.source.MaxBytes || asset.Size > c.policy.Limits.MaxBytes || !validDigest(asset.SHA256) {
			return nil, sourceError()
		}
		if asset.Role != "model" || asset.Variant != "glb" || asset.TaskID == "" || asset.SessionID != request.SessionID {
			return nil, &generation.Error{Code: generation.ErrUnsupportedModelInput, Operation: operation, Detail: "拆分与合并只接受当前会话中成功的平台任务所保存的模型文件"}
		}
		if asset.Size > (1<<62)/4*3 {
			return nil, limitError("total_encoded_bytes")
		}
		totalEncoded += int64(base64.StdEncoding.EncodedLen(int(asset.Size)))
		reader, err := a.inputs.Open(ctx, asset)
		if err != nil {
			return nil, err
		}
		raw, err := checkedRead(ctx, reader, asset.Size, c.source.MaxBytes, asset.SHA256)
		if err != nil {
			return nil, err
		}
		if err := budget.charge(int64(len(raw))); err != nil {
			return nil, err
		}
		if hasSkeletonOrAnimation(raw) {
			return nil, unsupportedModel("带骨骼或动画的模型暂不支持拆分或合并，请先移除骨骼和动画")
		}
		if _, err := modelinput.ValidateGLBBytes(ctx, raw, c.policy); err != nil {
			return nil, domainError(err)
		}
		document, bin, err := parseGLB(raw, c.policy.Limits)
		if err != nil {
			return nil, err
		}
		sources = append(sources, &sourceModel{asset: asset, doc: document, bin: bin, budget: budget})
	}
	if c.capability == generation.ModelMerge && totalEncoded > c.source.MaxTotalEncodedBytes {
		return nil, &generation.Error{Code: generation.ErrInvalidInput, Operation: operation, Detail: "合并来源模型的总大小超过上限"}
	}
	return sources, nil
}

// acquire waits for one of the Offering's declared slots. Each slot holds one
// source and its output graph in memory, so the bound matters for large models.
func (a *Adapter) acquire(ctx context.Context, c contract, offering generation.Offering) (func(), error) {
	key := string(offering.ID) + "@" + offering.Revision
	value, _ := a.slots.LoadOrStore(key, make(chan struct{}, int(c.options.MaxConcurrent)))
	slots := value.(chan struct{})
	select {
	case slots <- struct{}{}:
		return func() { <-slots }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

type execution struct {
	ownerID   generation.OwnerID
	sessionID string
	taskID    generation.TaskID
	attemptID generation.AttemptID
}

type outputFile struct {
	role      string
	variant   string
	mediaType string
	ordinal   int
	body      []byte
}

func (a *Adapter) storeFile(ctx context.Context, ids execution, requirement generation.AssetRequirement, file outputFile, claimID string) (generation.ArtifactClaim, generation.Asset, error) {
	assetID, err := randomAssetID()
	if err != nil {
		return generation.ArtifactClaim{}, generation.Asset{}, err
	}
	sha := digest(file.body)
	asset, err := a.assets.Put(ctx, generation.AssetWrite{
		AssetID: assetID, OwnerID: ids.ownerID, TaskID: ids.taskID, AttemptID: ids.attemptID,
		Role: file.role, Variant: file.variant, Ordinal: file.ordinal, MediaType: file.mediaType,
		MaxBytes: requirement.MaxBytes, DeliveryProfile: requirement.DeliveryProfile,
		Body: bytes.NewReader(file.body),
	})
	if err != nil {
		if failure, rejected := generation.AssetStoreRejection(err, file.role, file.variant); rejected {
			return generation.ArtifactClaim{}, generation.Asset{}, &generation.ProviderError{Failure: failure, Cause: err}
		}
		return generation.ArtifactClaim{}, generation.Asset{}, err
	}
	if asset.ID != assetID || asset.TaskID != ids.taskID || asset.AttemptID != ids.attemptID ||
		asset.Role != file.role || asset.MediaType != file.mediaType || asset.Size != int64(len(file.body)) || !strings.EqualFold(asset.SHA256, sha) {
		_ = a.assets.Delete(ctx, asset)
		return generation.ArtifactClaim{}, generation.Asset{}, sourceError()
	}
	metadata, _ := json.Marshal(struct {
		SHA256 string `json:"sha256"`
		Size   int64  `json:"size"`
	}{sha, int64(len(file.body))})
	return generation.ArtifactClaim{
		ID: claimID, Role: file.role, Variant: file.variant, MediaType: file.mediaType,
		StoredAsset: &asset, Metadata: metadata,
	}, asset, nil
}

func validateOutput(ctx context.Context, body []byte, c contract) error {
	if _, err := modelinput.ValidateGLBBytes(ctx, body, c.policy); err != nil {
		return unavailableError("本地拆分或合并生成的模型未通过校验", domainError(err))
	}
	return nil
}

func combinedDigest(files []outputFile) string {
	hasher := sha256.New()
	var length [8]byte
	for _, file := range files {
		for _, value := range []string{file.role, file.variant, file.mediaType, strconv.Itoa(file.ordinal), digest(file.body)} {
			binary.LittleEndian.PutUint64(length[:], uint64(len(value)))
			hasher.Write(length[:])
			hasher.Write([]byte(value))
		}
	}
	return hex.EncodeToString(hasher.Sum(nil))
}

func (a *Adapter) cleanup(ctx context.Context, stored []generation.Asset) {
	for _, asset := range stored {
		_ = a.assets.Delete(context.WithoutCancel(ctx), asset)
	}
}

func (a *Adapter) split(ctx context.Context, request generation.InputValidationRequest, c contract, store bool, ids execution) ([]generation.ArtifactClaim, string, error) {
	input, err := splitInputFrom(c, request.Input)
	if err != nil {
		return nil, "", err
	}
	budget := newWorkBudget(c.options.MaxWork)
	sources, err := a.readSources(ctx, request, c, []string{input.source}, budget)
	if err != nil {
		return nil, "", err
	}
	parts, err := splitModel(sources[0], input.mode, input.minRatio, input.maxParts)
	if err != nil {
		return nil, "", err
	}
	manifest, err := buildManifest(parts, input.mode)
	if err != nil {
		return nil, "", err
	}
	files := make([]outputFile, 0, len(parts)+1)
	claims := make([]generation.ArtifactClaim, 0, len(parts)+1)
	stored := make([]generation.Asset, 0, len(parts)+1)
	for index, part := range parts {
		builder := newOutputBuilder(c.policy.Limits, budget)
		if err := builder.buildPart(part); err != nil {
			a.cleanup(ctx, stored)
			return nil, "", err
		}
		body, err := builder.finish(part.name)
		if err != nil {
			a.cleanup(ctx, stored)
			return nil, "", err
		}
		if err := validateOutput(ctx, body, c); err != nil {
			a.cleanup(ctx, stored)
			return nil, "", err
		}
		file := outputFile{role: c.modelOutput.Role, variant: c.modelOutput.Variant, mediaType: modelMIME, ordinal: index + 1, body: body}
		files = append(files, file)
		if store {
			claim, asset, err := a.storeFile(ctx, ids, c.modelOutput, file, string(ids.attemptID)+":model:"+strconv.Itoa(index+1))
			if err != nil {
				a.cleanup(ctx, stored)
				return nil, "", err
			}
			claims = append(claims, claim)
			stored = append(stored, asset)
		}
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		a.cleanup(ctx, stored)
		return nil, "", unavailableError("拆分清单写出失败", err)
	}
	if int64(len(manifestBytes)) > c.manifestOutput.MaxBytes {
		a.cleanup(ctx, stored)
		return nil, "", limitError("manifest_bytes")
	}
	file := outputFile{role: c.manifestOutput.Role, variant: c.manifestOutput.Variant, mediaType: manifestMIME, ordinal: 1, body: manifestBytes}
	files = append(files, file)
	if store {
		claim, asset, err := a.storeFile(ctx, ids, *c.manifestOutput, file, string(ids.attemptID)+":parts_manifest")
		if err != nil {
			a.cleanup(ctx, stored)
			return nil, "", err
		}
		claims = append(claims, claim)
		stored = append(stored, asset)
	}
	return claims, combinedDigest(files), nil
}

func (a *Adapter) merge(ctx context.Context, request generation.InputValidationRequest, c contract, store bool, ids execution) ([]generation.ArtifactClaim, string, error) {
	input, err := mergeInputFrom(c, request.Input)
	if err != nil {
		return nil, "", err
	}
	budget := newWorkBudget(c.options.MaxWork)
	sources, err := a.readSources(ctx, request, c, input.sources, budget)
	if err != nil {
		return nil, "", err
	}
	body, err := buildMerged(sources, input.name, c.policy.Limits, budget)
	if err != nil {
		return nil, "", err
	}
	if err := validateOutput(ctx, body, c); err != nil {
		return nil, "", err
	}
	file := outputFile{role: c.modelOutput.Role, variant: c.modelOutput.Variant, mediaType: modelMIME, ordinal: 1, body: body}
	files := []outputFile{file}
	claims := make([]generation.ArtifactClaim, 0, 1)
	if store {
		claim, _, err := a.storeFile(ctx, ids, c.modelOutput, file, string(ids.attemptID)+":model:1")
		if err != nil {
			return nil, "", err
		}
		claims = append(claims, claim)
	}
	return claims, combinedDigest(files), nil
}

func (a *Adapter) run(ctx context.Context, request generation.InputValidationRequest, c contract, store bool, ids execution) ([]generation.ArtifactClaim, string, error) {
	if c.capability == generation.ModelSplit {
		return a.split(ctx, request, c, store, ids)
	}
	return a.merge(ctx, request, c, store, ids)
}

// ValidateInput is a read-only coverage check: it performs exactly the work
// Start would and discards the produced bytes. No asset, Task or grant is
// written. A deterministic verdict for the same frozen input is cached.
func (a *Adapter) ValidateInput(ctx context.Context, request generation.InputValidationRequest) error {
	c, err := partsContract(request.Offering)
	if err != nil {
		return unavailableError("本地拆分或合并配置无效", err)
	}
	key := verdictKey(request.Offering, request.Input, flattenAssets(request.InputAssets))
	if verdict, ok := a.verdicts.get(key); ok {
		return verdict
	}
	release, err := a.acquire(ctx, c, request.Offering)
	if err != nil {
		return err
	}
	defer release()
	_, _, verdict := a.run(ctx, request, c, false, execution{ownerID: request.OwnerID, sessionID: request.SessionID})
	if deterministic(verdict) {
		a.verdicts.put(key, verdict)
	}
	return verdict
}

func flattenAssets(inputs map[string][]generation.InputAsset) []generation.InputAsset {
	keys := make([]string, 0, len(inputs))
	for key := range inputs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var assets []generation.InputAsset
	for _, key := range keys {
		assets = append(assets, inputs[key]...)
	}
	return assets
}

// Start performs the split or merge synchronously and stores each produced
// GLB as a public Task asset before returning the claims.
func (a *Adapter) Start(ctx context.Context, request generation.StartRequest) (generation.Observation, error) {
	if request.TaskID == "" || request.AttemptID == "" {
		return generation.Observation{}, providerError(sourceError())
	}
	c, err := partsContract(request.Offering)
	if err != nil {
		return generation.Observation{}, providerError(unavailableError("本地拆分或合并配置无效", err))
	}
	release, err := a.acquire(ctx, c, request.Offering)
	if err != nil {
		return generation.Observation{}, providerError(err)
	}
	defer release()
	validation := generation.InputValidationRequest{
		SessionID: request.SessionID, OwnerID: request.OwnerID, Offering: request.Offering,
		Input: request.Input, InputAssets: request.InputAssets, TaskDependencies: request.TaskDependencies,
	}
	claims, dedupe, err := a.run(ctx, validation, c, true, execution{
		ownerID: request.OwnerID, sessionID: request.SessionID, taskID: request.TaskID, attemptID: request.AttemptID,
	})
	if err != nil {
		return generation.Observation{}, providerError(err)
	}
	return generation.Observation{Phase: generation.UpstreamCompleted, Progress: 100, Artifacts: claims, DedupeKey: dedupe}, nil
}

func (*Adapter) Observe(context.Context, generation.ObserveRequest) (generation.Observation, error) {
	return generation.Observation{}, providerError(sourceError())
}

func (*Adapter) OpenAsset(ctx context.Context, request generation.OpenAssetRequest) (io.ReadCloser, generation.AssetMetadata, error) {
	// Bytes are stored during Start and claimed as StoredAsset; the kernel
	// re-hashes that blob instead of asking this adapter for a stream.
	return nil, generation.AssetMetadata{}, providerError(sourceError())
}

func randomAssetID() (generation.AssetID, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return generation.AssetID("asset_" + hex.EncodeToString(value[:])), nil
}

// verdictKey binds a verdict to the frozen Offering, normalized input and the
// immutable source digests (in input order).
func verdictKey(offering generation.Offering, input json.RawMessage, assets []generation.InputAsset) string {
	hasher := sha256.New()
	outputs, _ := json.Marshal(offering.Outputs)
	parts := [][]byte{[]byte(offering.ID), []byte(offering.Revision), offering.ProviderOptions, outputs, input}
	for _, asset := range assets {
		parts = append(parts, []byte(asset.SHA256))
	}
	var length [8]byte
	for _, part := range parts {
		binary.LittleEndian.PutUint64(length[:], uint64(len(part)))
		hasher.Write(length[:])
		hasher.Write(part)
	}
	return hex.EncodeToString(hasher.Sum(nil))
}

func deterministic(err error) bool {
	if err == nil {
		return true
	}
	var domain *generation.Error
	if !errors.As(err, &domain) {
		return false
	}
	switch domain.Code {
	case generation.ErrInvalidInput, generation.ErrInvalidModelInput, generation.ErrUnsupportedModelInput, generation.ErrModelInputLimit:
		return true
	default:
		return false
	}
}

type verdictEntry struct {
	key     string
	verdict error
}

type verdictCache struct {
	mu       sync.Mutex
	capacity int
	order    *list.List
	entries  map[string]*list.Element
}

func newVerdictCache(capacity int) *verdictCache {
	return &verdictCache{capacity: capacity, order: list.New(), entries: map[string]*list.Element{}}
}

func (c *verdictCache) get(key string) (error, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	element, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	c.order.MoveToFront(element)
	return element.Value.(verdictEntry).verdict, true
}

func (c *verdictCache) put(key string, verdict error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if element, ok := c.entries[key]; ok {
		element.Value = verdictEntry{key, verdict}
		c.order.MoveToFront(element)
		return
	}
	c.entries[key] = c.order.PushFront(verdictEntry{key, verdict})
	for c.order.Len() > c.capacity {
		oldest := c.order.Back()
		c.order.Remove(oldest)
		delete(c.entries, oldest.Value.(verdictEntry).key)
	}
}
