package fakeupstream

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"math"
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

// ImageFile returns a fixed picture: "png", "jpeg" or "webp". The PNG is a
// 64x64 plain background with one centered subject so the imageinspect
// checklists (#245) and the three-view consistency checks (#243) see a usable
// picture in the fake stack; the 1x1 recipes remain for wire fixtures that
// only need decodable bytes.
func ImageFile(format string) (File, error) {
	if format == "png" {
		data, err := subjectPNG()
		if err != nil {
			return File{}, err
		}
		return File{MediaType: "image/png", Data: data}, nil
	}
	recipe := map[string]string{"jpeg": "jpeg-1x1", "webp": "webp-1x1"}[format]
	if recipe == "" {
		return File{}, fmt.Errorf("fakeupstream: no fixture for image format %q", format)
	}
	media, data, err := wirefixture.SyntheticMedia(recipe)
	if err != nil {
		return File{}, err
	}
	return File{MediaType: media.MediaType, Data: data}, nil
}

// subjectPNG draws the deterministic fake picture: a white background and one
// dark rectangle centered with margins on every side. Its subject ratio
// (~28%), border spread (0) and single component satisfy the shipped 3d_input
// checklist and the consistency thresholds the fake three-view step relies on.
func subjectPNG() ([]byte, error) {
	const width, height = 64, 64
	picture := image.NewNRGBA(image.Rect(0, 0, width, height))
	background := color.NRGBA{R: 255, G: 255, B: 255, A: 255}
	subject := color.NRGBA{R: 52, G: 58, B: 70, A: 255}
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			picture.SetNRGBA(x, y, background)
		}
	}
	for y := 8; y < 56; y++ {
		for x := 20; x < 44; x++ {
			picture.SetNRGBA(x, y, subject)
		}
	}
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, picture); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
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

// SplatFile returns a tiny valid Gaussian Splat in the 32-byte record profile
// Plinth delivers (little-endian xyz and scale float32, RGBA, quantized
// quaternion): three visible splats.
func SplatFile() (File, error) {
	var data []byte
	for i := 0; i < 3; i++ {
		record := make([]byte, 32)
		for axis, value := range []float32{float32(i) * 0.1, 0, 0, 0.05, 0.05, 0.05} {
			binary.LittleEndian.PutUint32(record[axis*4:], math.Float32bits(value))
		}
		copy(record[24:], []byte{200, 60, 60, 255})   // visible, reddish
		copy(record[28:], []byte{128, 128, 128, 255}) // identity rotation
		data = append(data, record...)
	}
	return File{MediaType: "application/vnd.art-ai.gaussian-splat", Data: data}, nil
}
