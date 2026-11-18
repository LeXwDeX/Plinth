package httpapi

import (
	"context"
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
	"github.com/LeXwDeX/Plinth/internal/listcrop"
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
	// Crop is the provenance of the item's crop reference (#331): the source
	// picture and the normalized region. It is omitted when the item has no
	// crop reference and only the list detail carries it, never the summary.
	Crop *assetListCropWire `json:"crop,omitempty"`
}

// assetListCropWire is one item's crop provenance (#331). The merge-duplicates
// editor unions the normalized regions cut from one source, so the wire exposes
// geometry only: no picture bytes, crop asset id, hash or owner id.
type assetListCropWire struct {
	SourceInputAssetID string             `json:"source_input_asset_id"`
	SourceKind         string             `json:"source_kind"`
	SourceWidth        int                `json:"source_width"`
	SourceHeight       int                `json:"source_height"`
	Region             listcrop.Region    `json:"region"`
	PixelRect          listcrop.PixelRect `json:"pixel_rect"`
}

type assetListWire struct {
	assetListSummaryWire
	Version      int64               `json:"version"`
	Brief        json.RawMessage     `json:"brief"`
	SupersededBy string              `json:"superseded_by,omitempty"`
	Items        []assetListItemWire `json:"items"`
}

// assetListReviewWire is one recorded acceptance decision (#329). The review
// snapshot is opaque to the transport; #329b owns its shape.
type assetListReviewWire struct {
	ID         string          `json:"id"`
	Round      int             `json:"round"`
	Verdict    string          `json:"verdict"`
	Reason     string          `json:"reason,omitempty"`
	ReviewerID string          `json:"reviewer_id,omitempty"`
	Review     json.RawMessage `json:"review"`
	CreatedAt  time.Time       `json:"created_at"`
}

// assetListBoardItemWire adds the produced-artifact reference, the item's
// acceptance snapshot and the latest decision to the item summary (#329a).
// The list detail contract stays unchanged.
type assetListBoardItemWire struct {
	assetListItemWire
	Result       json.RawMessage      `json:"result"`
	Review       json.RawMessage      `json:"review"`
	LatestReview *assetListReviewWire `json:"latest_review"`
	// The crop contract of the acceptance board (#331). They mirror the list
	// detail's crop object so one board read explains every item beside its
	// source picture, and stay absent on plain items. Source and region are
	// provenance snapshots: deleting the source picture changes nothing.
	ReferenceAssetID      string                    `json:"reference_asset_id,omitempty"`
	Source                *assetListBoardSourceWire `json:"source,omitempty"`
	Region                *assetListBoardRegionWire `json:"region,omitempty"`
	RepresentativeAssetID string                    `json:"representative_asset_id,omitempty"`
}

// assetListBoardSourceWire is the provenance snapshot of the picture one crop
// was cut from: the input form cropped, how it was reached and its size.
type assetListBoardSourceWire struct {
	SourceInputAssetID string `json:"source_input_asset_id"`
	SourceKind         string `json:"source_kind"`
	SourceWidth        int    `json:"source_width"`
	SourceHeight       int    `json:"source_height"`
}

// assetListBoardRegionWire is the cut geometry: the normalized region and its
// source-pixel rectangle.
type assetListBoardRegionWire struct {
	listcrop.Region
	PixelRect listcrop.PixelRect `json:"pixel_rect"`
}

