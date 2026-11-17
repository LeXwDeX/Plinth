package app

import (
	"time"

	"github.com/LeXwDeX/Plinth/internal/assetlinks"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/transport/httpapi"
)

// taskAssetLinkSigner adapts the task-asset token signer to the external MCP
// listener's interface (#332): the signer speaks Sign/Verify, the listener
// names the task-asset operations it needs.
type taskAssetLinkSigner struct{ signer *assetlinks.Signer }

func newTaskAssetLinkSigner(signer *assetlinks.Signer) httpapi.TaskAssetLinkSigner {
	return taskAssetLinkSigner{signer: signer}
}

func (s taskAssetLinkSigner) SignTaskAsset(
	taskID generation.TaskID,
	assetID generation.AssetID,
	ownerID generation.OwnerID,
	ttl time.Duration,
) (string, time.Time, error) {
	return s.signer.Sign(taskID, assetID, ownerID, ttl)
}

func (s taskAssetLinkSigner) OpenTaskAsset(token string) (generation.TaskID, generation.AssetID, generation.OwnerID, error) {
	payload, err := s.signer.Verify(token)
	if err != nil {
		return "", "", "", err
	}
	return payload.TaskID, payload.AssetID, payload.OwnerID, nil
}
