package input

import (
	"context"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"io"
)

// OpenOwnedUpload is deliberately native-only: a Task output must retain its
// task_asset identity and cannot exploit LoadInputAssets' output bridge here.
func (s *Service) OpenOwnedUpload(ctx context.Context, owner generation.OwnerID, id generation.AssetID) (generation.InputAsset, io.ReadCloser, error) {
	return s.OpenAuthorized(ctx, owner, id, false)
}
