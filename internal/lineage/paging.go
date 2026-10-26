package lineage

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/capture"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// A cursor is scoped to the owner and stable session ID. It wraps the task
// repository's keyset cursor; it is not an offset and new tasks do not move it.
type pageCursor struct {
	Owner   string `json:"owner"`
	Session string `json:"session"`
	Before  string `json:"before"`
}

func encodePageCursor(owner, session, before string) string {
	if before == "" {
		return ""
	}
	raw, _ := json.Marshal(pageCursor{owner, session, before})
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodePageCursor(raw, owner, session string) (string, error) {
	if raw == "" {
		return "", nil
	}
	if len(raw) > 4096 {
		return "", ErrInvalid
	}
	data, err := base64.RawURLEncoding.DecodeString(raw)
	var cursor pageCursor
	if err != nil || json.Unmarshal(data, &cursor) != nil || cursor.Owner != owner || cursor.Session != session || cursor.Before == "" {
		return "", ErrInvalid
	}
	return cursor.Before, nil
}

// SessionPage loads only one window of frozen inputs. A lightweight session-wide
// summary index (no Offering or Input) supplies exact source IDs and ordinals.
// Edges may name a verified parent outside this page. Clients retain these edges
// and resolve them when merging an older page, never inventing placeholder Tasks.
func (s *Service) SessionPage(ctx context.Context, owner, session, cursor string, limit int) (Graph, error) {
	if strings.TrimSpace(owner) == "" || strings.TrimSpace(session) == "" || limit < 1 || limit > s.config.MaxTasks {
		return Graph{}, ErrInvalid
	}
	before, err := decodePageCursor(cursor, owner, session)
	if err != nil {
		return Graph{}, err
	}
	scope := generation.OwnTasks(generation.OwnerID(owner))
	// Paging includes public attempts as well as succeeded versions. Legacy
	// Session remains succeeded-only, with its original response shape.
	page, err := s.tasks.List(ctx, scope, generation.ListQuery{SessionID: session, Cursor: before, Limit: limit})
	if err != nil {
		// Generation may wrap a repository's invalid-input error as unavailable.
		// Keep the repository cursor opaque, but retain its input-error contract.
		for cause := err; cause != nil; cause = errors.Unwrap(cause) {
			if generation.IsCode(cause, generation.ErrInvalidInput) {
				return Graph{}, ErrInvalid
			}
		}
		return Graph{}, err
	}
	var full []generation.Task
	selected := map[string]bool{}
	for _, summary := range page.Tasks {
		if string(summary.OwnerID) != owner || summary.SessionID != session {
			continue
		}
		task, err := s.tasks.GetScoped(ctx, scope, summary.ID)
		if generation.IsCode(err, generation.ErrNotFound) {
			continue
		}
		if err != nil {
			return Graph{}, err
		}
		if string(task.OwnerID) == owner && task.SessionID == session {
			full = append(full, task)
			selected[string(task.ID)] = true
		}
	}
	index, records, metadata, err := s.sessionIndex(ctx, owner, session)
	if err != nil {
		return Graph{}, err
	}
	ordinals := sessionOrdinals(metadata)
	// Reuse the complete source index, but materialize only this page's nodes.
	index.tasks, index.nodes, index.edges, index.references = full, nil, nil, nil
	index.includeAttempts = true
	index.edgeSeen = map[Edge]struct{}{}
	index.captures, index.uploads = map[string]string{}, map[string]string{}
	index.records = nil
	index.conversions, index.foldSince = s.config.Conversions, s.config.FoldConversionsSince
	for _, record := range records {
		if selected[record.SourceTaskID] {
			index.records = append(index.records, record)
		}
	}
	for _, task := range full {
		index.references = append(index.references, taskReferences(task)...)
	}
	inputs, err := s.loadInputs(ctx, owner, index.assetIDsToLoad())
	if err != nil {
		return Graph{}, err
	}
	index.modelUploads, err = s.loadNativeModelInputs(ctx, owner, index.references)
	if err != nil {
		return Graph{}, err
	}
	graph := index.build(inputs)
	if graph.Groups, err = s.groupsOf(ctx, owner, session, &graph); err != nil {
		return Graph{}, err
	}
	for i := range graph.Nodes {
		node := &graph.Nodes[i]
		node.Version = ordinals[node.ID]
		if node.Kind == KindUnknown {
			// Unknown nodes are page-local. Namespace their IDs so independent
			// unknown-1 nodes cannot accidentally merge across page boundaries.
			old := node.ID
			node.ID = fmt.Sprintf("unknown-%x-%s", sha256.Sum256([]byte(cursor)), old)
			for j := range graph.Edges {
				if graph.Edges[j].From == old {
					graph.Edges[j].From = node.ID
				}
			}
		}
	}
	graph.NextCursor = encodePageCursor(owner, session, page.NextCursor)
	graph.Truncated = graph.NextCursor != ""
	return graph, nil
}

// sessionIndex is the session-wide summary index: the builder over every
// succeeded Task and capture, the capture records, and the summary graph
// whose sessionOrdinals are the version strip's numbers ("3D v5").
func (s *Service) sessionIndex(ctx context.Context, owner, session string) (*builder, []capture.Record, Graph, error) {
	summaries, err := s.sessionSummaries(ctx, owner, session)
	if err != nil {
		return nil, nil, Graph{}, err
	}
	var records []capture.Record
	if s.captures != nil {
		records, err = s.captures.ListSession(ctx, owner, session)
		if err != nil {
			return nil, nil, Graph{}, err
		}
	}
	index := newBuilder(summaries, records)
	index.conversions, index.foldSince = s.config.Conversions, s.config.FoldConversionsSince
	metadata := index.build(nil)
	if metadata.Groups, err = s.groupsOf(ctx, owner, session, &metadata); err != nil {
		return nil, nil, Graph{}, err
	}
	return index, records, metadata, nil
}

// SessionVersions is the version number the page shows for each Task of the
// session that has one, by Task ID: the same numbers SessionPage puts on its
// nodes, so tool results name models as the version strip does (#326 L).
func (s *Service) SessionVersions(ctx context.Context, owner, session string) (map[generation.TaskID]int, error) {
	if strings.TrimSpace(owner) == "" || strings.TrimSpace(session) == "" {
		return nil, ErrInvalid
	}
	_, _, metadata, err := s.sessionIndex(ctx, owner, session)
	if err != nil {
		return nil, err
	}
	ordinals := sessionOrdinals(metadata)
	versions := map[generation.TaskID]int{}
	for _, node := range metadata.Nodes {
		if node.Kind == KindTask && node.TaskID != "" && ordinals[node.ID] > 0 {
			versions[generation.TaskID(node.TaskID)] = ordinals[node.ID]
		}
	}
	return versions, nil
}

// Summaries carry creation times and result assets, not frozen inputs. Their
// owner/session is rechecked before they can supply any parent identifier.
func (s *Service) sessionSummaries(ctx context.Context, owner, session string) ([]generation.Task, error) {
	var result []generation.Task
	cursor := ""
	for {
		page, err := s.tasks.List(ctx, generation.OwnTasks(generation.OwnerID(owner)), generation.ListQuery{SessionID: session, Cursor: cursor, Limit: s.config.MaxTasks, States: []generation.TaskState{generation.TaskSucceeded}})
		if err != nil {
			return nil, err
		}
		for _, task := range page.Tasks {
			if string(task.OwnerID) != owner || task.SessionID != session || task.State != generation.TaskSucceeded {
				continue
			}
			// A summary has no frozen input; a possible format conversion is
			// read in full so its source can fold it (#330).
			if s.config.Conversions != nil && s.config.Conversions.ConversionOffering(task.Offering.ID) {
				full, err := s.tasks.GetScoped(ctx, generation.OwnTasks(generation.OwnerID(owner)), task.ID)
				if generation.IsCode(err, generation.ErrNotFound) {
					continue
				}
				if err != nil {
					return nil, err
				}
				if string(full.OwnerID) != owner || full.SessionID != session {
					continue
				}
				task = full
			}
			result = append(result, task)
		}
		if len(page.Tasks) == 0 || page.NextCursor == "" {
			return result, nil
		}
		if page.NextCursor == cursor {
			return nil, ErrInvalid
		}
		cursor = page.NextCursor
	}
}

// Only successful media and captures occupy version numbers. A three-view
// group occupies one number; uploads and attempts do not occupy a number.
func sessionOrdinals(graph Graph) map[string]int {
	result := map[string]int{}
	counters := map[string]int{}
	groups := map[string]int{}
	for _, node := range graph.Nodes {
		if node.Kind != KindTask && node.Kind != KindCapture {
			continue
		}
		domain := ""
		for _, asset := range node.Assets {
			if asset.Role == "model" || strings.HasPrefix(asset.MediaType, "model/") {
				domain = "model"
				break
			}
			if strings.HasPrefix(asset.MediaType, "image/") {
				domain = "image"
			}
		}
		if domain == "" {
			continue
		}
		key := domain + ":" + node.Group
		if node.Group != "" && groups[key] > 0 {
			result[node.ID] = groups[key]
			continue
		}
		counters[domain]++
		result[node.ID] = counters[domain]
		if node.Group != "" {
			groups[key] = counters[domain]
		}
	}
	return result
}
