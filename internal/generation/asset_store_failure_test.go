package generation

import "testing"

// A content verdict (stage inspect) is deterministic: the kernel records it
// non-retryable, so a refetch of the same source that repeats it is
// recognised and not offered again. Write and I/O stages stay retryable and
// keep the refetch open.
func TestAssetStoreFailureRetryabilityFollowsStage(t *testing.T) {
	claim := ArtifactClaim{Role: "model", Variant: "glb"}
	inspect := assetStoreFailure(claim, AssetStorageDiagnostic{Stage: "inspect", Reason: "xml_size_limit", Errno: "other"})
	if inspect.Retryable || inspect.Class != FailureIntegrity || inspect.UpstreamCode != "asset_store_rejected" {
		t.Fatalf("inspect failure=%+v", inspect)
	}
	again := assetStoreFailure(claim, AssetStorageDiagnostic{Stage: "inspect", Reason: "xml_size_limit", Errno: "other"})
	if !repeatsSourceFailure(inspect, again) {
		t.Fatal("identical deterministic inspect refusal must count as a repeated source failure")
	}
	other := assetStoreFailure(claim, AssetStorageDiagnostic{Stage: "inspect", Reason: "usdc_size_limit", Errno: "other"})
	if repeatsSourceFailure(inspect, other) {
		t.Fatal("a different inspect reason is not a repeat")
	}
	for _, stage := range []string{"write", "temp_sync", "cas_rename", "unknown"} {
		failure := assetStoreFailure(claim, AssetStorageDiagnostic{Stage: stage, Reason: "io_failure", Errno: "io"})
		if !failure.Retryable {
			t.Fatalf("stage %q must stay retryable", stage)
		}
		if repeatsSourceFailure(failure, failure) {
			t.Fatalf("retryable stage %q must keep the refetch open", stage)
		}
	}
}

// An inspection that could not reach a verdict (execution slot contention,
// cancellation) is not a content verdict: it stays retryable, and its stage and
// reason pass the diagnostic allowlist unchanged.
func TestAssetStoreFailureInspectionUnavailableStaysRetryable(t *testing.T) {
	diagnostic := safeStorageDiagnostic(AssetStorageDiagnostic{Stage: "inspect_unavailable", Reason: "inspection_busy", Errno: "deadline"})
	if diagnostic.Stage != "inspect_unavailable" || diagnostic.Reason != "inspection_busy" {
		t.Fatalf("allowlist dropped the busy diagnostic: %+v", diagnostic)
	}
	failure := assetStoreFailure(ArtifactClaim{Role: "model", Variant: "glb"}, diagnostic)
	if !failure.Retryable {
		t.Fatalf("transient inspection failure must stay retryable: %+v", failure)
	}
	if repeatsSourceFailure(failure, failure) {
		t.Fatal("retryable inspection failure must keep the refetch open")
	}
}
