package input_test

import (
	"context"
	"encoding/json"
	"image/png"
	"path/filepath"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite/sqlitetest"
	inputassets "github.com/LeXwDeX/Plinth/internal/assets/input"
	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// droppingRepository is the real repository; a lookup that misses the
// re-encoded copy calls drop, which is when the long re-encode starts.
type droppingRepository struct {
	*sqlite.Repository
	drop func()
}

func (r *droppingRepository) GetInputAsset(ctx context.Context, id generation.AssetID) (generation.InputAsset, error) {
	asset, err := r.Repository.GetInputAsset(ctx, id)
	if err != nil && r.drop != nil {
		r.drop()
	}
	return asset, err
}

// #629: a large picture takes long to re-encode. When the request that asked
// for it is canceled meanwhile (the client stopped waiting), the copy is still
// saved, so the next request reuses it at once instead of re-encoding again,
// and the request's own cancellation does not throw the work away.
func TestFitKeepsTheCopyWhenTheRequestIsCanceledWhileEncoding(t *testing.T) {
	root := t.TempDir()
	repository, err := sqlitetest.Open(filepath.Join(root, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	store, err := local.New(filepath.Join(root, "storage"))
	if err != nil {
		t.Fatal(err)
	}
	request, cancel := context.WithCancel(context.Background())
	defer cancel()
	dropping := &droppingRepository{Repository: repository}
	service, err := inputassets.NewWithConfig(dropping, store, inputassets.Config{Messages: fitMessages, Fitting: fitPolicy})
	if err != nil {
		t.Fatal(err)
	}
	h := fitHarness{t: t, repository: repository, service: service}
	source := h.upload(picture(t, 320, 200, 255, png.BestCompression))
	offering := referenceOffering(generation.Parameter{MaxBytes: source.Size / 2})

	dropping.drop = cancel
	fitted, err := service.Fit(request, "owner-a", offering, referenceInput(source.ID))
	if err != nil {
		t.Fatalf("fit under a canceled request: %v", err)
	}
	if request.Err() == nil {
		t.Fatal("the request was never canceled; the test did not exercise the drop")
	}
	var values struct {
		References []generation.AssetID `json:"reference_assets"`
	}
	if err := json.Unmarshal(fitted, &values); err != nil {
		t.Fatal(err)
	}
	ids := values.References
	if len(ids) != 1 || ids[0] == source.ID {
		t.Fatalf("fitted input = %s", fitted)
	}
	derivation, found, err := repository.InputAssetDerivation(context.Background(), "owner-a", ids[0])
	if err != nil || !found || derivation.SourceAssetID != source.ID {
		t.Fatalf("copy not kept: %+v found=%v err=%v", derivation, found, err)
	}
	kept, err := repository.GetInputAsset(context.Background(), ids[0])
	if err != nil {
		t.Fatal(err)
	}
	body, err := h.service.Open(context.Background(), kept)
	if err != nil {
		t.Fatalf("kept copy has no bytes: %v", err)
	}
	_ = body.Close()
}
