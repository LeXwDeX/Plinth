package generationtool

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/modelinspect"
)

// modelReviewMediaType is the only model format the read-only review tools
// parse (internal/modelinspect reads binary glTF).
const modelReviewMediaType = "model/gltf-binary"

// modelReviewCompareGap is the dark column between the two pictures of a
// comparison; the background colour alone would not separate them.
const modelReviewCompareGap = 4

// ModelAssetGateway opens one of a Task's saved assets for its owner. The
// generation kernel checks the owner; a foreign Task or asset is not found.
type ModelAssetGateway interface {
	OpenAsset(context.Context, generation.OwnerID, generation.TaskID, generation.AssetID) (generation.AssetContent, error)
}

// ModelRenderStore stores a rendered picture as the owner's input asset, so
// the reply can attach it and the Agent can be shown it.
type ModelRenderStore interface {
	UploadAsset(context.Context, generation.OwnerID, generation.InputAssetUpload) (generation.InputAsset, error)
}

// modelReviewState is the process-wide state of the read-only model review
// tools: the parsed-model cache and the concurrency bound. Both are declared
// in application.chat.model_review. The render.max_concurrent_renders slots
// bound renders and inspections alike, because an inspection's topology pass
// allocates in proportion to the model just as a render does.
type modelReviewState struct {
	cache *modelReviewCache
	slots chan struct{}
}

func newModelReviewState(review catalog.ChatModelReviewSchema) *modelReviewState {
	maxModels := review.Cache.MaxModels
	if maxModels < 1 {
		maxModels = 1
	}
	maxBytes := review.Cache.MaxBytes
	if maxBytes < 1 {
		maxBytes = 1
	}
	concurrent := review.Render.MaxConcurrentRenders
	if concurrent < 1 {
		concurrent = 1
	}
	return &modelReviewState{
		cache: &modelReviewCache{
			maxModels: maxModels,
			maxBytes:  maxBytes,
			entries:   map[string]*modelReviewEntry{},
			loads:     map[string]*modelReviewLoad{},
		},
		slots: make(chan struct{}, concurrent),
	}
}

