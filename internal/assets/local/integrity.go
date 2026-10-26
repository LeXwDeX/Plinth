package local

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/sys/unix"
)

// AssetReference identifies a persisted logical reference to a CAS blob. It is
// intentionally read-only metadata; an integrity scan never changes task or
// asset state.
type AssetReference struct {
	StorageKey string `json:"storage_key"`
	Size       int64  `json:"size_bytes"`
	SHA256     string `json:"sha256,omitempty"`
	SourceKind string `json:"source_kind"`
	SourceID   string `json:"source_id"`
}

type IntegrityIssue struct {
	StorageKey string           `json:"storage_key"`
	Status     string           `json:"status"` // missing, corrupt, unknown_key, metadata_conflict
	ActualSize int64            `json:"actual_size_bytes,omitempty"`
	ActualSHA  string           `json:"actual_sha256,omitempty"`
	Error      string           `json:"error,omitempty"`
	References []AssetReference `json:"references"`
}

type IntegrityReport struct {
	ReadOnly       bool             `json:"read_only"`
	ScannedAt      string           `json:"scanned_at"`
	ReferenceCount int              `json:"reference_count"`
	BlobCount      int              `json:"blob_count"`
	IssueCount     int              `json:"issue_count"`
	Issues         []IntegrityIssue `json:"issues"`
}

// ScanIntegrity checks all supplied persisted references under the exclusive
// storage maintenance lock. The lock excludes cooperating Store writers while
// metadata and bytes are compared, preventing a scan/write TOCTOU. It does not
// mutate blobs or generation state; operators must stop non-cooperating writers.
func ScanIntegrity(ctx context.Context, rootPath string, readReferences func(context.Context) ([]AssetReference, error), scannedAt string) (IntegrityReport, error) {
	report := IntegrityReport{ReadOnly: true, ScannedAt: scannedAt, Issues: []IntegrityIssue{}}
	if readReferences == nil {
		return report, errors.New("integrity scan requires a reference snapshot")
	}
	rootPath, err := filepath.Abs(rootPath)
	if err != nil {
		return report, err
	}
	info, err := os.Stat(rootPath)
	if err != nil {
		return report, err
	}
	if !info.IsDir() {
		return report, errors.New("storage root is not a directory")
	}
	lock, err := storageLock(rootPath, true)
	if err != nil {
		return report, err
	}
	defer lock.Close()
	references, err := readReferences(ctx)
	if err != nil {
		return report, fmt.Errorf("read asset reference snapshot: %w", err)
	}
	report.ReferenceCount = len(references)
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return report, err
	}
	defer root.Close()

	byKey := make(map[string][]AssetReference)
	for _, reference := range references {
		byKey[reference.StorageKey] = append(byKey[reference.StorageKey], reference)
	}
	keys := make([]string, 0, len(byKey))
	for key := range byKey {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		report.BlobCount++
		refs := byKey[key]
		issue := IntegrityIssue{StorageKey: key, References: refs}
		digest, validKey := blobKeyDigest(key)
		if !validKey {
			issue.Status = "unknown_key"
			issue.Error = "reference does not use a supported CAS key"
			report.Issues = append(report.Issues, issue)
			continue
		}
		wantSize, wantSHA, conflict := expectedIntegrity(refs, digest)
		if conflict {
			issue.Status = "metadata_conflict"
			issue.Error = "references for this CAS key disagree on size or digest"
			report.Issues = append(report.Issues, issue)
			continue
		}
		file, err := root.OpenFile(key, os.O_RDONLY|unix.O_NOFOLLOW, 0)
		if errors.Is(err, os.ErrNotExist) {
			issue.Status = "missing"
			report.Issues = append(report.Issues, issue)
			continue
		}
		if err != nil {
			issue.Status = "corrupt"
			issue.Error = err.Error()
			report.Issues = append(report.Issues, issue)
			continue
		}
		stat, statErr := file.Stat()
		if statErr != nil || !stat.Mode().IsRegular() {
			_ = file.Close()
			issue.Status = "corrupt"
			if statErr != nil {
				issue.Error = statErr.Error()
			} else {
				issue.Error = "CAS target is not a regular file"
			}
			report.Issues = append(report.Issues, issue)
			continue
		}
		hasher := sha256.New()
		size, copyErr := io.Copy(hasher, file)
		closeErr := file.Close()
		actualSHA := hex.EncodeToString(hasher.Sum(nil))
		issue.ActualSize = size
		issue.ActualSHA = actualSHA
		if copyErr != nil || closeErr != nil || size != wantSize || actualSHA != wantSHA {
			issue.Status = "corrupt"
			switch {
			case copyErr != nil:
				issue.Error = copyErr.Error()
			case closeErr != nil:
				issue.Error = closeErr.Error()
			case size != wantSize:
				issue.Error = fmt.Sprintf("size mismatch: want %d, got %d", wantSize, size)
			default:
				issue.Error = "sha256 mismatch"
			}
			report.Issues = append(report.Issues, issue)
		}
	}
	report.IssueCount = len(report.Issues)
	return report, nil
}

func expectedIntegrity(references []AssetReference, keyDigest string) (int64, string, bool) {
	var size int64
	sha := keyDigest
	initialized := false
	for _, reference := range references {
		candidateSHA := reference.SHA256
		if candidateSHA == "" { // historical rows omit SHA; the CAS key remains authoritative.
			candidateSHA = keyDigest
		}
		if reference.Size < 0 || candidateSHA != keyDigest {
			return 0, "", true
		}
		if !initialized {
			size, sha, initialized = reference.Size, candidateSHA, true
			continue
		}
		if size != reference.Size || sha != candidateSHA {
			return 0, "", true
		}
	}
	return size, sha, false
}

func blobKeyDigest(key string) (string, bool) {
	if !validBlobKey(key) {
		return "", false
	}
	parts := strings.Split(key, "/")
	return strings.SplitN(parts[2], ".", 2)[0], true
}
