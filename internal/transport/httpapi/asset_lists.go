package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/access"
	"github.com/LeXwDeX/Plinth/internal/assetlist"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/project"
)

// The asset list wire names (#327) are the list card's and project panel's
// stable contract. Owner ids and the idempotency request_key stay internal.
type assetListSummaryWire struct {
	ID                      string     `json:"id"`
	ProjectID               string     `json:"project_id"`
	SourceSessionID         string     `json:"source_session_id,omitempty"`
	OriginMessageID         string     `json:"origin_message_id,omitempty"`
	Title                   string     `json:"title"`
	Status                  string     `json:"status"`
	EffectiveStatus         string     `json:"effective_status"`
	ItemCount               int        `json:"item_count"`
	PlannedStepCount        int        `json:"planned_step_count"`
	PlannedModel3DStepCount int        `json:"planned_model3d_step_count"`
	DecidedAt               *time.Time `json:"decided_at,omitempty"`
	DecisionReason          string     `json:"decision_reason,omitempty"`
	ExpiresAt               time.Time  `json:"expires_at"`
	CreatedAt               time.Time  `json:"created_at"`
	UpdatedAt               time.Time  `json:"updated_at"`
	// Run is the confirmed list's execution state (#328), null for a draft or
	// a deployment without the run store.
	Run *assetListRunWire `json:"run"`
}

// assetListRunWire is the run summary the list panel and the control route
// return. Version is the opaque updated_at token the control body echoes as
// expected_version.
type assetListRunWire struct {
	Status      string               `json:"status"`
	PauseReason string               `json:"pause_reason,omitempty"`
	Version     string               `json:"version"`
	ItemCounts  assetlist.ItemCounts `json:"item_counts"`
}

type assetListItemWire struct {
	ID                      string                `json:"id"`
	Kind                    string                `json:"kind"`
	Name                    string                `json:"name"`
	Description             string                `json:"description,omitempty"`
	References              []project.Reference   `json:"references"`
	RouteID                 string                `json:"route_id"`
	Route                   []assetlist.RouteStep `json:"route"`
	PlannedStepCount        int                   `json:"planned_step_count"`
	PlannedModel3DStepCount int                   `json:"planned_model3d_step_count"`
	State                   string                `json:"state"`
	StateReason             string                `json:"state_reason,omitempty"`
	Version                 int64                 `json:"version"`
	SessionID               string                `json:"session_id,omitempty"`
	ProjectAssetID          string                `json:"project_asset_id,omitempty"`
	Round                   int                   `json:"round"`
	CreatedAt               time.Time             `json:"created_at"`
	UpdatedAt               time.Time             `json:"updated_at"`
}

type assetListWire struct {
	assetListSummaryWire
	Version      int64               `json:"version"`
	Brief        json.RawMessage     `json:"brief"`
	SupersededBy string              `json:"superseded_by,omitempty"`
	Items        []assetListItemWire `json:"items"`
}

// assetListEditBody mirrors assetlist.EditInput. References are explicit
// project references (input asset or Task output); @handles are resolved by
// the proposal tool, not by this route.
type assetListEditBody struct {
	Title           string                `json:"title"`
	Items           []assetlist.ItemInput `json:"items"`
	Brief           json.RawMessage       `json:"brief"`
	ExpectedVersion int64                 `json:"expected_version"`
}

type assetListDecisionBody struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
}

// assetListRunControlBody mirrors the run route's body. expected_version is
// the opaque updated_at token of the run summary, echoed verbatim.
type assetListRunControlBody struct {
	Action          string `json:"action"`
	ExpectedVersion string `json:"expected_version"`
	Reason          string `json:"reason"`
}

// assetListItemControlBody is the item route's body. Only action skip exists.
// A missing expected_version is invalid, so the field stays a pointer.
type assetListItemControlBody struct {
	Action          string `json:"action"`
	ExpectedVersion *int64 `json:"expected_version"`
	Reason          string `json:"reason"`
}

func assetListRunFields(summary assetlist.RunSummary) assetListRunWire {
	return assetListRunWire{
		Status: summary.Status, PauseReason: summary.PauseReason,
		Version: summary.Version, ItemCounts: summary.ItemCounts,
	}
}

func (s *Server) assetListMessages() catalog.AssetListMessagesSchema {
	if s.application.AssetList == nil {
		return catalog.AssetListMessagesSchema{}
	}
	return s.application.AssetList.Messages
}