func (state *modelReviewState) acquire(ctx context.Context) error {
	select {
	case state.slots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (state *modelReviewState) release() { <-state.slots }

// modelReviewEntry is one parsed model. Inspect is computed lazily and once,
// so a reply that inspects and then renders the same file walks the geometry
// once. modelinspect documents Model as safe for concurrent Inspect/Render.
type modelReviewEntry struct {
	key   string
	model *modelinspect.Model
	// fileBytes is the saved file the model reads from; the entry keeps it in
	// memory for the model's lifetime.
	fileBytes int64

	// inspecting is a one-slot semaphore that guards report. Unlike a
	// sync.Once, a call waiting for another call's inspection can give up
	// with its own context, and an inspection cancelled half way leaves no
	// result behind, so the next call computes it.
	inspecting  chan struct{}
	report      *modelinspect.Report
	reportBytes atomic.Int64

	// accounted is the size the cache last charged for the entry; the
	// cache's mutex guards it.
	accounted int64
}

func newModelReviewEntry(key string, model *modelinspect.Model, fileBytes int64) *modelReviewEntry {
	return &modelReviewEntry{key: key, model: model, fileBytes: fileBytes, inspecting: make(chan struct{}, 1)}
}

// retainedBytes is the memory the entry holds now: the saved file, the
// model's parsed structure and decoded caches (accessors expand to four
// bytes per component, textures to four bytes per pixel) and the report.
func (entry *modelReviewEntry) retainedBytes() int64 {
	return entry.fileBytes + entry.model.RetainedBytes() + entry.reportBytes.Load()
}

// inspect returns the entry's report, computing it once under one of the
// state's slots. The topology pass allocates in proportion to the model, so
// it waits for a slot like a render does and stops when ctx is done.
func (entry *modelReviewEntry) inspect(
	ctx context.Context,
	state *modelReviewState,
	options modelinspect.InspectOptions,
) (modelinspect.Report, error) {
	select {
	case entry.inspecting <- struct{}{}:
	case <-ctx.Done():
		return modelinspect.Report{}, ctx.Err()
	}
	defer func() { <-entry.inspecting }()
	if entry.report != nil {
		return *entry.report, nil
	}
	if err := state.acquire(ctx); err != nil {
		return modelinspect.Report{}, err
	}
	report, err := modelinspect.InspectContext(ctx, entry.model, options)
	state.release()
	if err != nil {
		return modelinspect.Report{}, err
	}
	entry.report = &report
	entry.reportBytes.Store(report.RetainedBytes())
	state.cache.refresh(entry)
	return report, nil
}

// modelReviewCache is a small LRU bounded by entry count and retained bytes,
// whichever binds first. An entry is charged what it actually holds (the
// saved file plus decoded data and report), not just the file size, and is
// recharged after Inspect and Render grow it. The key is the asset's
// SHA-256, so the same bytes reused under another Task or asset share one
// parse; concurrent first loads of one key share one parse too.
type modelReviewCache struct {
	mu        sync.Mutex
	maxModels int
	maxBytes  int64
	bytes     int64
	entries   map[string]*modelReviewEntry
	order     []string // least recently used first
	loads     map[string]*modelReviewLoad
}

// modelReviewLoad is one in-flight first load of a key. done closes when
// it ends; entry or parseErr then hold its outcome. A load that ended with
// neither could not read the file, and a waiting call loads it itself.
type modelReviewLoad struct {
	done     chan struct{}
	entry    *modelReviewEntry
	parseErr error
}

func (cache *modelReviewCache) get(key string) *modelReviewEntry {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	entry, found := cache.entries[key]
	if !found {
		return nil
	}
	cache.touch(key)
	return entry
}

// begin returns the cached entry for key, or the load already running for
// it, or a new load the caller must end with finish.
func (cache *modelReviewCache) begin(key string) (*modelReviewEntry, *modelReviewLoad, *modelReviewLoad) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if entry, found := cache.entries[key]; found {
		cache.touch(key)
		return entry, nil, nil
	}
	if running, found := cache.loads[key]; found {
		return nil, running, nil
	}
	started := &modelReviewLoad{done: make(chan struct{})}
	if cache.loads == nil {
		cache.loads = map[string]*modelReviewLoad{}
	}
	cache.loads[key] = started
	return nil, nil, started
}

// finish publishes a load's outcome, caches a parsed entry and releases the
// calls waiting for the load.
func (cache *modelReviewCache) finish(key string, load *modelReviewLoad, entry *modelReviewEntry, parseErr error) {
	cache.mu.Lock()
	delete(cache.loads, key)
	load.entry, load.parseErr = entry, parseErr
	if entry != nil {
		cache.insertLocked(key, entry)
	}
	cache.mu.Unlock()
	close(load.done)
}

func (cache *modelReviewCache) put(key string, entry *modelReviewEntry) {
	if key == "" || entry == nil {
		return
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.insertLocked(key, entry)
}

// insertLocked charges entry its current retained size. An entry larger
// than the whole budget is not cached.
func (cache *modelReviewCache) insertLocked(key string, entry *modelReviewEntry) {
	size := entry.retainedBytes()
	if size > cache.maxBytes {
		return
	}
	if existing, found := cache.entries[key]; found {
		cache.bytes -= existing.accounted
		delete(cache.entries, key)
		cache.removeOrder(key)
	}
	entry.accounted = size
	cache.entries[key] = entry
	cache.bytes += size
	cache.order = append(cache.order, key)
	cache.evictLocked()
}

// refresh recharges a cached entry at its current retained size, evicting
// least recently used entries to stay within the budget. An entry that
// alone outgrows the budget leaves the cache; calls holding it keep using
// it until they return.
func (cache *modelReviewCache) refresh(entry *modelReviewEntry) {
	if entry == nil {
		return
	}
	size := entry.retainedBytes()
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.entries[entry.key] != entry {
		return
	}
	cache.bytes += size - entry.accounted
	entry.accounted = size
	if size > cache.maxBytes {
		cache.bytes -= size
		delete(cache.entries, entry.key)
		cache.removeOrder(entry.key)
		return
	}
	cache.evictLocked()
}

func (cache *modelReviewCache) evictLocked() {
	for len(cache.order) > 0 && (len(cache.order) > cache.maxModels || cache.bytes > cache.maxBytes) {
		oldest := cache.order[0]
		cache.order = cache.order[1:]
		if evicted, found := cache.entries[oldest]; found {
			cache.bytes -= evicted.accounted
			delete(cache.entries, oldest)
		}
	}
}

func (cache *modelReviewCache) touch(key string) {
	cache.removeOrder(key)
	cache.order = append(cache.order, key)
}

func (cache *modelReviewCache) removeOrder(key string) {
	for index, held := range cache.order {
		if held == key {
			cache.order = append(cache.order[:index], cache.order[index+1:]...)
			return
		}
	}
}

// reviewedModel is a source resolved for the review tools: a succeeded Task of
// the command's owner and session and one of its saved model files.
type reviewedModel struct {
	task  generation.Task
	asset generation.Asset
}

var errModelReviewArguments = errors.New("model review: invalid arguments")

// InspectModel backs inspect_model: the modelinspect report of one saved
// model file. It never creates a Task or spends credits.
func (executor *Executor) InspectModel(ctx context.Context, command chat.ToolCommand) (chat.ToolResult, error) {
	review := executor.chat.ModelReview
	arguments, err := modelReviewArguments(command)
	if err != nil {
		return executor.modelReviewRefusal(command, executor.fillText(command, review.Texts.InvalidArguments,
			"{detail}", "the arguments are not a JSON object")), nil
	}
	source, refusal, err := executor.resolveReviewedModel(ctx, command, review,
		rawString(arguments[review.Source.TaskArgument]), rawString(arguments[review.Source.AssetArgument]))
	if err != nil {
		return chat.ToolResult{}, err
	}
	if refusal != nil {
		return *refusal, nil
	}
	entry, refusal, err := executor.loadReviewedModel(ctx, command, review, source)
	if err != nil {
		return chat.ToolResult{}, err
	}
	if refusal != nil {
		return *refusal, nil
	}
	options := modelReviewInspectOptions(review)
	report, err := entry.inspect(ctx, executor.modelReview, options)
	if err != nil {
		return chat.ToolResult{}, err
	}
	total := len(report.Parts)
	truncated := false
	if review.Inspect.MaxListedParts > 0 && total > review.Inspect.MaxListedParts {
		report.Parts = append([]modelinspect.PartReport(nil), report.Parts[:review.Inspect.MaxListedParts]...)
		if len(report.UV.Primitives) > review.Inspect.MaxListedParts {
			report.UV.Primitives = append([]modelinspect.PrimitiveUVReport(nil), report.UV.Primitives[:review.Inspect.MaxListedParts]...)
		}
		truncated = true
	}
	identity := executor.modelReviewIdentity(ctx, command, source)
	label := identity.Model
	if label == "" {
		label = identity.TaskID
	}
	usageCheck := newModelReviewUsageCheck(
		report,
		executor.chat.Model3DUsage,
		review.Inspect.Production,
		command.Options.ProductionBrief,
		func(template string, replacements ...string) string {
			return executor.fillText(command, template, replacements...)
		},
		executor.modelReviewFixAvailability(),
	)
	result := modelReviewInspectResult{
		OK: true,
		Message: executor.fillText(command, review.Texts.InspectReady,
			"{model}", label, "{triangles}", strconv.FormatInt(report.Triangles, 10)),
		Model:          identity,
		Report:         report,
		UsageCheck:     usageCheck,
		PartsTotal:     total,
		PartsTruncated: truncated,
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return chat.ToolResult{Error: "model report not readable", NoSubmission: true}, nil
	}
	hasUV, watertight, hasSkin := report.UV.AllHaveUV, report.Topology.Watertight, len(report.Skins) > 0
	summary := modelReviewSummaryJSON(modelReviewMessageSummary{
		Tool: "inspect", Model: rawString(arguments[review.Source.TaskArgument]),
		TaskID:    string(source.task.ID),
		Triangles: report.Triangles, HasUV: &hasUV, Watertight: &watertight, HasSkin: &hasSkin,
		Usage: usageCheck,
	})
	return chat.ToolResult{AgentContent: string(encoded), NoSubmission: true, Summary: summary}, nil
}

// RenderModelViews backs render_model_views: one PNG per requested view,
// attached to the reply and reported with the handle each picture receives.
func (executor *Executor) RenderModelViews(ctx context.Context, command chat.ToolCommand) (chat.ToolResult, error) {
	review := executor.chat.ModelReview
	if executor.modelAssets == nil || executor.inputAssets == nil || review.Render.MaxViewsPerCall <= 0 {
		return chat.ToolResult{Error: "model rendering unavailable", NoSubmission: true}, nil
	}
	arguments, err := modelReviewArguments(command)
	if err != nil {
		return executor.modelReviewRefusal(command, executor.fillText(command, review.Texts.InvalidArguments,
			"{detail}", "the arguments are not a JSON object")), nil
	}
	if refusal := executor.modelReviewBudgetRefusal(command, review); refusal != nil {
		return *refusal, nil
	}
	views, refusal := executor.modelReviewViewsLimit(command, review, arguments, review.Render.MaxViewsPerCall)
	if refusal != nil {
		return *refusal, nil
	}
	mode, refusal := executor.modelReviewMode(command, review, rawString(arguments["mode"]))
	if refusal != nil {
		return *refusal, nil
	}
	parts, found := modelReviewStringList(arguments["parts"])
	if !found {
		return executor.modelReviewRefusal(command, executor.fillText(command, review.Texts.InvalidArguments,
			"{detail}", "parts must be a list of part IDs")), nil
	}
	isolate := modelReviewBool(arguments["isolate"])
	if isolate && len(parts) == 0 {
		return executor.modelReviewRefusal(command, executor.localized(command, review.Texts.PartsRequired)), nil
	}
	size, ok := modelReviewSize(arguments["size"], review.Render.Size, review.Render.MaxSize)
	if !ok {
		return executor.modelReviewRefusal(command, executor.fillText(command, review.Texts.InvalidArguments,
			"{detail}", "size must be a positive integer")), nil
	}
	source, refusal, err := executor.resolveReviewedModel(ctx, command, review,
		rawString(arguments[review.Source.TaskArgument]), rawString(arguments[review.Source.AssetArgument]))
	if err != nil {
		return chat.ToolResult{}, err
	}
	if refusal != nil {
		return *refusal, nil
	}
	entry, refusal, err := executor.loadReviewedModel(ctx, command, review, source)
	if err != nil {
		return chat.ToolResult{}, err
	}
	if refusal != nil {
		return *refusal, nil
	}
	report, err := entry.inspect(ctx, executor.modelReview, modelReviewInspectOptions(review))
	if err != nil {
		return chat.ToolResult{}, err
	}
	if refusal := executor.modelReviewPartsRefusal(command, review, report, parts); refusal != nil {
		return *refusal, nil
	}
	if mode == modelinspect.ModeWireframe && report.Triangles > review.Render.WireframeMaxTriangles {
		return executor.modelReviewRefusal(command, executor.fillText(command, review.Texts.WireframeTooDense,
			"{triangles}", strconv.FormatInt(report.Triangles, 10),
			"{limit}", strconv.FormatInt(review.Render.WireframeMaxTriangles, 10))), nil
	}
	result := modelReviewRenderResult{
		Model:                executor.modelReviewIdentity(ctx, command, source),
		Views:                []modelReviewViewResult{},
		FailedViews:          []modelReviewFailedView{},
		RenderCallsRemaining: executor.modelReviewCallsRemaining(command, review),
	}
	for _, view := range views {
		picture, err := executor.renderReviewedView(ctx, review, entry, view, mode, parts, isolate, size)
		if err != nil {
			executor.logger.Warn("model review view not rendered",
				"task_id", source.task.ID, "asset_id", source.asset.ID, "view", view, "error", err)
			result.FailedViews = append(result.FailedViews, modelReviewFailedView{
				View: string(view), Error: executor.fillText(command, review.Texts.RenderFailed,
					"{view}", string(view), "{reason}", modelReviewErrorReason(err)),
			})
			continue
		}
		url, failure, err := executor.uploadReviewedPicture(ctx, command, review, picture,
			string(source.task.ID)+"-"+string(view))
		if err != nil {
			return chat.ToolResult{}, err
		}
		if failure != "" {
			result.FailedViews = append(result.FailedViews, modelReviewFailedView{View: string(view), Error: failure})
			continue
		}
		result.Views = append(result.Views, modelReviewViewResult{
			View: string(view), Mode: string(mode), Width: size, Height: size,
		})
		executor.appendReviewedPicture(&result.Pictures, &result.ImageURLs, url)
	}
	executor.finishRenderResult(command, review, &result.OK, &result.Message, &result.Note,
		len(result.Views), modelReviewViewNames(result.Views))
	encoded, err := json.Marshal(result)
	if err != nil {
		return chat.ToolResult{Error: "model render not readable", NoSubmission: true}, nil
	}
	return chat.ToolResult{
		AgentContent: string(encoded),
		NoSubmission: true,
		ImageURLs:    result.ImageURLs,
		Pictures:     result.Pictures,
		Summary: modelReviewSummaryJSON(modelReviewMessageSummary{
			Tool: "render", Model: rawString(arguments[review.Source.TaskArgument]),
			TaskID: string(source.task.ID),
			Views:  modelReviewSummaryViews(result.Views), FailedViews: modelReviewSummaryFailedViews(result.FailedViews),
			RenderCallsRemaining: result.RenderCallsRemaining,
		}),
	}, nil
}

// CompareModels backs compare_models: the two models side by side, left then
// right, one picture per requested view.
func (executor *Executor) CompareModels(ctx context.Context, command chat.ToolCommand) (chat.ToolResult, error) {
	review := executor.chat.ModelReview
	if executor.modelAssets == nil || executor.inputAssets == nil || review.Compare.MaxViewsPerCall <= 0 {
		return chat.ToolResult{Error: "model comparison unavailable", NoSubmission: true}, nil
	}
	arguments, err := modelReviewArguments(command)
	if err != nil {
		return executor.modelReviewRefusal(command, executor.fillText(command, review.Texts.InvalidArguments,
			"{detail}", "the arguments are not a JSON object")), nil
	}
	models, found := modelReviewStringList(arguments["models"])
	if !found {
		return executor.modelReviewRefusal(command, executor.fillText(command, review.Texts.InvalidArguments,
			"{detail}", "models must be a list of two task IDs or model handles")), nil
	}
	models = modelReviewTrimmed(models)
	if len(models) != 2 {
		return executor.modelReviewRefusal(command, executor.fillText(command, review.Texts.TooManyModels,
			"{count}", strconv.Itoa(len(models)))), nil
	}
	if refusal := executor.modelReviewBudgetRefusal(command, review); refusal != nil {
		return *refusal, nil
	}
	views, refusal := executor.modelReviewViewsLimit(command, review, arguments, review.Compare.MaxViewsPerCall)
	if refusal != nil {
		return *refusal, nil
	}
	mode, refusal := executor.modelReviewMode(command, review, rawString(arguments["mode"]))
	if refusal != nil {
		return *refusal, nil
	}
	sources := make([]reviewedModel, 0, 2)
	for _, token := range models {
		source, refusal, err := executor.resolveReviewedModel(ctx, command, review, token, "")
		if err != nil {
			return chat.ToolResult{}, err
		}
		if refusal != nil {
			return *refusal, nil
		}
		sources = append(sources, source)
	}
	entries := make([]*modelReviewEntry, 0, 2)
	for _, source := range sources {
		entry, refusal, err := executor.loadReviewedModel(ctx, command, review, source)
		if err != nil {
			return chat.ToolResult{}, err
		}
		if refusal != nil {
			return *refusal, nil
		}
		entries = append(entries, entry)
	}
	options := modelReviewInspectOptions(review)
	reports := make([]modelinspect.Report, 0, len(entries))
	for _, entry := range entries {
		report, err := entry.inspect(ctx, executor.modelReview, options)
		if err != nil {
			return chat.ToolResult{}, err
		}
		reports = append(reports, report)
	}
	if mode == modelinspect.ModeWireframe {
		for _, report := range reports {
			if report.Triangles > review.Render.WireframeMaxTriangles {
				return executor.modelReviewRefusal(command, executor.fillText(command, review.Texts.WireframeTooDense,
					"{triangles}", strconv.FormatInt(report.Triangles, 10),
					"{limit}", strconv.FormatInt(review.Render.WireframeMaxTriangles, 10))), nil
			}
		}
	}
	labels := executor.versionLabels(ctx, command.OwnerID, command.SessionID)
	result := modelReviewCompareResult{
		Models: []modelReviewModelStats{
			executor.modelReviewStats(command, sources[0], reports[0], labels),
			executor.modelReviewStats(command, sources[1], reports[1], labels),
		},
		Differences: modelReviewDifferences{
			Triangles: reports[1].Triangles - reports[0].Triangles,
			Vertices:  reports[1].Vertices - reports[0].Vertices,
			BBoxSize: [3]float64{
				reports[1].BBox.Size[0] - reports[0].BBox.Size[0],
				reports[1].BBox.Size[1] - reports[0].BBox.Size[1],
				reports[1].BBox.Size[2] - reports[0].BBox.Size[2],
			},
		},
		Views:                []modelReviewCompareView{},
		FailedViews:          []modelReviewFailedView{},
		RenderCallsRemaining: executor.modelReviewCallsRemaining(command, review),
	}
	for _, view := range views {
		left, err := executor.renderReviewedView(ctx, review, entries[0], view, mode, nil, false, review.Render.Size)
		if err != nil {
			result.FailedViews = append(result.FailedViews, modelReviewFailedView{
				View: string(view), Error: executor.fillText(command, review.Texts.RenderFailed,
					"{view}", string(view), "{reason}", modelReviewErrorReason(err)),
			})
			continue
		}
		right, err := executor.renderReviewedView(ctx, review, entries[1], view, mode, nil, false, review.Render.Size)
		if err != nil {
			result.FailedViews = append(result.FailedViews, modelReviewFailedView{
				View: string(view), Error: executor.fillText(command, review.Texts.RenderFailed,
					"{view}", string(view), "{reason}", modelReviewErrorReason(err)),
			})
			continue
		}
		picture := modelReviewSideBySide(left, right)
		url, failure, err := executor.uploadReviewedPicture(ctx, command, review, picture,
			string(sources[0].task.ID)+"-vs-"+string(sources[1].task.ID)+"-"+string(view))
		if err != nil {
			return chat.ToolResult{}, err
		}
		if failure != "" {
			result.FailedViews = append(result.FailedViews, modelReviewFailedView{View: string(view), Error: failure})
			continue
		}
		result.Views = append(result.Views, modelReviewCompareView{
			View: string(view), Mode: string(mode),
			Width: picture.Bounds().Dx(), Height: picture.Bounds().Dy(),
			LeftTaskID: string(sources[0].task.ID), RightTaskID: string(sources[1].task.ID),
		})
		executor.appendReviewedPicture(&result.Pictures, &result.ImageURLs, url)
	}
	result.OK = len(result.Views) > 0
	if result.OK {
		result.Message = executor.fillText(command, review.Texts.CompareReady,
			"{count}", strconv.Itoa(len(result.Views)), "{views}", modelReviewCompareViewNames(result.Views))
	}
	result.Note = executor.modelReviewNote(command, review, len(result.Views))
	encoded, err := json.Marshal(result)
	if err != nil {
		return chat.ToolResult{Error: "model comparison not readable", NoSubmission: true}, nil
	}
	return chat.ToolResult{
		AgentContent: string(encoded),
		NoSubmission: true,
		ImageURLs:    result.ImageURLs,
		Pictures:     result.Pictures,
		Summary: modelReviewSummaryJSON(modelReviewMessageSummary{
			Tool: "compare", Models: models,
			TaskIDs: []string{string(sources[0].task.ID), string(sources[1].task.ID)},
			Views:   modelReviewSummaryCompareViews(result.Views), FailedViews: modelReviewSummaryFailedViews(result.FailedViews),
			RenderCallsRemaining: result.RenderCallsRemaining,
		}),
	}, nil
}

// resolveReviewedModel applies the processing tools' source rules to a review
// call: a model handle, task ID or the selection, restricted to the command's
// owner and session and to succeeded tasks with a saved model file.
func (executor *Executor) resolveReviewedModel(
	ctx context.Context,
	command chat.ToolCommand,
	review catalog.ChatModelReviewSchema,
	taskToken, assetToken string,
) (reviewedModel, *chat.ToolResult, error) {
	task, assetID, rejection, err := executor.resolveModelSource(ctx, command, taskToken, assetToken,
		modelSourceContract{
			VariantPreference: executor.sourcePreference(),
			SourceRequired:    review.Source.SourceRequired,
			SourceNotFound:    review.Source.SourceNotFound,
			SourceNotReady:    review.Source.SourceNotReady,
			SourceNotModel:    review.Source.SourceNotModel,
		})
	if err != nil {
		return reviewedModel{}, nil, err
	}
	if rejection != nil {
		refusal := executor.modelReviewRefusal(command, executor.localized(command, rejection.message))
		return reviewedModel{}, &refusal, nil
	}
	if assetID == "" {
		refusal := executor.modelReviewRefusal(command, executor.localized(command, review.Source.SourceNotReady))
		return reviewedModel{}, &refusal, nil
	}
	asset, found := taskAsset(task, assetID)
	if !found {
		refusal := executor.modelReviewRefusal(command, executor.localized(command, review.Source.SourceNotFound))
		return reviewedModel{}, &refusal, nil
	}
	return reviewedModel{task: task, asset: asset}, nil, nil
}

func taskAsset(task generation.Task, id generation.AssetID) (generation.Asset, bool) {
	for _, asset := range task.Assets {
		if asset.ID == id {
			return asset, true
		}
	}
	return generation.Asset{}, false
}

// loadReviewedModel opens the saved file, bounds it by the Schema limits,
// parses it and caches the result by content digest.
func (executor *Executor) loadReviewedModel(
	ctx context.Context,
	command chat.ToolCommand,
	review catalog.ChatModelReviewSchema,
	source reviewedModel,
) (*modelReviewEntry, *chat.ToolResult, error) {
	media := strings.ToLower(strings.TrimSpace(source.asset.MediaType))
	if media == "" {
		media = modelReviewMediaType
	}
	if media != modelReviewMediaType {
		refusal := executor.modelReviewRefusal(command, executor.fillText(command, review.Source.SourceNotGLB,
			"{asset}", string(source.asset.ID), "{media}", media))
		return nil, &refusal, nil
	}
	limits := modelReviewLimits(review)
	if source.asset.Size > limits.MaxBytes {
		refusal := executor.modelReviewRefusal(command, executor.fillText(command, review.Texts.TooLarge,
			"{bytes}", strconv.FormatInt(source.asset.Size, 10),
			"{limit}", strconv.FormatInt(limits.MaxBytes, 10)))
		return nil, &refusal, nil
	}
	content, err := executor.modelAssets.OpenAsset(ctx,
		generation.OwnerID(command.OwnerID), source.task.ID, source.asset.ID)
	if err != nil {
		executor.logger.Warn("model review asset not readable",
			"task_id", source.task.ID, "asset_id", source.asset.ID, "error", err)
		refusal := executor.modelReviewRefusal(command, executor.fillText(command, review.Texts.SourceReadFailed,
			"{reason}", "the asset store could not open the saved file"))
		return nil, &refusal, nil
	}
	defer content.Body.Close()
	key := strings.ToLower(strings.TrimSpace(content.SHA256))
	if key == "" {
		key = strings.ToLower(strings.TrimSpace(source.asset.SHA256))
	}
	// A known digest answers from the cache without reading the saved bytes;
	// the body was still opened above, so the owner check always ran
	// (#103 review C7).
	var data []byte
	if key == "" {
		// The digest is only known once the bytes are read.
		var refusal *chat.ToolResult
		if data, refusal = executor.readReviewedModel(command, review, source, content.Body, limits); refusal != nil {
			return nil, refusal, nil
		}
		digest := sha256.Sum256(data)
		key = hex.EncodeToString(digest[:])
	}
	cache := executor.modelReview.cache
	for {
		entry, running, started := cache.begin(key)
		if entry != nil {
			return entry, nil, nil
		}
		if running != nil {
			// Another call is parsing the same bytes; share its result
			// instead of parsing a second copy.
			select {
			case <-running.done:
			case <-ctx.Done():
				return nil, nil, ctx.Err()
			}
			if running.entry != nil {
				return running.entry, nil, nil
			}
			if running.parseErr != nil {
				return nil, executor.modelReviewParseRefusal(command, review, running.parseErr), nil
			}
			// That call could not read the file; load it here.
			continue
		}
		entry, refusal := executor.parseReviewedModel(command, review, source, content.Body, limits, data, key, started)
		return entry, refusal, nil
	}
}

// parseReviewedModel runs the load this call started: it reads the file
// unless data already holds it, parses it and always ends the load, so
// waiting calls are never left blocked.
func (executor *Executor) parseReviewedModel(
	command chat.ToolCommand,
	review catalog.ChatModelReviewSchema,
	source reviewedModel,
	body io.Reader,
	limits modelinspect.Limits,
	data []byte,
	key string,
	load *modelReviewLoad,
) (entry *modelReviewEntry, refusal *chat.ToolResult) {
	var parseErr error
	defer func() { executor.modelReview.cache.finish(key, load, entry, parseErr) }()
	if data == nil {
		if data, refusal = executor.readReviewedModel(command, review, source, body, limits); refusal != nil {
			return nil, refusal
		}
	}
	model, err := modelinspect.Parse(bytes.NewReader(data), int64(len(data)), limits)
	if err != nil {
		executor.logger.Warn("model review parse failed",
			"task_id", source.task.ID, "asset_id", source.asset.ID, "error", err)
		parseErr = err
		return nil, executor.modelReviewParseRefusal(command, review, err)
	}
	return newModelReviewEntry(key, model, int64(len(data))), nil
}

// readReviewedModel reads the opened file within the Schema's byte limit.
func (executor *Executor) readReviewedModel(
	command chat.ToolCommand,
	review catalog.ChatModelReviewSchema,
	source reviewedModel,
	body io.Reader,
	limits modelinspect.Limits,
) ([]byte, *chat.ToolResult) {
	data, err := io.ReadAll(io.LimitReader(body, limits.MaxBytes+1))
	if err != nil {
		executor.logger.Warn("model review asset read failed",
			"task_id", source.task.ID, "asset_id", source.asset.ID, "error", err)
		refusal := executor.modelReviewRefusal(command, executor.fillText(command, review.Texts.SourceReadFailed,
			"{reason}", "the asset bytes could not be read"))
		return nil, &refusal
	}
	if int64(len(data)) > limits.MaxBytes {
		refusal := executor.modelReviewRefusal(command, executor.fillText(command, review.Texts.TooLarge,
			"{bytes}", strconv.FormatInt(int64(len(data)), 10),
			"{limit}", strconv.FormatInt(limits.MaxBytes, 10)))
		return nil, &refusal
	}
	return data, nil
}

// modelReviewParseRefusal explains why Parse rejected a file. A required
// mesh compression extension gets its own Schema text, because the file is
// valid but compressed and the Agent should say so instead of calling it
// broken.
func (executor *Executor) modelReviewParseRefusal(
	command chat.ToolCommand,
	review catalog.ChatModelReviewSchema,
	err error,
) *chat.ToolResult {
	var compressed *modelinspect.CompressionError
	if errors.As(err, &compressed) {
		refusal := executor.modelReviewRefusal(command, executor.fillText(command, review.Texts.Compressed,
			"{extension}", compressed.Extension))
		return &refusal
	}
	refusal := executor.modelReviewRefusal(command, executor.fillText(command, review.Texts.Unsupported,
		"{reason}", modelReviewErrorReason(err)))
	return &refusal
}

// renderReviewedView draws one view through the review state's concurrency
// bound and scales it down to the Schema's output edge.
func (executor *Executor) renderReviewedView(
	ctx context.Context,
	review catalog.ChatModelReviewSchema,
	entry *modelReviewEntry,
	view modelinspect.View,
	mode modelinspect.RenderMode,
	parts []string,
	isolate bool,
	size int,
) (*image.NRGBA, error) {
	if err := executor.modelReview.acquire(ctx); err != nil {
		return nil, err
	}
	defer executor.modelReview.release()
	supersample := review.Render.Supersample
	if supersample < 1 {
		supersample = 1
	}
	rendered, err := modelinspect.Render(entry.model, modelinspect.RenderRequest{
		View:                  view,
		Mode:                  mode,
		Width:                 size * supersample,
		Height:                size * supersample,
		Background:            modelReviewBackground(review.Render.Background),
		Highlight:             parts,
		Isolate:               isolate,
		Workers:               review.Render.Workers,
		WireframeMaxTriangles: int(review.Render.WireframeMaxTriangles),
		Context:               ctx,
	})
	// Rendering decodes normals, texture coordinates, colours and textures
	// into the model's caches, so the entry is recharged even on failure.
	executor.modelReview.cache.refresh(entry)
	if err != nil {
		return nil, err
	}
	return modelReviewDownsample(rendered, supersample), nil
}

// uploadReviewedPicture encodes a rendered view, stores it as the owner's
// input asset and returns the route the reply attaches. A storage failure is
// reported as the Schema's text instead of failing the whole call.
func (executor *Executor) uploadReviewedPicture(
	ctx context.Context,
	command chat.ToolCommand,
	review catalog.ChatModelReviewSchema,
	picture *image.NRGBA,
	name string,
) (string, string, error) {
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, picture); err != nil {
		return "", "", err
	}
	asset, err := executor.inputAssets.UploadAsset(ctx, generation.OwnerID(command.OwnerID),
		generation.InputAssetUpload{
			Body:             bytes.NewReader(encoded.Bytes()),
			OriginalFilename: modelReviewFilename(name),
			AssetType:        review.Render.AssetType,
			MediaType:        "image/png",
			MaxBytes:         int64(encoded.Len()),
		})
	if err != nil {
		executor.logger.Warn("model review picture not stored",
			"owner_id", command.OwnerID, "session_id", command.SessionID, "error", err)
		return "", executor.fillText(command, review.Texts.UploadFailed,
			"{reason}", "the asset store rejected the picture"), nil
	}
	url, ok := executor.viewURL("", string(asset.ID))
	if !ok {
		executor.logger.Warn("model review picture route is not configured")
		return "", executor.fillText(command, review.Texts.UploadFailed,
			"{reason}", "the asset route is not configured"), nil
	}
	return url, "", nil
}

func (executor *Executor) appendReviewedPicture(pictures *[]chat.ToolPicture, urls *[]string, url string) {
	*urls = append(*urls, url)
	*pictures = append(*pictures, chat.ToolPicture{URL: url})
}

// modelReviewBudgetRefusal refuses another render or comparison when this
// reply has used its declared number of render calls.
func (executor *Executor) modelReviewBudgetRefusal(
	command chat.ToolCommand,
	review catalog.ChatModelReviewSchema,
) *chat.ToolResult {
	used := executor.modelReviewRenderCalls(command)
	if review.Render.MaxRenderCallsPerTurn <= 0 || used < review.Render.MaxRenderCallsPerTurn {
		return nil
	}
	refusal := executor.modelReviewRefusal(command, executor.fillText(command, review.Texts.BudgetExhausted,
		"{limit}", strconv.Itoa(review.Render.MaxRenderCallsPerTurn)))
	return &refusal
}

// modelReviewRenderCalls replays the reply's earlier render and comparison
// calls, like find_history_images replays its lookups, so the bound holds
// across tool rounds and a resumed reply. Only a call that produced a view
// used a render slot: a recorded summary with no views is a refusal or a call
// whose every view failed. A call without a summary predates the summary
// record and keeps counting as before (#103 review C7).
func (executor *Executor) modelReviewRenderCalls(command chat.ToolCommand) int {
	render, compare := executor.modelReviewToolNames()
	used := 0
	for _, call := range command.EarlierCalls {
		if (render == "" || call.Name != render) && (compare == "" || call.Name != compare) {
			continue
		}
		if !modelReviewCallRendered(call.Summary) {
			continue
		}
		used++
	}
	return used
}

// modelReviewCallRendered reports whether a stored render or comparison call
// produced at least one view. No summary means the record predates summaries
// or the call was refused before one was written; neither is distinguishable,
// so it counts as before.
func modelReviewCallRendered(summary json.RawMessage) bool {
	if len(summary) == 0 {
		return true
	}
	var parsed modelReviewMessageSummary
	if err := json.Unmarshal(summary, &parsed); err != nil {
		return true
	}
	return len(parsed.Views) > 0
}

// modelReviewToolNames reads the render and comparison tool names from the
// Schema instead of repeating them in the execution layer.
func (executor *Executor) modelReviewToolNames() (render, compare string) {
	for _, tool := range executor.chat.AgentTools {
		switch tool.Execution.ImplementationKey {
		case "model.render":
			render = tool.Name
		case "model.compare":
			compare = tool.Name
		}
	}
	return render, compare
}

func (executor *Executor) modelReviewCallsRemaining(command chat.ToolCommand, review catalog.ChatModelReviewSchema) int {
	used := executor.modelReviewRenderCalls(command)
	remaining := review.Render.MaxRenderCallsPerTurn - used - 1
	if remaining < 0 {
		remaining = 0
	}
	return remaining
}

func (executor *Executor) modelReviewNote(
	command chat.ToolCommand,
	review catalog.ChatModelReviewSchema,
	count int,
) string {
	var notes []string
	if count > 0 {
		notes = append(notes,
			executor.fillText(command, review.Texts.ImagesAttached, "{count}", strconv.Itoa(count)),
			executor.fillText(command, review.Texts.BudgetRemaining,
				"{remaining}", strconv.Itoa(executor.modelReviewCallsRemaining(command, review)),
				"{limit}", strconv.Itoa(review.Render.MaxRenderCallsPerTurn)))
	}
	if !executor.chat.Agent.Vision {
		notes = append(notes, executor.localized(command, review.Texts.VisionOff))
	}
	return strings.Join(notes, " ")
}

func (executor *Executor) finishRenderResult(
	command chat.ToolCommand,
	review catalog.ChatModelReviewSchema,
	ok *bool,
	message *string,
	note *string,
	count int,
	names string,
) {
	*ok = count > 0
	if *ok {
		*message = executor.fillText(command, review.Texts.RenderReady,
			"{count}", strconv.Itoa(count), "{views}", names)
	}
	*note = executor.modelReviewNote(command, review, count)
}

// modelReviewViewsLimit reads the optional views argument, checks it against
// the Schema's enumeration and bounds, and removes duplicates.
func (executor *Executor) modelReviewViewsLimit(
	command chat.ToolCommand,
	review catalog.ChatModelReviewSchema,
	arguments map[string]json.RawMessage,
	limit int,
) ([]modelinspect.View, *chat.ToolResult) {
	names := review.Render.DefaultViews
	if raw, present := arguments["views"]; present {
		values, ok := modelReviewStringList(raw)
		if !ok {
			refusal := executor.modelReviewRefusal(command, executor.fillText(command, review.Texts.InvalidArguments,
				"{detail}", "views must be a list of view names"))
			return nil, &refusal
		}
		names = modelReviewTrimmed(values)
		if len(names) == 0 {
			refusal := executor.modelReviewRefusal(command, executor.fillText(command, review.Texts.InvalidArguments,
				"{detail}", "views names no view"))
			return nil, &refusal
		}
	}
	if limit > 0 && len(names) > limit {
		refusal := executor.modelReviewRefusal(command, executor.fillText(command, review.Texts.TooManyViews,
			"{count}", strconv.Itoa(len(names)), "{limit}", strconv.Itoa(limit)))
		return nil, &refusal
	}
	declared := make(map[string]bool, len(review.Render.Views))
	for _, name := range review.Render.Views {
		declared[name] = true
	}
	seen := make(map[string]bool, len(names))
	views := make([]modelinspect.View, 0, len(names))
	for _, name := range names {
		if !declared[name] {
			refusal := executor.modelReviewRefusal(command, executor.fillText(command, review.Texts.UnknownView,
				"{view}", name, "{views}", strings.Join(review.Render.Views, ", ")))
			return nil, &refusal
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		views = append(views, modelinspect.View(name))
	}
	return views, nil
}

func (executor *Executor) modelReviewMode(
	command chat.ToolCommand,
	review catalog.ChatModelReviewSchema,
	name string,
) (modelinspect.RenderMode, *chat.ToolResult) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = review.Render.DefaultMode
	}
	for _, declared := range review.Render.Modes {
		if name == declared {
			return modelinspect.RenderMode(name), nil
		}
	}
	refusal := executor.modelReviewRefusal(command, executor.fillText(command, review.Texts.UnknownMode,
		"{mode}", name, "{modes}", strings.Join(review.Render.Modes, ", ")))
	return "", &refusal
}