// assetListBoardWire is the read-only acceptance board: the list summary plus
// every item in position order. A decision is absent, not zeroed, until the
// person makes one.
type assetListBoardWire struct {
	List  assetListSummaryWire     `json:"list"`
	Items []assetListBoardItemWire `json:"items"`
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

// assetListCropReference picks the crop asset one item references and the
// provenance behind it (#331). Only input-asset references can be crops; the
// provenance map is owner-scoped, so a foreign id or a list_crop asset without
// provenance yields nothing. An item may reference at most one crop, but
// stored data may briefly carry several: the last crop reference wins,
// matching the edit rule that replaces the previous crop with the new one
// appended last.
func assetListCropReference(item assetlist.Item,
	crops map[generation.AssetID]listcrop.Crop,
) (string, *listcrop.Crop) {
	var (
		chosen      listcrop.Crop
		referenceID string
		found       bool
	)
	for _, reference := range item.References {
		if reference.InputAssetID == "" {
			continue
		}
		crop, ok := crops[generation.AssetID(reference.InputAssetID)]
		if !ok {
			continue
		}
		chosen, referenceID, found = crop, reference.InputAssetID, true
	}
	if !found {
		return "", nil
	}
	return referenceID, &chosen
}

func assetListCropField(item assetlist.Item, crops map[generation.AssetID]listcrop.Crop) *assetListCropWire {
	_, chosen := assetListCropReference(item, crops)
	if chosen == nil {
		return nil
	}
	return &assetListCropWire{
		SourceInputAssetID: string(chosen.SourceInputAssetID),
		SourceKind:         chosen.SourceKind,
		SourceWidth:        chosen.SourceWidth,
		SourceHeight:       chosen.SourceHeight,
		Region:             chosen.Region,
		PixelRect:          chosen.PixelRect,
	}
}

func assetListReviewFields(review assetlist.ItemReview) assetListReviewWire {
	raw := review.Review
	if len(raw) == 0 {
		raw = json.RawMessage("{}")
	}
	return assetListReviewWire{
		ID: review.ID, Round: review.Round, Verdict: review.Verdict,
		Reason: review.Reason, ReviewerID: review.ReviewerID,
		Review: raw, CreatedAt: review.CreatedAt.UTC(),
	}
}

// assetListRepresentativeAsset picks the result asset a board item shows as
// its representative picture (#331): the 3D model first, then a rendered
// image, then the first asset of the completion snapshot. A missing or
// malformed snapshot names none.
func assetListRepresentativeAsset(result json.RawMessage) string {
	var snapshot struct {
		Assets []struct {
			ID   string `json:"id"`
			Role string `json:"role"`
		} `json:"assets"`
	}
	if len(result) == 0 || json.Unmarshal(result, &snapshot) != nil || len(snapshot.Assets) == 0 {
		return ""
	}
	for _, role := range []string{"model", "image"} {
		for _, asset := range snapshot.Assets {
			if asset.Role == role && asset.ID != "" {
				return asset.ID
			}
		}
	}
	return snapshot.Assets[0].ID
}

// assetListBoardFields renders the acceptance board. crops is the owner-scoped
// provenance of every referenced crop asset, keyed by crop asset id; nil (no
// store or no crop references) leaves every item without the crop fields.
func assetListBoardFields(board assetlist.Board,
	crops map[generation.AssetID]listcrop.Crop,
) assetListBoardWire {
	items := make([]assetListBoardItemWire, 0, len(board.Items))
	for _, entry := range board.Items {
		result := entry.Result
		if len(result) == 0 {
			result = json.RawMessage("{}")
		}
		review := entry.Review
		if len(review) == 0 {
			review = json.RawMessage("{}")
		}
		wire := assetListBoardItemWire{
			assetListItemWire:     assetListItemFields(entry.Item),
			Result:                result,
			Review:                review,
			RepresentativeAssetID: assetListRepresentativeAsset(result),
		}
		if referenceID, crop := assetListCropReference(entry.Item, crops); crop != nil {
			wire.ReferenceAssetID = referenceID
			wire.Source = &assetListBoardSourceWire{
				SourceInputAssetID: string(crop.SourceInputAssetID),
				SourceKind:         crop.SourceKind,
				SourceWidth:        crop.SourceWidth,
				SourceHeight:       crop.SourceHeight,
			}
			wire.Region = &assetListBoardRegionWire{
				Region:    crop.Region,
				PixelRect: crop.PixelRect,
			}
		}
		if entry.LatestReview != nil {
			latest := assetListReviewFields(*entry.LatestReview)
			wire.LatestReview = &latest
		}
		items = append(items, wire)
	}
	return assetListBoardWire{List: assetListSummaryFields(board.List), Items: items}
}

// assetListResultWire renders the list detail. crops is the owner-scoped
// provenance of every referenced crop asset, keyed by crop asset id; nil (no
// store or no crop references) leaves every item without the optional crop
// object.
func assetListResultWire(value assetlist.List, crops map[generation.AssetID]listcrop.Crop) assetListWire {
	items := make([]assetListItemWire, 0, len(value.Items))
	for _, item := range value.Items {
		wire := assetListItemFields(item)
		wire.Crop = assetListCropField(item, crops)
		items = append(items, wire)
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

// assetListCropProvenance loads the owner-scoped crop provenance of every
// input asset the list's items reference (#331) in one batch. A server without
// the store keeps the pre-#331 wire: no crop objects at all.
func (s *Server) assetListCropProvenance(ctx context.Context, owner string,
	value assetlist.List,
) (map[generation.AssetID]listcrop.Crop, error) {
	if s.assetListCrops == nil {
		return nil, nil
	}
	ids := make([]generation.AssetID, 0, len(value.Items))
	seen := make(map[generation.AssetID]struct{}, len(value.Items))
	for _, item := range value.Items {
		for _, reference := range item.References {
			if reference.InputAssetID == "" {
				continue
			}
			id := generation.AssetID(reference.InputAssetID)
			if _, duplicate := seen[id]; duplicate {
				continue
			}
			seen[id] = struct{}{}
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	return s.assetListCrops.Load(ctx, generation.OwnerID(owner), ids)
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
	case errors.Is(err, assetlist.ErrImageItemLimit):
		// #331: a list may not take more items from one source picture than
		// application.asset_list.decompose.maximum_items_per_image allows.
		detail := s.assetListImageItemLimitMessage()
		if detail == "" {
			detail = invalid
		}
		writeProblem(writer, http.StatusBadRequest, "asset_list_image_item_limit", detail, err)
	case errors.Is(err, assetlist.ErrCropRegionTooSmall):
		// #331: an edit crop instruction whose shortest side is under the
		// decompose crop bound. The too-small case must precede the generic
		// region case: it reports both.
		detail := s.assetListDecomposeMessage(func(messages catalog.AssetListDecomposeMessagesSchema) string {
			return messages.RegionTooSmall
		}, map[string]string{"minimum": strconv.Itoa(s.assetListCropMinimumSide())})
		writeProblem(writer, http.StatusBadRequest, "asset_list_region_too_small", detail, err)
	case errors.Is(err, assetlist.ErrCropRegionInvalid):
		detail := s.assetListDecomposeMessage(func(messages catalog.AssetListDecomposeMessagesSchema) string {
			return messages.RegionInvalid
		}, nil)
		writeProblem(writer, http.StatusBadRequest, "asset_list_region_invalid", detail, err)
	case errors.Is(err, assetlist.ErrReferenceInvalid):
		// #331: the decompose section owns the crop-reference refusal copy; a
		// Catalog without it keeps the generic invalid-parameters text.
		detail := s.assetListDecomposeMessage(func(messages catalog.AssetListDecomposeMessagesSchema) string {
			return messages.ReferenceInvalid
		}, nil)
		writeProblem(writer, http.StatusBadRequest, "asset_list_reference_invalid", detail, err)
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

// assetListImageItemLimitMessage words the refuse of a list that takes too many
// items from one source picture (#331), with the declared maximum filled in.
func (s *Server) assetListImageItemLimitMessage() string {
	if s.application.AssetList == nil || s.application.AssetList.Decompose == nil {
		return ""
	}
	decompose := s.application.AssetList.Decompose
	text := strings.TrimSpace(decompose.Messages.ImageItemLimit)
	if text == "" {
		return ""
	}
	return strings.ReplaceAll(text, "{maximum}", strconv.Itoa(decompose.MaximumItemsPerImage))
}

// assetListDecomposeMessage fills one decompose refusal copy (#331). A Catalog
// without the section falls back to the generic invalid-parameters text.
func (s *Server) assetListDecomposeMessage(
	pick func(catalog.AssetListDecomposeMessagesSchema) string,
	values map[string]string,
) string {
	detail := ""
	if s.application.AssetList != nil && s.application.AssetList.Decompose != nil {
		detail = strings.TrimSpace(pick(s.application.AssetList.Decompose.Messages))
	}
	if detail == "" {
		return s.application.Chat.Presentation.Messages.InvalidParameters
	}
	if len(values) == 0 {
		return detail
	}
	replacements := make([]string, 0, len(values)*2)
	for name, value := range values {
		replacements = append(replacements, "{"+name+"}", value)
	}
	return strings.NewReplacer(replacements...).Replace(detail)
}

// assetListCropMinimumSide is the declared crop shortest-side bound, filling
// the region_too_small copy.
func (s *Server) assetListCropMinimumSide() int {
	if s.application.AssetList == nil || s.application.AssetList.Decompose == nil {
		return 0
	}
	return s.application.AssetList.Decompose.Crop.MinimumShortEdge
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
	crops, err := s.assetListCropProvenance(request.Context(), owner, value)
	if err != nil {
		s.writeAssetListError(writer, err, value.Title)
		return
	}
	writer.Header().Set("Cache-Control", s.application.Runtime.HTTP.PrivateCacheControl)
	writeJSON(writer, http.StatusOK, assetListResultWire(value, crops))
}

// getProjectAssetListBoard is the read-only acceptance board (#329a). It
// writes nothing: the decisions, their snapshots and the batch archive arrive
// with #329b.
func (s *Server) getProjectAssetListBoard(writer http.ResponseWriter, request *http.Request, principal access.Principal) {
	owner, ok := s.assetListOwner(writer, request, principal)
	if !ok {
		return
	}
	board, err := s.assetLists.Board(request.Context(), owner,
		request.PathValue("projectID"), request.PathValue("listID"))
	if err != nil {
		s.writeAssetListError(writer, err, "")
		return
	}
	crops, err := s.assetListCropProvenance(request.Context(), owner, board.List)
	if err != nil {
		s.writeAssetListError(writer, err, board.List.Title)
		return
	}
	writer.Header().Set("Cache-Control", s.application.Runtime.HTTP.PrivateCacheControl)
	writeJSON(writer, http.StatusOK, assetListBoardFields(board, crops))
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
	crops, err := s.assetListCropProvenance(request.Context(), owner, value)
	if err != nil {
		s.writeAssetListError(writer, err, value.Title)
		return
	}
	writeJSON(writer, http.StatusOK, assetListResultWire(value, crops))
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
	crops, err := s.assetListCropProvenance(request.Context(), owner, value)
	if err != nil {
		s.writeAssetListError(writer, err, value.Title)
		return
	}
	writeJSON(writer, http.StatusOK, assetListResultWire(value, crops))
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
