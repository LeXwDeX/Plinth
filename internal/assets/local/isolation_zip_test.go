package local

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"context"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

type zipWireOptions struct {
	method         uint16
	descriptor     string
	wide           bool
	name           string
	content        []byte
	localExtra     []byte
	centralExtra   []byte
	comment        string
	compressedTail []byte
	creatorVersion uint16
	externalAttrs  uint32
}

type zipWireFixture struct {
	bytes                          []byte
	data, descriptor, central, end int
	zip64End, locator              int
}

// Build special wire forms independently of the validator. archive/zip reads
// every accepted fixture as an additional compatibility check.
func makeZIPWireFixture(t *testing.T, options zipWireOptions) zipWireFixture {
	t.Helper()
	if options.name == "" {
		options.name = "3D/3dmodel.model"
	}
	content := options.content
	if content == nil {
		content = []byte(`<model xmlns="http://schemas.microsoft.com/3dmanufacturing/core/2015/02"><resources/><build/></model>`)
	}
	payload := append([]byte(nil), content...)
	if options.method == zip.Deflate {
		var compressed bytes.Buffer
		writer, err := flate.NewWriter(&compressed, flate.DefaultCompression)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write(content); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		payload = compressed.Bytes()
	}
	payload = append(payload, options.compressedTail...)
	crc := crc32.ChecksumIEEE(content)
	flags, version := uint16(0), uint16(20)
	if options.descriptor != "" {
		flags = 8
	}
	localExtra := append([]byte(nil), options.localExtra...)
	centralExtra := append([]byte(nil), options.centralExtra...)
	if options.wide {
		version = 45
		localExtra = append(localExtra, zipWireExtra(1, zipWireUint64(uint64(len(content)), uint64(len(payload))))...)
		centralExtra = append(centralExtra, zipWireExtra(1, zipWireUint64(uint64(len(content)), uint64(len(payload)), 0))...)
	}
	local := make([]byte, 30)
	binary.LittleEndian.PutUint32(local[:4], zipLocalSignature)
	binary.LittleEndian.PutUint16(local[4:6], version)
	binary.LittleEndian.PutUint16(local[6:8], flags)
	binary.LittleEndian.PutUint16(local[8:10], options.method)
	if options.descriptor == "" || options.wide {
		binary.LittleEndian.PutUint32(local[14:18], crc)
		binary.LittleEndian.PutUint32(local[18:22], uint32(len(payload)))
		binary.LittleEndian.PutUint32(local[22:26], uint32(len(content)))
	}
	if options.wide {
		binary.LittleEndian.PutUint32(local[18:22], uint32(zip32Sentinel))
		binary.LittleEndian.PutUint32(local[22:26], uint32(zip32Sentinel))
	}
	binary.LittleEndian.PutUint16(local[26:28], uint16(len(options.name)))
	binary.LittleEndian.PutUint16(local[28:30], uint16(len(localExtra)))
	result := zipWireFixture{bytes: append(local, []byte(options.name)...)}
	result.bytes = append(result.bytes, localExtra...)
	result.data = len(result.bytes)
	result.bytes = append(result.bytes, payload...)
	result.descriptor = len(result.bytes)
	if options.descriptor != "" {
		if options.descriptor == "signed" {
			result.bytes = binary.LittleEndian.AppendUint32(result.bytes, zipDescriptorSignature)
		}
		result.bytes = binary.LittleEndian.AppendUint32(result.bytes, crc)
		if options.wide {
			result.bytes = append(result.bytes, zipWireUint64(uint64(len(payload)), uint64(len(content)))...)
		} else {
			result.bytes = binary.LittleEndian.AppendUint32(result.bytes, uint32(len(payload)))
			result.bytes = binary.LittleEndian.AppendUint32(result.bytes, uint32(len(content)))
		}
	}
	result.central = len(result.bytes)
	central := make([]byte, 46)
	binary.LittleEndian.PutUint32(central[:4], zipCentralSignature)
	binary.LittleEndian.PutUint16(central[4:6], version|options.creatorVersion&0xff00)
	binary.LittleEndian.PutUint16(central[6:8], version)
	binary.LittleEndian.PutUint16(central[8:10], flags)
	binary.LittleEndian.PutUint16(central[10:12], options.method)
	binary.LittleEndian.PutUint32(central[16:20], crc)
	binary.LittleEndian.PutUint32(central[20:24], uint32(len(payload)))
	binary.LittleEndian.PutUint32(central[24:28], uint32(len(content)))
	if options.wide {
		binary.LittleEndian.PutUint32(central[20:24], uint32(zip32Sentinel))
		binary.LittleEndian.PutUint32(central[24:28], uint32(zip32Sentinel))
		binary.LittleEndian.PutUint32(central[42:46], uint32(zip32Sentinel))
	}
	binary.LittleEndian.PutUint16(central[28:30], uint16(len(options.name)))
	binary.LittleEndian.PutUint16(central[30:32], uint16(len(centralExtra)))
	binary.LittleEndian.PutUint32(central[38:42], options.externalAttrs)
	result.bytes = append(result.bytes, central...)
	result.bytes = append(result.bytes, []byte(options.name)...)
	result.bytes = append(result.bytes, centralExtra...)
	directorySize := len(result.bytes) - result.central
	if options.wide {
		result.zip64End = len(result.bytes)
		footer := make([]byte, 56)
		binary.LittleEndian.PutUint32(footer[:4], zip64EndSignature)
		binary.LittleEndian.PutUint64(footer[4:12], 44)
		binary.LittleEndian.PutUint16(footer[12:14], 45)
		binary.LittleEndian.PutUint16(footer[14:16], 45)
		binary.LittleEndian.PutUint64(footer[24:32], 1)
		binary.LittleEndian.PutUint64(footer[32:40], 1)
		binary.LittleEndian.PutUint64(footer[40:48], uint64(directorySize))
		binary.LittleEndian.PutUint64(footer[48:56], uint64(result.central))
		result.bytes = append(result.bytes, footer...)
		result.locator = len(result.bytes)
		locator := make([]byte, 20)
		binary.LittleEndian.PutUint32(locator[:4], zip64LocatorSignature)
		binary.LittleEndian.PutUint64(locator[8:16], uint64(result.zip64End))
		binary.LittleEndian.PutUint32(locator[16:20], 1)
		result.bytes = append(result.bytes, locator...)
	}
	result.end = len(result.bytes)
	end := make([]byte, 22)
	binary.LittleEndian.PutUint32(end[:4], zipEndSignature)
	binary.LittleEndian.PutUint16(end[8:10], 1)
	binary.LittleEndian.PutUint16(end[10:12], 1)
	binary.LittleEndian.PutUint32(end[12:16], uint32(directorySize))
	binary.LittleEndian.PutUint32(end[16:20], uint32(result.central))
	if options.wide {
		binary.LittleEndian.PutUint16(end[8:10], uint16(zip16Sentinel))
		binary.LittleEndian.PutUint16(end[10:12], uint16(zip16Sentinel))
		binary.LittleEndian.PutUint32(end[12:16], uint32(zip32Sentinel))
		binary.LittleEndian.PutUint32(end[16:20], uint32(zip32Sentinel))
	}
	binary.LittleEndian.PutUint16(end[20:22], uint16(len(options.comment)))
	result.bytes = append(result.bytes, end...)
	result.bytes = append(result.bytes, []byte(options.comment)...)
	return result
}

