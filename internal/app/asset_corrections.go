package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/assetcorrection"
	"github.com/LeXwDeX/Plinth/internal/assets/local"
)

// assetCorrectionTimeout bounds the start-up correction; assets it does not
// reach are retried on the next start.
const assetCorrectionTimeout = 10 * time.Minute

// correctStoredAssetLabels runs the one-off asset label corrections before
// any worker or HTTP handler starts. They are idempotent and recorded per
// asset; a failure is logged and never blocks start-up.
func correctStoredAssetLabels(repository *sqlite.Repository, store *local.Store, logger *slog.Logger) {
	ctx, cancel := context.WithTimeout(context.Background(), assetCorrectionTimeout)
	defer cancel()
	report, err := assetcorrection.CorrectTripoNativeOBJ(ctx, repository, store, local.ErrMediaTypeRejected, time.Now, logger)
	if err != nil {
		logger.Error("Tripo OBJ 资产标签修正未完成，下次启动继续", "error", err,
			"corrected", report.Corrected, "kept", report.Kept, "deferred", report.Deferred)
		return
	}
	if report.Candidates > 0 {
		logger.Info("Tripo OBJ 资产标签修正完成", "correction", assetcorrection.TripoNativeOBJArchive,
			"candidates", report.Candidates, "corrected", report.Corrected, "kept", report.Kept,
			"deferred", report.Deferred, "ignored", report.Ignored)
	}
}