func (executor *Executor) modelReviewPartsRefusal(
	command chat.ToolCommand,
	review catalog.ChatModelReviewSchema,
	report modelinspect.Report,
	parts []string,
) *chat.ToolResult {
	if len(parts) == 0 {
		return nil
	}
	known := make(map[string]bool, len(report.Parts))
	for _, part := range report.Parts {
		known[part.ID] = true
	}
	for _, part := range parts {
		if !known[part] {
			refusal := executor.modelReviewRefusal(command, executor.fillText(command, review.Texts.UnknownPart,
				"{part}", part))
			return &refusal
		}
	}
	return nil
}

func (executor *Executor) modelReviewRefusal(command chat.ToolCommand, text string) chat.ToolResult {
	executor.logger.Warn("model review call refused",
		"tool", command.Tool.Name, "call_id", command.Call.ID,
		"owner_id", command.OwnerID, "session_id", command.SessionID, "reason", text)
	return chat.ToolResult{Error: text, NoSubmission: true}
}

// modelReviewIdentity names the reviewed model the way list_session_models
// does: platform IDs and public facts only, no upstream identifiers or URLs.
func (executor *Executor) modelReviewIdentity(
	ctx context.Context,
	command chat.ToolCommand,
	source reviewedModel,
) modelReviewIdentity {
	labels := executor.versionLabels(ctx, command.OwnerID, command.SessionID)
	return executor.modelReviewIdentityWithVersions(command, source, labels)
}

