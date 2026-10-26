package generation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"reflect"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/schema"
)

type RetentionFileStore interface {
	Inspect(context.Context, RetentionFile) (RetentionFile, error)
	PrepareBackup(context.Context, string, string, []RetentionFile) error
	SaveManifest(context.Context, string, RetentionManifest) error
	VerifyBackup(context.Context, string, RetentionManifest, string) error
	Backup(context.Context, []RetentionFile, string) error
	Remove(context.Context, RetentionFile) error
	WriteCleanupAttempt(context.Context, string, RetentionApproval) error
}
type RetentionApprover interface {
	Approve(context.Context, string, string) (RetentionApproval, error)
}
type RetentionIdentity struct {
	Database, Storage                                          string
	DatabaseDevice, DatabaseInode, StorageDevice, StorageInode uint64
}
type RetentionService struct {
	Repository RetentionSession
	Files      RetentionFileStore
	Approver   RetentionApprover
	Policy     schema.TaskRetentionPolicy
	Identity   RetentionIdentity
	Now        func() time.Time
}
type RetentionResult struct {
	ManifestHash      string            `json:"manifest_sha256"`
	DatabaseCommitted bool              `json:"database_committed"`
	Receipt           *RetentionReceipt `json:"receipt,omitempty"`
	Deleted           []string          `json:"deleted"`
	AlreadyAbsent     []string          `json:"already_absent"`
	FailedKey         string            `json:"failed_key,omitempty"`
}

func (s RetentionService) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}
func (s RetentionService) Prepare(ctx context.Context, cutoff time.Time) (RetentionManifest, error) {
	var m RetentionManifest
	if s.Repository == nil || s.Files == nil {
		return m, errors.New("retention requires complete offline ports")
	}
	in, e := s.Repository.Inventory(ctx)
	if e != nil {
		return m, e
	}
	for i := range in.Blobs {
		got, e := s.Files.Inspect(ctx, in.Blobs[i].File)
		if e != nil {
			return m, e
		}
		in.Blobs[i].File = got
	}
	m, e = PlanRetention(in, s.Policy, cutoff, s.now())
	if e != nil {
		return m, e
	}
	m.Database = s.Identity.Database
	m.Storage = s.Identity.Storage
	m.DatabaseDevice = s.Identity.DatabaseDevice
	m.DatabaseInode = s.Identity.DatabaseInode
	m.StorageDevice = s.Identity.StorageDevice
	m.StorageInode = s.Identity.StorageInode
	return m, nil
}

// DecodeRetentionManifest accepts only the canonical bytes emitted by prepare.
// Formatting changes, unknown fields and duplicate JSON keys cannot preserve a
// previous approval, even if a generic JSON decoder would ignore the change.
func DecodeRetentionManifest(encoded []byte, expected string) (RetentionManifest, error) {
	var m RetentionManifest
	d := json.NewDecoder(bytes.NewReader(encoded))
	d.DisallowUnknownFields()
	if e := d.Decode(&m); e != nil {
		return m, e
	}
	canonical, e := json.Marshal(m)
	if e != nil {
		return m, e
	}
	if !bytes.Equal(canonical, encoded) {
		return m, errors.New("retention manifest is not canonical or was modified")
	}
	hash, e := m.Digest()
	if e != nil {
		return m, e
	}
	if len(expected) != 64 || hash != expected || m.Version != RetentionManifestVersion {
		return m, errors.New("retention manifest hash or version mismatch")
	}
	return m, nil
}
func (s RetentionService) validateManifest(ctx context.Context, m RetentionManifest) error {
	if m.Version != RetentionManifestVersion || !reflect.DeepEqual(m.Policy, s.Policy) {
		return ErrRetentionChanged
	}
	cutoff, e := time.Parse(time.RFC3339Nano, m.Cutoff)
	if e != nil {
		return e
	}
	current, e := s.Prepare(ctx, cutoff)
	if e != nil {
		return e
	}
	a, e := current.Digest()
	if e != nil {
		return e
	}
	b, e := m.Digest()
	if e != nil {
		return e
	}
	if a != b {
		return ErrRetentionChanged
	}
	return nil
}
func (s RetentionService) approve(ctx context.Context, hash, purpose string) (RetentionApproval, error) {
	var a RetentionApproval
	if s.Approver == nil {
		return a, errors.New("independent human manifest approval is required")
	}
	a, e := s.Approver.Approve(ctx, hash, purpose)
	if e != nil {
		return a, e
	}
	if a.ManifestHash != hash || a.Purpose != purpose || strings.TrimSpace(a.Actor) == "" || len(a.Nonce) != 32 {
		return a, errors.New("invalid exact manifest approval")
	}
	for _, c := range a.Nonce {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return a, errors.New("invalid approval nonce")
		}
	}
	if _, e = time.Parse(time.RFC3339Nano, a.ApprovedAt); e != nil {
		return a, errors.New("invalid approval timestamp")
	}
	return a, nil
}

