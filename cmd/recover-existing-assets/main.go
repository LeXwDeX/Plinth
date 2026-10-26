// recover-existing-assets is a trusted operator tool for a bounded, already
// collected archive. It never contacts a Provider or edits historical tasks.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

type arguments struct {
	database, storageRoot, owner, sourceTask, sourceAttempt, archive, archiveSHA, offering, catalogPath string
	apply                                                                                               bool
}

func parseArguments(args []string, stderr io.Writer) (arguments, error) {
	var a arguments
	flags := flag.NewFlagSet("recover-existing-assets", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&a.database, "database", "", "existing Plinth database (read-only unless --apply)")
	flags.StringVar(&a.storageRoot, "storage-root", "", "existing asset store; written only with --apply")
	flags.StringVar(&a.owner, "owner", "", "source owner ID")
	flags.StringVar(&a.sourceTask, "source-task", "", "unchanged failed source Task ID")
	flags.StringVar(&a.sourceAttempt, "source-attempt", "", "exact source Attempt ID")
	flags.StringVar(&a.archive, "package", "", "locally collected existing-artifact ZIP")
	flags.StringVar(&a.archiveSHA, "expected-package-sha256", "", "independently reviewed archive SHA-256")
	flags.StringVar(&a.offering, "offering", "", "Schema-declared local recovery Offering ID")
	flags.StringVar(&a.catalogPath, "catalog", "", "optional exact deployed Product Schema; defaults to embedded catalog")
	flags.BoolVar(&a.apply, "apply", false, "stage verified bytes and insert one immutable receipt; never submit a task")
	if err := flags.Parse(args); err != nil {
		return a, err
	}
	if flags.NArg() != 0 || a.database == "" || a.owner == "" || a.sourceTask == "" || a.sourceAttempt == "" || a.archive == "" || a.offering == "" || !validDigest(a.archiveSHA) || (a.apply && a.storageRoot == "") {
		return a, errors.New("database, owner, exact source task/attempt, package digest and offering are required; apply also requires storage-root")
	}
	return a, nil
}
func validDigest(s string) bool {
	b, e := hex.DecodeString(s)
	return e == nil && len(b) == 32 && s == strings.ToLower(s)
}
func loadCatalog(path string) (*catalog.Catalog, error) {
	if path == "" {
		return catalog.LoadDefault()
	}
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	return catalog.Load(f)
}
func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "recovery collection failed:", err)
		os.Exit(1)
	}
}
func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	a, err := parseArguments(args, stderr)
	if err != nil {
		return err
	}
	c, err := loadCatalog(a.catalogPath)
	if err != nil {
		return err
	}
	offering, ok := c.Get(generation.OfferingID(a.offering))
	if !ok || !offering.Enabled {
		return errors.New("recovery Offering is unavailable")
	}
	source, err := sqlite.ReadRecoverySourceFile(ctx, a.database, generation.OwnerID(a.owner), generation.TaskID(a.sourceTask))
	if err != nil {
		return err
	}
	if string(source.Binding.AttemptID) != a.sourceAttempt {
		return errors.New("source attempt no longer matches")
	}
	temporary, err := os.MkdirTemp("", "art-ai-recovery-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temporary)
	// Copy and hash once into a private file. Every ZIP member is read from these
	// exact pinned bytes even if the operator's original file changes concurrently.
	archivePath := filepath.Join(temporary, "source.zip")
	if err = copyPinnedArchive(ctx, a.archive, archivePath, a.archiveSHA); err != nil {
		return err
	}
	staging, err := local.New(filepath.Join(temporary, "verified"))
	if err != nil {
		return err
	}
	defer staging.Close()
	receipt, err := collectPackage(ctx, archivePath, a.archiveSHA, source, offering, staging)
	if err != nil {
		return err
	}
	if a.apply {
		if err = sqlite.CheckRecoveryReceiptTargetFile(ctx, a.database); err != nil {
			return err
		}
		info, err := os.Lstat(a.storageRoot)
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("apply requires an existing non-symlink asset root")
		}
		destination, err := local.New(a.storageRoot)
		if err != nil {
			return err
		}
		defer destination.Close()
		for i, slot := range receipt.Slots {
			body, err := staging.Open(ctx, slot.Asset())
			if err != nil {
				return err
			}
			var out generation.AssetRequirement
			for _, candidate := range offering.Outputs {
				if candidate.Role == slot.Role && candidate.Variant == slot.Variant {
					out = candidate
					break
				}
			}
			copied, putErr := destination.Put(ctx, stagingWrite(source, i, out, body))
			closeErr := body.Close()
			if putErr != nil {
				return putErr
			}
			if closeErr != nil {
				return closeErr
			}
			if copied.SHA256 != slot.SHA256 || copied.Size != slot.Size || copied.StorageKey != slot.StorageKey || copied.MediaType != slot.MediaType || copied.DeliveryProfile != slot.DeliveryProfile {
				return errors.New("staged asset identity changed")
			}
		}
		// This helper refuses absent or different 0054; deployment owns migrations.
		// It transactionally rechecks the original rows and adds only the receipt.
		if err = sqlite.CreateRecoveryReceiptFile(ctx, a.database, receipt, offering); err != nil {
			return err
		}
	}
	views := make([]generation.RecoverySlotView, 0, len(receipt.Slots))
	for _, s := range receipt.Slots {
		views = append(views, generation.RecoverySlotView{SourceRole: s.SourceRole, SourceVariant: s.SourceVariant, Role: s.Role, Variant: s.Variant, Required: s.Required, Size: s.Size, SHA256: s.SHA256, MediaType: s.MediaType, DeliveryProfile: s.DeliveryProfile})
	}
	return json.NewEncoder(stdout).Encode(map[string]any{"dry_run": !a.apply, "receipt_id": receipt.ID, "receipt_digest": receipt.Digest, "source": receipt.Source, "archive_sha256": receipt.ArchiveSHA256, "manifest_sha256": receipt.ManifestSHA256, "offering_id": receipt.OfferingID, "offering_revision": receipt.OfferingRevision, "slots": views, "task_submitted": false, "historical_task_updated": false, "provider_requests": 0})
}
func copyPinnedArchive(ctx context.Context, source, target, expected string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > 2<<30 {
		return errors.New("package must be a bounded regular file")
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	h := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(out, h), io.LimitReader(&cancelReader{ctx, in}, (2<<30)+1))
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if n != info.Size() || n > 2<<30 || hex.EncodeToString(h.Sum(nil)) != expected {
		return errors.New("package digest or byte count differs from reviewed input")
	}
	return nil
}

type cancelReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *cancelReader) Read(p []byte) (int, error) {
	if e := r.ctx.Err(); e != nil {
		return 0, e
	}
	return r.reader.Read(p)
}
