package fakeupstream

import (
	"fmt"
	"sync"

	"github.com/LeXwDeX/Plinth/internal/providers/mock"
	"github.com/LeXwDeX/Plinth/internal/testutil/wirefixture"
)

// File is a deterministic delivered file.
type File struct {
	MediaType string
	Data      []byte
}

var (
	modelsOnce sync.Once
	modelFiles map[string]mock.ModelFile
	modelsErr  error
)

// ModelFile returns the fixed bytes of a model container: "glb", "gltf",
// "fbx", "obj" (OBJ ZIP), "stl", "usdz", "3mf" or the texture "png". They are
// the bytes Mock generation delivers, so Plinth's delivery checks accept them.
func ModelFile(format string) (File, error) {
	modelsOnce.Do(func() { modelFiles, modelsErr = mock.SmallModelFiles() })
	if modelsErr != nil {
		return File{}, modelsErr
	}
	file, ok := modelFiles[format]
	if !ok {
		return File{}, fmt.Errorf("fakeupstream: no fixture for model format %q", format)
	}
	return File{MediaType: file.MediaType, Data: file.Data}, nil
}

// ImageFile returns a fixed picture: "png", "jpeg" or "webp".
func ImageFile(format string) (File, error) {
	recipe := map[string]string{"png": "png-1x1", "jpeg": "jpeg-1x1", "webp": "webp-1x1"}[format]
	if recipe == "" {
		return File{}, fmt.Errorf("fakeupstream: no fixture for image format %q", format)
	}
	media, data, err := wirefixture.SyntheticMedia(recipe)
	if err != nil {
		return File{}, err
	}
	return File{MediaType: media.MediaType, Data: data}, nil
}

// Corrupt returns bytes that no longer match the declared format: the magic is
// wiped and the tail is cut off.
func Corrupt(data []byte) []byte {
	out := append([]byte(nil), data[:max(len(data)/2, 1)]...)
	for i := 0; i < len(out) && i < 8; i++ {
		out[i] = 0
	}
	return out
}