func (executor *Executor) modelReviewIdentityWithVersions(
	command chat.ToolCommand,
	source reviewedModel,
	versions map[generation.TaskID]string,
) modelReviewIdentity {
	localize := func(text string) string { return executor.localized(command, text) }
	return modelReviewIdentity{
		TaskID:     string(source.task.ID),
		AssetID:    string(source.asset.ID),
		Capability: string(source.task.Capability),
		Provider:   localize(source.task.Offering.ProviderName),
		ProviderID: source.task.Offering.ProviderID,
		Model:      localize(source.task.Offering.ModelName),
		Version:    versions[source.task.ID],
	}
}

func (executor *Executor) modelReviewStats(
	command chat.ToolCommand,
	source reviewedModel,
	report modelinspect.Report,
	versions map[generation.TaskID]string,
) modelReviewModelStats {
	return modelReviewModelStats{
		modelReviewIdentity: executor.modelReviewIdentityWithVersions(command, source, versions),
		Triangles:           report.Triangles,
		Vertices:            report.Vertices,
		BBox:                report.BBox,
	}
}

func modelReviewArguments(command chat.ToolCommand) (map[string]json.RawMessage, error) {
	arguments := map[string]json.RawMessage{}
	if len(command.Call.Arguments) == 0 {
		return arguments, nil
	}
	if err := json.Unmarshal(command.Call.Arguments, &arguments); err != nil || arguments == nil {
		return nil, errModelReviewArguments
	}
	return arguments, nil
}

