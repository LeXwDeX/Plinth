// Package lineage projects the version chain of one session: which finished
// task was made from which earlier task or asset.
//
// It only reads. Every link comes from a fact the platform already stored:
// the frozen input of a task (task and asset parameters), a 3D capture record,
// or a byte-identical copy of a task asset. A reference that cannot be tied to
// a task of the session is reported as an unknown node; the package never
// guesses an origin, and it never reads another owner's data.
package lineage

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/capture"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// ErrInvalid reports a missing owner, session or an unusable configuration.
var ErrInvalid = errors.New("lineage: invalid request")

// Node kinds.
const (
	KindTask    = "task"
	KindUnknown = "unknown"
	// KindCapture is a screenshot of a 3D model: a picture of its own that no
	// task made, recorded with the model it was taken from.
	KindCapture = "capture"
	// KindUpload is an owned image or model upload used by a session task.
	KindUpload = "upload"
)

// OperationInpaint marks a task that edited only the masked part of its source.
const OperationInpaint = "inpaint"

// Edge kinds say how the child used its parent.
const (
	// EdgeSourceTask: the child's source-task parameter names the parent task.
	EdgeSourceTask = "source_task"
	// EdgeSourceAsset: the child processes an asset of the parent.
	EdgeSourceAsset = "source_asset"
	// EdgeReference: the child used an asset of the parent as a reference.
	EdgeReference = "reference"
	// EdgeCapture: the child used a screenshot captured from the parent model.
	EdgeCapture = "capture"
)

// Evidence says why the platform believes the link.
const (
	// EvidenceRecorded: an ID stored on the child's input or on a capture
	// record names the parent exactly.
	EvidenceRecorded = "recorded"
	// EvidenceSameBytes: the child's input asset is byte-identical (SHA-256)
	// to exactly one asset of the parent. A re-upload of a generated picture is
	// a new ID with the same bytes, so this is the only exact tie available.
	EvidenceSameBytes = "same_bytes"
	// EvidenceUnknown: the child names a parent the session cannot identify.
	EvidenceUnknown = "unknown"
)

// Tasks lists a session's tasks and reads one in full. A listing carries
// summaries only; the frozen input that records a task's parents comes from
// the full read.
type Tasks interface {
	GetScoped(context.Context, generation.TaskListScope, generation.TaskID) (generation.Task, error)
	List(context.Context, generation.TaskListScope, generation.ListQuery) (generation.TaskPage, error)
}

type Captures interface {
	ListSession(ctx context.Context, owner, session string) ([]capture.Record, error)
}

type InputAssets interface {
	LoadInputAssets(context.Context, generation.OwnerID, []generation.AssetID) (map[generation.AssetID]generation.InputAsset, error)
}

// Config bounds the work of one projection.
type Config struct {
	// MaxTasks is the most tasks of a session that are read.
	MaxTasks int
	// Conversions recognizes format conversions (#330): a conversion Task of
	// a session source is folded into that source instead of being a version
	// of its own. Nil folds nothing.
	Conversions ConversionRule
	// FoldConversionsSince keeps every conversion created before it as the
	// version it has always been (#330 review): chat history, tool results
	// and Agent replies already name those versions by number ("3D v2"), so
	// folding them would renumber what the history says. Such a node is
	// marked Conversion instead. The zero time folds every conversion.
	FoldConversionsSince time.Time
}

// ConversionRule is the Product Schema's model_delivery conversions as the
// version chain reads them. A format conversion does not change the model;
// its files are files of the source version.
type ConversionRule interface {
	// ConversionOffering reports an Offering that may run a conversion; a
	// summary without its frozen input is read in full only then.
	ConversionOffering(generation.OfferingID) bool
	// IsConversion reports a Task whose Offering and frozen input run one.
	IsConversion(generation.OfferingID, json.RawMessage) bool
}

type Service struct {
	tasks    Tasks
	captures Captures
	inputs   InputAssets
	config   Config
	groups   GroupSource
}

// SetGroups wires the source of task groups. Without one the graph lists none.
func (s *Service) SetGroups(groups GroupSource) { s.groups = groups }

