package local

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

var ErrStorageInUse = errors.New("storage is in use; stop all writers before executing garbage collection")

func storageLock(root string, exclusive bool) (*os.File, error) {
	file, err := os.OpenFile(filepath.Join(root, ".maintenance.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	operation := unix.LOCK_SH
	if exclusive {
		operation = unix.LOCK_EX
	}
	if err = unix.Flock(int(file.Fd()), operation|unix.LOCK_NB); err != nil {
		file.Close()
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, ErrStorageInUse
		}
		return nil, err
	}
	return file, nil
}

// Close releases the maintenance lease after all writes and registrations stop.
func (s *Store) Close() error {
	s.closeOnce.Do(func() {
		if s.lock != nil {
			s.closeErr = s.lock.Close()
		}
	})
	return s.closeErr
}

type BlobCandidate struct {
	StorageKey string    `json:"storage_key"`
	Size       int64     `json:"size_bytes"`
	ModifiedAt time.Time `json:"modified_at"`
	Deleted    bool      `json:"deleted"`
}
type MaintenanceReport struct {
	DryRun                  bool      `json:"dry_run"`
	AsOf                    time.Time `json:"as_of"`
	MinimumAgeSeconds       int64     `json:"minimum_age_seconds"`
	ReferenceBasis          string    `json:"reference_basis"`
	ReferenceCount          int       `json:"reference_count"`
	BlobCount               int       `json:"blob_count"`
	BlobBytes               int64     `json:"blob_bytes"`
	RecentUnreferencedCount int       `json:"recent_unreferenced_count"`
	OrphanCount             int       `json:"orphan_count"`
	OrphanBytes             int64     `json:"orphan_bytes"`
	DeletedCount            int       `json:"deleted_count"`
	DeletedBytes            int64     `json:"deleted_bytes"`
	CapacityBytes           uint64    `json:"capacity_bytes"`
	AvailableBytes          uint64    `json:"available_bytes"`
	UsedPercent             float64   `json:"used_percent"`
	// Asset previews (#290, #291): downscaled copies cached by the original's SHA-256 under
	// PreviewSubdirectory. A preview is an orphan once no referenced blob has its digest, or when it was
	// made for another preview size than the Schema's current one. Its candidates are in Candidates too.
	PreviewSubdirectory            string          `json:"preview_subdirectory,omitempty"`
	PreviewCount                   int             `json:"preview_count"`
	PreviewBytes                   int64           `json:"preview_bytes"`
	PreviewRecentUnreferencedCount int             `json:"preview_recent_unreferenced_count"`
	PreviewOrphanCount             int             `json:"preview_orphan_count"`
	PreviewOrphanBytes             int64           `json:"preview_orphan_bytes"`
	Candidates                     []BlobCandidate `json:"candidates"`
}

type MaintenanceOptions struct {
	Root       string
	Execute    bool
	MinimumAge time.Duration
	Now        time.Time
	// References must read a fresh, complete database snapshot. It is called only
	// after execution has excluded every store using this content root.
	References     func(context.Context) (map[string]struct{}, error)
	ReferenceBasis string
	// Previews names the asset preview cache under Root (#291); nil leaves it alone.
	Previews *PreviewMaintenance
}

// PreviewMaintenance is the asset preview cache the runtime keeps under the storage root
// (application.runtime.asset_delivery.asset_preview): its folder name and the preview size now in use.
type PreviewMaintenance struct {
	Subdirectory string
	// MaximumDimension is the current preview size; previews of any other size are no longer served.
	MaximumDimension int
}

func Maintain(ctx context.Context, options MaintenanceOptions) (MaintenanceReport, error) {
	report := MaintenanceReport{DryRun: !options.Execute, AsOf: options.Now.UTC(), MinimumAgeSeconds: int64(options.MinimumAge / time.Second), ReferenceBasis: options.ReferenceBasis, Candidates: []BlobCandidate{}}
	if options.MinimumAge <= 0 || options.References == nil || options.Now.IsZero() {
		return report, errors.New("invalid storage maintenance policy")
	}
	if options.Previews != nil && !validPreviewSubdirectory(options.Previews.Subdirectory) {
		return report, errors.New("invalid asset preview cache folder")
	}
	root, err := filepath.Abs(options.Root)
	if err != nil {
		return report, err
	}
	info, err := os.Stat(root)
	if err != nil {
		return report, err
	}
	if !info.IsDir() {
		return report, errors.New("storage root is not a directory")
	}
	// Shared lock makes dry-run safe alongside the runtime; exclusive execution
	// prevents both old-blob reuse and delayed database registration races.
	lock, err := storageLock(root, options.Execute)
	if err != nil {
		return report, err
	}
	defer lock.Close()
	references, err := options.References(ctx)
	if err != nil {
		return report, fmt.Errorf("read blob references: %w", err)
	}
	report.ReferenceCount = len(references)
	var stat unix.Statfs_t
	if err := unix.Statfs(root, &stat); err != nil {
		return report, err
	}
	report.CapacityBytes = stat.Blocks * uint64(stat.Bsize)
	report.AvailableBytes = stat.Bavail * uint64(stat.Bsize)
	if report.CapacityBytes > 0 {
		report.UsedPercent = 100 * (1 - float64(report.AvailableBytes)/float64(report.CapacityBytes))
	}
	for _, directory := range []string{"blobs", "tmp"} {
		err = filepath.WalkDir(filepath.Join(root, directory), func(path string, entry fs.DirEntry, walkErr error) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return nil
			}
			if !entry.Type().IsRegular() {
				return nil
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			key := filepath.ToSlash(relative)
			report.BlobCount++
			report.BlobBytes += info.Size()
			if _, referenced := references[key]; referenced {
				return nil
			}
			if info.ModTime().After(options.Now.Add(-options.MinimumAge)) {
				report.RecentUnreferencedCount++
				return nil
			}
			// Only files emitted by the CAS writer are eligible. Unknown files remain.
			if directory == "blobs" && !validBlobKey(key) {
				return nil
			}
			// tmp/provider-* are the Provider Adapters' private upload copies (#321 A).
			if directory == "tmp" && !strings.HasPrefix(entry.Name(), "asset-") && !strings.HasPrefix(entry.Name(), "provider-") {
				return nil
			}
			report.Candidates = append(report.Candidates, BlobCandidate{StorageKey: key, Size: info.Size(), ModifiedAt: info.ModTime().UTC()})
			report.OrphanCount++
			report.OrphanBytes += info.Size()
			return nil
		})
		if err != nil {
			return report, err
		}
	}
	if options.Previews != nil {
		if err := scanPreviews(ctx, root, *options.Previews, references, options, &report); err != nil {
			return report, err
		}
	}
	sort.Slice(report.Candidates, func(i, j int) bool { return report.Candidates[i].StorageKey < report.Candidates[j].StorageKey })
	// Inventory must finish successfully before the first deletion.
	if options.Execute {
		for index := range report.Candidates {
			if err := ctx.Err(); err != nil {
				return report, err
			}
			item := &report.Candidates[index]
			if err := os.Remove(filepath.Join(root, filepath.FromSlash(item.StorageKey))); err != nil {
				return report, err
			}
			item.Deleted = true
			report.DeletedCount++
			report.DeletedBytes += item.Size
		}
	}
	return report, nil
}

