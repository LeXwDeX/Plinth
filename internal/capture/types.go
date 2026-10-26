// Package capture records a browser-rendered 3D viewport as an owned local
// image asset with explicit source provenance. The record proves ownership and
// byte identity; it does not claim that a server re-rendered the 3D pixels.
package capture

import (
	"context"
	"errors"
	"io"
	"time"
)

var (
	ErrInvalid  = errors.New("capture: invalid input")
	ErrNotFound = errors.New("capture: source or asset not found")
	ErrConflict = errors.New("capture: identity conflict")
	ErrMismatch = errors.New("capture: screenshot bytes mismatch")
)

type View struct {
	CameraOrbit     string `json:"camera_orbit"`
	CameraTarget    string `json:"camera_target"`
	FieldOfView     string `json:"field_of_view"`
	Environment     string `json:"environment"`
	AnimationName   string `json:"animation_name,omitempty"`
	AnimationTimeMS int64  `json:"animation_time_ms"`
	RendererVersion string `json:"renderer_version"`
}

type Command struct {
	ID                string `json:"id"`
	OwnerID           string `json:"-"`
	SessionID         string `json:"session_id"`
	SourceTaskID      string `json:"source_task_id"`
	ModelAssetID      string `json:"model_asset_id"`
	ScreenshotAssetID string `json:"screenshot_asset_id"`
	IdempotencyKey    string `json:"idempotency_key"`
	View              View   `json:"view"`
}

type Record struct {
	Command
	PixelWidth  int       `json:"pixel_width"`
	PixelHeight int       `json:"pixel_height"`
	SizeBytes   int64     `json:"size_bytes"`
	SHA256      string    `json:"sha256"`
	CreatedAt   time.Time `json:"created_at"`
}

// AssetBytes must read the same owner-scoped local CAS bytes used by download.
type AssetBytes interface {
	OpenOwned(context.Context, string, string) (io.ReadCloser, error)
}

type Recorder interface {
	Record(context.Context, Command) (Record, error)
	Get(context.Context, string, string) (Record, error)
}
