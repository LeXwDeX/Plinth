package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

type packageRole struct {
	Role    string `json:"role"`
	Variant string `json:"variant"`
}
type packageFile struct {
	File            string        `json:"file"`
	Roles           []packageRole `json:"roles"`
	Size            int64         `json:"size"`
	SHA256          string        `json:"sha256"`
	StoredMediaType string        `json:"stored_media_type"`
}
type packageManifest struct {
	OriginalTaskID               string        `json:"original_task_id"`
	OriginalTaskState            string        `json:"original_task_state"`
	OriginalRegisteredAssetCount *int          `json:"original_registered_asset_count"`
	SourceRevision               string        `json:"source_revision"`
	FrozenOfferingRevision       string        `json:"frozen_offering_revision"`
	ValidatedAt                  string        `json:"validated_at"`
	Scope                        string        `json:"scope"`
	Files                        []packageFile `json:"files"`
}

func readSmallMember(file *zip.File, limit int64) ([]byte, error) {
	if file.UncompressedSize64 > uint64(limit) {
		return nil, errors.New("package metadata is too large")
	}
	body, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer body.Close()
	raw, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > limit {
		return nil, errors.New("package metadata exceeds limit")
	}
	return raw, nil
}
func stagingWrite(source generation.RecoverySource, index int, out schema.AssetRequirement, body io.Reader) generation.AssetWrite {
	return generation.AssetWrite{AssetID: generation.AssetID(fmt.Sprintf("recovery-stage-%d", index)), OwnerID: source.Binding.OwnerID, TaskID: source.Binding.TaskID, AttemptID: source.Binding.AttemptID, Role: out.Role, Variant: out.Variant, Ordinal: index + 1, Required: out.Delivery == schema.AssetDeliveryRequired, MediaType: out.MediaTypes[0], DeliveryProfile: out.DeliveryProfile, MaxBytes: out.MaxBytes, Body: body}
}
func collectPackage(ctx context.Context, filename, archiveSHA string, source generation.RecoverySource, offering generation.Offering, store generation.AssetStore) (generation.RecoveryReceipt, error) {
	var zero generation.RecoveryReceipt
	options, err := schema.DecodeRecoveryOptions(offering)
	if err != nil {
		return zero, err
	}
	var mappings []schema.RecoverySlotMapping
	for _, candidate := range options.Sources {
		if candidate.OfferingID == source.Binding.OfferingID && candidate.OfferingRevision == source.Binding.OfferingRevision {
			mappings = candidate.Slots
			break
		}
	}
	if len(mappings) == 0 {
		return zero, errors.New("source Offering revision is not allowed by recovery Schema")
	}
	archive, err := zip.OpenReader(filename)
	if err != nil {
		return zero, err
	}
	defer archive.Close()
	if len(archive.File) < 2 || len(archive.File) > 35 {
		return zero, errors.New("package member count is invalid")
	}
	members := map[string]*zip.File{}
	var total uint64
	for _, file := range archive.File {
		if file.Name == "" || strings.Contains(file.Name, "\\") || path.IsAbs(file.Name) || path.Clean(file.Name) != file.Name || file.Name == ".." || strings.HasPrefix(file.Name, "../") || !file.Mode().IsRegular() || members[file.Name] != nil || file.UncompressedSize64 > 1<<30 {
			return zero, errors.New("package contains an unsafe or duplicate member")
		}
		total += file.UncompressedSize64
		if total > 4<<30 {
			return zero, errors.New("package uncompressed size exceeds limit")
		}
		members[file.Name] = file
	}
	if members["manifest.json"] == nil {
		return zero, errors.New("package manifest is missing")
	}
	raw, err := readSmallMember(members["manifest.json"], 128<<10)
	if err != nil {
		return zero, err
	}
	var manifest packageManifest
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&manifest) != nil || decoder.Decode(new(any)) != io.EOF {
		return zero, errors.New("package manifest is invalid")
	}
	if manifest.OriginalTaskID != string(source.Binding.TaskID) || manifest.OriginalTaskState != "FAILED" || manifest.FrozenOfferingRevision != source.Binding.OfferingRevision || manifest.OriginalRegisteredAssetCount == nil || *manifest.OriginalRegisteredAssetCount != source.RegisteredAssetCount || len(manifest.Files) == 0 || len(manifest.Files) > 32 {
		return zero, errors.New("package manifest does not match the unchanged source")
	}
	byRole := map[string]packageFile{}
	allowed := map[string]bool{"manifest.json": true, "validation-receipt.json": true, "README.txt": true}
	for _, file := range manifest.Files {
		member := members[file.File]
		if !strings.HasPrefix(file.File, "assets/") || member == nil || allowed[file.File] || file.Size <= 0 || uint64(file.Size) != member.UncompressedSize64 || !validDigest(file.SHA256) || len(file.Roles) == 0 || len(file.Roles) > 32 {
			return zero, errors.New("package file manifest is invalid")
		}
		allowed[file.File] = true
		for _, role := range file.Roles {
			key := role.Role + "\x00" + role.Variant
			if role.Role == "" || byRole[key].File != "" {
				return zero, errors.New("source slot is duplicated")
			}
			byRole[key] = file
		}
	}
	for name, member := range members {
		if !allowed[name] {
			return zero, errors.New("package contains an unlisted member")
		}
		if name == "validation-receipt.json" || name == "README.txt" {
			if _, err = readSmallMember(member, 256<<10); err != nil {
				return zero, err
			}
		}
	}
	if len(byRole) != len(mappings) {
		return zero, errors.New("package source slots are missing or unrecognized")
	}
	manifestSum := sha256.Sum256(raw)
	offeringSHA, err := generation.RecoveryDigest(offering)
	if err != nil {
		return zero, err
	}
	receipt := generation.RecoveryReceipt{Source: source.Binding, ArchiveSHA256: archiveSHA, ManifestSHA256: hex.EncodeToString(manifestSum[:]), OfferingID: offering.ID, OfferingRevision: offering.Revision, OfferingSHA256: offeringSHA}
	outputs := map[string]schema.AssetRequirement{}
	for _, out := range offering.Outputs {
		outputs[out.Role+"\x00"+out.Variant] = out
	}
	for index, mapping := range mappings {
		file, ok := byRole[mapping.SourceRole+"\x00"+mapping.SourceVariant]
		if !ok {
			return zero, errors.New("required source slot is missing")
		}
		out := outputs[mapping.Role+"\x00"+mapping.Variant]
		if file.Size > out.MaxBytes {
			return zero, errors.New("source slot exceeds recovery limit")
		}
		body, err := members[file.File].Open()
		if err != nil {
			return zero, err
		}
		asset, putErr := store.Put(ctx, stagingWrite(source, index, out, &cancelReader{ctx, body}))
		closeErr := body.Close()
		if putErr != nil {
			return zero, putErr
		}
		if closeErr != nil {
			return zero, closeErr
		}
		if asset.SHA256 != file.SHA256 || asset.Size != file.Size || asset.MediaType != out.MediaTypes[0] || asset.DeliveryProfile != out.DeliveryProfile {
			return zero, errors.New("verified file differs from manifest or canonical slot")
		}
		receipt.Slots = append(receipt.Slots, generation.RecoverySlot{SourceRole: mapping.SourceRole, SourceVariant: mapping.SourceVariant, Role: mapping.Role, Variant: mapping.Variant, Required: asset.Required, StorageKey: asset.StorageKey, SHA256: asset.SHA256, Size: asset.Size, MediaType: asset.MediaType, DeliveryProfile: asset.DeliveryProfile})
	}
	receipt, err = generation.SealRecoveryReceipt(receipt)
	if err != nil {
		return zero, err
	}
	if err = generation.ValidateRecoveryReceipt(receipt, source, offering); err != nil {
		return zero, err
	}
	return receipt, nil
}
