package httpapi

import (
	"errors"
	"net/http"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// chatSelfCheckCheckWire is the public evidence line of one recorded read-only
// call. The deterministic metric definitions and the record's internal
// revision stay server-side; the numbers themselves are facts the conversation
// shows.
type chatSelfCheckCheckWire struct {
	Round   int                       `json:"round"`
	Kind    string                    `json:"kind"`
	Tool    string                    `json:"tool"`
	AssetID string                    `json:"asset_id,omitempty"`
	Views   []string                  `json:"views,omitempty"`
	Handles []string                  `json:"handles,omitempty"`
	Metrics *chatSelfCheckMetricsWire `json:"metrics,omitempty"`
	OK      bool                      `json:"ok"`
	Error   string                    `json:"error,omitempty"`
	At      string                    `json:"at,omitempty"`
}

type chatSelfCheckMetricsWire struct {
	SilhouetteIOU         float64 `json:"silhouette_iou"`
	BBoxAspectDelta       float64 `json:"bbox_aspect_delta"`
	DominantColorDistance float64 `json:"dominant_color_distance"`
	DominantReference     string  `json:"dominant_reference,omitempty"`
	DominantRender        string  `json:"dominant_render,omitempty"`
	ReferenceSubjectRatio float64 `json:"reference_subject_ratio"`
	RenderSubjectRatio    float64 `json:"render_subject_ratio"`
}

type chatSelfCheckConclusionWire struct {
	Status    string   `json:"status"`
	Summary   string   `json:"summary"`
	Findings  []string `json:"findings"`
	Fixes     []string `json:"fixes"`
	Remaining []string `json:"remaining"`
	// Waiver and PurposeLabel make an exempted pass and the judged purpose
	// visible on the card (#281).
	Waiver       string `json:"waiver,omitempty"`
	PurposeLabel string `json:"purpose_label,omitempty"`
	At           string `json:"at,omitempty"`
}

// chatSelfChecksWire renders the session's records with the response field
// names the Product Schema declares.
func (s *Server) chatSelfChecksWire(sessionID string, schema catalog.ChatSelfCheckSchema,
	records []chat.SelfCheckRecord) map[string]any {
	fields := s.application.Chat.API.ResponseFields
	items := make([]map[string]any, 0, len(records))
	for _, record := range records {
		counters := chat.SelfCheckCounts(schema, record)
		checks := make([]chatSelfCheckCheckWire, 0, len(record.Checks))
		for _, check := range record.Checks {
			item := chatSelfCheckCheckWire{
				Round: check.Round, Kind: check.Kind, Tool: check.Tool,
				AssetID: check.AssetID, Views: check.Views, Handles: check.Handles,
				OK: check.OK, Error: check.Error,
			}
			if !check.At.IsZero() {
				item.At = check.At.UTC().Format("2006-01-02T15:04:05.999999999Z07:00")
			}
			if check.Metrics != nil {
				item.Metrics = &chatSelfCheckMetricsWire{
					SilhouetteIOU:         check.Metrics.SilhouetteIOU,
					BBoxAspectDelta:       check.Metrics.BBoxAspectDelta,
					DominantColorDistance: check.Metrics.DominantColorDistance,
					DominantReference:     check.Metrics.DominantReference,
					DominantRender:        check.Metrics.DominantRender,
					ReferenceSubjectRatio: check.Metrics.ReferenceSubjectRatio,
					RenderSubjectRatio:    check.Metrics.RenderSubjectRatio,
				}
			}
			checks = append(checks, item)
		}
		var conclusion any
		if record.Conclusion != nil {
			conclusion = chatSelfCheckConclusionWire{
				Status: record.Conclusion.Status, Summary: record.Conclusion.Summary,
				Findings: record.Conclusion.Findings, Fixes: record.Conclusion.Fixes,
				Remaining: record.Conclusion.Remaining,
				Waiver:    record.Conclusion.Waiver, PurposeLabel: record.Conclusion.PurposeLabel,
				At: record.Conclusion.At.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"),
			}
		}
		items = append(items, map[string]any{
			fields.TaskID:           record.TaskID,
			fields.Status:           counters.StatusID,
			fields.RoundsUsed:       counters.RoundsUsed,
			fields.RoundsLimit:      counters.RoundsLimit,
			fields.RendersInRound:   counters.RendersInRound,
			fields.RendersLimit:     counters.RendersLimit,
			fields.CorrectionsUsed:  counters.CorrectionsUsed,
			fields.CorrectionsLimit: counters.CorrectionsLimit,
			fields.Checks:           checks,
			fields.Conclusion:       conclusion,
			fields.UpdatedAt:        record.UpdatedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"),
		})
	}
	return map[string]any{
		fields.ID:         sessionID,
		fields.SelfChecks: items,
	}
}

// getChatSessionSelfChecks returns the caller's own session records. A foreign
// or missing session is the same 404 and leaks nothing.
func (s *Server) getChatSessionSelfChecks(writer http.ResponseWriter, request *http.Request,
	owner generation.OwnerID) {
	sessionID := request.PathValue("sessionID")
	records, err := s.chat.ListSelfChecks(request.Context(), string(owner), sessionID)
	if err != nil {
		noteCause(writer, err)
		if errors.Is(err, chat.ErrSessionNotFound) {
			s.writeChatError(writer, http.StatusNotFound, s.application.Chat.Presentation.Messages.SessionNotFound)
			return
		}
		s.writeChatInternalError(writer, err)
		return
	}
	writer.Header().Set("Cache-Control", s.application.Runtime.HTTP.PrivateCacheControl)
	writeJSON(writer, http.StatusOK, s.chatSelfChecksWire(sessionID,
		s.application.Chat.SelfCheck, records))
}
