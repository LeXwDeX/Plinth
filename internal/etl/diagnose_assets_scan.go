package etl

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/adapters/legacygeneration"
	"github.com/LeXwDeX/Plinth/internal/administration"
	"github.com/LeXwDeX/Plinth/internal/assets/local"
)

type diagnosticObserved struct {
	info   os.FileInfo
	digest string
}
type diagnosticScanner struct {
	root     string
	maximum  int64
	observed map[string]diagnosticObserved
	changed  bool
}

func newDiagnosticScanner(root string, maximum int64) *diagnosticScanner {
	return &diagnosticScanner{root: root, maximum: maximum, observed: map[string]diagnosticObserved{}}
}
func diagnosticContained(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
func (s *diagnosticScanner) candidate(path, resolver string) diagnosticCandidate {
	relative, err := filepath.Rel(s.root, path)
	if err != nil {
		relative = path
	}
	return diagnosticCandidate{PathReference: "sha256:" + diagnosticHash([]byte(filepath.ToSlash(relative))), Resolver: resolver}
}

// probe hashes only an opened regular file whose inode agrees with the verified
// in-snapshot target. Nothing from OS errors or raw metadata is serialized.
func (s *diagnosticScanner) probe(path string, metadata bool, asset *administration.GenerationAsset) (string, string, []byte) {
	if !diagnosticContained(s.root, path) {
		return "illegal_path", "", nil
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		if info, e := os.Lstat(path); e == nil && info.Mode()&os.ModeSymlink != 0 {
			return "symlink_unresolved", "", nil
		}
		return "missing_file", "", nil
	}
	if !diagnosticContained(s.root, resolved) {
		return "illegal_path", "", nil
	}
	before, err := os.Lstat(resolved)
	if err != nil || !before.Mode().IsRegular() {
		return "unreadable_file", "", nil
	}
	limit := before.Size()
	if metadata {
		limit = min(s.maximum, int64(64*1024))
		if before.Size() > limit {
			return "unreadable_file", "", nil
		}
	} else if asset != nil && before.Size() > s.maximum {
		return "too_large", "", nil
	}
	// Root.Open prevents a directory/symlink swap from redirecting the read
	// outside the declared snapshot after the lexical checks above.
	root, err := os.OpenRoot(s.root)
	if err != nil {
		return "unreadable_file", "", nil
	}
	defer root.Close()
	relative, err := filepath.Rel(s.root, resolved)
	if err != nil {
		return "illegal_path", "", nil
	}
	body, err := root.Open(relative)
	if err != nil {
		return "unreadable_file", "", nil
	}
	defer body.Close()
	actual, err := body.Stat()
	if err != nil || !os.SameFile(before, actual) || !actual.Mode().IsRegular() {
		s.changed = true
		return "unreadable_file", "", nil
	}
	hash := sha256.New()
	var raw []byte
	if metadata {
		raw, err = io.ReadAll(io.LimitReader(body, limit+1))
		if err == nil {
			_, err = hash.Write(raw)
		}
	} else {
		_, err = io.Copy(hash, io.LimitReader(body, limit+1))
	}
	after, statErr := body.Stat()
	if err != nil || statErr != nil || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		s.changed = true
		return "unreadable_file", "", nil
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	if prior, ok := s.observed[resolved]; ok && (prior.digest != digest || !os.SameFile(prior.info, before)) {
		s.changed = true
	}
	s.observed[resolved] = diagnosticObserved{info: before, digest: digest}
	if asset != nil {
		if _, err := body.Seek(0, io.SeekStart); err != nil {
			return "unreadable_file", "", nil
		}
		if local.ValidateDeliveredFile(body, asset.Role, asset.Variant, asset.MediaType) != nil {
			return "content_rejected", "", nil
		}
	}
	return "materialized", digest, raw
}
func (s *diagnosticScanner) unchanged() bool {
	if s.changed {
		return false
	}
	for path, observed := range s.observed {
		info, digest, err := diagnosticSnapshotFile(path)
		if err != nil || !os.SameFile(info, observed.info) || info.Size() != observed.info.Size() || !info.ModTime().Equal(observed.info.ModTime()) || digest != observed.digest {
			return false
		}
	}
	return true
}
func (s *diagnosticScanner) attempt(entry *diagnosticEntry, path, resolver string, metadata bool, asset *administration.GenerationAsset) (string, string, []byte) {
	outcome, digest, raw := "", "", []byte(nil)
	// Direct input keys are confined to assets, unlike the separate declared-SHA
	// recovery index which may search the existing five storage families.
	if resolver == "input_storage_path" {
		root, rootErr := filepath.EvalSymlinks(filepath.Join(s.root, "assets"))
		target, targetErr := filepath.EvalSymlinks(path)
		if rootErr == nil && targetErr == nil && !diagnosticContained(root, target) {
			outcome = "illegal_path"
		}
	}
	// The generated/token/cache readers Lstat their main file and require a
	// regular file. Following a final symlink here would overstate ETL recovery.
	if !metadata && (resolver == "generated_route" || resolver == "legacy_download_route" || resolver == "cache_path_hash" || resolver == "cache_url_hash") {
		if info, err := os.Lstat(path); err == nil {
			if !info.Mode().IsRegular() {
				outcome = "unreadable_file"
			} else if info.Size() > s.maximum {
				outcome = "too_large"
			}
		}
	}
	if outcome == "" {
		outcome, digest, raw = s.probe(path, metadata, asset)
	}
	candidate := s.candidate(path, resolver)
	candidate.Detail = outcome
	if info, err := os.Lstat(path); err == nil {
		candidate.Exists = info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0
	}
	entry.CandidatePaths = append(entry.CandidatePaths, candidate)
	return outcome, digest, raw
}
func diagnosticReason(outcome string) string {
	switch outcome {
	case "illegal_path", "sha256_mismatch":
		return "H2:" + outcome
	case "symlink_unresolved":
		return "H3:" + outcome
	case "remote_blocked_offline":
		return "H4:remote_recoverability_unknown"
	case "duplicate_conflict", "audit_name_miss":
		return "H5:" + outcome
	case "cache_key_miss", "sidecar_missing_or_invalid":
		return "H6:" + outcome
	default:
		return "H2:" + outcome
	}
}
func settleDiagnostic(entry *diagnosticEntry, resolver, outcome string) {
	entry.Resolver = resolver
	entry.Outcome = outcome
	entry.Reason = diagnosticReason(outcome)
}

type diagnosticInputRow struct {
	id, path, sha string
	size          int64
}

func diagnoseInputs(source legacyQuery, s *diagnosticScanner, doc *diagnosticReport) error {
	rows, err := source.Query(`SELECT CAST(id AS TEXT),COALESCE(storagePath,''),COALESCE(storedSha256,''),COALESCE(fileSize,0) FROM asset_registry ORDER BY id`)
	if err != nil {
		return err
	}
	inputs := []diagnosticInputRow{}
	sizes := map[int64]bool{}
	for rows.Next() {
		var row diagnosticInputRow
		if err := rows.Scan(&row.id, &row.path, &row.sha, &row.size); err != nil {
			rows.Close()
			return err
		}
		inputs = append(inputs, row)
		if validSHA256(strings.ToLower(strings.TrimSpace(row.sha))) && row.size > 0 {
			sizes[row.size] = true
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	doc.LoadedCounts["asset_registry"] = len(inputs)
	if len(inputs) != doc.SourceCounts["asset_registry"] {
		return errors.New("input count mismatch")
	}
	recovery := map[string]string{}
	// Same five source families as existing SHA recovery; never traverse a link
	// and never read a candidate outside the declared storage root.
	for _, directory := range []string{"assets", "cache", "audit", "generated", "task-inputs"} {
		root := filepath.Join(s.root, directory)
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				if path == root && errors.Is(walkErr, os.ErrNotExist) {
					return nil
				}
				return walkErr
			}
			if entry.IsDir() {
				return nil
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return nil
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if directory == "audit" && info.Mode().IsRegular() {
				record := newDiagnosticEntry("audit_file", filepath.ToSlash(strings.TrimPrefix(path, s.root+string(filepath.Separator))), filepath.Base(path))
				outcome, _, _ := s.attempt(&record, path, "audit_filename_recovery", false, nil)
				settleDiagnostic(&record, "audit_filename_recovery", outcome)
				record.Reason = "H5:inventory_only_not_a_recovery_match"
				doc.Entries = append(doc.Entries, record)
			}
			if !info.Mode().IsRegular() || !sizes[info.Size()] {
				return nil
			}
			outcome, digest, _ := s.probe(path, false, nil)
			if outcome == "materialized" {
				if prior := recovery[digest]; prior == "" || path < prior {
					recovery[digest] = path
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	for _, row := range inputs {
		record := newDiagnosticEntry("input_asset", row.id, row.path)
		key := filepath.Clean(filepath.FromSlash(row.path))
		outcome := "illegal_path"
		if row.path != "" && !strings.ContainsRune(row.path, '\x00') && !filepath.IsAbs(row.path) && key != "." && key != ".." && !strings.HasPrefix(key, ".."+string(filepath.Separator)) {
			outcome, digest, _ := s.attempt(&record, filepath.Join(s.root, "assets", key), "input_storage_path", false, nil)
			expected := strings.ToLower(strings.TrimSpace(row.sha))
			if outcome == "materialized" && expected != "" && expected != digest {
				outcome = "sha256_mismatch"
			}
			settleDiagnostic(&record, "input_storage_path", outcome)
		} else {
			settleDiagnostic(&record, "input_storage_path", outcome)
		}
		if record.Outcome != "materialized" {
			expected := strings.ToLower(strings.TrimSpace(row.sha))
			if candidate := recovery[expected]; candidate != "" {
				outcome, digest, _ := s.attempt(&record, candidate, "input_sha256_recovery", false, nil)
				if outcome == "materialized" && digest == expected {
					settleDiagnostic(&record, "input_sha256_recovery", outcome)
				}
			}
		}
		doc.Entries = append(doc.Entries, record)
	}
	return nil
}
func (s *diagnosticScanner) output(record *diagnosticEntry, taskID, source string, reader *legacygeneration.Reader, audit legacyAuditAssetRecovery, asset administration.GenerationAsset) {
	candidates := reader.DiagnosticCandidates(source, filepath.Join(s.root, "generated"))
	primary := "cache_key_miss"
	resolver := "none"
	for _, candidate := range candidates {
		resolver = candidate.Resolver
		if candidate.Invalid {
			settleDiagnostic(record, resolver, "illegal_path")
			return
		}
		outcome, _, _ := s.attempt(record, candidate.Path, resolver, false, nil)
		mediaType, validMetadata := reader.DiagnosticMediaType(candidate, nil)
		if outcome == "materialized" && candidate.MetadataPath != "" {
			metadataOutcome, _, raw := s.attempt(record, candidate.MetadataPath, resolver, true, nil)
			mediaType, validMetadata = reader.DiagnosticMediaType(candidate, raw)
			if metadataOutcome != "materialized" || !validMetadata {
				outcome = "sidecar_missing_or_invalid"
				record.CandidatePaths[len(record.CandidatePaths)-1].Detail = outcome
			}
		}
		if outcome == "materialized" {
			effectiveAsset := asset
			effectiveAsset.MediaType = mediaType
			outcome, _, _ = s.probe(candidate.Path, false, &effectiveAsset)
			if outcome == "content_rejected" {
				settleDiagnostic(record, resolver, outcome)
				return
			}
		}
		if outcome == "materialized" {
			settleDiagnostic(record, resolver, outcome)
			return
		}
		if candidate.MetadataPath == "" {
			if outcome == "too_large" {
				settleDiagnostic(record, resolver, outcome)
				return
			}
			primary = outcome
			break
		}
		if outcome != "missing_file" {
			primary = outcome
		}
	}
	// Audit recovery follows only unavailable routes, never rejected bytes/paths.
	if primary == "illegal_path" || primary == "content_rejected" {
		settleDiagnostic(record, resolver, primary)
		return
	}
	filename, ok := audit.auditFilename(taskID, source)
	if ok {
		paths := audit.byFilename[filename]
		var expected string
		conflict := false
		for _, path := range paths {
			auditAsset := asset
			auditAsset.MediaType = audit.mediaType(filename, asset.MediaType)
			outcome, digest, _ := s.attempt(record, path, "audit_filename_recovery", false, &auditAsset)
			if outcome != "materialized" {
				primary = outcome
				conflict = true
				continue
			}
			if expected == "" {
				expected = digest
			} else if expected != digest {
				primary = "duplicate_conflict"
				conflict = true
			}
		}
		if len(paths) > 0 && !conflict {
			settleDiagnostic(record, "audit_filename_recovery", "materialized")
			return
		}
		if conflict {
			settleDiagnostic(record, "audit_filename_recovery", primary)
			return
		}
		missing := s.candidate(filepath.Join(s.root, "audit", filename), "audit_filename_recovery")
		missing.Detail = "audit_name_miss"
		record.CandidatePaths = append(record.CandidatePaths, missing)
	}
	if primary == "cache_key_miss" {
		parsed, err := url.Parse(source)
		if err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") {
			settleDiagnostic(record, "remote_offline", "remote_blocked_offline")
			return
		}
		settleDiagnostic(record, "none", "unresolved_route")
		return
	}
	settleDiagnostic(record, resolver, primary)
}
