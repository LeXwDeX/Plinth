package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/rendermanifest"
	"github.com/LeXwDeX/Plinth/internal/scene"
)

type sceneRectInput struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}
type sceneAnchorInput struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

func (v sceneRectInput) value() scene.Rect {
	return scene.Rect{X: v.X, Y: v.Y, Width: v.Width, Height: v.Height}
}
func (v sceneAnchorInput) value() scene.Anchor { return scene.Anchor{X: v.X, Y: v.Y} }

type sceneRepresentationWire struct {
	ID                     string     `json:"id"`
	ObjectID               string     `json:"object_id"`
	Kind                   scene.Kind `json:"kind"`
	AssetID                string     `json:"asset_id"`
	SourceTaskID           string     `json:"source_task_id,omitempty"`
	SourceRepresentationID string     `json:"source_representation_id,omitempty"`
	PreparedInputID        string     `json:"prepared_input_id,omitempty"`
	MetadataSchemaRevision string     `json:"metadata_schema_revision"`
	CreatedAt              time.Time  `json:"created_at"`
}

func sceneRepresentationResponse(v scene.Representation) sceneRepresentationWire {
	return sceneRepresentationWire{ID: v.ID, ObjectID: v.ObjectID, Kind: v.Kind, AssetID: v.AssetID,
		SourceTaskID: v.SourceTaskID, SourceRepresentationID: v.SourceRepresentationID,
		PreparedInputID: v.PreparedInputID, MetadataSchemaRevision: v.MetadataSchemaRevision, CreatedAt: v.CreatedAt}
}

type sceneObjectWire struct {
	ID                     string           `json:"id"`
	Revision               int64            `json:"revision"`
	ActiveRepresentationID string           `json:"active_representation_id"`
	Rect                   sceneRectInput   `json:"rect"`
	Anchor                 sceneAnchorInput `json:"anchor"`
	DeletedAt              *time.Time       `json:"deleted_at,omitempty"`
	CreatedAt              time.Time        `json:"created_at"`
	UpdatedAt              time.Time        `json:"updated_at"`
}

func sceneObjectResponse(v scene.Object) sceneObjectWire {
	return sceneObjectWire{ID: v.ID, Revision: v.Revision, ActiveRepresentationID: v.ActiveRepresentationID,
		Rect: sceneRectInput{v.Rect.X, v.Rect.Y, v.Rect.Width, v.Rect.Height}, Anchor: sceneAnchorInput{v.Anchor.X, v.Anchor.Y},
		DeletedAt: v.DeletedAt, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt}
}
func (s *Server) sceneSessionOwned(w http.ResponseWriter, r *http.Request, owner generation.OwnerID) (string, bool) {
	session := r.PathValue("sessionID")
	if s.ownsAgentSession != nil {
		if session == "" {
			s.writeMessageProblem(w, http.StatusBadRequest, "session_required", "error.session_required")
			return "", false
		}
		if !s.ownsAgentSession(r.Context(), string(owner), session) {
			s.writeMessageProblem(w, http.StatusNotFound, "not_found", "error.task_not_found")
			return "", false
		}
		return session, true
	}
	if !s.taskSessionOwned(w, r, owner, session) {
		return "", false
	}
	return session, true
}
func (s *Server) scenePrivate(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", s.application.Runtime.HTTP.PrivateCacheControl)
}

