package uploadname_test

import (
	"errors"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/uploadname"
)

func TestFilenameFollowsVerifiedMediaType(t *testing.T) {
	for name, tc := range map[string]struct {
		asset    generation.InputAsset
		declared []string
		want     string
	}{
		"task output glb":          {generation.InputAsset{MediaType: "model/gltf-binary", Role: "model", Variant: "glb"}, []string{".glb"}, "upload.glb"},
		"matching name kept":       {generation.InputAsset{MediaType: "model/gltf-binary", OriginalFilename: "Scan.GLB"}, []string{".glb"}, "Scan.GLB"},
		"mismatched name fixed":    {generation.InputAsset{MediaType: "image/png", OriginalFilename: "photo.jpeg"}, []string{"png", "jpg"}, "photo.png"},
		"jpeg alias kept":          {generation.InputAsset{MediaType: "image/jpeg", OriginalFilename: "photo.jpeg"}, nil, "photo.jpeg"},
		"jpeg canonical":           {generation.InputAsset{MediaType: "image/jpeg"}, nil, "upload.jpg"},
		"declared alias only":      {generation.InputAsset{MediaType: "image/jpeg"}, []string{"jpeg"}, "upload.jpeg"},
		"parameters ignored":       {generation.InputAsset{MediaType: "Image/TIFF; charset=binary"}, nil, "upload.tiff"},
		"octet-stream by variant":  {generation.InputAsset{MediaType: "application/octet-stream", Variant: "glb"}, []string{".glb"}, "upload.glb"},
		"octet-stream by filename": {generation.InputAsset{MediaType: "application/octet-stream", OriginalFilename: "model.glb"}, []string{".glb"}, "model.glb"},
		"suffix-only name":         {generation.InputAsset{MediaType: "model/gltf-binary", OriginalFilename: ".glb"}, nil, ".glb"},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := uploadname.Filename(tc.asset, tc.declared)
			if err != nil || got != tc.want {
				t.Fatalf("Filename = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

func TestFilenameRefusesWithoutReliableExtension(t *testing.T) {
	for name, tc := range map[string]struct {
		asset    generation.InputAsset
		declared []string
	}{
		"unmapped media type":           {generation.InputAsset{MediaType: "image/x-portable-pixmap", OriginalFilename: "a.ppm"}, nil},
		"empty media type":              {generation.InputAsset{OriginalFilename: "model.glb"}, []string{".glb"}},
		"undeclared extension":          {generation.InputAsset{MediaType: "model/obj"}, []string{".glb"}},
		"octet-stream undeclared":       {generation.InputAsset{MediaType: "application/octet-stream", OriginalFilename: "model.bin"}, []string{".glb"}},
		"octet-stream without contract": {generation.InputAsset{MediaType: "application/octet-stream", OriginalFilename: "model.glb"}, nil},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := uploadname.Filename(tc.asset, tc.declared)
			if !errors.Is(err, uploadname.ErrNoReliableExtension) || got != "" {
				t.Fatalf("Filename = %q, %v; want refusal", got, err)
			}
		})
	}
	if extension, err := uploadname.Extension("image/bmp", []string{"png", "bmp"}); err != nil || extension != ".bmp" {
		t.Fatalf("Extension = %q, %v", extension, err)
	}
}