func modelReviewStringList(raw json.RawMessage) ([]string, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, true
	}
	var values []string
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, false
	}
	return values, true
}

func modelReviewTrimmed(values []string) []string {
	trimmed := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			trimmed = append(trimmed, value)
		}
	}
	return trimmed
}

func modelReviewBool(raw json.RawMessage) bool {
	var value bool
	if len(raw) == 0 || json.Unmarshal(raw, &value) != nil {
		return false
	}
	return value
}

func modelReviewSize(raw json.RawMessage, fallback, maximum int) (int, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		if fallback <= 0 {
			return 0, false
		}
		if maximum > 0 && fallback > maximum {
			fallback = maximum
		}
		return fallback, true
	}
	var size int
	if err := json.Unmarshal(raw, &size); err != nil || size <= 0 {
		return 0, false
	}
	if maximum > 0 && size > maximum {
		size = maximum
	}
	return size, true
}

func modelReviewLimits(review catalog.ChatModelReviewSchema) modelinspect.Limits {
	limits := review.Inspect.Limits
	return modelinspect.Limits{
		MaxBytes:              limits.MaxBytes,
		MaxTriangles:          limits.MaxTriangles,
		MaxVertices:           limits.MaxVertices,
		MaxTexturePixels:      limits.MaxTexturePixels,
		MaxTotalTexturePixels: limits.MaxTotalTexturePixels,
		MaxTextureBytes:       limits.MaxTextureBytes,
		MaxNodes:              limits.MaxNodes,
		MaxDepth:              limits.MaxDepth,
	}
}

