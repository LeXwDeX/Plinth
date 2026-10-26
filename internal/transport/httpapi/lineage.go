package httpapi

import (
	"errors"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/lineage"
)

// lineageAssetWire is one result file of a version node. Its URL is the
// platform asset route of the owning task, never an upstream address.
type lineageAssetWire struct {
	DeliveryProfile string `json:"delivery_profile,omitempty"`
	ID              string `json:"id"`
	Role            string `json:"role"`
	Variant         string `json:"variant,omitempty"`
	MediaType       string `json:"media_type"`
	Ordinal         int    `json:"ordinal"`
	// Required: a required output of the task, not an attachment (#326).
	Required bool   `json:"required,omitempty"`
	URL      string `json:"url"`
}

type lineageNodeWire struct {
	ID          string             `json:"id"`
	Kind        string             `json:"kind"`
	TaskID      string             `json:"task_id,omitempty"`
	Capability  string             `json:"capability,omitempty"`
	CreatedAt   *time.Time         `json:"created_at,omitempty"`
	Assets      []lineageAssetWire `json:"assets,omitempty"`
	Group       string             `json:"group,omitempty"`
	Slot        string             `json:"slot,omitempty"`
	Operation   string             `json:"operation,omitempty"`
	CaptureID   string             `json:"capture_id,omitempty"`
	PixelWidth  int                `json:"pixel_width,omitempty"`
	PixelHeight int                `json:"pixel_height,omitempty"`
	Version     int                `json:"version,omitempty"`
	// Conversion marks a format conversion made before conversions were
	// folded into their source version (#330); it keeps its own version.
	Conversion bool           `json:"conversion,omitempty"`
	State      string         `json:"state,omitempty"`
	Progress   int            `json:"progress,omitempty"`
	Failure    *publicFailure `json:"failure,omitempty"`
}

// lineageConversionWire is a format-conversion Task folded into the version
// whose files it made (#330): it has no node of its own.
type lineageConversionWire struct {
	TaskID       string `json:"task_id"`
	SourceTaskID string `json:"source_task_id"`
}

type lineageWire struct {
	Nodes       []lineageNodeWire       `json:"nodes"`
	Edges       []lineage.Edge          `json:"edges"`
	Groups      []lineage.Group         `json:"groups,omitempty"`
	Conversions []lineageConversionWire `json:"conversions,omitempty"`
	Truncated   bool                    `json:"truncated"`
}

func (s *Server) lineageWire(locale string, graph lineage.Graph) lineageWire {
	nodes := make([]lineageNodeWire, 0, len(graph.Nodes))
	for _, node := range graph.Nodes {
		wire := lineageNodeWire{
			ID: node.ID, Kind: node.Kind, TaskID: node.TaskID,
			Capability: node.Capability, CreatedAt: node.CreatedAt,
			Group: node.Group, Slot: node.Slot, Operation: node.Operation,
			CaptureID: node.CaptureID, PixelWidth: node.PixelWidth, PixelHeight: node.PixelHeight,
			Version: node.Version, Conversion: node.Conversion,
			State: string(node.State), Progress: node.Progress, Failure: s.publicFailure(locale, node.Failure),
		}
		for _, asset := range node.Assets {
			// A screenshot or an upload belongs to no task: it is read from the owner's asset route.
			url := nativeTaskAssetURL(s.taskAssetRoute, generation.TaskID(node.TaskID), generation.AssetID(asset.ID))
			if node.Kind == lineage.KindCapture || node.Kind == lineage.KindUpload {
				url = s.assetItemRoute.Render(asset.ID)
			}
			wire.Assets = append(wire.Assets, lineageAssetWire{
				DeliveryProfile: asset.DeliveryProfile,
				ID:              asset.ID, Role: asset.Role, Variant: asset.Variant,
				MediaType: asset.MediaType, Ordinal: asset.Ordinal, Required: asset.Required, URL: url,
			})
		}
		nodes = append(nodes, wire)
	}
	groups := make([]lineage.Group, 0, len(graph.Groups))
	for _, group := range graph.Groups {
		// The group label is Schema copy (a recipe name such as three views).
		group.Label = s.textIn(locale, group.Label)
		groups = append(groups, group)
	}
	conversions := make([]lineageConversionWire, 0, len(graph.Conversions))
	for task, source := range graph.Conversions {
		conversions = append(conversions, lineageConversionWire{TaskID: task, SourceTaskID: source})
	}
	sort.Slice(conversions, func(i, j int) bool { return conversions[i].TaskID < conversions[j].TaskID })
	return lineageWire{Nodes: nodes, Edges: graph.Edges, Groups: groups, Conversions: conversions, Truncated: graph.Truncated}
}

// getChatSessionLineage returns the version chain of the caller's own session.
// A session of someone else and a missing one are the same 404; the chain is
// never readable with an "any owner" scope.
func (s *Server) getChatSessionLineage(writer http.ResponseWriter, request *http.Request, owner generation.OwnerID) {
	messages := s.application.Chat.Presentation.Messages
	sessionID := request.PathValue("sessionID")
	owned, err := s.chat.OwnsSession(request.Context(), string(owner), sessionID)
	if err != nil {
		s.writeChatInternalError(writer, err)
		return
	}
	if !owned {
		s.writeChatError(writer, http.StatusNotFound, messages.SessionNotFound)
		return
	}
	values := request.URL.Query()
	paged := values.Has("limit") || values.Has("cursor")
	var graph lineage.Graph
	if paged {
		limit := s.application.Runtime.HTTP.LineagePageSize
		if raw := values.Get("limit"); raw != "" {
			parsed, parseErr := strconv.Atoi(raw)
			if parseErr != nil || parsed < 1 || parsed > min(s.application.Runtime.HTTP.MaximumLineageTasks, s.application.Runtime.HTTP.MaximumPageSize) {
				s.writeChatError(writer, http.StatusBadRequest, messages.InvalidParameters)
				return
			}
			limit = parsed
		}
		graph, err = s.lineage.SessionPage(request.Context(), string(owner), sessionID, values.Get("cursor"), limit)
	} else {
		graph, err = s.lineage.Session(request.Context(), string(owner), sessionID)
	}
	if err != nil {
		if errors.Is(err, lineage.ErrInvalid) || generation.IsCode(err, generation.ErrInvalidInput) {
			s.writeChatError(writer, http.StatusBadRequest, messages.InvalidParameters)
			return
		}
		if s.logger != nil {
			s.logger.WarnContext(request.Context(), "版本链读取失败", "error", err)
		}
		s.writeChatInternalError(writer, err)
		return
	}
	writer.Header().Set("Cache-Control", s.application.Runtime.HTTP.PrivateCacheControl)
	if paged {
		writeJSON(writer, http.StatusOK, struct {
			lineageWire
			NextCursor string `json:"next_cursor"`
		}{s.lineageWire(requestLocale(request), graph), graph.NextCursor})
		return
	}
	writeJSON(writer, http.StatusOK, s.lineageWire(requestLocale(request), graph))
}
