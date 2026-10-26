package scene

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// Repository is the server-owned scene state and CAS port.
type Repository interface {
	Create(context.Context, Create) (Object, error)
	RegisterCandidate(context.Context, Candidate) (Representation, error)
	ApplyCandidate(context.Context, Mutation, string) (Object, error)
	Move(context.Context, Mutation, Rect, Anchor) (Object, error)
	Delete(context.Context, Mutation) (Object, error)
	Get(context.Context, string, string, string) (Object, error)
	GetRepresentation(context.Context, string, string, string, string) (Representation, error)
	ListRepresentations(context.Context, string, string, string) ([]Representation, error)
	Snapshot(context.Context, string, string) (Snapshot, error)
	ReconcileTaskCandidates(context.Context, int) (int, error)
}

type Service struct{ repository Repository }

func New(repository Repository) (*Service, error) {
	if repository == nil {
		return nil, ErrInvalid
	}
	return &Service{repository: repository}, nil
}

func randomID(prefix string) (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(value[:]), nil
}

func (s *Service) Create(ctx context.Context, owner, session string, rect Rect, anchor Anchor, initial Representation) (Object, error) {
	objectID, err := randomID("obj_")
	if err != nil {
		return Object{}, err
	}
	repID, err := randomID("repr_")
	if err != nil {
		return Object{}, err
	}
	initial.ID, initial.ObjectID = repID, objectID
	return s.repository.Create(ctx, Create{Object: Object{ID: objectID, OwnerID: owner, SessionID: session, Rect: rect, Anchor: anchor}, Initial: initial})
}

func (s *Service) RegisterCandidate(ctx context.Context, candidate Candidate) (Representation, error) {
	id, err := randomID("repr_")
	if err != nil {
		return Representation{}, err
	}
	candidate.Representation.ID = id
	return s.repository.RegisterCandidate(ctx, candidate)
}

func (s *Service) ApplyCandidate(ctx context.Context, mutation Mutation, representationID string) (Object, error) {
	return s.repository.ApplyCandidate(ctx, mutation, representationID)
}
func (s *Service) Move(ctx context.Context, mutation Mutation, rect Rect, anchor Anchor) (Object, error) {
	return s.repository.Move(ctx, mutation, rect, anchor)
}
func (s *Service) Delete(ctx context.Context, mutation Mutation) (Object, error) {
	return s.repository.Delete(ctx, mutation)
}
func (s *Service) Get(ctx context.Context, owner, session, objectID string) (Object, error) {
	return s.repository.Get(ctx, owner, session, objectID)
}
func (s *Service) ListRepresentations(ctx context.Context, owner, session, objectID string) ([]Representation, error) {
	return s.repository.ListRepresentations(ctx, owner, session, objectID)
}
func (s *Service) Snapshot(ctx context.Context, owner, session string) (Snapshot, error) {
	return s.repository.Snapshot(ctx, owner, session)
}
func (s *Service) ReconcileTaskCandidates(ctx context.Context, limit int) (int, error) {
	return s.repository.ReconcileTaskCandidates(ctx, limit)
}

// VerifyPreparedSceneObject binds the frozen generation input to exactly one
// current bitmap representation. Multi-reference input cannot identify a
// unique source object and is deliberately rejected.
func (s *Service) VerifyPreparedSceneObject(ctx context.Context, owner, session, objectID string, revision int64, normalized json.RawMessage) error {
	_, err := s.ResolvePreparedSource(ctx, owner, session, objectID, revision, normalized)
	return err
}

// ResolvePreparedSource freezes the representation selected by the human at
// Prepare time. A later scene move cannot change this identity.
func (s *Service) ResolvePreparedSource(ctx context.Context, owner, session, objectID string, revision int64, normalized json.RawMessage) (string, error) {
	if owner == "" || session == "" || objectID == "" || revision < 1 {
		return "", ErrInvalid
	}
	snapshot, err := s.repository.Snapshot(ctx, owner, session)
	if err != nil {
		return "", err
	}
	var item *SnapshotItem
	for i := range snapshot.Items {
		if snapshot.Items[i].Object.ID == objectID {
			item = &snapshot.Items[i]
			break
		}
	}
	if item == nil {
		return "", ErrNotFound
	}
	if item.Object.Revision != revision || item.Representation.Kind != Bitmap {
		return "", ErrConflict
	}
	id, err := FrozenSourceAsset(normalized)
	if err != nil || id != item.Representation.AssetID {
		return "", ErrConflict
	}
	return item.Representation.ID, nil
}

// VerifyFrozenPreparedSource checks immutable provenance after a later object
// move. It does not require the old representation to remain active.
func (s *Service) VerifyFrozenPreparedSource(ctx context.Context, owner, session, objectID string,
	revision int64, representationID string, normalized json.RawMessage) error {
	if revision < 1 || representationID == "" {
		return ErrInvalid
	}
	object, err := s.repository.Get(ctx, owner, session, objectID)
	if err != nil {
		return err
	}
	if object.Revision < revision {
		return ErrConflict
	}
	rep, err := s.repository.GetRepresentation(ctx, owner, session, objectID, representationID)
	if err != nil {
		return err
	}
	if rep.Kind != Bitmap {
		return ErrConflict
	}
	id, err := FrozenSourceAsset(normalized)
	if err != nil || id != rep.AssetID {
		return ErrConflict
	}
	return nil
}

// FrozenSourceAsset accepts only an unambiguous, normalized source asset.
func FrozenSourceAsset(normalized json.RawMessage) (string, error) {
	var values map[string]json.RawMessage
	if json.Unmarshal(normalized, &values) != nil {
		return "", ErrInvalid
	}
	var ids []string
	if raw, ok := values["reference_assets"]; ok {
		if json.Unmarshal(raw, &ids) != nil {
			return "", ErrInvalid
		}
	}
	if raw, ok := values["source_asset"]; ok {
		var id string
		if json.Unmarshal(raw, &id) != nil {
			return "", ErrInvalid
		}
		ids = append(ids, id)
	}
	if len(ids) != 1 || ids[0] == "" {
		return "", ErrConflict
	}
	return ids[0], nil
}

// RegisterTaskCandidate records a successful 2D-to-3D output as a candidate
// without changing the user's active representation. The write transaction
// rechecks the exact source object revision and source representation.
func (s *Service) RegisterTaskCandidate(ctx context.Context, owner, session, objectID string,
	sourceRevision int64, sourceRepresentationID, preparedID, taskID, assetID, metadataRevision string) (Representation, error) {
	if sourceRevision < 1 || sourceRepresentationID == "" || preparedID == "" || taskID == "" || assetID == "" || metadataRevision == "" {
		return Representation{}, ErrInvalid
	}
	return s.repository.RegisterCandidate(ctx, Candidate{OwnerID: owner, SessionID: session, ObjectID: objectID,
		FrozenSourceRevision: sourceRevision, FrozenSourceRepresentationID: sourceRepresentationID, Idempotent: true,
		Representation: Representation{ID: TaskCandidateID(taskID, assetID), ObjectID: objectID, Kind: Model3D,
			AssetID: assetID, SourceTaskID: taskID, SourceRepresentationID: sourceRepresentationID,
			PreparedInputID: preparedID, MetadataSchemaRevision: metadataRevision}})
}

func TaskCandidateID(taskID, assetID string) string {
	sum := sha256.Sum256([]byte(taskID + "\x00" + assetID))
	return "repr_task_" + hex.EncodeToString(sum[:])
}