// New builds the projection. Captures may be nil when the deployment records
// none; then capture-based links are unknown.
func New(tasks Tasks, captures Captures, inputs InputAssets, config Config) (*Service, error) {
	if tasks == nil || inputs == nil || config.MaxTasks < 1 {
		return nil, ErrInvalid
	}
	return &Service{tasks: tasks, captures: captures, inputs: inputs, config: config}, nil
}

type Asset struct {
	DeliveryProfile string `json:"delivery_profile,omitempty"`
	ID              string `json:"id"`
	Role            string `json:"role"`
	Variant         string `json:"variant,omitempty"`
	MediaType       string `json:"media_type"`
	Ordinal         int    `json:"ordinal"`
	// Required marks a required output of the task, as against an
	// attachment; the stage shows a required model first (#326).
	Required bool `json:"required,omitempty"`
}

type Node struct {
	ID         string     `json:"id"`
	Kind       string     `json:"kind"`
	TaskID     string     `json:"task_id,omitempty"`
	Capability string     `json:"capability,omitempty"`
	CreatedAt  *time.Time `json:"created_at,omitempty"`
	Assets     []Asset    `json:"assets,omitempty"`
	// Group and Slot name the group this task is one member of, and which
	// member it is, so a version strip can draw the group as one version.
	Group string `json:"group,omitempty"`
	Slot  string `json:"slot,omitempty"`
	// Operation says what a task did when its capability alone does not: a
	// picture edit with a mask is an inpaint.
	Operation string `json:"operation,omitempty"`
	// CaptureID, PixelWidth and PixelHeight describe a capture node.
	CaptureID   string `json:"capture_id,omitempty"`
	PixelWidth  int    `json:"pixel_width,omitempty"`
	PixelHeight int    `json:"pixel_height,omitempty"`
	// Version is the session-wide per-domain ordinal, present only on paged projections.
	Version int `json:"version,omitempty"`
	// Conversion marks a format conversion of a session version that keeps a
	// node of its own because it was made before conversions were folded.
	Conversion bool                 `json:"conversion,omitempty"`
	State      generation.TaskState `json:"-"`
	Progress   int                  `json:"-"`
	Failure    *generation.Failure  `json:"-"`
}

// Group kinds.
const GroupThreeView = "three_view"

// GroupMember is one finished task of a group.
type GroupMember struct {
	Slot   string `json:"slot"`
	Label  string `json:"label"`
	TaskID string `json:"task_id"`
}

// Group is a set of tasks that make up one version: the views of one
// three-view run. Complete is true when every slot of the group finished.
// SourceSlot names the member a 3D model is made from.
type Group struct {
	ID         string        `json:"id"`
	Kind       string        `json:"kind"`
	Label      string        `json:"label"`
	Complete   bool          `json:"complete"`
	SourceSlot string        `json:"source_slot,omitempty"`
	CreatedAt  time.Time     `json:"created_at"`
	Members    []GroupMember `json:"members"`
}

// GroupSource lists the groups of one session. It reads the session's own
// records; lineage only keeps the members that are nodes of its graph.
type GroupSource interface {
	LineageGroups(ctx context.Context, owner, session string) ([]Group, error)
}

type Edge struct {
	From      string `json:"from"`
	To        string `json:"to"`
	Kind      string `json:"kind"`
	Evidence  string `json:"evidence"`
	AssetID   string `json:"asset_id,omitempty"`
	CaptureID string `json:"capture_id,omitempty"`
}

// Graph lists nodes oldest first. Edges point from the parent to the task made
// from it. Truncated means older tasks of the session were not read, so some
// parents may show as unknown.
type Graph struct {
	Nodes []Node `json:"nodes"`
	Edges []Edge `json:"edges"`
	// Groups lists the groups that have at least one finished member in the
	// graph, newest first. It is absent when the session has none.
	Groups     []Group `json:"groups,omitempty"`
	Truncated  bool    `json:"truncated"`
	NextCursor string  `json:"-"`
	// Conversions maps each folded format-conversion Task of the session to
	// the version it belongs to (#330). Such a Task has no node; edges from
	// it start at that version.
	Conversions map[string]string `json:"-"`
}

