package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/catalog"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(ctx context.Context, args []string, output io.Writer) error {
	flags := flag.NewFlagSet("art-ai-storage", flag.ContinueOnError)
	root := flags.String("storage", "", "existing CAS storage root")
	database := flags.String("database", "", "existing database belonging to this storage root")
	catalogPath := flags.String("catalog", "", "optional Product Schema file")
	execute := flags.Bool("execute", false, "delete eligible orphans; requires all storage writers stopped")
	check := flags.Bool("check", false, "read-only CAS integrity scan; requires all storage writers stopped")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *root == "" || *database == "" || flags.NArg() != 0 {
		return errors.New("explicit --storage and --database are required")
	}
	if *execute && *check {
		return errors.New("--check and --execute cannot be combined")
	}
	if *check {
		report, scanErr := local.ScanIntegrity(ctx, *root, func(ctx context.Context) ([]local.AssetReference, error) {
			records, err := sqlite.BlobReferenceRecords(ctx, *database)
			if err != nil {
				return nil, err
			}
			references := make([]local.AssetReference, 0, len(records))
			for _, record := range records {
				references = append(references, local.AssetReference{
					StorageKey: record.StorageKey, Size: record.Size, SHA256: record.SHA256,
					SourceKind: record.SourceKind, SourceID: record.SourceID,
				})
			}
			return references, nil
		}, time.Now().UTC().Format(time.RFC3339Nano))
		if err := json.NewEncoder(output).Encode(report); err != nil {
			return errors.Join(scanErr, err)
		}
		return scanErr
	}
	taskCatalog, err := catalog.LoadDefault()
	if *catalogPath != "" {
		var data []byte
		data, err = os.ReadFile(*catalogPath)
		if err == nil {
			taskCatalog, err = catalog.Load(bytes.NewReader(data))
		}
	}
	if err != nil {
		return err
	}
	policy := taskCatalog.Application().Runtime.StorageMaintenance
	if policy.MinimumAgeSeconds <= 0 {
		return errors.New("storage maintenance minimum age must be configured")
	}
	// #291: the asset previews the runtime caches under the same root go with the blobs they were made from.
	preview := taskCatalog.Application().Runtime.AssetDelivery.AssetPreview
	var previews *local.PreviewMaintenance
	if preview.CacheSubdirectory != "" {
		previews = &local.PreviewMaintenance{Subdirectory: preview.CacheSubdirectory, MaximumDimension: preview.MaximumDimension}
	}
	report, maintenanceErr := local.Maintain(ctx, local.MaintenanceOptions{
		Root: *root, Execute: *execute, MinimumAge: time.Duration(policy.MinimumAgeSeconds) * time.Second, Now: time.Now(),
		ReferenceBasis: "generation_assets.storage_key + generation_input_assets.storage_key + generation_artifact_claims.stored_asset_json.storage_key; all tasks including tombstones; asset previews kept while a referenced blob has their digest",
		References:     func(ctx context.Context) (map[string]struct{}, error) { return sqlite.BlobReferences(ctx, *database) },
		Previews:       previews,
	})
	if err := json.NewEncoder(output).Encode(report); err != nil {
		return errors.Join(maintenanceErr, err)
	}
	return maintenanceErr
}