func (s RetentionService) Apply(ctx context.Context, m RetentionManifest, backupDirectory string) (RetentionResult, error) {
	hash, e := m.Digest()
	out := RetentionResult{ManifestHash: hash, Deleted: []string{}, AlreadyAbsent: []string{}}
	if e != nil {
		return out, e
	}
	if e = s.validateManifest(ctx, m); e != nil {
		return out, e
	}
	// The immutable receipt is the only authority for an uncertain prior commit.
	if receipt, err := s.Repository.Receipt(ctx, hash); err == nil {
		out.DatabaseCommitted = true
		out.Receipt = &receipt
		return out, errors.New("manifest already committed; use separately approved cleanup, never replay apply")
	} else if !errors.Is(err, ErrRetentionReceiptNotFound) {
		return out, err
	}
	approval, e := s.approve(ctx, hash, "apply")
	if e != nil {
		return out, e
	}
	if e = s.Files.PrepareBackup(ctx, backupDirectory, m.Database, m.BackupFiles); e != nil {
		return out, e
	}
	if e = s.Files.SaveManifest(ctx, backupDirectory, m); e != nil {
		return out, e
	}
	databaseHash, e := s.Repository.BackupAndVerify(ctx, backupDirectory)
	if e != nil {
		return out, e
	}
	if e = s.Files.Backup(ctx, m.BackupFiles, backupDirectory); e != nil {
		return out, e
	}
	// Rehash sources after the independent copy, before the first SQL DELETE.
	for _, file := range m.BackupFiles {
		if _, e = s.Files.Inspect(ctx, file); e != nil {
			return out, e
		}
	}
	if e = s.Files.VerifyBackup(ctx, backupDirectory, m, databaseHash); e != nil {
		return out, e
	}
	receipt, e := s.Repository.DeleteAndRecord(ctx, m, RetentionReceipt{ManifestHash: hash, Approval: approval, BackupDirectory: backupDirectory, BackupDatabaseHash: databaseHash})
	if e != nil {
		return out, e
	}
	out.DatabaseCommitted = true
	out.Receipt = &receipt
	// Filesystem deletion is deliberately after the database commit. A failure
	// leaves a recoverable orphan and a durable receipt, never half a SQL purge.
	return s.cleanup(ctx, m, receipt, out, false)
}

func (s RetentionService) Cleanup(ctx context.Context, m RetentionManifest) (RetentionResult, error) {
	hash, e := m.Digest()
	out := RetentionResult{ManifestHash: hash, Deleted: []string{}, AlreadyAbsent: []string{}}
	if e != nil {
		return out, e
	}
	if m.Version != RetentionManifestVersion || !reflect.DeepEqual(m.Policy, s.Policy) || m.Database != s.Identity.Database || m.Storage != s.Identity.Storage || m.DatabaseDevice != s.Identity.DatabaseDevice || m.DatabaseInode != s.Identity.DatabaseInode || m.StorageDevice != s.Identity.StorageDevice || m.StorageInode != s.Identity.StorageInode {
		return out, ErrRetentionChanged
	}
	receipt, e := s.Repository.Receipt(ctx, hash)
	if e != nil {
		return out, e
	}
	out.DatabaseCommitted = true
	out.Receipt = &receipt
	approval, e := s.approve(ctx, hash, "cleanup")
	if e != nil {
		return out, e
	}
	if e = s.Files.WriteCleanupAttempt(ctx, receipt.BackupDirectory, approval); e != nil {
		return out, e
	}
	return s.cleanup(ctx, m, receipt, out, true)
}
func (s RetentionService) cleanup(ctx context.Context, m RetentionManifest, receipt RetentionReceipt, out RetentionResult, allowAbsent bool) (RetentionResult, error) {
	if e := s.Repository.ReserveCleanup(ctx); e != nil {
		return out, e
	}
	if e := s.Files.VerifyBackup(ctx, receipt.BackupDirectory, m, receipt.BackupDatabaseHash); e != nil {
		return out, e
	}
	in, e := s.Repository.Inventory(ctx)
	if e != nil {
		return out, e
	}
	if in.SnapshotHash != receipt.AfterSnapshotHash || in.SchemaHash != m.SchemaHash {
		return out, ErrRetentionChanged
	}
	referenced := map[string]bool{}
	for _, blob := range in.Blobs {
		referenced[blob.File.Key] = true
	}
	for _, file := range m.Files {
		if referenced[file.Key] {
			return out, errors.New("approved cleanup file has a surviving reference")
		}
	}
	// Validate the entire remaining set before the first unlink. A missing file
	// on an explicitly approved resume is reported as absent, not claimed deleted.
	remaining := make([]RetentionFile, 0, len(m.Files))
	for _, file := range m.Files {
		_, e = s.Files.Inspect(ctx, file)
		if errors.Is(e, fs.ErrNotExist) && allowAbsent {
			out.AlreadyAbsent = append(out.AlreadyAbsent, file.Key)
			continue
		}
		if e != nil {
			out.FailedKey = file.Key
			return out, e
		}
		remaining = append(remaining, file)
	}
	for _, file := range remaining {
		if e = s.Files.Remove(ctx, file); e != nil {
			out.FailedKey = file.Key
			return out, fmt.Errorf("database committed; file cleanup stopped: %w", e)
		}
		out.Deleted = append(out.Deleted, file.Key)
	}
	return out, nil
}