const pageSize = 100

// inputBatch keeps each asset lookup well below SQLite's variable limit.
const inputBatch = 200

// Session builds the lineage of one session for its owner. The caller has
// already proven the session belongs to the owner; tasks, captures and input
// assets are read with the owner as a filter regardless.
func (s *Service) Session(ctx context.Context, owner, session string) (Graph, error) {
	if strings.TrimSpace(owner) == "" || strings.TrimSpace(session) == "" {
		return Graph{}, ErrInvalid
	}
	tasks, truncated, err := s.readTasks(ctx, owner, session)
	if err != nil {
		return Graph{}, err
	}
	var records []capture.Record
	if s.captures != nil {
		if records, err = s.captures.ListSession(ctx, owner, session); err != nil {
			return Graph{}, err
		}
	}
	builder := newBuilder(tasks, records)
	builder.conversions, builder.foldSince = s.config.Conversions, s.config.FoldConversionsSince
	pending, err := s.loadInputs(ctx, owner, builder.assetIDsToLoad())
	if err != nil {
		return Graph{}, err
	}
	builder.modelUploads, err = s.loadNativeModelInputs(ctx, owner, builder.references)
	if err != nil {
		return Graph{}, err
	}
	graph := builder.build(pending)
	graph.Truncated = truncated
	if graph.Groups, err = s.groupsOf(ctx, owner, session, &graph); err != nil {
		return Graph{}, err
	}
	return graph, nil
}

// groupsOf keeps the members of each group that are nodes of the graph and
// marks those nodes with their group. A group none of whose tasks is in the
// graph is left out; a task the graph does not hold is never named.
func (s *Service) groupsOf(ctx context.Context, owner, session string, graph *Graph) ([]Group, error) {
	if s.groups == nil {
		return nil, nil
	}
	groups, err := s.groups.LineageGroups(ctx, owner, session)
	if err != nil {
		return nil, err
	}
	index := make(map[string]int, len(graph.Nodes))
	for i, node := range graph.Nodes {
		if node.TaskID != "" {
			index[node.TaskID] = i
		}
	}
	var result []Group
	for _, group := range groups {
		kept := group
		kept.Members = nil
		for _, member := range group.Members {
			position, ok := index[member.TaskID]
			if !ok || graph.Nodes[position].Group != "" {
				continue
			}
			graph.Nodes[position].Group, graph.Nodes[position].Slot = group.ID, member.Slot
			kept.Members = append(kept.Members, member)
		}
		if len(kept.Members) > 0 {
			result = append(result, kept)
		}
	}
	return result, nil
}

func (s *Service) readTasks(ctx context.Context, owner, session string) ([]generation.Task, bool, error) {
	scope := generation.OwnTasks(generation.OwnerID(owner))
	var tasks []generation.Task
	cursor := ""
	for {
		limit := pageSize
		if remaining := s.config.MaxTasks - len(tasks); remaining < limit {
			limit = remaining
		}
		page, err := s.tasks.List(ctx, scope, generation.ListQuery{
			SessionID: session, Cursor: cursor, Limit: limit,
			States: []generation.TaskState{generation.TaskSucceeded},
		})
		if err != nil {
			return nil, false, err
		}
		for _, summary := range page.Tasks {
			// The query already filters; these checks keep a sloppy
			// Repository from leaking another owner's or session's task.
			if string(summary.OwnerID) != owner || summary.SessionID != session {
				continue
			}
			task, err := s.tasks.GetScoped(ctx, scope, summary.ID)
			if err != nil {
				var failure *generation.Error
				if errors.As(err, &failure) && failure.Code == generation.ErrNotFound {
					continue // deleted between the listing and the read
				}
				return nil, false, err
			}
			if string(task.OwnerID) == owner && task.SessionID == session {
				tasks = append(tasks, task)
			}
		}
		if page.NextCursor == "" || len(page.Tasks) == 0 {
			return tasks, false, nil
		}
		if len(tasks) >= s.config.MaxTasks {
			return tasks, true, nil
		}
		cursor = page.NextCursor
	}
}

