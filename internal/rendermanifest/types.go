// Package rendermanifest records a verified, immutable rendering snapshot.
// A browser-provided description alone is never proof of rendered pixels.
package rendermanifest

import (
	"context"
	"errors"
	"io"
	"time"
)

var (
	ErrInvalid  = errors.New("render manifest: invalid input")
	ErrNotFound = errors.New("render manifest: evidence not found")
	ErrDrift    = errors.New("render manifest: scene revision changed")
	ErrMismatch = errors.New("render manifest: bytes or render proof mismatch")
	ErrConflict = errors.New("render manifest: already exists")
)

// Matrix maps logical scene coordinates to output pixel coordinates.
type Frame struct {
	X, Y, Width, Height     float64
	PixelWidth, PixelHeight int
	Matrix                  [6]float64
}

// Input binds one current scene object representation to an owned READY asset.
// Inputs are persisted in object ID order for deterministic snapshot identity.
type Input struct {
	ObjectID         string `json:"object_id"`
	ObjectRevision   int64  `json:"object_revision"`
	RepresentationID string `json:"representation_id"`
	AssetID          string `json:"asset_id"`
	AssetSHA256      string `json:"asset_sha256"`
}

type Output struct {
	AssetID   string
	SizeBytes int64
	SHA256    string
}

type Manifest struct {
	ID              string
	OwnerID         string
	SessionID       string
	SceneRevision   int64
	Frame           Frame
	Inputs          []Input
	RendererVersion string
	Output          Output
	CreatedAt       time.Time
}

// AssetBytes must open the platform-owned, already uploaded asset bytes.
type AssetBytes interface {
	OpenOwned(context.Context, string, string) (io.ReadCloser, error)
}

// RenderVerifier is a trusted renderer/proof adapter. It must verify that the
// supplied bytes correspond to the frame, transforms, inputs and version;
// accepting a client-generated JSON assertion does not implement this port.
type RenderVerifier interface {
	Verify(context.Context, Manifest, []byte) error
}
