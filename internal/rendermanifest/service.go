package rendermanifest

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	"image/draw"
	_ "image/jpeg"
	"image/png"
	"io"
	"math"
	"reflect"

	"github.com/LeXwDeX/Plinth/internal/imaging"
	"github.com/LeXwDeX/Plinth/internal/scene"
	_ "golang.org/x/image/webp"
)

const BitmapRendererVersion = "bitmap-v1"

var ErrUnsupported = errors.New("render manifest: unsupported scene representation or transform")

type SceneReader interface {
	Snapshot(context.Context, string, string) (scene.Snapshot, error)
}
type AssetStore interface {
	AssetBytes
	UploadPNG(context.Context, string, []byte) (string, error)
}
type Repository interface {
	Create(context.Context, Manifest) (Manifest, error)
	Get(context.Context, string, string, string) (Manifest, error)
}
type Service struct {
	scenes        SceneReader
	assets        AssetStore
	repository    Repository
	maxPixels     int64
	maxAssetBytes int64
}

func New(scenes SceneReader, assets AssetStore, repository Repository, maxPixels, maxAssetBytes int64) (*Service, error) {
	if scenes == nil || assets == nil || repository == nil || maxPixels <= 0 || maxAssetBytes <= 0 {
		return nil, ErrInvalid
	}
	return &Service{scenes: scenes, assets: assets, repository: repository, maxPixels: maxPixels, maxAssetBytes: maxAssetBytes}, nil
}

// NewVerifier constructs the proof adapter before its SQLite repository is
// available. It exposes Verify only; Create remains unavailable.
func NewVerifier(scenes SceneReader, assets AssetStore, maxPixels, maxAssetBytes int64) (*Service, error) {
	if scenes == nil || assets == nil || maxPixels <= 0 || maxAssetBytes <= 0 {
		return nil, ErrInvalid
	}
	return &Service{scenes: scenes, assets: assets, maxPixels: maxPixels, maxAssetBytes: maxAssetBytes}, nil
}

// Create renders pixels on the server. Its caller supplies only the frame;
// object identities, input digests and output bytes are derived from trusted
// scene and asset stores. The repository rechecks the scene revision on commit.
func (s *Service) Create(ctx context.Context, owner, session string, frame Frame) (Manifest, error) {
	snapshot, err := s.scenes.Snapshot(ctx, owner, session)
	if err != nil {
		return Manifest{}, err
	}
	inputs, pixels, err := s.render(ctx, snapshot, frame)
	if err != nil {
		return Manifest{}, err
	}
	assetID, err := s.assets.UploadPNG(ctx, owner, pixels)
	if err != nil {
		return Manifest{}, err
	}
	var id [16]byte
	if _, err = rand.Read(id[:]); err != nil {
		return Manifest{}, err
	}
	digest := sha256.Sum256(pixels)
	m := Manifest{ID: "render_" + hex.EncodeToString(id[:]), OwnerID: owner, SessionID: session,
		SceneRevision: snapshot.Revision, Frame: frame, Inputs: inputs, RendererVersion: BitmapRendererVersion,
		Output: Output{AssetID: assetID, SizeBytes: int64(len(pixels)), SHA256: hex.EncodeToString(digest[:])}}
	return s.repository.Create(ctx, m)
}

func (s *Service) Get(ctx context.Context, owner, session, manifestID string) (Manifest, error) {
	return s.repository.Get(ctx, owner, session, manifestID)
}

// Verify is the repository's trusted proof adapter. It recomputes the exact
// PNG and complete in-frame input list; a client-authored manifest cannot pass.
func (s *Service) Verify(ctx context.Context, m Manifest, output []byte) error {
	if m.RendererVersion != BitmapRendererVersion {
		return ErrUnsupported
	}
	snapshot, err := s.scenes.Snapshot(ctx, m.OwnerID, m.SessionID)
	if err != nil {
		return err
	}
	if snapshot.Revision != m.SceneRevision {
		return ErrDrift
	}
	inputs, pixels, err := s.render(ctx, snapshot, m.Frame)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(inputs, m.Inputs) || !bytes.Equal(pixels, output) {
		return ErrMismatch
	}
	return nil
}

// VerifyPreparedContext freezes a session scene and its proven rendered PNG
// into Generation's PreparedInput. ManifestHash is the output content digest.
func (s *Service) VerifyPreparedContext(ctx context.Context, owner, session, sceneID, manifestID, digest string) error {
	_, err := s.VerifiedPreparedOutput(ctx, owner, session, sceneID, manifestID, digest)
	return err
}