func modelReviewInspectOptions(review catalog.ChatModelReviewSchema) modelinspect.InspectOptions {
	// The usage check judges debris against the larger of the report's own
	// classification threshold and the production threshold, so topology
	// never hides a component the stricter check would count.
	debrisRatio := review.Inspect.DebrisRatio
	if production := review.Inspect.Production.DebrisRatio; production > debrisRatio {
		debrisRatio = production
	}
	return modelinspect.InspectOptions{
		WeldEpsilonRatio: review.Inspect.WeldEpsilonRatio,
		DebrisRatio:      debrisRatio,
	}
}

func modelReviewBackground(value string) color.NRGBA {
	value = strings.TrimPrefix(strings.TrimSpace(value), "#")
	if len(value) != 6 {
		return color.NRGBA{}
	}
	parsed, err := strconv.ParseUint(value, 16, 32)
	if err != nil {
		return color.NRGBA{}
	}
	return color.NRGBA{R: uint8(parsed >> 16), G: uint8(parsed >> 8), B: uint8(parsed), A: 0xff}
}

// modelReviewDownsample averages supersample x supersample blocks of the
// renderer's output down to the requested edge.
func modelReviewDownsample(source *image.NRGBA, factor int) *image.NRGBA {
	if source == nil || factor <= 1 {
		return source
	}
	bounds := source.Bounds()
	width, height := bounds.Dx()/factor, bounds.Dy()/factor
	output := image.NewNRGBA(image.Rect(0, 0, width, height))
	count := uint32(factor * factor)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			var red, green, blue, alpha uint32
			for dy := 0; dy < factor; dy++ {
				for dx := 0; dx < factor; dx++ {
					offset := source.PixOffset(bounds.Min.X+x*factor+dx, bounds.Min.Y+y*factor+dy)
					red += uint32(source.Pix[offset])
					green += uint32(source.Pix[offset+1])
					blue += uint32(source.Pix[offset+2])
					alpha += uint32(source.Pix[offset+3])
				}
			}
			offset := output.PixOffset(x, y)
			output.Pix[offset] = uint8(red / count)
			output.Pix[offset+1] = uint8(green / count)
			output.Pix[offset+2] = uint8(blue / count)
			output.Pix[offset+3] = uint8(alpha / count)
		}
	}
	return output
}

