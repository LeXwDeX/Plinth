package input

import (
	"context"
	"errors"
	"io"

	"github.com/LeXwDeX/Plinth/internal/assets/legacyinput"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

type Compatible struct {
	native *Service
	legacy *legacyinput.Module
}

func NewCompatible(native *Service, legacy *legacyinput.Module) (*Compatible, error) {
	if native == nil {
		return nil, errors.New("compatible input assets: native module 不能为空")
	}
	if legacy == nil {
		return nil, errors.New("compatible input assets: legacy module 不能为空")
	}
	return &Compatible{native: native, legacy: legacy}, nil
}

func (m *Compatible) UploadAsset(
	ctx context.Context,
	owner generation.OwnerID,
	upload generation.InputAssetUpload,
) (generation.InputAsset, error) {
	return m.native.UploadAsset(ctx, owner, upload)
}

func (m *Compatible) OpenAuthorized(
	ctx context.Context,
	owner generation.OwnerID,
	id generation.AssetID,
	readAll bool,
) (generation.InputAsset, io.ReadCloser, error) {
	asset, body, err := m.native.OpenAuthorized(ctx, owner, id, readAll)
	if err == nil {
		return asset, body, nil
	}
	var domainErr *generation.Error
	if !errors.As(err, &domainErr) || domainErr.Code != generation.ErrNotFound {
		return generation.InputAsset{}, nil, err
	}
	return m.legacy.OpenAuthorized(ctx, owner, id, readAll)
}