// assetListOwner refuses a request without a real browser session. A
// Bearer identity, a development credential and a key never decide or edit a
// list, even when they name the owner in JSON.
func (s *Server) assetListOwner(writer http.ResponseWriter, request *http.Request, principal access.Principal) (string, bool) {
	if _, human := s.taskHumanPrincipal(request, generation.OwnerID(principal.ID())); !human {
		s.writeMessageProblem(writer, http.StatusForbidden, "forbidden", "error.forbidden")
		return "", false
	}
	return principal.ID(), true
}

func assetListSummaryFields(value assetlist.List) assetListSummaryWire {
	wire := assetListSummaryWire{
		ID: value.ID, ProjectID: value.ProjectID, SourceSessionID: value.SourceSessionID,
		OriginMessageID: value.OriginMessageID, Title: value.Title, Status: value.Status,
		EffectiveStatus: value.EffectiveStatus, ItemCount: value.ItemCount,
		PlannedStepCount: value.PlannedStepCount, PlannedModel3DStepCount: value.PlannedModel3DStepCount,
		DecisionReason: value.DecisionReason,
		ExpiresAt:      value.ExpiresAt.UTC(), CreatedAt: value.CreatedAt.UTC(), UpdatedAt: value.UpdatedAt.UTC(),
	}
	if value.DecidedAt != nil {
		decided := value.DecidedAt.UTC()
		wire.DecidedAt = &decided
	}
	if value.Run != nil {
		run := assetListRunFields(*value.Run)
		wire.Run = &run
	}
	return wire
}

func assetListItemFields(item assetlist.Item) assetListItemWire {
	return assetListItemWire{
		ID: item.ID, Kind: item.Kind, Name: item.Name, Description: item.Description,
		References: append([]project.Reference{}, item.References...),
		RouteID:    item.RouteID, Route: append([]assetlist.RouteStep{}, item.Route...),
		PlannedStepCount: item.PlannedStepCount, PlannedModel3DStepCount: item.PlannedModel3DStepCount,
		State: item.State, StateReason: item.StateReason, Version: item.Version,
		SessionID: item.SessionID, ProjectAssetID: item.ProjectAssetID, Round: item.Round,
		CreatedAt: item.CreatedAt.UTC(), UpdatedAt: item.UpdatedAt.UTC(),
	}
}

func assetListResultWire(value assetlist.List) assetListWire {
	items := make([]assetListItemWire, 0, len(value.Items))
	for _, item := range value.Items {
		items = append(items, assetListItemFields(item))
	}
	brief := value.Brief
	if len(brief) == 0 {
		brief = json.RawMessage("{}")
	}
	return assetListWire{
		assetListSummaryWire: assetListSummaryFields(value),
		Version:              value.Version,
		Brief:                brief,
		SupersededBy:         value.SupersededBy,
		Items:                items,
	}
}

// writeAssetListError maps the service's owner-scoped errors. NotFound (a
// missing, foreign or terminal list) is always 404; the state conflicts carry
// the list that blocks the decision so the browser can offer an explicit
// replace. Every answer names the request id the access log records.
func (s *Server) writeAssetListError(writer http.ResponseWriter, err error, title string) {
	noteCause(writer, err)
	messages := s.assetListMessages()
	invalid := s.application.Chat.Presentation.Messages.InvalidParameters
	var activeConflict *assetlist.ErrActiveConflict
	var activeWorking *assetlist.ErrActiveWorking
	switch {
	case errors.As(err, &activeConflict):
		detail := s.message("error.conflict")
		if trace := traceFromWriter(writer); trace != nil {
			trace.noteProblem("asset_list_active_conflict", detail, []error{err})
		}
		writeJSON(writer, http.StatusConflict, map[string]any{
			"code":              "asset_list_active_conflict",
			"detail":            detail,
			"active_list_id":    activeConflict.ListID,
			"active_list_title": activeConflict.Title,
		})
	case errors.As(err, &activeWorking):
		detail := s.message("error.conflict")
		if trace := traceFromWriter(writer); trace != nil {
			trace.noteProblem("asset_list_active_working", detail, []error{err})
		}
		writeJSON(writer, http.StatusConflict, map[string]any{
			"code":           "asset_list_active_working",
			"detail":         detail,
			"active_list_id": activeWorking.ListID,
		})
	case errors.Is(err, assetlist.ErrExpired):
		detail := messages.Expired
		if detail == "" {
			detail = s.message("error.conflict")
		}
		detail = strings.NewReplacer(
			"{title}", title,
			"{days}", strconv.Itoa(s.assetListTTLDays()),
		).Replace(detail)
		writeProblem(writer, http.StatusConflict, "asset_list_expired", detail, err)
	case errors.Is(err, assetlist.ErrConflict):
		writeProblem(writer, http.StatusConflict, "conflict", s.message("error.conflict"), err)
	case errors.Is(err, assetlist.ErrItemState):
		writeProblem(writer, http.StatusConflict, "asset_list_item_state", s.message("error.conflict"), err)
	case errors.Is(err, assetlist.ErrRunUnavailable), errors.Is(err, assetlist.ErrExecutionDisabled):
		writeProblem(writer, http.StatusConflict, "asset_list_execution_disabled", s.message("error.conflict"), err)
	case errors.Is(err, assetlist.ErrLimit):
		detail := messages.LimitReached
		if detail == "" {
			detail = s.message("error.conflict")
		}
		detail = strings.ReplaceAll(detail, "{maximum}", strconv.Itoa(s.assetListMaxLists()))
		writeProblem(writer, http.StatusConflict, "asset_list_limit", detail, err)
	case errors.Is(err, assetlist.ErrNotFound):
		detail := messages.ListNotFound
		if detail == "" {
			detail = s.message("error.not_found")
		}
		writeProblem(writer, http.StatusNotFound, "not_found", detail, err)
	case errors.Is(err, assetlist.ErrProjectNotFound):
		writeProblem(writer, http.StatusNotFound, "not_found", s.message("error.not_found"), err)
	case errors.Is(err, assetlist.ErrSessionNotInProject):
		writeProblem(writer, http.StatusBadRequest, "invalid_input", invalid, err)
	case errors.Is(err, assetlist.ErrInvalid):
		writeProblem(writer, http.StatusBadRequest, "invalid_input", invalid, err)
	default:
		writeProblem(writer, http.StatusInternalServerError, "internal",
			s.application.Chat.Presentation.Messages.InternalError, err)
	}
}

