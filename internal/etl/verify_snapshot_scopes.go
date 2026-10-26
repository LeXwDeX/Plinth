package etl

import (
	"encoding/hex"
	"errors"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

var proofStorageDirectories = []string{"assets", "generated", "cache", "audit", "thumbnails", "task-inputs"}

func proofHexDigest(value string) bool {
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
func proofValidIdentity(i proofIdentity) bool {
	return i.Host != "" && i.Daemon != "" && proofHexDigest(i.ContainerID) && i.ComposeProject != "" && i.ComposeService != "" && strings.HasPrefix(i.ImageID, "sha256:") && proofHexDigest(strings.TrimPrefix(i.ImageID, "sha256:"))
}

func (p *proofCheck) relevant(container string) bool {
	return proofWithin(p.evidence.StoragePrefix, container) || proofWithin(container, p.evidence.StoragePrefix)
}
func (p *proofCheck) mountScope(m proofMount) bool {
	effective := m.Destination
	origin := m.Source
	if proofWithin(effective, p.evidence.StoragePrefix) {
		relative := strings.TrimPrefix(p.evidence.StoragePrefix, effective)
		effective = p.evidence.StoragePrefix
		origin = path.Join(origin, relative)
	}
	matches := 0
	for _, scope := range p.bindings.Scopes {
		if scope.Kind == "mount" && scope.ContainerPath == effective && scope.Origin == origin {
			matches++
		}
	}
	return matches == 1
}
func (p *proofCheck) topology() error {
	e, b := p.evidence, p.bindings
	if e.Format != "art-ai-snapshot-evidence-v1" || b.Format != "art-ai-snapshot-bindings-v1" {
		return proofUnknown("C1", "unsupported_evidence_format")
	}
	if e.CaptureEpoch == "" || e.Consistency.Reference == "" || (e.Consistency.Kind != "stopped_writer" && e.Consistency.Kind != "filesystem_snapshot") {
		return proofUnknown("C1", "capture_consistency_not_declared")
	}
	captured, err := time.Parse(time.RFC3339, e.CapturedAtUTC)
	if err != nil || captured.After(p.config.stamp) {
		return proofUnknown("C1", "capture_epoch_invalid")
	}
	if !proofValidIdentity(e.Identity) || !proofValidIdentity(b.ExpectedIdentity) || e.Identity != b.ExpectedIdentity {
		return proofReject("C1", "capture_identity_mismatch")
	}
	if !proofAbsolute(e.StoragePrefix) || e.StoragePrefix == "/" || e.UpperDir == "" || !proofAbsolute(e.UpperDir) {
		return proofUnknown("C1", "storage_topology_missing")
	}
	if e.Mounts == nil || e.Binds == nil || e.Volumes == nil || e.Diff == nil || len(b.Scopes) == 0 {
		return proofUnknown("C1", "scope_or_topology_missing")
	}
	switch e.State {
	case "running":
		if e.Consistency.Kind == "stopped_writer" || e.MountInfo == nil || len(*e.MountInfo) == 0 {
			return proofUnknown("C1", "running_capture_mountinfo_missing")
		}
	case "stopped":
		if e.ExportPaths == nil || len(*e.ExportPaths) == 0 {
			return proofUnknown("C1", "stopped_capture_export_listing_missing")
		}
	default:
		return proofUnknown("C1", "container_state_unknown")
	}
	seen := map[string]bool{}
	for _, scope := range b.Scopes {
		if scope.ID == "" || seen[scope.ID] || !proofAbsolute(scope.ContainerPath) || !proofWithin(e.StoragePrefix, scope.ContainerPath) || !proofRelative(scope.SnapshotPath) || (scope.Kind != "mount" && scope.Kind != "writable_layer") {
			return proofReject("C1", "invalid_scope_binding")
		}
		seen[scope.ID] = true
		expected := strings.TrimPrefix(strings.TrimPrefix(scope.ContainerPath, e.StoragePrefix), "/")
		if expected == "" {
			expected = "."
		}
		if expected != scope.SnapshotPath {
			return proofReject("C1", "scope_namespace_mismatch")
		}
		if scope.Kind == "mount" && !proofAbsolute(scope.Origin) {
			return proofReject("C1", "invalid_scope_origin")
		}
		if scope.Kind == "writable_layer" && scope.Origin != e.UpperDir {
			return proofReject("C1", "writable_origin_mismatch")
		}
	}
	mountKeys := map[string]bool{}
	mountDestinations := map[string]bool{}
	for _, m := range *e.Mounts {
		if !proofAbsolute(m.Destination) || !proofAbsolute(m.Source) || (m.Type != "bind" && m.Type != "volume" && m.Type != "tmpfs") {
			return proofUnknown("C1", "mount_metadata_invalid")
		}
		key := m.Source + "\x00" + m.Destination
		if mountKeys[key] || mountDestinations[m.Destination] {
			return proofReject("C1", "duplicate_mount")
		}
		mountKeys[key] = true
		mountDestinations[m.Destination] = true
		if p.relevant(m.Destination) && !p.mountScope(m) {
			return proofUnknown("C1", "data_mount_not_captured")
		}
	}
	for _, bind := range *e.Binds {
		if !proofAbsolute(bind.Destination) || !proofAbsolute(bind.Source) {
			return proofReject("C1", "bind_metadata_invalid")
		}
		if p.relevant(bind.Destination) && !mountKeys[bind.Source+"\x00"+bind.Destination] {
			return proofUnknown("C1", "bind_not_in_mount_topology")
		}
	}
	for _, volume := range *e.Volumes {
		if !proofAbsolute(volume) {
			return proofReject("C1", "volume_metadata_invalid")
		}
		if p.relevant(volume) {
			found := false
			for _, m := range *e.Mounts {
				found = found || m.Destination == volume
			}
			if !found {
				return proofUnknown("C1", "volume_not_in_mount_topology")
			}
		}
	}
	if e.State == "running" {
		points := map[string]bool{}
		for _, point := range *e.MountInfo {
			if !proofAbsolute(point) {
				return proofReject("C1", "mountinfo_invalid")
			}
			points[point] = true
			if point != "/" && p.relevant(point) {
				found := false
				for _, m := range *e.Mounts {
					found = found || m.Destination == point
				}
				if !found {
					return proofUnknown("C1", "mountinfo_scope_missing")
				}
			}
		}
		for _, m := range *e.Mounts {
			if p.relevant(m.Destination) && !points[m.Destination] {
				return proofUnknown("C1", "mount_not_in_mountinfo")
			}
		}
	}
	if e.ExportPaths != nil {
		for _, entry := range *e.ExportPaths {
			if !proofAbsolute(entry) {
				return proofReject("C1", "export_listing_invalid")
			}
		}
	}
	for _, scope := range b.Scopes {
		if scope.Kind == "mount" {
			found := false
			for _, m := range *e.Mounts {
				found = found || (p.relevant(m.Destination) && p.mountScope(m) && scope.Origin == path.Join(m.Source, strings.TrimPrefix(strings.TrimPrefix(scope.ContainerPath, m.Destination), "/")))
			}
			if !found {
				return proofReject("C1", "scope_origin_not_in_topology")
			}
		}
	}
	return nil
}
func proofKV(raw []byte) (map[string]string, []string, error) {
	values := map[string]string{}
	mounts := []string{}
	for _, line := range strings.Split(string(raw), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		if key == "legacy_mount" {
			mounts = append(mounts, value)
			continue
		}
		if _, duplicate := values[key]; duplicate {
			return nil, nil, errors.New("duplicate_inventory_field")
		}
		values[key] = value
	}
	return values, mounts, nil
}
func (p *proofCheck) trees() error {
	info, digest, err := diagnosticSnapshotIdentity(p.config.database)
	if err != nil {
		return proofUnknown("C2", "database_not_standalone")
	}
	p.files[p.config.database] = proofStableFile{info: info, digest: digest}
	p.report.Evidence["database"] = "sha256:" + digest
	if p.evidence.DatabaseSHA256 != digest || p.evidence.InventorySHA256 != p.files[p.config.inventory].digest || p.evidence.ManifestSHA256 != p.files[p.config.manifest].digest {
		return proofReject("C2", "capture_artifact_digest_mismatch")
	}
	snapshotObjects := map[string]os.FileInfo{}
	p.snapshot, err = scanProofObservedTree(p.config.storage, snapshotObjects)
	p.treeIdentities[p.config.storage] = snapshotObjects
	if err != nil {
		return proofUnknown("C3", err.Error())
	}
	snapshotIdentities, identitiesAvailable := proofRegularIdentities(snapshotObjects)
	if !identitiesAvailable {
		return proofUnknown("C2", "file_identity_unavailable")
	}
	p.report.Evidence["storage_tree"] = "sha256:" + proofTreeHash(p.snapshot)
	var totalSourceEntries int
	var totalSourceBytes int64
	for _, scope := range p.bindings.Scopes {
		canonicalSource, rootErr := filepath.EvalSymlinks(scope.SourceRoot)
		if rootErr != nil {
			return proofUnknown("C2", "source_scope_unreadable")
		}
		canonicalSource, rootErr = filepath.Abs(canonicalSource)
		if rootErr != nil || diagnosticContained(p.config.storage, canonicalSource) || diagnosticContained(canonicalSource, p.config.storage) {
			return proofReject("C2", "source_snapshot_not_independent")
		}
		tree, observedAlready := p.sourceTrees[scope.SourceRoot]
		sourceObjects := p.treeIdentities[scope.SourceRoot]
		if !observedAlready {
			sourceObjects = map[string]os.FileInfo{}
			tree, err = scanProofObservedTree(scope.SourceRoot, sourceObjects)
			if err != nil {
				return proofUnknown("C3", err.Error())
			}
			if len(tree) > proofMaximumFiles-totalSourceEntries {
				return proofUnknown("C2", "source_tree_budget_exceeded")
			}
			totalSourceEntries += len(tree)
			for _, entry := range tree {
				if entry.Kind == "file" {
					if entry.Size > proofMaximumTreeBytes-totalSourceBytes {
						return proofUnknown("C2", "source_tree_budget_exceeded")
					}
					totalSourceBytes += entry.Size
				}
			}
			p.treeIdentities[scope.SourceRoot] = sourceObjects
			p.sourceTrees[scope.SourceRoot] = tree
		}
		for _, info := range sourceObjects {
			if !info.Mode().IsRegular() {
				continue
			}
			identity, ok := proofRegularIdentity(info)
			if !ok {
				return proofUnknown("C2", "file_identity_unavailable")
			}
			if _, shared := snapshotIdentities[identity]; shared {
				return proofReject("C2", "source_snapshot_not_independent")
			}
		}
		for _, relative := range proofTreePaths(tree) {
			entry := tree[relative]
			namespace := path.Join(scope.SnapshotPath, relative)
			entry.PathReference = proofRef(namespace)
			if previous, ok := p.merged[namespace]; ok && previous != entry {
				return proofReject("C2", "overlapping_source_scopes_disagree")
			}
			p.merged[namespace] = entry
			if actual, ok := p.snapshot[namespace]; !ok || actual != entry {
				return proofReject("C2", "source_snapshot_tree_mismatch")
			}
		}
		p.report.ScopeTrees[proofRef(scope.ID)] = "sha256:" + proofTreeHash(tree)
	}
	// An exact database input may live outside storage. When it lives inside,
	// it is still compared through the source tree; no implicit subtree exclusion.
	for _, name := range proofTreePaths(p.snapshot) {
		entry := p.snapshot[name]
		if expected, ok := p.merged[name]; !ok || expected != entry {
			return proofUnknown("C2", "snapshot_scope_not_evidenced")
		}
	}
	inventoryRaw, _, err := readProofFile(p.config.inventory, true)
	if err != nil {
		return proofUnknown("C2", "inventory_unreadable")
	}
	manifestRaw, _, err := readProofFile(p.config.manifest, true)
	if err != nil {
		return proofUnknown("C2", "manifest_unreadable")
	}
	inventory, mounts, err := proofKV(inventoryRaw)
	if err != nil {
		return proofUnknown("C2", "inventory_invalid")
	}
	if inventory["format"] != "art-ai-formal-production-inventory-v1" || inventory["database_snapshot_sha256"] != digest || inventory["hostname"] != p.evidence.Identity.Host || inventory["legacy_container_image_id"] != p.evidence.Identity.ImageID || inventory["legacy_compose_project"] != p.evidence.Identity.ComposeProject || inventory["legacy_compose_service"] != p.evidence.Identity.ComposeService || inventory["legacy_container_running"] != strconv.FormatBool(p.evidence.State == "running") {
		return proofReject("C2", "inventory_capture_identity_mismatch")
	}
	inventoryAt, timeErr := time.Parse(time.RFC3339, inventory["collected_at_utc"])
	capturedAt, _ := time.Parse(time.RFC3339, p.evidence.CapturedAtUTC)
	if timeErr != nil || inventoryAt.After(capturedAt) {
		return proofUnknown("C2", "inventory_epoch_mismatch")
	}
	manifest, _, err := proofKV(manifestRaw)
	if err != nil || manifest["format"] != "art-ai-go-cutover-backup-v1" || manifest["backup_database_sha256"] != digest {
		return proofReject("C2", "manifest_database_mismatch")
	}
	for _, m := range *p.evidence.Mounts {
		if !p.relevant(m.Destination) {
			continue
		}
		found := false
		for _, raw := range mounts {
			parts := strings.Split(raw, "|")
			if len(parts) != 4 {
				return proofUnknown("C2", "inventory_mount_invalid")
			}
			found = found || (parts[0] == m.Type && parts[1] == m.Source && parts[2] == m.Destination && (parts[3] == "true" || parts[3] == "false"))
		}
		if !found {
			return proofReject("C2", "inventory_mount_mismatch")
		}
	}
	for _, raw := range mounts {
		parts := strings.Split(raw, "|")
		if len(parts) != 4 || !proofAbsolute(parts[2]) {
			return proofUnknown("C2", "inventory_mount_invalid")
		}
		if p.relevant(parts[2]) {
			found := false
			for _, m := range *p.evidence.Mounts {
				found = found || (parts[0] == m.Type && parts[1] == m.Source && parts[2] == m.Destination)
			}
			if !found {
				return proofReject("C2", "inventory_mount_not_in_topology")
			}
		}
	}
	for _, dir := range proofStorageDirectories {
		if p.snapshot[dir].Kind != "directory" || p.merged[dir].Kind != "directory" {
			return proofUnknown("C2", "required_directory_missing")
		}
		files, size := proofDirectoryStats(p.snapshot, dir)
		sourceFiles, sourceSize := proofDirectoryStats(p.merged, dir)
		countText := strconv.FormatInt(files, 10)
		sizeText := strconv.FormatInt(size, 10)
		if sourceFiles != files || sourceSize != size || inventory["storage_"+dir+"_files"] != countText || inventory["storage_"+dir+"_bytes"] != sizeText || manifest["storage."+dir+".files"] != countText {
			return proofReject("C2", "three_way_storage_count_mismatch")
		}
	}
	return nil
}
func (p *proofCheck) writable() error {
	for _, change := range *p.evidence.Diff {
		if !proofAbsolute(change.Path) || (change.Change != "A" && change.Change != "M" && change.Change != "D") {
			return proofUnknown("C1", "writable_diff_invalid")
		}
		if !proofWithin(p.evidence.StoragePrefix, change.Path) {
			continue
		}
		relative := strings.TrimPrefix(strings.TrimPrefix(change.Path, p.evidence.StoragePrefix), "/")
		if relative == "" {
			relative = "."
		}
		if change.Change == "D" {
			if _, exists := p.snapshot[relative]; exists {
				return proofReject("C1", "deleted_writable_path_in_snapshot")
			}
			continue
		}
		covered := false
		for _, scope := range p.bindings.Scopes {
			if scope.Kind != "writable_layer" || !proofWithin(scope.ContainerPath, change.Path) {
				continue
			}
			local := strings.TrimPrefix(strings.TrimPrefix(change.Path, scope.ContainerPath), "/")
			if local == "" {
				local = "."
			}
			entry, exists := p.sourceTrees[scope.SourceRoot][local]
			if !exists {
				continue
			}
			entry.PathReference = proofRef(relative)
			if actual, exists := p.snapshot[relative]; exists && actual == entry {
				covered = true
			}
		}
		if !covered {
			return proofUnknown("C1", "writable_bytes_not_captured")
		}
		if p.evidence.State == "stopped" {
			listed := false
			for _, entry := range *p.evidence.ExportPaths {
				listed = listed || entry == change.Path
			}
			if !listed {
				return proofUnknown("C1", "writable_export_listing_missing")
			}
		}
	}
	return nil
}
