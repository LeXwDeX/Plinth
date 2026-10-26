package app

import (
	"context"
	"io"

	inputassets "github.com/LeXwDeX/Plinth/internal/assets/input"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

type captureInputBytes struct{ inputs *inputassets.Service }

func (a captureInputBytes) OpenOwned(ctx context.Context, owner, id string) (io.ReadCloser, error) {
	_, body, err := a.inputs.OpenOwned(ctx, generation.OwnerID(owner), generation.AssetID(id))
	return body, err
}