func (s *Server) assetListTTLDays() int {
	if s.application.AssetList == nil {
		return 0
	}
	return s.application.AssetList.Limits.DraftTTLSeconds / 86400
}

func (s *Server) assetListMaxLists() int {
	if s.application.AssetList == nil {
		return 0
	}
	return s.application.AssetList.Limits.MaxListsPerProject
}

func (s *Server) listProjectAssetLists(writer http.ResponseWriter, request *http.Request, principal access.Principal) {
	owner, ok := s.assetListOwner(writer, request, principal)
	if !ok {
		return
	}
	projectID := request.PathValue("projectID")
	exists, err := s.assetLists.ProjectExists(request.Context(), owner, projectID)
	if err != nil {
		s.writeAssetListError(writer, err, "")
		return
	}
	if !exists {
		s.writeAssetListError(writer, assetlist.ErrProjectNotFound, "")
		return
	}
	lists, err := s.assetLists.List(request.Context(), owner, projectID)
	if err != nil {
		s.writeAssetListError(writer, err, "")
		return
	}
	items := make([]assetListSummaryWire, 0, len(lists))
	for _, list := range lists {
		items = append(items, assetListSummaryFields(list))
	}
	writer.Header().Set("Cache-Control", s.application.Runtime.HTTP.PrivateCacheControl)
	writeJSON(writer, http.StatusOK, map[string]any{"data": items})
}

func (s *Server) getProjectAssetList(writer http.ResponseWriter, request *http.Request, principal access.Principal) {
	owner, ok := s.assetListOwner(writer, request, principal)
	if !ok {
		return
	}
	value, err := s.assetLists.Get(request.Context(), owner,
		request.PathValue("projectID"), request.PathValue("listID"))
	if err != nil {
		s.writeAssetListError(writer, err, "")
		return
	}
	writer.Header().Set("Cache-Control", s.application.Runtime.HTTP.PrivateCacheControl)
	writeJSON(writer, http.StatusOK, assetListResultWire(value))
}

func (s *Server) putProjectAssetList(writer http.ResponseWriter, request *http.Request, principal access.Principal) {
	owner, ok := s.assetListOwner(writer, request, principal)
	if !ok {
		return
	}
	projectID := request.PathValue("projectID")
	listID := request.PathValue("listID")
	// The list's proposal session shapes the availability snapshot of an
	// edit; reading the list first also answers a missing or foreign one with
	// 404 before any body is parsed.
	existing, err := s.assetLists.Get(request.Context(), owner, projectID, listID)
	if err != nil {
		s.writeAssetListError(writer, err, "")
		return
	}
	var body assetListEditBody
	if err := s.decodeJSON(writer, request, &body); err != nil {
		return
	}
	value, err := s.assetLists.Edit(request.Context(), assetlist.EditInput{
		Owner: owner, ProjectID: projectID, ListID: listID,
		ExpectedVersion: body.ExpectedVersion,
		Title:           body.Title,
		Items:           body.Items,
		Brief:           body.Brief,
		SessionID:       existing.SourceSessionID,
	})
	if err != nil {
		s.writeAssetListError(writer, err, existing.Title)
		return
	}
	writeJSON(writer, http.StatusOK, assetListResultWire(value))
}