func (s *Service) loadInputs(ctx context.Context, owner string, ids []generation.AssetID) (map[generation.AssetID]generation.InputAsset, error) {
	result := make(map[generation.AssetID]generation.InputAsset, len(ids))
	for start := 0; start < len(ids); start += inputBatch {
		end := min(start+inputBatch, len(ids))
		found, err := s.inputs.LoadInputAssets(ctx, generation.OwnerID(owner), ids[start:end])
		if err != nil {
			return nil, err
		}
		for id, asset := range found {
			if string(asset.OwnerID) == owner || asset.OwnerID == "" {
				result[id] = asset
			}
		}
	}
	return result, nil
}

// reference is one parent claim found on a task's frozen input.
type reference struct {
	uploadedModel bool
	child         string
	kind          string
	taskID        string
	assetID       string
}

type builder struct {
	modelUploads    map[generation.AssetID]generation.InputAsset
	includeAttempts bool
	tasks           []generation.Task
	byTask          map[string]generation.Task
	assetTask       map[string]string   // task asset ID -> task ID
	hashTasks       map[string][]string // sha256 -> task IDs having an asset with it
	captureBy       map[string]capture.Record
	references      []reference
	nodes           []Node
	edges           []Edge
	edgeSeen        map[Edge]struct{}
	unknown         map[string]string // key -> unknown node ID
	captures        map[string]string // capture ID -> capture node ID
	uploads         map[string]string // input asset ID -> upload node ID
	records         []capture.Record
	conversions     ConversionRule
	// folded maps a format-conversion Task to the Task whose files it made
	// (#330); it accumulates across builds of one projection. kept holds the
	// conversions made before foldSince, which stay versions.
	folded    map[string]string
	kept      map[string]bool
	foldSince time.Time
}

func newBuilder(tasks []generation.Task, records []capture.Record) *builder {
	b := &builder{
		byTask:    make(map[string]generation.Task, len(tasks)),
		assetTask: map[string]string{},
		hashTasks: map[string][]string{},
		captureBy: map[string]capture.Record{},
		edgeSeen:  map[Edge]struct{}{},
		unknown:   map[string]string{},
		captures:  map[string]string{},
		uploads:   map[string]string{},
		records:   records,
		folded:    map[string]string{},
		kept:      map[string]bool{},
	}
	sort.SliceStable(tasks, func(i, j int) bool {
		if !tasks[i].CreatedAt.Equal(tasks[j].CreatedAt) {
			return tasks[i].CreatedAt.Before(tasks[j].CreatedAt)
		}
		return tasks[i].ID < tasks[j].ID
	})
	for _, task := range tasks {
		if task.State != generation.TaskSucceeded || len(task.Assets) == 0 {
			continue
		}
		id := string(task.ID)
		b.tasks = append(b.tasks, task)
		b.byTask[id] = task
		seen := map[string]bool{}
		for _, asset := range task.Assets {
			b.assetTask[string(asset.ID)] = id
			if hash := strings.ToLower(asset.SHA256); hash != "" && !seen[hash] {
				seen[hash] = true
				b.hashTasks[hash] = append(b.hashTasks[hash], id)
			}
		}
	}
	for _, record := range records {
		b.captureBy[record.ScreenshotAssetID] = record
	}
	for _, task := range b.tasks {
		b.references = append(b.references, taskReferences(task)...)
	}
	return b
}