// modelReviewSideBySide places the left model beside the right one with a
// dark column between them.
func modelReviewSideBySide(left, right *image.NRGBA) *image.NRGBA {
	if left == nil || right == nil {
		return left
	}
	leftBounds, rightBounds := left.Bounds(), right.Bounds()
	height := max(leftBounds.Dy(), rightBounds.Dy())
	width := leftBounds.Dx() + modelReviewCompareGap + rightBounds.Dx()
	output := image.NewNRGBA(image.Rect(0, 0, width, height))
	separator := image.NewUniform(color.NRGBA{R: 0x33, G: 0x33, B: 0x33, A: 0xff})
	draw.Draw(output, image.Rect(0, 0, width, height), separator, image.Point{}, draw.Src)
	draw.Draw(output, image.Rect(0, 0, leftBounds.Dx(), leftBounds.Dy()), left, leftBounds.Min, draw.Src)
	draw.Draw(output, image.Rect(leftBounds.Dx()+modelReviewCompareGap, 0,
		leftBounds.Dx()+modelReviewCompareGap+rightBounds.Dx(), rightBounds.Dy()), right, rightBounds.Min, draw.Src)
	return output
}

func modelReviewFilename(name string) string {
	cleaned := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			return r
		default:
			return '-'
		}
	}, name)
	if cleaned == "" {
		cleaned = "model"
	}
	return "model-review-" + cleaned + ".png"
}

