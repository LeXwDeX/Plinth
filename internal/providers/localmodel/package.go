package localmodel

import (
	"container/list"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"sync"

	"github.com/LeXwDeX/Plinth/internal/assets/modelconvert"
	"github.com/LeXwDeX/Plinth/internal/assets/modelinput"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

const (
	maxPackageConcurrency = 8
	verdictCacheEntries   = 256
)

var errPackageAbandoned = errors.New("package write abandoned by the asset store")

func packageLimits(c contract) modelconvert.Limits {
	return modelconvert.Limits{MaxInputBytes: c.source.MaxBytes, MaxOutputBytes: c.output.MaxBytes, MaxWork: c.options.MaxWork}
}

// acquire waits for one of the Offering's declared conversion slots. Each slot
// holds at most one source buffer, so slots bound the adapter's peak memory.
func (a *Adapter) acquire(ctx context.Context, c contract, offering generation.Offering) (func(), error) {
	// Keyed by revision too: a republished bound takes effect for new work.
	key := string(offering.ID) + "@" + offering.Revision
	value, _ := a.slots.LoadOrStore(key, make(chan struct{}, c.options.MaxConcurrent))
	slots := value.(chan struct{})
	select {
	case slots <- struct{}{}:
		return func() { <-slots }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// validatePackage runs the complete conversion into a discarding writer, so a
// read-only coverage check reports exactly what Start would. The verdict for an
// immutable source digest under the same frozen Offering is cached, so a
// polled format list does not re-read a large model on every request.
func (a *Adapter) validatePackage(ctx context.Context, request generation.InputValidationRequest, c contract) error {
	asset, err := sourceAsset(request, c)
	if err != nil {
		return err
	}
	key := verdictKey(request.Offering, asset)
	if verdict, cached := a.verdicts.get(key); cached {
		return verdict
	}
	release, err := a.acquire(ctx, c, request.Offering)
	if err != nil {
		return err
	}
	defer release()
	raw, err := a.readSource(ctx, request, c)
	if err != nil {
		return err
	}
	_, convertErr := modelconvert.GLBToGLTFPackage(ctx, raw, c.policy, packageLimits(c), io.Discard)
	verdict := domainError(convertErr)
	if deterministic(convertErr) {
		a.verdicts.put(key, verdict)
	}
	return verdict
}

// startPackage streams the package straight into the asset store: the ZIP is
// never held in memory or in the claim table. The store inspects the bytes
// before commit; the kernel re-hashes the stored blob before the Task succeeds.
func (a *Adapter) startPackage(ctx context.Context, request generation.StartRequest, validation generation.InputValidationRequest, c contract) (generation.Observation, error) {
	release, err := a.acquire(ctx, c, request.Offering)
	if err != nil {
		return generation.Observation{}, providerError(err)
	}
	defer release()
	raw, err := a.readSource(ctx, validation, c)
	if err != nil {
		return generation.Observation{}, providerError(err)
	}
	assetID, err := randomAssetID()
	if err != nil {
		return generation.Observation{}, providerError(err)
	}
	reader, writer := io.Pipe()
	type outcome struct {
		result modelconvert.PackageResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := modelconvert.GLBToGLTFPackage(ctx, raw, c.policy, packageLimits(c), writer)
		_ = writer.CloseWithError(err)
		done <- outcome{result, err}
	}()
	// Unblock the converter however Put ends, including a panic recovered by
	// the coordinator; otherwise the goroutine would keep the source alive.
	defer reader.CloseWithError(errPackageAbandoned)
	asset, putErr := a.assets.Put(ctx, generation.AssetWrite{
		AssetID: assetID, OwnerID: request.OwnerID, TaskID: request.TaskID, AttemptID: request.AttemptID,
		Role: c.output.Role, Variant: c.output.Variant, Ordinal: 1, MediaType: c.targetMIME,
		MaxBytes: c.output.MaxBytes, DeliveryProfile: c.output.DeliveryProfile, Body: reader,
	})
	// A store that stopped reading early must not leave the writer blocked.
	_ = reader.CloseWithError(errPackageAbandoned)
	converted := <-done
	if converted.err != nil && (putErr == nil || deterministic(converted.err)) {
		return generation.Observation{}, providerError(domainError(converted.err))
	}
	if putErr != nil {
		// The store refused the package (#330): report the integrity failure
		// and its allowlisted cause instead of a transport failure.
		if failure, rejected := generation.AssetStoreRejection(putErr, c.output.Role, c.output.Variant); rejected {
			return generation.Observation{}, &generation.ProviderError{Failure: failure, Cause: putErr}
		}
		return generation.Observation{}, providerError(putErr)
	}
	if asset.ID != assetID || asset.TaskID != request.TaskID || asset.AttemptID != request.AttemptID || asset.Role != c.output.Role ||
		asset.MediaType != c.targetMIME || asset.Size != converted.result.Bytes || !validDigest(asset.SHA256) {
		return generation.Observation{}, providerError(sourceError())
	}
	metadata, _ := json.Marshal(struct {
		SHA256 string `json:"sha256"`
		Size   int64  `json:"size"`
	}{asset.SHA256, asset.Size})
	stored := asset
	claim := generation.ArtifactClaim{ID: string(request.AttemptID) + ":model:" + c.output.Variant, Role: c.output.Role, Variant: c.output.Variant, MediaType: c.targetMIME, StoredAsset: &stored, Metadata: metadata}
	return generation.Observation{Phase: generation.UpstreamCompleted, Progress: 100, Artifacts: []generation.ArtifactClaim{claim}, DedupeKey: asset.SHA256}, nil
}

func randomAssetID() (generation.AssetID, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return generation.AssetID("asset_" + hex.EncodeToString(value[:])), nil
}

// deterministic reports a verdict that the same bytes and contract always
// reproduce. Cancellation, I/O and store failures are never cached.
func deterministic(err error) bool {
	if err == nil {
		return true
	}
	var input *modelinput.Error
	if !errors.As(err, &input) {
		return false
	}
	switch input.Kind {
	case modelinput.Invalid, modelinput.Unsupported, modelinput.Limit:
		return true
	}
	return false
}

// verdictKey binds a verdict to the frozen Offering (its limits are inside the
// Provider options) and the immutable source digest.
func verdictKey(offering generation.Offering, asset generation.InputAsset) string {
	hash := sha256.New()
	output, _ := json.Marshal(offering.Outputs)
	for _, part := range [][]byte{[]byte(offering.ID), []byte(offering.Revision), offering.ProviderOptions, output, []byte(asset.SHA256)} {
		var length [8]byte
		binary.LittleEndian.PutUint64(length[:], uint64(len(part)))
		hash.Write(length[:])
		hash.Write(part)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

// verdictCache is a small LRU of conversion verdicts (nil or a domain error).
type verdictCache struct {
	mu       sync.Mutex
	capacity int
	order    *list.List
	entries  map[string]*list.Element
}

type verdictEntry struct {
	key     string
	verdict error
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
