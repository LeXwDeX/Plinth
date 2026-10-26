package generation

import (
	"context"
	"errors"
)

func (m *Module) checkAssetFidelity(ctx context.Context, asset Asset, requirement AssetRequirement) error {
	if requirement.Fidelity == "" {
		return nil
	}
	checker, ok := m.assets.(AssetFidelityChecker)
	if !ok {
		return &fidelityCheckerMissing{}
	}
	return checker.CheckFidelity(ctx, asset, requirement.Fidelity)
}

type fidelityCheckerMissing struct{}

func (*fidelityCheckerMissing) Error() string { return "asset fidelity checker unavailable" }
func (*fidelityCheckerMissing) StorageDiagnostic() AssetStorageDiagnostic {
	return AssetStorageDiagnostic{Stage: "inspect", Reason: "fidelity_unsupported", Errno: "other"}
}

func fidelityFailure(err error, claim ArtifactClaim) *Failure {
	diagnostic := storageDiagnosticOf(err)
	// No raw checker error or user-facing text crosses this boundary. Public
	// failure text is the existing Schema failure.integrity_failure message.
	return &Failure{
		Class: FailureIntegrity, Retryable: false,
		UpstreamCode:    "asset_fidelity_rejected",
		UpstreamExcerpt: assetStoreForensics(claim, diagnostic),
	}
}

func isFidelityRejection(err error) bool {
	var diagnosed DiagnosedAssetStorageError
	if !errors.As(err, &diagnosed) {
		return false
	}
	switch storageDiagnosticOf(err).Reason {
	case "fidelity_unsupported", "fidelity_invalid_structure", "fidelity_skeleton_missing", "fidelity_animation_missing":
		return true
	}
	return false
}