// taskReferences reads the parent claims of one task from its frozen input,
// guided by the parameter types its own Offering declares.
func taskReferences(task generation.Task) []reference {
	var values map[string]json.RawMessage
	if json.Unmarshal(task.Input, &values) != nil {
		return nil
	}
	var result []reference
	for _, parameter := range task.Offering.Parameters {
		if parameter.Semantic == schema.SemanticMask {
			continue
		}
		raw, exists := values[parameter.Name]
		if !exists {
			continue
		}
		role, isSource := schema.SourceParameterRole(parameter)
		switch parameter.Type {
		case "task":
			var id string
			if json.Unmarshal(raw, &id) == nil && id != "" {
				result = append(result, reference{child: string(task.ID), kind: EdgeSourceTask, taskID: id})
			}
		case "asset":
			var id string
			if json.Unmarshal(raw, &id) == nil && id != "" {
				result = append(result, reference{child: string(task.ID), kind: assetEdgeKind(role, isSource), assetID: id, uploadedModel: isSource && role == schema.SemanticSourceAsset && parameter.UploadedModel != nil})
			}
		case "asset-array":
			var ids []string
			if json.Unmarshal(raw, &ids) == nil {
				for _, id := range ids {
					if id != "" {
						result = append(result, reference{child: string(task.ID), kind: assetEdgeKind(role, isSource), assetID: id})
					}
				}
			}
		}
	}
	return result
}

func assetEdgeKind(role schema.ParameterSemantic, isSource bool) string {
	if isSource && role == schema.SemanticSourceTask {
		return EdgeSourceTask
	}
	if isSource {
		return EdgeSourceAsset
	}
	return EdgeReference
}