func (s *Server) decideProjectAssetList(writer http.ResponseWriter, request *http.Request, principal access.Principal) {
	owner, ok := s.assetListOwner(writer, request, principal)
	if !ok {
		return
	}
	projectID := request.PathValue("projectID")
	listID := request.PathValue("listID")
	existing, err := s.assetLists.Get(request.Context(), owner, projectID, listID)
	if err != nil {
		s.writeAssetListError(writer, err, "")
		return
	}
	var body assetListDecisionBody
	if err := s.decodeJSON(writer, request, &body); err != nil {
		return
	}
	value, err := s.assetLists.Decide(request.Context(), assetlist.DecideInput{
		Owner: owner, ProjectID: projectID, ListID: listID,
		Decision: body.Decision, Reason: body.Reason,
	})
	if err != nil {
		s.writeAssetListError(writer, err, existing.Title)
		return
	}
	writeJSON(writer, http.StatusOK, assetListResultWire(value))
}

// controlProjectAssetListRun applies one person action to the list's run
// (#328): start, pause, resume or stop under the expected_version CAS. The
// answer is the refreshed run summary the panel shows.
func (s *Server) controlProjectAssetListRun(writer http.ResponseWriter, request *http.Request, principal access.Principal) {
	owner, ok := s.assetListOwner(writer, request, principal)
	if !ok {
		return
	}
	projectID := request.PathValue("projectID")
	listID := request.PathValue("listID")
	var body assetListRunControlBody
	if err := s.decodeJSON(writer, request, &body); err != nil {
		return
	}
	if _, err := s.assetLists.ControlRun(request.Context(), owner, projectID, listID,
		body.Action, body.ExpectedVersion, body.Reason); err != nil {
		s.writeAssetListError(writer, err, "")
		return
	}
	summary, err := s.assetLists.RunSummaryByList(request.Context(), owner, projectID, listID)
	if err != nil {
		s.writeAssetListError(writer, err, "")
		return
	}
	writeJSON(writer, http.StatusOK, assetListRunFields(summary))
}

// updateProjectAssetListItem is the person's one item action (#328): skip. The
// item answer is the same summary shape the list detail uses.
func (s *Server) updateProjectAssetListItem(writer http.ResponseWriter, request *http.Request, principal access.Principal) {
	owner, ok := s.assetListOwner(writer, request, principal)
	if !ok {
		return
	}
	var body assetListItemControlBody
	if err := s.decodeJSON(writer, request, &body); err != nil {
		return
	}
	if body.Action != assetlist.ItemActionSkip {
		s.writeAssetListError(writer, fmt.Errorf("%w: item action %q", assetlist.ErrInvalid, body.Action), "")
		return
	}
	// The browser always sends the version it read; a negative value would
	// otherwise select the port's unchecked sentinel.
	if body.ExpectedVersion == nil || *body.ExpectedVersion < 0 {
		s.writeAssetListError(writer, fmt.Errorf("%w: expected_version", assetlist.ErrInvalid), "")
		return
	}
	item, err := s.assetLists.SkipItem(request.Context(), owner,
		request.PathValue("projectID"), request.PathValue("listID"),
		request.PathValue("itemID"), *body.ExpectedVersion, body.Reason)
	if err != nil {
		s.writeAssetListError(writer, err, "")
		return
	}
	writeJSON(writer, http.StatusOK, assetListItemFields(item))
}

func (s *Server) listChatSessionAssetLists(writer http.ResponseWriter, request *http.Request, principal access.Principal) {
	owner, ok := s.assetListOwner(writer, request, principal)
	if !ok {
		return
	}
	lists, err := s.assetLists.ListSession(request.Context(), owner, request.PathValue("sessionID"))
	if err != nil {
		s.writeAssetListError(writer, err, "")
		return
	}
	items := make([]assetListSummaryWire, 0, len(lists))
	for _, list := range lists {
		items = append(items, assetListSummaryFields(list))
	}
	writer.Header().Set("Cache-Control", s.application.Runtime.HTTP.PrivateCacheControl)
	writeJSON(writer, http.StatusOK, map[string]any{"data": items})
}