func zipWireExtra(tag uint16, value []byte) []byte {
	result := binary.LittleEndian.AppendUint16(nil, tag)
	result = binary.LittleEndian.AppendUint16(result, uint16(len(value)))
	return append(result, value...)
}

func zipWireUint64(values ...uint64) []byte {
	result := []byte{}
	for _, value := range values {
		result = binary.LittleEndian.AppendUint64(result, value)
	}
	return result
}

func TestZIPIsolationPreservesStoredDeflatedAndZIP64Models(t *testing.T) {
	for _, method := range []uint16{zip.Store, zip.Deflate} {
		for _, wide := range []bool{false, true} {
			for _, descriptor := range []string{"", "signed", "unsigned"} {
				t.Run(fmt.Sprintf("method-%d/zip64-%t/descriptor-%s", method, wide, descriptor), func(t *testing.T) {
					fixture := makeZIPWireFixture(t, zipWireOptions{method: method, wide: wide, descriptor: descriptor,
						localExtra: zipWireExtra(0x1986, []byte{0, 0, 0, 0}), comment: "self-contained 3MF"})
					archive, err := zip.NewReader(bytes.NewReader(fixture.bytes), int64(len(fixture.bytes)))
					if err != nil {
						t.Fatal(err)
					}
					for _, entry := range archive.File {
						body, err := entry.Open()
						if err != nil {
							t.Fatal(err)
						}
						_, err = io.ReadAll(body)
						_ = body.Close()
						if err != nil {
							t.Fatalf("standard ZIP reader rejected fixture: %v", err)
						}
					}
					store, err := New(t.TempDir())
					if err != nil {
						t.Fatal(err)
					}
					defer store.Close()
					asset, err := store.Put(context.Background(), modelWrite(fixture.bytes, "zip", "application/zip"))
					if err != nil {
						t.Fatal(err)
					}
					body, err := store.Open(context.Background(), asset)
					if err != nil {
						t.Fatal(err)
					}
					defer body.Close()
					actual, err := io.ReadAll(body)
					if err != nil || !bytes.Equal(actual, fixture.bytes) {
						t.Fatalf("delivered bytes changed: %v", err)
					}
				})
			}
		}
	}
}