// assetIDsToLoad lists the referenced asset IDs that are neither assets of the
// session's tasks nor capture screenshots: only those need a byte-identity
// lookup.
func (b *builder) assetIDsToLoad() []generation.AssetID {
	seen := map[string]bool{}
	var ids []generation.AssetID
	for _, reference := range b.references {
		id := reference.assetID
		if id == "" || seen[id] {
			continue
		}
		if _, ok := b.assetTask[id]; ok {
			continue
		}
		if _, ok := b.captureBy[id]; ok {
			continue
		}
		seen[id] = true
		ids = append(ids, generation.AssetID(id))
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// foldConversions records each format-conversion Task whose source (its
// source-task or source-asset parameter) is a Task of the session. A
// conversion of an upload or of a Task outside the session stays a version.
func (b *builder) foldConversions() {
	if b.conversions == nil {
		return
	}
	for _, task := range b.tasks {
		id := string(task.ID)
		if _, done := b.folded[id]; done || b.kept[id] || !b.conversions.IsConversion(task.Offering.ID, task.Input) {
			continue
		}
		for _, reference := range b.references {
			if reference.child != id || (reference.kind != EdgeSourceTask && reference.kind != EdgeSourceAsset) {
				continue
			}
			parent := ""
			if reference.taskID != "" {
				if _, ok := b.byTask[reference.taskID]; ok {
					parent = reference.taskID
				}
			} else if _, upload := b.modelUploads[generation.AssetID(reference.assetID)]; !upload || !reference.uploadedModel {
				// A parameter that also takes uploaded models still names a
				// session Task's saved file here; link resolves it the same way.
				parent = b.assetTask[reference.assetID]
			}
			if parent != "" && parent != id {
				// A local delivery completion makes an old failed conversion
				// successful without adding a version: its number was never on
				// the strip, and inserting it now would renumber later history.
				if task.CreatedAt.Before(b.foldSince) && !task.DeliveryCompleted {
					b.kept[id] = true
				} else {
					b.folded[id] = parent
				}
				break
			}
		}
	}
}

// version is the Task a node or an edge of id stands for: id itself, or the
// source a folded conversion belongs to.
func (b *builder) version(id string) string {
	for range 32 {
		parent, folded := b.folded[id]
		if !folded {
			return id
		}
		id = parent
	}
	return id
}

func (b *builder) build(inputs map[generation.AssetID]generation.InputAsset) Graph {
	b.foldConversions()
	for _, task := range b.tasks {
		if _, folded := b.folded[string(task.ID)]; folded {
			continue
		}
		createdAt := task.CreatedAt
		node := Node{
			ID: string(task.ID), Kind: KindTask, TaskID: string(task.ID),
			Capability: string(task.Capability), CreatedAt: &createdAt,
			Operation: taskOperation(task), Conversion: b.kept[string(task.ID)],
		}
		if b.includeAttempts {
			node.State, node.Progress, node.Failure = task.State, task.Progress, task.Failure
		}
		for _, asset := range task.Assets {
			if b.includeAttempts && task.State != generation.TaskSucceeded {
				break
			}
			if asset.StorageKey == "" {
				continue
			}
			node.Assets = append(node.Assets, Asset{
				DeliveryProfile: asset.DeliveryProfile,
				ID:              string(asset.ID), Role: asset.Role, Variant: asset.Variant,
				MediaType: asset.MediaType, Ordinal: asset.Ordinal, Required: asset.Required,
			})
		}
		b.nodes = append(b.nodes, node)
	}
	for _, reference := range b.references {
		b.link(reference, inputs)
	}
	// A screenshot is a version even when nothing has been made from it yet.
	for _, record := range b.records {
		if _, inSession := b.byTask[record.SourceTaskID]; inSession {
			b.captureNode(record)
		}
	}
	b.sortNodes()
	if b.nodes == nil {
		b.nodes = []Node{}
	}
	if b.edges == nil {
		b.edges = []Edge{}
	}
	var conversions map[string]string
	for id := range b.folded {
		if conversions == nil {
			conversions = map[string]string{}
		}
		conversions[id] = b.version(id)
	}
	return Graph{Nodes: b.nodes, Edges: b.edges, Conversions: conversions}
}

func (b *builder) link(reference reference, inputs map[generation.AssetID]generation.InputAsset) {
	child := reference.child
	if _, folded := b.folded[child]; folded {
		return
	}
	switch {
	case reference.taskID != "":
		if _, ok := b.byTask[reference.taskID]; ok {
			b.addEdge(Edge{From: reference.taskID, To: child, Kind: reference.kind, Evidence: EvidenceRecorded})
			return
		}
		b.addEdge(Edge{From: b.unknownNode("task:" + reference.taskID), To: child, Kind: reference.kind, Evidence: EvidenceUnknown})
	case reference.assetID != "":
		if source, ok := b.modelUploads[generation.AssetID(reference.assetID)]; ok && reference.uploadedModel {
			b.addEdge(Edge{From: b.uploadNode(source, "model"), To: child, Kind: reference.kind, Evidence: EvidenceRecorded, AssetID: reference.assetID})
			return
		}
		if parent, ok := b.assetTask[reference.assetID]; ok {
			b.addEdge(Edge{From: parent, To: child, Kind: reference.kind, Evidence: EvidenceRecorded, AssetID: reference.assetID})
			return
		}
		if record, ok := b.captureBy[reference.assetID]; ok {
			if _, inSession := b.byTask[record.SourceTaskID]; inSession {
				// The screenshot is a version between the model and what was made from it.
				node := b.captureNode(record)
				b.addEdge(Edge{From: node, To: child, Kind: reference.kind, Evidence: EvidenceRecorded,
					AssetID: record.ScreenshotAssetID, CaptureID: record.ID})
				return
			}
			b.addEdge(Edge{From: b.unknownNode("capture:" + record.ID), To: child, Kind: EdgeCapture, Evidence: EvidenceUnknown, CaptureID: record.ID})
			return
		}
		if input, ok := inputs[generation.AssetID(reference.assetID)]; ok {
			if matches := b.hashTasks[strings.ToLower(input.SHA256)]; input.SHA256 != "" && len(matches) == 1 {
				if asset, ok := b.sameBytesAsset(matches[0], input.SHA256); ok {
					b.addEdge(Edge{From: matches[0], To: child, Kind: reference.kind, Evidence: EvidenceSameBytes, AssetID: asset})
					return
				}
			}
		}
		if input, ok := inputs[generation.AssetID(reference.assetID)]; ok && isUpload(input) {
			b.addEdge(Edge{From: b.uploadNode(input, "image"), To: child, Kind: reference.kind, Evidence: EvidenceRecorded, AssetID: reference.assetID})
			return
		}
		b.addEdge(Edge{From: b.unknownNode("asset:" + reference.assetID), To: child, Kind: reference.kind, Evidence: EvidenceUnknown})
	}
}

// isUpload tells a picture the person uploaded from an output of some task:
// only uploads carry the declared asset type and original file name.
func isUpload(input generation.InputAsset) bool {
	return input.AssetType != "" && strings.HasPrefix(strings.ToLower(input.MediaType), "image/")
}

// captureNode returns the node of a screenshot, creating it once, and ties it
// to the model it was taken from.
func (b *builder) captureNode(record capture.Record) string {
	if id, ok := b.captures[record.ID]; ok {
		return id
	}
	id := "capture:" + record.ID
	b.captures[record.ID] = id
	createdAt := record.CreatedAt
	b.nodes = append(b.nodes, Node{
		ID: id, Kind: KindCapture, CreatedAt: &createdAt, CaptureID: record.ID,
		PixelWidth: record.PixelWidth, PixelHeight: record.PixelHeight,
		Assets: []Asset{{ID: record.ScreenshotAssetID, Role: "image", MediaType: "image/png"}},
	})
	b.addEdge(Edge{From: record.SourceTaskID, To: id, Kind: EdgeCapture, Evidence: EvidenceRecorded,
		AssetID: record.ModelAssetID, CaptureID: record.ID})
	return id
}

// uploadNode returns a real owned upload node without a public Task identity.
func (b *builder) uploadNode(input generation.InputAsset, role string) string {
	key := string(input.ID)
	if id, ok := b.uploads[key]; ok {
		return id
	}
	id := "upload:" + key
	b.uploads[key] = id
	createdAt := input.CreatedAt
	b.nodes = append(b.nodes, Node{
		ID: id, Kind: KindUpload, CreatedAt: &createdAt,
		Assets: []Asset{{ID: key, Role: role, MediaType: input.MediaType}},
	})
	return id
}

// sortNodes keeps nodes oldest first; the unknown parents, which have no time,
// stay behind them in the order they were met.
func (b *builder) sortNodes() {
	sort.SliceStable(b.nodes, func(i, j int) bool {
		left, right := b.nodes[i].CreatedAt, b.nodes[j].CreatedAt
		if left == nil || right == nil {
			return left != nil && right == nil
		}
		return left.Before(*right)
	})
}

// taskOperation names what a task did beyond its capability: a mask that was
// actually supplied makes a picture edit an inpaint.
func taskOperation(task generation.Task) string {
	var values map[string]json.RawMessage
	if json.Unmarshal(task.Input, &values) != nil {
		return ""
	}
	for _, parameter := range task.Offering.Parameters {
		if parameter.Semantic != schema.SemanticMask {
			continue
		}
		raw, exists := values[parameter.Name]
		if !exists {
			continue
		}
		var id string
		if json.Unmarshal(raw, &id) == nil && id != "" {
			return OperationInpaint
		}
	}
	return ""
}

// sameBytesAsset names the single asset of the task with these bytes; two
// assets with identical bytes would make the pick arbitrary, so it refuses.
func (b *builder) sameBytesAsset(taskID, hash string) (string, bool) {
	found := ""
	for _, asset := range b.byTask[taskID].Assets {
		if strings.EqualFold(asset.SHA256, hash) {
			if found != "" {
				return "", false
			}
			found = string(asset.ID)
		}
	}
	return found, found != ""
}

// unknownNode returns the ID of the unknown parent for a key, creating it once.
// It never carries the unresolved platform ID, which may belong to someone else.
func (b *builder) unknownNode(key string) string {
	if id, ok := b.unknown[key]; ok {
		return id
	}
	id := "unknown-" + strconv.Itoa(len(b.unknown)+1)
	b.unknown[key] = id
	b.nodes = append(b.nodes, Node{ID: id, Kind: KindUnknown})
	return id
}

func (b *builder) addEdge(edge Edge) {
	edge.From = b.version(edge.From)
	if edge.From == edge.To {
		return
	}
	if _, dup := b.edgeSeen[edge]; dup {
		return
	}
	b.edgeSeen[edge] = struct{}{}
	b.edges = append(b.edges, edge)
}