func (s *Server) createSceneObject(w http.ResponseWriter, r *http.Request, owner generation.OwnerID) {
	session, ok := s.sceneSessionOwned(w, r, owner)
	if !ok {
		return
	}
	var body struct {
		Rect    sceneRectInput   `json:"rect"`
		Anchor  sceneAnchorInput `json:"anchor"`
		Initial struct {
			Kind                   scene.Kind `json:"kind"`
			AssetID                string     `json:"asset_id"`
			MetadataSchemaRevision string     `json:"metadata_schema_revision"`
		} `json:"initial"`
	}
	if s.decodeJSON(w, r, &body) != nil {
		return
	}
	obj, err := s.scene.Create(r.Context(), string(owner), session, body.Rect.value(), body.Anchor.value(),
		scene.Representation{Kind: body.Initial.Kind, AssetID: body.Initial.AssetID, MetadataSchemaRevision: body.Initial.MetadataSchemaRevision})
	if err != nil {
		s.writeSceneError(w, err)
		return
	}
	s.scenePrivate(w)
	writeJSON(w, http.StatusCreated, sceneObjectResponse(obj))
}
func (s *Server) getSceneSnapshot(w http.ResponseWriter, r *http.Request, owner generation.OwnerID) {
	session, ok := s.sceneSessionOwned(w, r, owner)
	if !ok {
		return
	}
	snapshot, err := s.scene.Snapshot(r.Context(), string(owner), session)
	if err != nil {
		s.writeSceneError(w, err)
		return
	}
	items := make([]any, 0, len(snapshot.Items))
	for _, item := range snapshot.Items {
		items = append(items, map[string]any{"object": sceneObjectResponse(item.Object), "representation": sceneRepresentationResponse(item.Representation)})
	}
	s.scenePrivate(w)
	writeJSON(w, http.StatusOK, map[string]any{"session_id": session, "revision": snapshot.Revision, "items": items})
}
func (s *Server) moveSceneObject(w http.ResponseWriter, r *http.Request, owner generation.OwnerID) {
	session, ok := s.sceneSessionOwned(w, r, owner)
	if !ok {
		return
	}
	var body struct {
		ExpectedRevision int64            `json:"expected_revision"`
		Rect             sceneRectInput   `json:"rect"`
		Anchor           sceneAnchorInput `json:"anchor"`
	}
	if s.decodeJSON(w, r, &body) != nil {
		return
	}
	obj, err := s.scene.Move(r.Context(), scene.Mutation{OwnerID: string(owner), SessionID: session, ObjectID: r.PathValue("objectID"), ExpectedRevision: body.ExpectedRevision}, body.Rect.value(), body.Anchor.value())
	if err != nil {
		s.writeSceneError(w, err)
		return
	}
	s.scenePrivate(w)
	writeJSON(w, http.StatusOK, sceneObjectResponse(obj))
}
func (s *Server) deleteSceneObject(w http.ResponseWriter, r *http.Request, owner generation.OwnerID) {
	session, ok := s.sceneSessionOwned(w, r, owner)
	if !ok {
		return
	}
	var body struct {
		ExpectedRevision int64 `json:"expected_revision"`
	}
	if s.decodeJSON(w, r, &body) != nil {
		return
	}
	obj, err := s.scene.Delete(r.Context(), scene.Mutation{OwnerID: string(owner), SessionID: session, ObjectID: r.PathValue("objectID"), ExpectedRevision: body.ExpectedRevision})
	if err != nil {
		s.writeSceneError(w, err)
		return
	}
	s.scenePrivate(w)
	writeJSON(w, http.StatusOK, sceneObjectResponse(obj))
}
func (s *Server) applySceneCandidate(w http.ResponseWriter, r *http.Request, owner generation.OwnerID) {
	session, ok := s.sceneSessionOwned(w, r, owner)
	if !ok {
		return
	}
	var body struct {
		ExpectedRevision int64  `json:"expected_revision"`
		RepresentationID string `json:"representation_id"`
	}
	if s.decodeJSON(w, r, &body) != nil {
		return
	}
	obj, err := s.scene.ApplyCandidate(r.Context(), scene.Mutation{OwnerID: string(owner), SessionID: session, ObjectID: r.PathValue("objectID"), ExpectedRevision: body.ExpectedRevision}, body.RepresentationID)
	if err != nil {
		s.writeSceneError(w, err)
		return
	}
	s.scenePrivate(w)
	writeJSON(w, http.StatusOK, sceneObjectResponse(obj))
}
func (s *Server) listSceneRepresentations(w http.ResponseWriter, r *http.Request, owner generation.OwnerID) {
	session, ok := s.sceneSessionOwned(w, r, owner)
	if !ok {
		return
	}
	values, err := s.scene.ListRepresentations(r.Context(), string(owner), session, r.PathValue("objectID"))
	if err != nil {
		s.writeSceneError(w, err)
		return
	}
	out := make([]sceneRepresentationWire, 0, len(values))
	for _, v := range values {
		out = append(out, sceneRepresentationResponse(v))
	}
	s.scenePrivate(w)
	writeJSON(w, http.StatusOK, map[string]any{"representations": out})
}

