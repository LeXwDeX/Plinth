package local

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"os"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// CheckFidelity reads a local CAS file, never an upstream locator. Put/Open own
// byte integrity and isolation; this extra gate observes model semantics only.
func (s *Store) CheckFidelity(ctx context.Context, asset generation.Asset, level string) (result error) {
	// Only rejectInspection marks confirmed structural rejection. Any other
	// error (open, stat, read, cancellation) is inspection unavailability and
	// keeps the reason fidelity_inspection_unavailable, which is retryable.
	defer func() { result = diagnoseWrite("inspect", "fidelity_inspection_unavailable", result) }()
	if err := ctx.Err(); err != nil {
		return err
	}
	file, err := s.openStoredFile(asset.StorageKey)
	if err != nil {
		return err
	}
	defer file.Close()
	return ValidateFidelityFile(file, level)
}

// ValidateFidelityFile checks GLB/glTF JSON and binary FBX. ASCII FBX is not
// supported. Catalog therefore enables fidelity only for GLB/glTF media types.
// This is structural evidence, not a guarantee of useful rigging or motion.
func ValidateFidelityFile(file *os.File, level string) error {
	if level == "" {
		return nil
	}
	if level != "skeleton" && level != "animation" {
		return rejectInspection("fidelity_unsupported", errors.New("unsupported fidelity level"))
	}
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if info.Size() <= 0 || info.Size() > maxArchiveBytes {
		return invalidFidelity()
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	defer file.Seek(0, io.SeekStart)
	var magic [8]byte
	if _, err := file.ReadAt(magic[:], 0); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	work := newInspectionWorkBudgetForSize(info.Size())
	if isBinaryFBXMagic(magic[:]) {
		facts := &fbxFidelity{}
		if err := inspectBinaryFBXFidelity(file, info.Size(), work, facts); err != nil {
			return invalidFidelity()
		}
		if !facts.skin {
			return missingSkeleton()
		}
		if level == "animation" && !facts.animation {
			return missingAnimation()
		}
		return nil
	}
	var data []byte
	if bytes.Equal(magic[:4], []byte("glTF")) {
		data, err = fidelityGLBJSON(file, info.Size(), work)
	} else if info.Size() <= maxModelJSONBytes {
		data, err = io.ReadAll(io.LimitReader(file, maxModelJSONBytes+1))
	} else {
		return invalidFidelity()
	}
	if err != nil {
		return fidelityReadError(err)
	}
	if work.reserve(int64(len(data))*2) != nil || validateUniqueJSONFields(data) != nil {
		return invalidFidelity()
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var document map[string]any
	if decoder.Decode(&document) != nil || document == nil {
		return invalidFidelity()
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return invalidFidelity()
	}
	return checkGLTFFidelity(document, level, work)
}

// Read only the bounded JSON chunk; large BIN payloads are not allocated or
// scanned a second time. All chunk lengths are checked against the file size.
func fidelityGLBJSON(file *os.File, size int64, work *inspectionWorkBudget) ([]byte, error) {
	var header [12]byte
	if size < 20 || size%4 != 0 {
		return nil, invalidFidelity()
	}
	if _, err := io.ReadFull(file, header[:]); err != nil {
		return nil, fidelityReadError(err)
	}
	if binary.LittleEndian.Uint32(header[4:8]) != 2 || int64(binary.LittleEndian.Uint32(header[8:])) != size {
		return nil, invalidFidelity()
	}
	var data []byte
	seenBIN := false
	chunks := 0
	for offset := int64(12); offset < size; {
		chunks++
		if chunks > maxArchiveFiles || work.reserve(8) != nil {
			return nil, invalidFidelity()
		}
		var chunk [8]byte
		if size-offset < 8 {
			return nil, invalidFidelity()
		}
		if _, err := io.ReadFull(file, chunk[:]); err != nil {
			return nil, fidelityReadError(err)
		}
		length := int64(binary.LittleEndian.Uint32(chunk[:4]))
		offset += 8
		if length%4 != 0 || length > size-offset || (data == nil && string(chunk[4:]) != "JSON") {
			return nil, invalidFidelity()
		}
		if string(chunk[4:]) == "JSON" {
			if data != nil || length == 0 || length > maxModelJSONBytes {
				return nil, invalidFidelity()
			}
			data = make([]byte, int(length))
			if _, err := io.ReadFull(file, data); err != nil {
				return nil, fidelityReadError(err)
			}
		} else {
			if string(chunk[4:]) == "BIN\x00" {
				if seenBIN {
					return nil, invalidFidelity()
				}
				seenBIN = true
			}
			if _, err := file.Seek(length, io.SeekCurrent); err != nil {
				return nil, err
			}
		}
		offset += length
	}
	return data, nil
}

func checkGLTFFidelity(document map[string]any, level string, work *inspectionWorkBudget) error {
	nodes, err := gltfArray(document, "nodes")
	if err != nil {
		return invalidFidelity()
	}
	skins, err := gltfArray(document, "skins")
	if err != nil {
		return invalidFidelity()
	}
	hasSkeleton := false
	for _, value := range skins {
		skin, ok := value.(map[string]any)
		if !ok {
			return invalidFidelity()
		}
		joints, err := gltfArray(skin, "joints")
		if err != nil || work.reserve(int64(len(joints))+1) != nil {
			return invalidFidelity()
		}
		for _, joint := range joints {
			if !fidelityIndex(joint, nodes) {
				return invalidFidelity()
			}
		}
		hasSkeleton = hasSkeleton || len(joints) > 0
	}
	if !hasSkeleton {
		return missingSkeleton()
	}
	if level == "skeleton" {
		return nil
	}
	animations, err := gltfArray(document, "animations")
	if err != nil {
		return invalidFidelity()
	}
	accessors, err := gltfArray(document, "accessors")
	if err != nil {
		return invalidFidelity()
	}
	hasAnimation := false
	for _, value := range animations {
		animation, ok := value.(map[string]any)
		if !ok {
			return invalidFidelity()
		}
		channels, err := gltfArray(animation, "channels")
		if err != nil || work.reserve(int64(len(channels))+1) != nil {
			return invalidFidelity()
		}
		samplers, err := gltfArray(animation, "samplers")
		if err != nil {
			return invalidFidelity()
		}
		for _, value := range channels {
			channel, ok := value.(map[string]any)
			if !ok || !fidelityIndex(channel["sampler"], samplers) {
				return invalidFidelity()
			}
			target, ok := channel["target"].(map[string]any)
			if !ok || !fidelityIndex(target["node"], nodes) {
				return invalidFidelity()
			}
			index, _ := gltfInteger(channel, "sampler", -1)
			sampler := samplers[index].(map[string]any)
			if !fidelityIndex(sampler["input"], accessors) {
				return invalidFidelity()
			}
			input, _ := gltfInteger(sampler, "input", -1)
			count, err := gltfInteger(accessors[input].(map[string]any), "count", -1)
			if err != nil || count < 0 {
				return invalidFidelity()
			}
			hasAnimation = hasAnimation || count > 0
		}
	}
	if !hasAnimation {
		return missingAnimation()
	}
	return nil
}

func fidelityIndex(value any, array []any) bool {
	number, ok := value.(json.Number)
	if !ok {
		return false
	}
	index, err := number.Int64()
	if err != nil || index < 0 || index >= int64(len(array)) {
		return false
	}
	_, ok = array[index].(map[string]any)
	return ok
}

// fidelityReadError keeps a short read of a size-checked file as a structural
// rejection; every other read failure is an operational error to retry.
func fidelityReadError(err error) error {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return invalidFidelity()
	}
	return err
}

func invalidFidelity() error {
	return rejectInspection("fidelity_invalid_structure", errors.New("invalid model fidelity structure"))
}
func missingSkeleton() error {
	return rejectInspection("fidelity_skeleton_missing", errors.New("model skeleton missing"))
}
func missingAnimation() error {
	return rejectInspection("fidelity_animation_missing", errors.New("model animation missing"))
}
