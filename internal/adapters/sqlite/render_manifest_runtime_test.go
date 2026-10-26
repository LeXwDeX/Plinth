package sqlite

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/rendermanifest"
	"github.com/LeXwDeX/Plinth/internal/scene"
)

type runtimeFrameAssets struct {
	repo     *Repository
	bytes    map[string][]byte
	onUpload func()
}

func (a *runtimeFrameAssets) OpenOwned(_ context.Context, owner, id string) (io.ReadCloser, error) {
	b, ok := a.bytes[owner+"/"+id]
	if !ok {
		return nil, errors.New("unowned asset")
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}
func (a *runtimeFrameAssets) UploadPNG(ctx context.Context, owner string, data []byte) (string, error) {
	if a.onUpload != nil {
		a.onUpload()
	}
	id := "render-output"
	a.bytes[owner+"/"+id] = append([]byte(nil), data...)
	sum := sha256.Sum256(data)
	_, err := a.repo.db.ExecContext(ctx, `INSERT OR REPLACE INTO generation_input_assets(id,owner_id,storage_key,media_type,size_bytes,sha256,created_at)
		VALUES (?,?,?,?,?,?,?)`, id, owner, "render-output-key", "image/png", len(data), hex.EncodeToString(sum[:]), "2026-01-01T00:00:00Z")
	return id, err
}
func runtimePNG(c color.NRGBA) []byte {
	im := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	im.SetNRGBA(0, 0, c)
	var b bytes.Buffer
	_ = png.Encode(&b, im)
	return b.Bytes()
}

func TestRuntimeBitmapManifestUsesRealPixelsAndSceneCAS(t *testing.T) {
	ctx := context.Background()
	repo, sr := sceneTestRepository(t)
	blue := runtimePNG(color.NRGBA{B: 255, A: 255})
	sum := sha256.Sum256(blue)
	_, err := repo.db.ExecContext(ctx, `UPDATE generation_input_assets SET size_bytes=?,sha256=? WHERE id='asset-a'`, len(blue), hex.EncodeToString(sum[:]))
	if err != nil {
		t.Fatal(err)
	}
	first := sceneCreate("asset-a")
	first.Object.Rect = scene.Rect{Width: 2, Height: 2}
	first.Object.Anchor = scene.Anchor{}
	if _, err = sr.Create(ctx, first); err != nil {
		t.Fatal(err)
	}
	assets := &runtimeFrameAssets{repo: repo, bytes: map[string][]byte{"owner-a/asset-a": blue}}
	proof, err := rendermanifest.NewVerifier(sr, assets, 64, 1024)
	if err != nil {
		t.Fatal(err)
	}
	mr, err := repo.RenderManifestRepository(RenderManifestRepositoryConfig{Bytes: assets, Verifier: proof,
		Evidence: SQLiteRenderManifestEvidence{}, MaxOutputBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	service, err := rendermanifest.New(sr, assets, mr, 64, 1024)
	if err != nil {
		t.Fatal(err)
	}
	frame := rendermanifest.Frame{Width: 2, Height: 2, PixelWidth: 2, PixelHeight: 2, Matrix: [6]float64{1, 0, 0, 1, 0, 0}}
	m, err := service.Create(ctx, "owner-a", "session-a", frame)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Inputs) != 1 || m.Inputs[0].ObjectID != "object-a" {
		t.Fatalf("inputs=%+v", m.Inputs)
	}
	actual, err := png.Decode(bytes.NewReader(assets.bytes["owner-a/"+m.Output.AssetID]))
	if err != nil {
		t.Fatal(err)
	}
	_, _, b, a := actual.At(1, 1).RGBA()
	if b != 65535 || a != 65535 {
		t.Fatalf("wrong rendered pixel b=%d a=%d", b, a)
	}
	if _, err := mr.Get(ctx, "owner-b", "session-b", m.ID); !errors.Is(err, rendermanifest.ErrNotFound) {
		t.Fatalf("cross-owner read: %v", err)
	}
	if err := service.VerifyPreparedContext(ctx, "owner-a", "session-a", "session-a", m.ID, m.Output.SHA256); err != nil {
		t.Fatal(err)
	}
	if _, err := sr.Move(ctx, scene.Mutation{OwnerID: "owner-a", SessionID: "session-a", ObjectID: "object-a", ExpectedRevision: 1},
		scene.Rect{X: 1, Width: 2, Height: 2}, scene.Anchor{}); err != nil {
		t.Fatal(err)
	}
	if err := service.VerifyPreparedContext(ctx, "owner-a", "session-a", "session-a", m.ID, m.Output.SHA256); !errors.Is(err, rendermanifest.ErrDrift) {
		t.Fatalf("stale proof accepted: %v", err)
	}
	assets.onUpload = func() {
		_, _ = sr.Move(ctx, scene.Mutation{OwnerID: "owner-a", SessionID: "session-a", ObjectID: "object-a", ExpectedRevision: 2},
			scene.Rect{X: 0, Width: 2, Height: 2}, scene.Anchor{})
	}
	if _, err := service.Create(ctx, "owner-a", "session-a", frame); !errors.Is(err, rendermanifest.ErrMismatch) {
		t.Fatalf("concurrent revision drift accepted: %v", err)
	}
}
