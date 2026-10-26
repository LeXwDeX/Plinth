// Package scene defines stable creative object and immutable representation identities.
// A generation task may produce a candidate representation, but never owns the object.
package scene

import (
	"errors"
	"time"
)

var (
	ErrInvalid  = errors.New("scene: invalid input")
	ErrNotFound = errors.New("scene: object or source not found")
	ErrConflict = errors.New("scene: revision conflict")
)

type Kind string

const (
	Bitmap  Kind = "bitmap"
	Vector  Kind = "vector"
	Model3D Kind = "model3d"
	Capture Kind = "capture"
)

type Rect struct{ X, Y, Width, Height float64 }
type Anchor struct{ X, Y float64 }

type Object struct {
	ID                     string
	OwnerID                string
	SessionID              string
	Revision               int64
	ActiveRepresentationID string
	Rect                   Rect
	Anchor                 Anchor
	DeletedAt              *time.Time
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

// Representation fields are write-once; applying a candidate changes only the object pointer.
type Representation struct {
	ID                     string
	ObjectID               string
	Kind                   Kind
	AssetID                string
	SourceTaskID           string
	SourceRepresentationID string
	PreparedInputID        string
	MetadataSchemaRevision string
	CreatedAt              time.Time
}

type Create struct {
	Object  Object
	Initial Representation
}

type Candidate struct {
	OwnerID                        string
	SessionID                      string
	ObjectID                       string
	ExpectedObjectRevision         int64
	ExpectedSourceRepresentationID string
	FrozenSourceRevision           int64
	FrozenSourceRepresentationID   string
	Idempotent                     bool
	Representation                 Representation
}

type Mutation struct {
	OwnerID          string
	SessionID        string
	ObjectID         string
	ExpectedRevision int64
}

// Snapshot is a consistent session scene revision and its active objects.
// Consumers must still compare Revision when committing derived artifacts.
type Snapshot struct {
	OwnerID   string
	SessionID string
	Revision  int64
	Items     []SnapshotItem
}

type SnapshotItem struct {
	Object         Object
	Representation Representation
}
