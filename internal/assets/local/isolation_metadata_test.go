package local

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

func assertIsolationBoundary(t *testing.T, body []byte, variant, mediaType string, accepted bool) {
	t.Helper()
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	fileName := filepath.Join(t.TempDir(), "fixture")
	if err := os.WriteFile(fileName, body, 0600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(fileName)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := ValidateDeliveredFile(file, "model", variant, mediaType); (err == nil) != accepted {
		t.Fatalf("ValidateDeliveredFile acceptance=%v, expected %v", err == nil, accepted)
	}
	write := modelWrite(body, variant, mediaType)
	write.MaxBytes = maxRasterInspect + maxModelJSONBytes
	asset, err := store.Put(context.Background(), write)
	if (err == nil) != accepted {
		t.Fatalf("Store.Put acceptance=%v, expected %v", err == nil, accepted)
	}
	if accepted {
		reader, err := store.Open(context.Background(), asset)
		if err != nil {
			t.Fatal(err)
		}
		got, readErr := io.ReadAll(reader)
		_ = reader.Close()
		if readErr != nil || !bytes.Equal(got, body) {
			t.Fatal("admitted asset bytes changed")
		}
	}
	blob, err := store.PutBlob(context.Background(), bytes.NewReader(body), mediaType, write.MaxBytes)
	if err != nil {
		t.Fatal(err)
	}
	historical := generation.Asset{Role: "model", Variant: variant, MediaType: mediaType, StorageKey: blob.StorageKey, Size: blob.Size, SHA256: blob.SHA256}
	reader, err := store.Open(context.Background(), historical)
	if (err == nil) != accepted {
		if reader != nil {
			_ = reader.Close()
		}
		t.Fatalf("historical Store.Open acceptance=%v, expected %v", err == nil, accepted)
	}
	if reader != nil {
		got, readErr := io.ReadAll(reader)
		_ = reader.Close()
		if readErr != nil || !bytes.Equal(got, body) {
			t.Fatal("historical asset bytes changed")
		}
	}
}

func TestPublicMetadataExceptionsRequireCompleteIdentifiers(t *testing.T) {
	for _, prefix := range []string{
		"http://schemas.microsoft.com/3dmanufacturing/core/2015/02",
		"http://schemas.openxmlformats.org/package/2006/relationships",
		"http://schemas.3mf.io/3dmanufacturing/toolpath/2026/03",
		"http://www.w3.org/2001/XMLSchema",
		"http://purl.org/dc/terms/",
		"http://schemas.bambulab.com/package/2021",
	} {
		for _, suffix := range []string{"/https://private.example/resource", "/%68%74%74%70%73%3A%2F%2Fprivate.example/resource", "//localhost/resource", "/%252F%252Flocalhost/resource", "?", "#"} {
			if standardSpecificationURI(prefix + suffix) {
				t.Fatal("partial XML identifier was exempted")
			}
		}
	}
}

func TestNestedPublicMetadataAddressesRejectedOnEveryBoundary(t *testing.T) {
	const repository = "https://www.microsoft.com/pkiops/docs/repository.htm"
	const vocabulary = "http://cv.iptc.org/newscodes/digitalsourcetype/trainedAlgorithmicMedia"
	for index, prefix := range []string{repository, vocabulary, "http://www.microsoft.com/pkiops/crl/MicrosoftRoot.crl"} {
		for name, suffix := range map[string]string{
			"raw":      "/https://private.example/resource%3Ftoken%3Dsynth",
			"percent":  "/%68%74%74%70%73%3A%2F%2Fprivate.example/resource",
			"relative": "//localhost/resource",
			"layered":  "/%252F%252Flocalhost/resource",
			"escaped":  `/\u002f\u002flocalhost/resource`,
		} {
			t.Run(strings.Repeat("p", index+1)+name, func(t *testing.T) {
				// Images are cached as delivered: a valid PNG is admitted whole
				// even when its provenance chunk carries addresses.
				assertIsolationBoundary(t, pngWithProvenance(t, "caBX", prefix+suffix), "png", "image/png", true)
			})
		}
	}
	for name, value := range map[string]string{
		"raw":         "http://schemas.microsoft.com/schema/https://private.example/resource%3Ftoken%3Dsynth",
		"percent":     "http://schemas.microsoft.com/schema/%68%74%74%70%73%3A%2F%2Fprivate.example/resource",
		"relative":    "http://schemas.microsoft.com/3dmanufacturing/core/2015/02//localhost/resource",
		"layered":     "http://schemas.microsoft.com/3dmanufacturing/core/2015/02/%252F%252Flocalhost/resource",
		"xml_escaped": "http://schemas.microsoft.com/schema/https:&#47;&#47;private.example/resource",
	} {
		for _, attribute := range []string{"xmlns", "Type"} {
			t.Run(name+attribute, func(t *testing.T) {
				body := zipModel(t, map[string]string{"document.xml": `<Relationship ` + attribute + `="` + value + `"/>`})
				assertIsolationBoundary(t, body, "zip", "application/zip", false)
			})
		}
	}
}

func TestCompletePublicMetadataIdentifiersPreserveExactBytes(t *testing.T) {
	for _, address := range []string{
		"http://cv.iptc.org/newscodes/digitalsourcetype/trainedAlgorithmicMedia",
		"https://www.microsoft.com/pkiops/docs/repository.htm",
		"http://www.microsoft.com/pkiops/certs/MicrosoftRoot",
		"http://www.microsoft.com/pkiops/certs/Microsoft%20Identity%20Verification%20Root%20Certificate%20Authority%202020.crt",
		"http://www.microsoft.com/pkiops/crl/MicrosoftRoot.crl",
		"http://oneocsp.microsoft.com/ocsp",
	} {
		assertIsolationBoundary(t, pngWithProvenance(t, "caBX", address), "png", "image/png", true)
	}
	for _, address := range []string{
		"http://schemas.microsoft.com/3dmanufacturing/core/2015/02",
		"http://schemas.microsoft.com/3dmanufacturing/production/2015/06",
		"http://schemas.microsoft.com/3dmanufacturing/2013/01/3dmodel",
		"http://schemas.microsoft.com/3dmanufacturing/beamlattice/balls/2020/07",
		"http://schemas.3mf.io/3dmanufacturing/toolpath/2026/03/layer",
		"http://schemas.openxmlformats.org/package/2006/content-types",
		"http://schemas.openxmlformats.org/package/2006/relationships/metadata/thumbnail",
		"http://www.w3.org/XML/1998/namespace", "http://purl.org/dc/terms/", "http://schemas.bambulab.com/package/2021",
	} {
		if !standardSpecificationURI(address) {
			t.Fatal("complete specification identifier rejected")
		}
		body := zipModel(t, map[string]string{"document.xml": `<Relationship xmlns="` + address + `" Type="` + address + `"/>`})
		assertIsolationBoundary(t, body, "zip", "application/zip", true)
	}
}
