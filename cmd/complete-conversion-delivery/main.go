// complete-conversion-delivery is a trusted operator tool for one exact
// already delivered conversion pair (#330 third follow-ups). It never contacts
// a Provider. Dry-run is the default; --apply writes only the local delivery
// completion prepared by the kernel.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/modeldelivery"
	"github.com/LeXwDeX/Plinth/internal/providers"
)

type arguments struct {
	database, storageRoot, owner, failedTask, deliveryTask, catalogPath string
	apply                                                               bool
}

func parseArguments(args []string, stderr io.Writer) (arguments, error) {
	var a arguments
	flags := flag.NewFlagSet("complete-conversion-delivery", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&a.database, "database", "", "existing Plinth database")
	flags.StringVar(&a.storageRoot, "storage-root", "", "existing asset store (read for verification; written only with --apply)")
	flags.StringVar(&a.owner, "owner", "", "owner ID of both Tasks")
	flags.StringVar(&a.failedTask, "failed-task", "", "exact failed conversion Task ID")
	flags.StringVar(&a.deliveryTask, "delivery-task", "", "exact delivered Task ID of the same relation")
	flags.StringVar(&a.catalogPath, "catalog", "", "optional exact deployed Product Schema; defaults to embedded catalog")
	flags.BoolVar(&a.apply, "apply", false, "commit the validated local delivery completion; without it, dry-run only")
	if err := flags.Parse(args); err != nil {
		return a, err
	}
	if flags.NArg() != 0 || a.database == "" || a.storageRoot == "" || a.owner == "" || a.failedTask == "" || a.deliveryTask == "" || a.failedTask == a.deliveryTask {
		return a, errors.New("database, storage-root, owner and two distinct exact Task IDs are required")
	}
	return a, nil
}

func loadCatalog(path string) (*catalog.Catalog, error) {
	if path == "" {
		return catalog.LoadDefault()
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return catalog.Load(file)
}

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "conversion delivery completion failed:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	a, err := parseArguments(args, stderr)
	if err != nil {
		return err
	}
	if info, err := os.Lstat(a.database); err != nil || !info.Mode().IsRegular() {
		return errors.New("database must be an existing regular file")
	}
	if info, err := os.Lstat(a.storageRoot); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("storage-root must be an existing non-symlink directory")
	}
	product, err := loadCatalog(a.catalogPath)
	if err != nil {
		return err
	}
	var repository *sqlite.Repository
	var store generation.AssetStore
	if a.apply {
		repository, err = sqlite.OpenExisting(a.database)
	} else {
		repository, err = sqlite.OpenReadOnly(a.database)
	}
	if err != nil {
		return err
	}
	defer repository.Close()
	if a.apply {
		writable, openErr := local.New(a.storageRoot)
		if openErr != nil {
			return openErr
		}
		store = writable
		defer writable.Close()
	} else {
		readOnly, openErr := local.OpenReadOnly(a.storageRoot)
		if openErr != nil {
			return openErr
		}
		store = readOnly
		defer readOnly.Close()
	}
	if product.Application().ModelDelivery.Disabled() {
		return errors.New("Product Schema declares no model delivery conversions")
	}
	module, err := generation.New(generation.Dependencies{
		Repository: repository,
		Catalog:    product,
		Providers:  providers.NewRegistry(),
		Assets:     store,
		Conversions: modeldelivery.NewSchemaConversionRule(
			product.Application().ModelDelivery,
		),
	})
	if err != nil {
		return err
	}
	owner := generation.OwnerID(a.owner)
	failed := generation.TaskID(a.failedTask)
	delivery := generation.TaskID(a.deliveryTask)
	var result generation.DeliveryCompletionPreparation
	if a.apply {
		result, err = module.CompleteDeliveryCompletion(ctx, owner, failed, delivery)
	} else {
		result, err = module.PrepareDeliveryCompletion(ctx, owner, failed, delivery)
	}
	if err != nil {
		return err
	}
	updated := a.apply && !result.AlreadyCompleted
	return json.NewEncoder(stdout).Encode(map[string]any{
		"dry_run": !a.apply, "task_updated": updated, "already_completed": result.AlreadyCompleted,
		"kind": result.Kind, "failed_task_id": result.FailedTaskID, "delivery_task_id": result.DeliveryTaskID,
		"delivery_attempt_id": result.DeliveryAttemptID, "original_attempt_id": result.OriginalAttemptID,
		"asset_count": result.AssetCount, "bytes": result.Bytes, "contract_sha256": result.ContractSHA256,
		"provenance": result.Provenance, "provider_requests": 0,
	})
}