func TestZIPIsolationInspectsDeflatedEmptyDirectories(t *testing.T) {
	for _, tail := range [][]byte{nil, []byte("unused compressed bytes")} {
		fixture := makeZIPWireFixture(t, zipWireOptions{
			method: zip.Deflate, name: "3D/", content: []byte{}, compressedTail: tail,
		})
		store, err := New(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		_, err = store.Put(context.Background(), modelWrite(fixture.bytes, "zip", "application/zip"))
		_ = store.Close()
		if tail == nil && err != nil {
			t.Fatalf("deflated empty directory = %v", err)
		}
		if tail != nil && err == nil {
			t.Fatal("directory compressed tail was not inspected")
		}
	}
}

func TestZIPDeflateRemainderCheckIncludesPrefetchedBytes(t *testing.T) {
	var compressed bytes.Buffer
	writer, err := flate.NewWriter(&compressed, flate.DefaultCompression)
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("self-contained model payload")
	if _, err := writer.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	wire := append(append([]byte(nil), compressed.Bytes()...), []byte("uninspected tail")...)
	underlying := bytes.NewReader(wire)
	reader := inspectZIPDeflate(underlying)
	defer reader.Close()
	if _, err := io.ReadAll(reader); err == nil {
		t.Fatal("tail buffered by the Deflate reader was accepted")
	}
	if underlying.Len() != 0 {
		t.Fatal("fixture did not exercise buffered read-ahead to underlying EOF")
	}
}

func TestZIPIsolationRejectsDirectoryAttributePayloads(t *testing.T) {
	for _, method := range []uint16{zip.Store, zip.Deflate} {
		for _, attributes := range []string{"UNIX", "DOS"} {
			t.Run(fmt.Sprintf("method-%d/%s", method, attributes), func(t *testing.T) {
				var buffer bytes.Buffer
				archive := zip.NewWriter(&buffer)
				header := zipDirectoryAttributes(attributes)
				header.Name, header.Method = "hidden.bin", method
				body, err := archive.CreateHeader(&header)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := io.WriteString(body, "https://private.example.test/signed?token=demonstration"); err != nil {
					t.Fatal(err)
				}
				if err := archive.Close(); err != nil {
					t.Fatal(err)
				}
				reader, err := zip.NewReader(bytes.NewReader(buffer.Bytes()), int64(buffer.Len()))
				if err != nil || len(reader.File) != 1 || !reader.File[0].FileInfo().IsDir() || reader.File[0].UncompressedSize64 == 0 {
					t.Fatalf("directory-attribute fixture does not reproduce the skipped nonempty entry: %v", err)
				}
				assertZIPDeliveryRejected(t, buffer.Bytes())
			})
		}
	}
}

func TestZIPIsolationRejectsDeclaredEmptyAttributeDirectoryTails(t *testing.T) {
	for _, method := range []uint16{zip.Store, zip.Deflate} {
		for _, attributes := range []string{"UNIX", "DOS"} {
			for _, wide := range []bool{false, true} {
				t.Run(fmt.Sprintf("method-%d/%s/ZIP64-%t", method, attributes, wide), func(t *testing.T) {
					header := zipDirectoryAttributes(attributes)
					fixture := makeZIPWireFixture(t, zipWireOptions{
						method: method, name: "hidden.bin", content: []byte{}, wide: wide,
						creatorVersion: header.CreatorVersion, externalAttrs: header.ExternalAttrs,
						compressedTail: []byte("https://private.example.test/signed?token=demonstration"),
					})
					assertZIPDeliveryRejected(t, fixture.bytes)
				})
			}
		}
	}
}

func TestZIPIsolationPreservesEmptyAttributeDirectories(t *testing.T) {
	for _, method := range []uint16{zip.Store, zip.Deflate} {
		for _, attributes := range []string{"UNIX", "DOS"} {
			for _, wide := range []bool{false, true} {
				for _, descriptor := range []string{"", "signed", "unsigned"} {
					t.Run(fmt.Sprintf("method-%d/%s/ZIP64-%t/descriptor-%s", method, attributes, wide, descriptor), func(t *testing.T) {
						header := zipDirectoryAttributes(attributes)
						fixture := makeZIPWireFixture(t, zipWireOptions{
							method: method, name: "empty-directory", content: []byte{}, wide: wide, descriptor: descriptor,
							creatorVersion: header.CreatorVersion, externalAttrs: header.ExternalAttrs,
						})
						archive, err := zip.NewReader(bytes.NewReader(fixture.bytes), int64(len(fixture.bytes)))
						if err != nil || len(archive.File) != 1 || !archive.File[0].FileInfo().IsDir() {
							t.Fatalf("fixture is not a valid attribute directory: %v", err)
						}
						store, err := New(t.TempDir())
						if err != nil {
							t.Fatal(err)
						}
						defer store.Close()
						asset, err := store.Put(context.Background(), modelWrite(fixture.bytes, "zip", "application/zip"))
						if err != nil {
							t.Fatal(err)
						}
						body, err := store.Open(context.Background(), asset)
						if err != nil {
							t.Fatal(err)
						}
						defer body.Close()
						actual, err := io.ReadAll(body)
						if err != nil || !bytes.Equal(actual, fixture.bytes) {
							t.Fatalf("empty directory bytes changed: %v", err)
						}
					})
				}
			}
		}
	}
}

func TestZIPIsolationRejectsAttributeDirectoryWithBothSizesZeroAndTrailingBytes(t *testing.T) {
	for _, method := range []uint16{zip.Store, zip.Deflate} {
		for _, attributes := range []string{"UNIX", "DOS"} {
			t.Run(fmt.Sprintf("method-%d/%s", method, attributes), func(t *testing.T) {
				header := zipDirectoryAttributes(attributes)
				fixture := makeZIPWireFixture(t, zipWireOptions{
					method: method, name: "hidden.bin", content: []byte{},
					creatorVersion: header.CreatorVersion, externalAttrs: header.ExternalAttrs,
					compressedTail: []byte("undeclared trailing bytes"),
				})
				binary.LittleEndian.PutUint32(fixture.bytes[18:22], 0)
				binary.LittleEndian.PutUint32(fixture.bytes[fixture.central+20:fixture.central+24], 0)
				assertZIPDeliveryRejected(t, fixture.bytes)
			})
		}
	}
}

func zipDirectoryAttributes(attributes string) zip.FileHeader {
	header := zip.FileHeader{}
	if attributes == "UNIX" {
		header.SetMode(os.ModeDir | 0755)
		// Isolate the UNIX high-bit mode from the redundant DOS directory flag.
		header.ExternalAttrs &^= 0x10
	} else {
		header.CreatorVersion, header.ExternalAttrs = 20, 0x10
	}
	return header
}

func assertZIPDeliveryRejected(t *testing.T, content []byte) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "model.zip")
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := ValidateDeliveredFile(file, "model", "zip", "application/zip"); err == nil {
		t.Fatal("attribute directory bypassed delivery inspection")
	}
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.Put(context.Background(), modelWrite(content, "zip", "application/zip")); err == nil {
		t.Fatal("attribute directory was committed")
	}
	blob, err := store.PutBlob(context.Background(), bytes.NewReader(content), "application/zip", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	asset := generation.Asset{Role: "model", Variant: "zip", StorageKey: blob.StorageKey, SHA256: blob.SHA256, Size: blob.Size, MediaType: blob.MediaType}
	if body, err := store.Open(context.Background(), asset); err == nil {
		_ = body.Close()
		t.Fatal("historical attribute directory bypassed inspection")
	}
}

