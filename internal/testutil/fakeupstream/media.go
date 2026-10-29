package fakeupstream

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"sync"

	"github.com/LeXwDeX/Plinth/internal/testutil/wirefixture"
)

// File is a deterministic delivered file.
type File struct {
	MediaType string
	Data      []byte
}

var (
	modelsOnce sync.Once
	modelFiles map[string]File
	modelsErr  error
)

// ModelFile returns the fixed bytes of a model container: "glb", "gltf",
// "fbx", "obj" (OBJ ZIP), "stl", "usdz", "3mf" or the texture "png". They are
// written by the same format writers Mock generation uses (providers/mock/modelfiles),
// so Plinth's delivery checks accept them.
func ModelFile(format string) (File, error) {
	modelsOnce.Do(func() { modelFiles, modelsErr = smallModelFiles() })
	if modelsErr != nil {
		return File{}, modelsErr
	}
	file, ok := modelFiles[format]
	if !ok {
		return File{}, fmt.Errorf("fakeupstream: no fixture for model format %q", format)
	}
	return file, nil
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

// ObjFiles returns the fixed OBJ text and MTL text of the OBJ container, for
// upstreams that serve them as separate files.
func ObjFiles() (obj, mtl File, err error) {
	zipped, err := ModelFile("obj")
	if err != nil {
		return File{}, File{}, err
	}
	reader, err := zip.NewReader(bytes.NewReader(zipped.Data), int64(len(zipped.Data)))
	if err != nil {
		return File{}, File{}, err
	}
	read := func(name string) ([]byte, error) {
		for _, entry := range reader.File {
			if entry.Name == name {
				handle, err := entry.Open()
				if err != nil {
					return nil, err
				}
				defer handle.Close()
				return io.ReadAll(handle)
			}
		}
		return nil, fmt.Errorf("fakeupstream: OBJ fixture lacks %s", name)
	}
	objData, err := read("model.obj")
	if err != nil {
		return File{}, File{}, err
	}
	mtlData, err := read("model.mtl")
	if err != nil {
		return File{}, File{}, err
	}
	return File{MediaType: "text/plain", Data: objData}, File{MediaType: "text/plain", Data: mtlData}, nil
}