// VerifiedPreparedOutput returns the immutable local pixels a paid request
// must actually use after the scene and manifest proof have been checked.
func (s *Service) VerifiedPreparedOutput(ctx context.Context, owner, session, sceneID, manifestID, digest string) (Output, error) {
	if owner == "" || session == "" || sceneID != session || manifestID == "" || digest == "" {
		return Output{}, ErrInvalid
	}
	m, err := s.repository.Get(ctx, owner, session, manifestID)
	if err != nil {
		return Output{}, err
	}
	if m.Output.SHA256 != digest || m.RendererVersion != BitmapRendererVersion {
		return Output{}, ErrMismatch
	}
	snapshot, err := s.scenes.Snapshot(ctx, owner, session)
	if err != nil {
		return Output{}, err
	}
	if snapshot.Revision != m.SceneRevision {
		return Output{}, ErrDrift
	}
	return m.Output, nil
}

func intersects(rect scene.Rect, f Frame) bool {
	return rect.X < f.X+f.Width && rect.X+rect.Width > f.X && rect.Y < f.Y+f.Height && rect.Y+rect.Height > f.Y
}
func (s *Service) render(ctx context.Context, snapshot scene.Snapshot, f Frame) ([]Input, []byte, error) {
	if f.Width <= 0 || f.Height <= 0 || f.PixelWidth <= 0 || f.PixelHeight <= 0 ||
		int64(f.PixelWidth) > s.maxPixels/int64(f.PixelHeight) ||
		math.IsNaN(f.X) || math.IsNaN(f.Y) || math.IsInf(f.X, 0) || math.IsInf(f.Y, 0) {
		return nil, nil, ErrInvalid
	}
	sx, sy := float64(f.PixelWidth)/f.Width, float64(f.PixelHeight)/f.Height
	if f.Matrix != [6]float64{sx, 0, 0, sy, -f.X * sx, -f.Y * sy} {
		return nil, nil, ErrInvalid
	}
	canvas := image.NewNRGBA(image.Rect(0, 0, f.PixelWidth, f.PixelHeight))
	inputs := make([]Input, 0)
	for _, item := range snapshot.Items {
		r, rep := item.Object.Rect, item.Representation
		if !intersects(r, f) {
			continue
		}
		if rep.Kind != scene.Bitmap || item.Object.Anchor != (scene.Anchor{}) {
			return nil, nil, ErrUnsupported
		}
		body, err := s.assets.OpenOwned(ctx, snapshot.OwnerID, rep.AssetID)
		if err != nil {
			return nil, nil, err
		}
		data, readErr := io.ReadAll(io.LimitReader(body, s.maxAssetBytes+1))
		closeErr := body.Close()
		if readErr != nil || closeErr != nil {
			return nil, nil, errors.Join(readErr, closeErr)
		}
		if int64(len(data)) > s.maxAssetBytes {
			return nil, nil, ErrInvalid
		}
		cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width) > s.maxPixels/int64(cfg.Height) {
			return nil, nil, ErrUnsupported
		}
		decoded, _, err := image.Decode(bytes.NewReader(data))
		if err != nil {
			return nil, nil, err
		}
		hash := sha256.Sum256(data)
		inputs = append(inputs, Input{ObjectID: item.Object.ID, ObjectRevision: item.Object.Revision,
			RepresentationID: rep.ID, AssetID: rep.AssetID, AssetSHA256: hex.EncodeToString(hash[:])})
		left, top := int(math.Round((r.X-f.X)*sx)), int(math.Round((r.Y-f.Y)*sy))
		width, height := int(math.Round(r.Width*sx)), int(math.Round(r.Height*sy))
		if width <= 0 || height <= 0 || int64(width) > s.maxPixels/int64(height) {
			return nil, nil, ErrUnsupported
		}
		resized := imaging.ResizeBilinear(decoded, width, height)
		draw.Draw(canvas, image.Rect(left, top, left+width, top+height), resized, image.Point{}, draw.Over)
	}
	if len(inputs) == 0 {
		return nil, nil, ErrInvalid
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, canvas); err != nil {
		return nil, nil, err
	}
	if int64(encoded.Len()) > s.maxAssetBytes {
		return nil, nil, ErrInvalid
	}
	return inputs, encoded.Bytes(), nil
}