func TestZIPIsolationRejectsUncheckedOrInconsistentWireBytes(t *testing.T) {
	classic := makeZIPWireFixture(t, zipWireOptions{method: zip.Deflate, descriptor: "signed"})
	wide := makeZIPWireFixture(t, zipWireOptions{method: zip.Deflate, descriptor: "signed", wide: true})
	mutate := func(fixture zipWireFixture, change func([]byte)) []byte {
		body := append([]byte(nil), fixture.bytes...)
		change(body)
		return body
	}
	gapBeforeDirectory := append([]byte(nil), classic.bytes[:classic.central]...)
	gapBeforeDirectory = append(gapBeforeDirectory, []byte("unchecked gap")...)
	gapBeforeDirectory = append(gapBeforeDirectory, classic.bytes[classic.central:]...)
	binary.LittleEndian.PutUint32(gapBeforeDirectory[len(gapBeforeDirectory)-6:len(gapBeforeDirectory)-2], uint32(classic.central+len("unchecked gap")))
	gapAfterDirectory := append([]byte(nil), classic.bytes[:classic.end]...)
	gapAfterDirectory = append(gapAfterDirectory, []byte("unchecked gap")...)
	gapAfterDirectory = append(gapAfterDirectory, classic.bytes[classic.end:]...)
	duplicateLocalReference := append([]byte(nil), classic.bytes[:classic.end]...)
	duplicateLocalReference = append(duplicateLocalReference, classic.bytes[classic.central:classic.end]...)
	duplicateEnd := len(duplicateLocalReference)
	duplicateLocalReference = append(duplicateLocalReference, classic.bytes[classic.end:]...)
	binary.LittleEndian.PutUint16(duplicateLocalReference[duplicateEnd+8:duplicateEnd+10], 2)
	binary.LittleEndian.PutUint16(duplicateLocalReference[duplicateEnd+10:duplicateEnd+12], 2)
	binary.LittleEndian.PutUint32(duplicateLocalReference[duplicateEnd+12:duplicateEnd+16], uint32(2*(classic.end-classic.central)))
	privateExtra := zipWireExtra(0xbeef, []byte("//192.0.2.10/private.mtl?sig=example"))
	cases := map[string][]byte{
		"trailing provider URL":      append(append([]byte(nil), classic.bytes...), []byte("https://private.example/model?sig=example")...),
		"trailing non-address bytes": append(append([]byte(nil), classic.bytes...), []byte("unchecked")...),
		"self-extracting prefix":     append([]byte("unchecked prefix"), classic.bytes...),
		"gap before directory":       gapBeforeDirectory,
		"gap before footer":          gapAfterDirectory,
		"overlapping local records":  duplicateLocalReference,
		"truncated footer":           classic.bytes[:len(classic.bytes)-1],
		"local flags mismatch":       mutate(classic, func(body []byte) { body[6] = 0 }),
		"local name mismatch":        mutate(classic, func(body []byte) { body[30] = 'X' }),
		"local CRC mismatch":         mutate(classic, func(body []byte) { binary.LittleEndian.PutUint32(body[14:18], 1) }),
		"local size mismatch":        mutate(classic, func(body []byte) { binary.LittleEndian.PutUint32(body[18:22], 1) }),
		"local header truncated":     mutate(classic, func(body []byte) { binary.LittleEndian.PutUint16(body[28:30], 65535) }),
		"central size mismatch":      mutate(classic, func(body []byte) { binary.LittleEndian.PutUint32(body[classic.central+20:classic.central+24], 1) }),
		"central header truncated":   mutate(classic, func(body []byte) { binary.LittleEndian.PutUint16(body[classic.central+30:classic.central+32], 65535) }),
		"central count mismatch": mutate(classic, func(body []byte) {
			binary.LittleEndian.PutUint16(body[classic.end+8:classic.end+10], 2)
			binary.LittleEndian.PutUint16(body[classic.end+10:classic.end+12], 2)
		}),
		"central offset mismatch":   mutate(classic, func(body []byte) { binary.LittleEndian.PutUint32(body[classic.central+42:classic.central+46], 1) }),
		"descriptor CRC mismatch":   mutate(classic, func(body []byte) { body[classic.descriptor+4] ^= 1 }),
		"descriptor size mismatch":  mutate(classic, func(body []byte) { body[classic.descriptor+8] ^= 1 }),
		"ZIP64 locator mismatch":    mutate(wide, func(body []byte) { body[wide.locator+8] ^= 1 }),
		"ZIP64 size mismatch":       mutate(wide, func(body []byte) { body[wide.zip64End+40] ^= 1 }),
		"ZIP64 descriptor mismatch": mutate(wide, func(body []byte) { body[wide.descriptor+8] ^= 1 }),
		"ZIP64 file count limit": mutate(wide, func(body []byte) {
			binary.LittleEndian.PutUint64(body[wide.zip64End+24:wide.zip64End+32], maxArchiveFiles+1)
			binary.LittleEndian.PutUint64(body[wide.zip64End+32:wide.zip64End+40], maxArchiveFiles+1)
		}),
		"local metadata address":        makeZIPWireFixture(t, zipWireOptions{localExtra: privateExtra}).bytes,
		"central metadata address":      makeZIPWireFixture(t, zipWireOptions{centralExtra: privateExtra}).bytes,
		"footer comment address":        makeZIPWireFixture(t, zipWireOptions{comment: "//localhost/private.mtl"}).bytes,
		"truncated extra":               makeZIPWireFixture(t, zipWireOptions{localExtra: []byte{1}}).bytes,
		"unused deflate payload bytes":  makeZIPWireFixture(t, zipWireOptions{method: zip.Deflate, compressedTail: []byte("https://private.example/uninspected?sig=example")}).bytes,
		"directory with hidden payload": makeZIPWireFixture(t, zipWireOptions{name: "hidden/"}).bytes,
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			store, err := New(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			if _, err := store.Put(context.Background(), modelWrite(content, "zip", "application/zip")); err == nil {
				t.Fatal("unchecked ZIP bytes were committed")
			}
			blob, err := store.PutBlob(context.Background(), bytes.NewReader(content), "application/zip", 1<<20)
			if err != nil {
				t.Fatal(err)
			}
			legacy := generation.Asset{Role: "model", Variant: "zip", StorageKey: blob.StorageKey, SHA256: blob.SHA256, Size: blob.Size, MediaType: blob.MediaType}
			if body, err := store.Open(context.Background(), legacy); err == nil {
				_ = body.Close()
				t.Fatal("historical cache delivered unchecked ZIP bytes")
			}
			path := filepath.Join(t.TempDir(), "legacy.zip")
			if err := os.WriteFile(path, content, 0600); err != nil {
				t.Fatal(err)
			}
			file, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			// Footer detection must reject prefixed ZIPs even if the upstream
			// extension and media type identify another model format.
			if err := ValidateDeliveredFile(file, "model", "obj", "application/octet-stream"); err == nil {
				t.Fatal("legacy delivery accepted unchecked ZIP bytes")
			}
		})
	}
}