func modelReviewViewNames(views []modelReviewViewResult) string {
	names := make([]string, 0, len(views))
	for _, view := range views {
		names = append(names, view.View)
	}
	return strings.Join(names, ", ")
}

func modelReviewCompareViewNames(views []modelReviewCompareView) string {
	names := make([]string, 0, len(views))
	for _, view := range views {
		names = append(names, view.View)
	}
	return strings.Join(names, ", ")
}

// modelReviewErrorReason strips the package sentinel prefix from an error so
// the Schema's text names the actual cause.
func modelReviewErrorReason(err error) string {
	for _, sentinel := range []error{
		modelinspect.ErrInvalid, modelinspect.ErrUnsupported,
		modelinspect.ErrTooLarge, modelinspect.ErrTooDense,
	} {
		prefix := sentinel.Error() + ": "
		if strings.HasPrefix(err.Error(), prefix) {
			return strings.TrimPrefix(err.Error(), prefix)
		}
	}
	return err.Error()
}

type modelReviewIdentity struct {
	TaskID     string `json:"task_id"`
	AssetID    string `json:"asset_id"`
	Capability string `json:"capability,omitempty"`
	Provider   string `json:"provider,omitempty"`
	ProviderID string `json:"provider_id,omitempty"`
	Model      string `json:"model,omitempty"`
	Version    string `json:"version,omitempty"`
}

type modelReviewInspectResult struct {
	OK             bool                   `json:"ok"`
	Message        string                 `json:"message,omitempty"`
	Model          modelReviewIdentity    `json:"model"`
	Report         modelinspect.Report    `json:"report"`
	UsageCheck     *modelReviewUsageCheck `json:"usage_check,omitempty"`
	PartsTotal     int                    `json:"parts_total"`
	PartsTruncated bool                   `json:"parts_truncated"`
}

type modelReviewViewResult struct {
	View   string `json:"view"`
	Mode   string `json:"mode"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

type modelReviewFailedView struct {
	View  string `json:"view"`
	Error string `json:"error"`
}

type modelReviewRenderResult struct {
	OK                   bool                    `json:"ok"`
	Message              string                  `json:"message,omitempty"`
	Model                modelReviewIdentity     `json:"model"`
	Views                []modelReviewViewResult `json:"views"`
	FailedViews          []modelReviewFailedView `json:"failed_views"`
	RenderCallsRemaining int                     `json:"render_calls_remaining"`
	Note                 string                  `json:"note,omitempty"`
	ImageURLs            []string                `json:"-"`
	Pictures             []chat.ToolPicture      `json:"-"`
}

type modelReviewCompareView struct {
	View        string `json:"view"`
	Mode        string `json:"mode"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	LeftTaskID  string `json:"left_task_id"`
	RightTaskID string `json:"right_task_id"`
}

type modelReviewModelStats struct {
	modelReviewIdentity
	Triangles int64                   `json:"triangles"`
	Vertices  int64                   `json:"vertices"`
	BBox      modelinspect.BBoxReport `json:"bbox"`
}

type modelReviewDifferences struct {
	Triangles int64      `json:"triangles_b_minus_a"`
	Vertices  int64      `json:"vertices_b_minus_a"`
	BBoxSize  [3]float64 `json:"bbox_size_b_minus_a"`
}

type modelReviewCompareResult struct {
	OK                   bool                     `json:"ok"`
	Message              string                   `json:"message,omitempty"`
	Models               []modelReviewModelStats  `json:"models"`
	Differences          modelReviewDifferences   `json:"differences"`
	Views                []modelReviewCompareView `json:"views"`
	FailedViews          []modelReviewFailedView  `json:"failed_views"`
	RenderCallsRemaining int                      `json:"render_calls_remaining"`
	Note                 string                   `json:"note,omitempty"`
	ImageURLs            []string                 `json:"-"`
	Pictures             []chat.ToolPicture       `json:"-"`
}

// modelReviewMessageSummary is the compact result of one review tool call,
// stored with the call and shown as one line in the conversation (#103). It
// carries only facts the conversation renders; the full report stays in the
// Agent-facing result.
type modelReviewMessageSummary struct {
	// Tool is the Schema tool key: inspect, render or compare.
	Tool string `json:"tool"`
	// Model and Models are the handles or task IDs the call named, so the
	// person recognises the object; empty means the selected model.
	Model  string   `json:"model,omitempty"`
	Models []string `json:"models,omitempty"`
	// TaskID and TaskIDs are the resolved Tasks of the reviewed model(s), so
	// the session work ledger can attribute the conclusions to their steps
	// even when the call named the selected model (#248).
	TaskID  string   `json:"task_id,omitempty"`
	TaskIDs []string `json:"task_ids,omitempty"`
	// Views and FailedViews name the Schema view keys.
	Views                []string `json:"views,omitempty"`
	FailedViews          []string `json:"failed_views,omitempty"`
	RenderCallsRemaining int      `json:"render_calls_remaining,omitempty"`
	// Triangles, HasUV, Watertight and HasSkin are the fallback line when the
	// Catalog declares no purpose profiles.
	Triangles  int64                  `json:"triangles,omitempty"`
	HasUV      *bool                  `json:"has_uv,omitempty"`
	Watertight *bool                  `json:"watertight,omitempty"`
	HasSkin    *bool                  `json:"has_skin,omitempty"`
	Usage      *modelReviewUsageCheck `json:"usage,omitempty"`
}

func modelReviewSummaryViews(views []modelReviewViewResult) []string {
	names := make([]string, 0, len(views))
	for _, view := range views {
		names = append(names, view.View)
	}
	return names
}

func modelReviewSummaryCompareViews(views []modelReviewCompareView) []string {
	names := make([]string, 0, len(views))
	for _, view := range views {
		names = append(names, view.View)
	}
	return names
}

func modelReviewSummaryFailedViews(views []modelReviewFailedView) []string {
	names := make([]string, 0, len(views))
	for _, view := range views {
		names = append(names, view.View)
	}
	return names
}

func modelReviewSummaryJSON(summary modelReviewMessageSummary) json.RawMessage {
	encoded, err := json.Marshal(summary)
	if err != nil {
		return nil
	}
	return encoded
}
