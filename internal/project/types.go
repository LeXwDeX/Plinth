package project

import (
	"errors"
	"time"
)

// Owner-scoped project errors. Every repository method must map a missing or
// foreign project to ErrNotFound so HTTP can answer the same 404 without
// leaking existence.
var (
	ErrNotFound         = errors.New("project: not found")
	ErrInvalid          = errors.New("project: invalid")
	ErrNameRequired     = errors.New("project: name required")
	ErrNameTooLong      = errors.New("project: name too long")
	ErrLimit            = errors.New("project: limit reached")
	ErrStyleTextTooLong = errors.New("project: style text too long")
	ErrPaletteInvalid   = errors.New("project: invalid palette color")
	ErrReferenceLimit   = errors.New("project: too many references")
	ErrReferenceInvalid = errors.New("project: invalid reference")
)

// Reference is one stored reference to a picture or model. A project never
// stores bytes: exactly one shape is valid, either an input asset the owner
// uploaded or a finished Task's asset (TaskID+AssetID).
type Reference struct {
	TaskID       string `json:"task_id,omitempty"`
	AssetID      string `json:"asset_id,omitempty"`
	InputAssetID string `json:"input_asset_id,omitempty"`
}

// Valid reports whether the reference names exactly one source shape.
func (reference Reference) Valid() bool {
	if reference.InputAssetID != "" {
		return reference.TaskID == "" && reference.AssetID == ""
	}
	return reference.TaskID != "" && reference.AssetID != ""
}

func (reference Reference) key() string {
	if reference.InputAssetID != "" {
		return "input:" + reference.InputAssetID
	}
	return "task:" + reference.TaskID + "/" + reference.AssetID
}

// Project is one owner-scoped project container.
type Project struct {
	ID        string
	OwnerID   string
	Name      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// StyleGuide is a project's reusable 2D style guide. References are stored in
// request order; Palette entries match the Schema's colour pattern.
type StyleGuide struct {
	ProjectID  string
	Enabled    bool
	Text       string
	Palette    []string
	References []Reference
	UpdatedAt  time.Time
}

// Conventions are a project's production defaults. Brief holds the declared
// production-brief fields, Texts the free-form naming/coordinate conventions.
type Conventions struct {
	ProjectID string
	Brief     []byte
	Texts     map[string]string
	UpdatedAt time.Time
}

// Asset is one character, prop or style reference in a project. Images and
// Models only hold references to the owner's input or finished Task assets.
type Asset struct {
	ID          string
	ProjectID   string
	OwnerID     string
	Kind        string
	Name        string
	Description string
	Images      []Reference
	Models      []Reference
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// AssetInput is one create or update request.
type AssetInput struct {
	Kind        string
	Name        string
	Description string
	Images      []Reference
	Models      []Reference
}

// PromoteInput turns one owner-verified picture into a project asset.
type PromoteInput struct {
	Kind        string
	Name        string
	Description string
	Source      Reference
}