type renderFrameInput struct {
	X           float64 `json:"x"`
	Y           float64 `json:"y"`
	Width       float64 `json:"width"`
	Height      float64 `json:"height"`
	PixelWidth  int     `json:"pixel_width"`
	PixelHeight int     `json:"pixel_height"`
}

func (v renderFrameInput) value() rendermanifest.Frame {
	f := rendermanifest.Frame{X: v.X, Y: v.Y, Width: v.Width, Height: v.Height, PixelWidth: v.PixelWidth, PixelHeight: v.PixelHeight}
	if f.Width > 0 && f.Height > 0 {
		sx, sy := float64(f.PixelWidth)/f.Width, float64(f.PixelHeight)/f.Height
		f.Matrix = [6]float64{sx, 0, 0, sy, -f.X * sx, -f.Y * sy}
	}
	return f
}
func renderManifestResponse(v rendermanifest.Manifest) map[string]any {
	return map[string]any{"id": v.ID, "session_id": v.SessionID, "scene_revision": v.SceneRevision,
		"frame": map[string]any{"x": v.Frame.X, "y": v.Frame.Y, "width": v.Frame.Width, "height": v.Frame.Height,
			"pixel_width": v.Frame.PixelWidth, "pixel_height": v.Frame.PixelHeight, "matrix": v.Frame.Matrix},
		"inputs": v.Inputs, "renderer_version": v.RendererVersion,
		"output":     map[string]any{"asset_id": v.Output.AssetID, "size_bytes": v.Output.SizeBytes, "sha256": v.Output.SHA256},
		"created_at": v.CreatedAt}
}
func (s *Server) createRenderManifest(w http.ResponseWriter, r *http.Request, owner generation.OwnerID) {
	session, ok := s.sceneSessionOwned(w, r, owner)
	if !ok {
		return
	}
	var body struct {
		Frame renderFrameInput `json:"frame"`
	}
	if s.decodeJSON(w, r, &body) != nil {
		return
	}
	manifest, err := s.renderManifests.Create(r.Context(), string(owner), session, body.Frame.value())
	if err != nil {
		s.writeRenderManifestError(w, err)
		return
	}
	s.scenePrivate(w)
	writeJSON(w, http.StatusCreated, renderManifestResponse(manifest))
}
func (s *Server) getRenderManifest(w http.ResponseWriter, r *http.Request, owner generation.OwnerID) {
	session, ok := s.sceneSessionOwned(w, r, owner)
	if !ok {
		return
	}
	manifest, err := s.renderManifests.Get(r.Context(), string(owner), session, r.PathValue("manifestID"))
	if err != nil {
		s.writeRenderManifestError(w, err)
		return
	}
	s.scenePrivate(w)
	writeJSON(w, http.StatusOK, renderManifestResponse(manifest))
}
func (s *Server) writeSceneError(w http.ResponseWriter, err error) {
	noteCause(w, err)
	switch {
	case errors.Is(err, scene.ErrInvalid):
		s.writeMessageProblem(w, http.StatusBadRequest, "invalid_input", "error.invalid_input")
	case errors.Is(err, scene.ErrNotFound):
		s.writeMessageProblem(w, http.StatusNotFound, "not_found", "error.not_found")
	case errors.Is(err, scene.ErrConflict):
		s.writeMessageProblem(w, http.StatusConflict, "conflict", "error.conflict")
	default:
		s.writeMessageProblem(w, http.StatusServiceUnavailable, "unavailable", "error.internal")
	}
}
func (s *Server) writeRenderManifestError(w http.ResponseWriter, err error) {
	noteCause(w, err)
	switch {
	case errors.Is(err, rendermanifest.ErrInvalid), errors.Is(err, rendermanifest.ErrUnsupported):
		s.writeMessageProblem(w, http.StatusBadRequest, "invalid_input", "error.invalid_input")
	case errors.Is(err, rendermanifest.ErrNotFound), errors.Is(err, scene.ErrNotFound):
		s.writeMessageProblem(w, http.StatusNotFound, "not_found", "error.not_found")
	case errors.Is(err, rendermanifest.ErrConflict), errors.Is(err, rendermanifest.ErrDrift), errors.Is(err, rendermanifest.ErrMismatch):
		s.writeMessageProblem(w, http.StatusConflict, "conflict", "error.conflict")
	default:
		s.writeMessageProblem(w, http.StatusServiceUnavailable, "unavailable", "error.internal")
	}
}
