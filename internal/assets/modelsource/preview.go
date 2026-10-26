package modelsource

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"io"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/assets/modelinput"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// OpenPreview returns a private, validated copy of an owned GLB for a preview
// response. It intentionally resolves only native uploads; Task projections
// are not accepted as upload previews.
func (s *Service) OpenPreview(ctx context.Context, owner generation.OwnerID, id generation.AssetID) (result generation.InputAsset, preview io.ReadCloser, retErr error) {
	asset, body, err := s.native.OpenOwnedUpload(ctx, owner, id)
	if err != nil {
		if body != nil {
			_ = body.Close()
		}
		if schema.IsCode(err, generation.ErrNotFound) || schema.IsCode(err, generation.ErrForbidden) || errors.Is(err, generation.RepositoryNotFound) {
			err = domain(generation.ErrNotFound, err)
		}
		return generation.InputAsset{}, nil, err
	}
	if body == nil {
		return generation.InputAsset{}, nil, domain(generation.ErrUnavailable, nil)
	}

	// Close on every path after a successful open. A close failure is also a
	// failed preview: the backing store's integrity/cleanup state is uncertain.
	defer func() {
		if closeErr := body.Close(); closeErr != nil {
			result = generation.InputAsset{}
			preview = nil
			retErr = domain(generation.ErrUnavailable, closeErr)
		}
	}()
	if owner == "" || asset.OwnerID != owner || id == "" || asset.ID != id {
		return generation.InputAsset{}, nil, domain(generation.ErrNotFound, nil)
	}
	if !validPreviewMetadata(asset) {
		return generation.InputAsset{}, nil, domain(generation.ErrConflict, nil)
	}
	if asset.Size > s.policy.Limits.MaxBytes {
		return generation.InputAsset{}, nil, domain(generation.ErrModelInputLimit, nil)
	}

	raw, readErr := io.ReadAll(io.LimitReader(contextReader{ctx: ctx, reader: body}, s.policy.Limits.MaxBytes+1))
	if readErr != nil {
		return generation.InputAsset{}, nil, domain(generation.ErrUnavailable, readErr)
	}
	if ctx.Err() != nil {
		return generation.InputAsset{}, nil, domain(generation.ErrUnavailable, ctx.Err())
	}
	if int64(len(raw)) > s.policy.Limits.MaxBytes {
		return generation.InputAsset{}, nil, domain(generation.ErrModelInputLimit, nil)
	}
	profile, err := modelinput.ValidateGLBBytes(ctx, raw, s.policy)
	if err != nil {
		return generation.InputAsset{}, nil, validationError(err)
	}
	if !strings.EqualFold(profile.SHA256, asset.SHA256) || profile.Size != asset.Size {
		return generation.InputAsset{}, nil, domain(generation.ErrConflict, nil)
	}
	if ctx.Err() != nil {
		return generation.InputAsset{}, nil, domain(generation.ErrUnavailable, ctx.Err())
	}

	return asset, io.NopCloser(bytes.NewReader(raw)), nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

func validPreviewMetadata(asset generation.InputAsset) bool {
	if asset.Size <= 0 || len(asset.SHA256) != 64 {
		return false
	}
	_, err := hex.DecodeString(asset.SHA256)
	return err == nil
}