func validPreviewSubdirectory(name string) bool {
	return name != "" && name != "." && name != ".." && name != "blobs" && name != "tmp" &&
		!strings.ContainsAny(name, `/\`) && filepath.Clean(name) == name
}

// previewFile matches what assetdelivery.PreviewStore writes: <sha256>-<size>.png, and the temporary file of
// an interrupted write, <sha256>-<size>.png.tmp-<random>.
var previewFile = regexp.MustCompile(`^([0-9a-f]{64})-([1-9][0-9]*)\.png(\.tmp-[A-Za-z0-9]+)?$`)

// scanPreviews adds the asset previews nothing needs any more to the report's candidates (#291). A preview is
// keyed by its original's SHA-256, which also names the original's CAS blob, so the reference snapshot that
// decides the blobs decides the previews: one is kept while a referenced blob has its digest and it has the
// current size. Files the preview store does not write are left alone; a missing folder is fine.
func scanPreviews(ctx context.Context, root string, previews PreviewMaintenance, references map[string]struct{}, options MaintenanceOptions, report *MaintenanceReport) error {
	report.PreviewSubdirectory = previews.Subdirectory
	digests := make(map[string]struct{}, len(references))
	for key := range references {
		if validBlobKey(key) {
			digests[strings.SplitN(strings.Split(key, "/")[2], ".", 2)[0]] = struct{}{}
		}
	}
	folder := filepath.Join(root, previews.Subdirectory)
	info, err := os.Lstat(folder)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("asset preview cache is not a directory")
	}
	return filepath.WalkDir(folder, func(path string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !entry.Type().IsRegular() {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		key := filepath.ToSlash(relative)
		parts := strings.Split(key, "/")
		match := previewFile.FindStringSubmatch(entry.Name())
		if len(parts) != 3 || match == nil || parts[1] != match[1][:2] {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		report.PreviewCount++
		report.PreviewBytes += info.Size()
		_, referenced := digests[match[1]]
		current := previews.MaximumDimension <= 0 || match[2] == strconv.Itoa(previews.MaximumDimension)
		if referenced && current && match[3] == "" {
			return nil
		}
		if info.ModTime().After(options.Now.Add(-options.MinimumAge)) {
			report.PreviewRecentUnreferencedCount++
			return nil
		}
		report.Candidates = append(report.Candidates, BlobCandidate{StorageKey: key, Size: info.Size(), ModifiedAt: info.ModTime().UTC()})
		report.PreviewOrphanCount++
		report.PreviewOrphanBytes += info.Size()
		return nil
	})
}

func validBlobKey(key string) bool {
	parts := strings.Split(key, "/")
	if len(parts) != 3 || parts[0] != "blobs" || len(parts[1]) != 2 {
		return false
	}
	digest := strings.SplitN(parts[2], ".", 2)[0]
	if len(digest) != 64 || parts[1] != digest[:2] {
		return false
	}
	for _, character := range digest {
		if !(character >= '0' && character <= '9' || character >= 'a' && character <= 'f') {
			return false
		}
	}
	return true
}
