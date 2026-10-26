package rendermanifest_test

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/rendermanifest"
	"github.com/LeXwDeX/Plinth/internal/scene"
)

type sceneSnapshot struct{ value scene.Snapshot }

func (s *sceneSnapshot) Snapshot(_ context.Context, owner, session string) (scene.Snapshot, error) {
	if owner != s.value.OwnerID || session != s.value.SessionID {
		return scene.Snapshot{}, scene.ErrNotFound
	}
	return s.value, nil
}

type pixelsStore struct {
	values  map[string][]byte
	uploads int
}

func (s *pixelsStore) OpenOwned(_ context.Context, owner, id string) (io.ReadCloser, error) {
	b, ok := s.values[owner+"/"+id]
	if !ok {
		return nil, errors.New("asset not owned")
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}
func (s *pixelsStore) UploadPNG(_ context.Context, owner string, b []byte) (string, error) {
	s.uploads++
	id := "frame-output"
	s.values[owner+"/"+id] = append([]byte(nil), b...)
	return id, nil
}

type proofRepository struct {
	verifier rendermanifest.RenderVerifier
	stored   rendermanifest.Manifest
	store    *pixelsStore
	scene    *sceneSnapshot
	drift    bool
}

func (r *proofRepository) Create(ctx context.Context, m rendermanifest.Manifest) (rendermanifest.Manifest, error) {
	if r.drift {
		r.scene.value.Revision++
	}
	body, err := r.store.OpenOwned(ctx, m.OwnerID, m.Output.AssetID)
	if err != nil {
		return rendermanifest.Manifest{}, err
	}
	b, err := io.ReadAll(body)
	body.Close()
	if err != nil {
		return rendermanifest.Manifest{}, err
	}
	if err := r.verifier.Verify(ctx, m, b); err != nil {
		return rendermanifest.Manifest{}, err
	}
	r.stored = m
	return m, nil
}
func (r *proofRepository) Get(_ context.Context, owner, session, id string) (rendermanifest.Manifest, error) {
	if r.stored.ID != id || r.stored.OwnerID != owner || r.stored.SessionID != session {
		return rendermanifest.Manifest{}, rendermanifest.ErrNotFound
	}
	return r.stored, nil
}
func pngPixel(c color.NRGBA) []byte {
	im := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	im.SetNRGBA(0, 0, c)
	var b bytes.Buffer
	_ = png.Encode(&b, im)
	return b.Bytes()
}
func TestTrustedBitmapFramePixelsAndExclusion(t *testing.T) {
	ctx := context.Background()
	frame := rendermanifest.Frame{X: 0, Y: 0, Width: 2, Height: 2, PixelWidth: 2, PixelHeight: 2, Matrix: [6]float64{1, 0, 0, 1, 0, 0}}
	scenes := &sceneSnapshot{value: scene.Snapshot{OwnerID: "owner-a", SessionID: "session-a", Revision: 2, Items: []scene.SnapshotItem{
		{Object: scene.Object{ID: "a", Revision: 1, Rect: scene.Rect{Width: 2, Height: 2}}, Representation: scene.Representation{ID: "ra", Kind: scene.Bitmap, AssetID: "blue"}},
		{Object: scene.Object{ID: "z", Revision: 1, Rect: scene.Rect{X: 3, Width: 1, Height: 1}}, Representation: scene.Representation{ID: "rz", Kind: scene.Vector, AssetID: "outside"}},
	}}}
	assets := &pixelsStore{values: map[string][]byte{"owner-a/blue": pngPixel(color.NRGBA{B: 255, A: 255})}}
	proof, err := rendermanifest.NewVerifier(scenes, assets, 100, 1024)
	if err != nil {
		t.Fatal(err)
	}
	repo := &proofRepository{verifier: proof, store: assets, scene: scenes}
	service, err := rendermanifest.New(scenes, assets, repo, 100, 1024)
	if err != nil {
		t.Fatal(err)
	}
	m, err := service.Create(ctx, "owner-a", "session-a", frame)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Inputs) != 1 || m.Inputs[0].ObjectID != "a" || assets.uploads != 1 {
		t.Fatalf("frame inputs=%+v uploads=%d", m.Inputs, assets.uploads)
	}
	imageData, err := png.Decode(bytes.NewReader(assets.values["owner-a/"+m.Output.AssetID]))
	if err != nil {
		t.Fatal(err)
	}
	r, g, b, a := imageData.At(1, 1).RGBA()
	if r != 0 || g != 0 || b != 65535 || a != 65535 {
		t.Fatalf("actual pixel = %d,%d,%d,%d", r, g, b, a)
	}
	if err := service.VerifyPreparedContext(ctx, "owner-b", "session-a", "session-a", m.ID, m.Output.SHA256); !errors.Is(err, rendermanifest.ErrNotFound) {
		t.Fatalf("cross owner manifest accepted: %v", err)
	}
	if err := service.VerifyPreparedContext(ctx, "owner-a", "session-a", "session-a", m.ID, m.Output.SHA256); err != nil {
		t.Fatal(err)
	}
	bad := m
	bad.Inputs = append([]rendermanifest.Input(nil), m.Inputs...)
	bad.Inputs[0].AssetSHA256 = "forged"
	if err := proof.Verify(ctx, bad, assets.values["owner-a/"+m.Output.AssetID]); !errors.Is(err, rendermanifest.ErrMismatch) {
		t.Fatalf("forged input accepted: %v", err)
	}
	scenes.value.Items[1].Object.Rect.X = 1
	if err := proof.Verify(ctx, m, assets.values["owner-a/"+m.Output.AssetID]); !errors.Is(err, rendermanifest.ErrUnsupported) {
		t.Fatalf("in-frame vector accepted: %v", err)
	}
}

func TestRenderRejectsConcurrentSceneRevisionDrift(t *testing.T) {
	ctx := context.Background()
	scenes := &sceneSnapshot{value: scene.Snapshot{OwnerID: "o", SessionID: "s", Revision: 1, Items: []scene.SnapshotItem{{
		Object:         scene.Object{ID: "obj", Revision: 1, Rect: scene.Rect{Width: 1, Height: 1}},
		Representation: scene.Representation{ID: "rep", Kind: scene.Bitmap, AssetID: "pixel"}}}}}
	assets := &pixelsStore{values: map[string][]byte{"o/pixel": pngPixel(color.NRGBA{R: 255, A: 255})}}
	proof, _ := rendermanifest.NewVerifier(scenes, assets, 100, 1024)
	repo := &proofRepository{verifier: proof, store: assets, scene: scenes, drift: true}
	service, _ := rendermanifest.New(scenes, assets, repo, 100, 1024)
	_, err := service.Create(ctx, "o", "s", rendermanifest.Frame{Width: 1, Height: 1, PixelWidth: 1, PixelHeight: 1, Matrix: [6]float64{1, 0, 0, 1, 0, 0}})
	if !errors.Is(err, rendermanifest.ErrDrift) || repo.stored.ID != "" {
		t.Fatalf("revision drift committed: %+v %v", repo.stored, err)
	}
}
