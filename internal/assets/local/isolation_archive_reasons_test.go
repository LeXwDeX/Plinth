package local

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #386: a delivered 3MF that the archive isolation refused only said
// archive_rejected with errno=other, so a paid result could be lost with no way
// to tell whether the bytes were corrupt, over a limit or merely unusual. Every
// container, member and XML rule now names itself. The reasons below are the
// allowlisted vocabulary operators see in materialization.asset_store_rejected.

// archiveDiagnostic stores body the way the materializer does and returns the
// allowlisted diagnostic the log would carry, or false when it was accepted.
func archiveDiagnostic(t *testing.T, body []byte, variant, mediaType string) (generation.AssetStorageDiagnostic, bool) {
	t.Helper()
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	write := modelWrite(body, variant, mediaType)
	write.MaxBytes = 16 << 20
	_, err = store.Put(context.Background(), write)
	if err == nil {
		return generation.AssetStorageDiagnostic{}, false
	}
	diagnostic, ok := generation.AssetStorageDiagnosticOf(err)
	if !ok {
		t.Fatalf("rejection carries no storage diagnostic: %v", err)
	}
	return diagnostic, true
}

func TestArchiveRejectionsNameTheRuleThatFired(t *testing.T) {
	classic := makeZIPWireFixture(t, zipWireOptions{method: zip.Deflate, descriptor: "signed"})
	wide := makeZIPWireFixture(t, zipWireOptions{method: zip.Deflate, descriptor: "signed", wide: true})
	stored := makeZIPWireFixture(t, zipWireOptions{method: zip.Store})
	deflated := makeZIPWireFixture(t, zipWireOptions{method: zip.Deflate})
	mutate := func(fixture zipWireFixture, change func([]byte)) []byte {
		body := append([]byte(nil), fixture.bytes...)
		change(body)
		return body
	}
	gapBeforeDirectory := append([]byte(nil), deflated.bytes[:deflated.central]...)
	gapBeforeDirectory = append(gapBeforeDirectory, []byte("unchecked gap")...)
	gapBeforeDirectory = append(gapBeforeDirectory, deflated.bytes[deflated.central:]...)
	binary.LittleEndian.PutUint32(gapBeforeDirectory[len(gapBeforeDirectory)-6:len(gapBeforeDirectory)-2], uint32(deflated.central+len("unchecked gap")))
	gapBeforeFooter := append([]byte(nil), classic.bytes[:classic.end]...)
	gapBeforeFooter = append(gapBeforeFooter, []byte("unchecked gap")...)
	gapBeforeFooter = append(gapBeforeFooter, classic.bytes[classic.end:]...)
	overlapping := append([]byte(nil), classic.bytes[:classic.end]...)
	overlapping = append(overlapping, classic.bytes[classic.central:classic.end]...)
	overlappingEnd := len(overlapping)
	overlapping = append(overlapping, classic.bytes[classic.end:]...)
	binary.LittleEndian.PutUint16(overlapping[overlappingEnd+8:overlappingEnd+10], 2)
	binary.LittleEndian.PutUint16(overlapping[overlappingEnd+10:overlappingEnd+12], 2)
	binary.LittleEndian.PutUint32(overlapping[overlappingEnd+12:overlappingEnd+16], uint32(2*(classic.end-classic.central)))
	privateExtra := zipWireExtra(0xbeef, []byte("//192.0.2.10/private.mtl?sig=example"))
	many := map[string]string{}
	for index := 0; index <= maxArchiveFiles; index++ {
		many[fmt.Sprintf("m%d", index)] = ""
	}
	storedLength := binary.LittleEndian.Uint32(stored.bytes[22:26])
	setStored := func(local, central int, value uint32) func([]byte) {
		return func(body []byte) {
			binary.LittleEndian.PutUint32(body[local:local+4], value)
			binary.LittleEndian.PutUint32(body[stored.central+central:stored.central+central+4], value)
		}
	}
	const xmlns = `xmlns="http://schemas.microsoft.com/3dmanufacturing/core/2015/02"`
	for _, test := range []struct {
		name, variant, mediaType string
		body                     []byte
		reason                   string
		errno                    string
	}{
		// Footer: the end-of-central-directory record.
		{"HTML error page in a 3MF slot", "converted.3mf", "application/octet-stream", []byte(errorPageHTML), "zip_footer_invalid", "other"},
		{"trailing bytes after the footer", "", "", append(append([]byte(nil), classic.bytes...), []byte("unchecked")...), "zip_footer_invalid", "other"},
		{"truncated footer", "", "", classic.bytes[:len(classic.bytes)-1], "zip_footer_invalid", "other"},
		{"split archive footer", "", "", mutate(classic, func(b []byte) { b[classic.end+4] = 1 }), "zip_footer_invalid", "other"},
		// Central directory.
		{"self-extracting prefix", "", "", append([]byte("unchecked prefix"), classic.bytes...), "zip_directory_invalid", "other"},
		{"gap before the footer", "", "", gapBeforeFooter, "zip_directory_invalid", "other"},
		{"central count larger than its bytes", "", "", mutate(classic, func(b []byte) {
			binary.LittleEndian.PutUint16(b[classic.end+8:classic.end+10], 2)
			binary.LittleEndian.PutUint16(b[classic.end+10:classic.end+12], 2)
		}), "zip_directory_invalid", "other"},
		{"central signature damaged", "", "", mutate(classic, func(b []byte) { b[classic.central] ^= 1 }), "zip_directory_invalid", "other"},
		{"directory entry with payload", "", "", makeZIPWireFixture(t, zipWireOptions{name: "hidden/"}).bytes, "zip_directory_invalid", "other"},
		// Record layout: prefix, gaps, overlap, unaccounted bytes.
		{"gap before the directory", "", "", gapBeforeDirectory, "zip_layout_invalid", "other"},
		{"overlapping local records", "", "", overlapping, "zip_layout_invalid", "other"},
		{"central offset points elsewhere", "", "", mutate(classic, func(b []byte) { binary.LittleEndian.PutUint32(b[classic.central+42:classic.central+46], 1) }), "zip_layout_invalid", "other"},
		{"deflate stream followed by unchecked bytes", "", "", makeZIPWireFixture(t, zipWireOptions{method: zip.Deflate, compressedTail: []byte("unchecked tail")}).bytes, "zip_layout_invalid", "other"},
		// Local header against the central directory.
		{"local flags differ", "", "", mutate(classic, func(b []byte) { b[6] = 0 }), "zip_local_header_invalid", "other"},
		{"local name differs", "", "", mutate(classic, func(b []byte) { b[30] = 'X' }), "zip_local_header_invalid", "other"},
		{"local CRC differs", "", "", mutate(classic, func(b []byte) { binary.LittleEndian.PutUint32(b[14:18], 1) }), "zip_local_header_invalid", "other"},
		{"local size differs", "", "", mutate(classic, func(b []byte) { binary.LittleEndian.PutUint32(b[18:22], 1) }), "zip_local_header_invalid", "other"},
		{"local signature damaged", "", "", mutate(classic, func(b []byte) { b[0] ^= 1 }), "zip_local_header_invalid", "other"},
		// Records that run past the file.
		{"local header runs past its record", "", "", mutate(classic, func(b []byte) { binary.LittleEndian.PutUint16(b[28:30], 65535) }), "zip_truncated", "other"},
		{"central header runs past the directory", "", "", mutate(classic, func(b []byte) { binary.LittleEndian.PutUint16(b[classic.central+30:classic.central+32], 65535) }), "zip_truncated", "other"},
		// Data descriptors.
		{"descriptor CRC differs", "", "", mutate(classic, func(b []byte) { b[classic.descriptor+4] ^= 1 }), "zip_descriptor_invalid", "other"},
		{"descriptor size differs", "", "", mutate(classic, func(b []byte) { b[classic.descriptor+8] ^= 1 }), "zip_descriptor_invalid", "other"},
		{"central size leaves an odd descriptor", "", "", mutate(classic, func(b []byte) { binary.LittleEndian.PutUint32(b[classic.central+20:classic.central+24], 1) }), "zip_descriptor_invalid", "other"},
		{"ZIP64 descriptor size differs", "", "", mutate(wide, func(b []byte) { b[wide.descriptor+8] ^= 1 }), "zip_descriptor_invalid", "other"},
		// ZIP64.
		{"ZIP64 locator points elsewhere", "", "", mutate(wide, func(b []byte) { binary.LittleEndian.PutUint64(b[wide.locator+8:wide.locator+16], 0) }), "zip64_invalid", "other"},
		{"ZIP64 disk and total counts differ", "", "", mutate(wide, func(b []byte) { b[wide.zip64End+24] ^= 1 }), "zip64_invalid", "other"},
		{"ZIP64 locator missing", "", "", mutate(classic, func(b []byte) { binary.LittleEndian.PutUint32(b[classic.end+16:classic.end+20], uint32(zip32Sentinel)) }), "zip64_invalid", "other"},
		{"ZIP64 extra although sizes are not sentinels", "", "", makeZIPWireFixture(t, zipWireOptions{localExtra: zipWireExtra(1, zipWireUint64(1, 1))}).bytes, "zip64_invalid", "other"},
		{"ZIP64 sentinel without its extra", "", "", mutate(classic, func(b []byte) {
			binary.LittleEndian.PutUint32(b[classic.central+20:classic.central+24], uint32(zip32Sentinel))
		}), "zip64_invalid", "other"},
		// Extra fields that are not ZIP64.
		{"truncated extra field", "", "", makeZIPWireFixture(t, zipWireOptions{localExtra: []byte{1}}).bytes, "zip_extra_invalid", "other"},
		// Methods, flags and disks the isolation does not read.
		{"unsupported compression method", "", "", makeZIPWireFixture(t, zipWireOptions{method: 12}).bytes, "zip_unsupported_structure", "other"},
		{"encrypted entry", "", "", mutate(deflated, func(b []byte) { b[deflated.central+8] |= 1 }), "zip_unsupported_structure", "other"},
		// Addresses in container metadata.
		{"address in a local extra field", "", "", makeZIPWireFixture(t, zipWireOptions{localExtra: privateExtra}).bytes, "external_address", "other"},
		{"address in a central extra field", "", "", makeZIPWireFixture(t, zipWireOptions{centralExtra: privateExtra}).bytes, "external_address", "other"},
		{"address in the footer comment", "", "", makeZIPWireFixture(t, zipWireOptions{comment: "//localhost/private.mtl"}).bytes, "external_address", "other"},
		// Limits.
		{"more members than the limit", "", "", zipModel(t, many), "zip_entry_limit", "other"},
		{"ZIP64 member count over the limit", "", "", mutate(wide, func(b []byte) {
			binary.LittleEndian.PutUint64(b[wide.zip64End+24:wide.zip64End+32], maxArchiveFiles+1)
			binary.LittleEndian.PutUint64(b[wide.zip64End+32:wide.zip64End+40], maxArchiveFiles+1)
		}), "zip_entry_limit", "other"},
		{"member declares more than the expansion bound", "", "", mutate(stored, setStored(22, 24, 3<<29)), "zip_size_limit", "other"},
		// Names.
		{"parent directory traversal", "", "", zipModel(t, map[string]string{"../evil.txt": "x"}), "zip_unsafe_path", "other"},
		{"absolute member path", "", "", zipModel(t, map[string]string{"/abs.txt": "x"}), "zip_unsafe_path", "other"},
		{"two names normalize to one", "", "", zipModel(t, map[string]string{"a.txt": "x", "./a.txt": "y"}), "zip_unsafe_path", "other"},
		// Nested archives.
		{"member named like an archive", "", "", zipModel(t, map[string]string{"inner.zip": "x"}), "zip_nested_archive", "other"},
		{"member holding an archive", "", "", zipModel(t, map[string]string{"data.bin": string(classic.bytes)}), "zip_nested_archive", "other"},
		// XML members.
		{"XML that is not well formed", "", "", zipModel(t, map[string]string{"3D/3dmodel.model": `<model ` + xmlns + `><open>`}), "xml_invalid", "other"},
		{"XML with a DTD", "", "", zipModel(t, map[string]string{"3D/3dmodel.model": `<!DOCTYPE model [<!ENTITY x "y">]><model ` + xmlns + `/>`}), "xml_dtd", "other"},
		{"relationship to an external target", "", "", zipModel(t, map[string]string{"_rels/.rels": `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Target="x" Id="r" TargetMode="External"/></Relationships>`}), "xml_external_resource", "other"},
		{"address in an XML attribute", "", "", zipModel(t, map[string]string{"3D/3dmodel.model": `<model ` + xmlns + ` texture="https://private.example/t.png"/>`}), "external_address", "other"},
		{"address in XML text", "", "", zipModel(t, map[string]string{"3D/3dmodel.model": `<model ` + xmlns + `><metadata name="a">https://private.example/t.png</metadata></model>`}), "external_address", "other"},
		{"address in an XML comment", "", "", zipModel(t, map[string]string{"3D/3dmodel.model": `<model ` + xmlns + `><!-- https://private.example/t.png --></model>`}), "external_address", "other"},
		// Member bytes that do not match their record.
		{"member CRC differs", "", "", mutate(stored, setStored(14, 16, 1)), "zip_member_corrupt", "other"},
		{"member shorter than declared", "", "", mutate(stored, setStored(22, 24, storedLength+10)), "zip_member_corrupt", "unexpected_eof"},
		{"corrupt deflate stream", "", "", mutate(deflated, func(b []byte) { b[deflated.data] = 0x07 }), "zip_member_corrupt", "other"},
	} {
		t.Run(test.name, func(t *testing.T) {
			variant, mediaType := test.variant, test.mediaType
			if variant == "" {
				variant, mediaType = "zip", "application/zip"
			}
			diagnostic, rejected := archiveDiagnostic(t, test.body, variant, mediaType)
			if !rejected {
				t.Fatalf("expected %s, but the archive was accepted", test.reason)
			}
			if diagnostic.Stage != "inspect" || diagnostic.Reason != test.reason || diagnostic.Errno != test.errno {
				t.Fatalf("diagnostic=%+v, want stage=inspect reason=%s errno=%s", diagnostic, test.reason, test.errno)
			}
		})
	}
}

// The read budget is shared by every member of one archive.
func TestArchiveInspectionReadBudgetNamesItsLimit(t *testing.T) {
	reader := archiveInspectionReader{reader: strings.NewReader("data"), work: &archiveInspectionWork{remaining: 0}}
	_, err := reader.Read(make([]byte, 4))
	if got := inspectionReasonOf(err); got != "inspection_work_limit" {
		t.Fatalf("budget exhaustion reason=%q: %v", got, err)
	}
}

// A reader failure that is not a content verdict stays an operational error:
// the kernel retries it instead of ending a paid task on bytes it never judged.
func TestArchiveReadFailuresStayRetryable(t *testing.T) {
	for _, failure := range []error{context.Canceled, context.DeadlineExceeded, io.ErrClosedPipe} {
		classified := archiveMemberReadFailure(failure)
		if inspectionReasonOf(classified) != "" {
			t.Fatalf("operational failure %v was turned into a content verdict", failure)
		}
		if !errors.Is(classified, failure) {
			t.Fatalf("operational failure %v lost its identity", failure)
		}
	}
}

// Shapes real 3MF writers produce. Each is accepted today; the fixtures keep it
// so a stricter container rule cannot silently reject a writer's output.
//
// The base64 bodies are Python zipfile output for a small three-part package:
// writestr, a seekable open("w"), open("w", force_zip64=True) for the model part
// (a ZIP64 local extra with 32-bit central sizes, which some writers produce),
// and an unseekable stream that forces data descriptors, with and without ZIP64.
var legitimateWriterFixtures = map[string]string{
	"python_default":      "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",
	"python_open":         "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",
	"python_zip64":        "UEsDBBQAAAAIAAAAIQAiPklNxAAAAD4BAAATAAAAW0NvbnRlbnRfVHlwZXNdLnhtbIWQzUrEQBCE7z7F0FdJJiqISJI9uOsTrA/QTDo/ONMzzHSW9e3t7CqCCB6rq7o+qHZ3Dt6cKJclcgd3dQOG2MVh4amDt+Nr9QS7/qY9fiQqRrNcOphF0rO1xc0UsNQxEaszxhxQVObJJnTvOJG9b5pH6yILsVSydUDf7mnE1Ys5nPV85WbyBczLNbixOsCU/OJQ1LcnHn5Rqi9CrZ+XTJmXVG41APZPQogD+X8Q4af2YQjI64hO1qxTVKq3gm+AvezRfwJQSwMEFAAAAAgAAAAhAPeGIM+xAAAAAwEAAAsAAABfcmVscy8ucmVsc2WPSwrCMBCG954izN5Oa0FEmnYjglupBwjJNC02D5JU9PZGQVDcDAwz3/9ouruZ2Y1CnJzlUBUlMLLSqclqDpf+uN5B166aM80i5Zc4Tj6yzNjIYUzJ7xGjHMmIWDhPNl8GF4xIeQ0avZBXoQk3ZbnF8K0B7Y8m60XQlDhgfcBaGadoLt4T2ElxyGxO1j88/dmaSQYX3ZAK6cyLFXYZhExLyB2yc1VjWX00AdsGf8q0T1BLAwQtAAAACAAAACEA6nIDjf//////////EAAUADNELzNkbW9kZWwubW9kZWwBABAAdgwAAAAAAAA4AwAAAAAAAG3WTXKbYAyA4X1PwbDxKgZJ4J8MdqabXqDNAQgmDR1+PIA9Tk9f4k9S0pF2TCQc+3kZoHi6dW10rcepGfrDCtbpKqr7ajg1/e/D6vnXj4fd6un4reiGU91Gl76ZD3HXtG3T1XM9xtFy9mNbLrtx3T88/7z/oZ8O8ds8nx+TZKre6q6c1l1TjcM0vM7raugSOnVlf3ktq/kyLv8nqYaxTjCFPEkxPhbLR5enci6jvuzqQ/z9fG6bqpyXLxgfX5vbclJdJLJ0LMZ6Gi5jVU/HYnj5U1dz1JwOMcTR/H5eTr9/8/unTm/HYvmlc3Pf/Tiqb9HtEKfrPI7el1PWuBz8PcS4huUo+boDvIOyQ3YHeYdkJ7M7xDuZ7OR2J+OdXHY2difnnY3sbO3Ohne2srOzO1ve2cnO3u7seGcvO5Dapb0gproFDqNaKzZ42sIN6g0OOIg4KDk45iDooOrgsIO4g8KDIw9CD2oPDj6IPig/OP4gAUALgJMApAFoBHQigFRArYBOBZQK+HnJOxVQL3qtgN5lLxVQK6BTAaUCagV0KqBUQK2ATgWUCqgV0KmAUgG1AjoVUCqgVkCnAkoF1ArkVECpQFqBnAokFUgrkFOBpAJ93nucCqR3H61A3v1HKpBWIKcCSQXSCuRUIKlAWoGcCiQVSCuQU4GkAmkFciqQVCCtkDkVSCpkWiHTCsnnc2Aem+X51X49jK6wPBXi6Ir358iVlqYfZ/03hzDHMCczxzCnMM/MnMI8C/PczLMwz8N8Y+Z5mG/CfGvmmzDfhvnOzLdhvgvzvZnvwnwf5pCahT0DpbwBlkgMGREcRWYEdgQLCSwJTAnWEhgTWBMsJ7AnMChYUWBSYFOwqMCqwKxgXYFhgWXB0gLbAuOixQXWRdZFq4usi3KJWl2Ui5R10blMWRdZF60usi6yLlpdZF1kXbS6yLrIumh1kXWRddHqIusi66LVRdZF1iWri6xLrEtWl1iXWJesLrEuyT3A6pLcBViXnPsA6xLrktUl1iXWJatLrEusS1aXWJdYl6wusS6xLt11ky+3yCS8RifhTXs5+PL2/XJp2tOxaOa6i8I8vIh/fATPkvvb+PEfUEsBAhQDFAAAAAgAAAAhACI+SU3EAAAAPgEAABMAAAAAAAAAAAAAAIABAAAAAFtDb250ZW50X1R5cGVzXS54bWxQSwECFAMUAAAACAAAACEA94Ygz7EAAAADAQAACwAAAAAAAAAAAAAAgAH1AAAAX3JlbHMvLnJlbHNQSwECLQMtAAAACAAAACEA6nIDjTgDAAB2DAAAEAAAAAAAAAAAAAAAgAHPAQAAM0QvM2Rtb2RlbC5tb2RlbFBLBQYAAAAAAwADALgAAABJBQAAAAA=",
	"python_stream":       "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",
	"python_stream_zip64": "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",
}

func TestLegitimate3MFWriterOutputIsDelivered(t *testing.T) {
	for name, encoded := range legitimateWriterFixtures {
		t.Run(name, func(t *testing.T) {
			body, err := base64.StdEncoding.DecodeString(encoded)
			if err != nil {
				t.Fatal(err)
			}
			checkFormatSlot(t, body, "converted.3mf", "application/octet-stream", "")
		})
	}
	// Go's archive/zip streams every entry with a data descriptor.
	checkFormatSlot(t, zipModel(t, standard3MFEntries()), "converted.3mf", "application/octet-stream", "")
}

func syntheticTexture(width, height int, seed uint64) *image.NRGBA {
	random := rand.New(rand.NewPCG(seed, 386))
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			noise := uint8(random.IntN(24))
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(x*255/width) + noise, G: uint8(y*255/height) + noise, B: uint8((x+y)*255/(width+height)) + noise, A: 255})
		}
	}
	return img
}

// A textured, production-extension 3MF shaped like the export the Blender 3MF
// add-on documents: base colour, roughness and metallic maps as images (PNG and
// JPEG, stored), a thumbnail, production UUIDs, triangle sets and slicer metadata.
func TestTexturedProductionExtension3MFIsDelivered(t *testing.T) {
	encode := func(img image.Image, jpegOut bool) []byte {
		var out bytes.Buffer
		if jpegOut {
			if err := jpeg.Encode(&out, img, &jpeg.Options{Quality: 90}); err != nil {
				t.Fatal(err)
			}
		} else if err := png.Encode(&out, img); err != nil {
			t.Fatal(err)
		}
		return out.Bytes()
	}
	gray := image.NewGray(image.Rect(0, 0, 64, 64))
	gray16 := image.NewGray16(image.Rect(0, 0, 64, 64))
	for index := 0; index < 64*64; index++ {
		gray.Pix[index] = uint8(index)
		gray16.Pix[2*index], gray16.Pix[2*index+1] = uint8(index>>4), uint8(index)
	}
	var model strings.Builder
	model.WriteString(`<?xml version='1.0' encoding='UTF-8'?>` + "\n")
	model.WriteString(`<model unit="millimeter" xml:lang="en-US" xmlns="http://schemas.microsoft.com/3dmanufacturing/core/2015/02" xmlns:m="http://schemas.microsoft.com/3dmanufacturing/material/2015/02" xmlns:p="http://schemas.microsoft.com/3dmanufacturing/production/2015/06" xmlns:t="http://schemas.3mf.io/3dmanufacturing/triangleset/2021/07" requiredextensions="p m t">`)
	model.WriteString(`<metadata name="Application">Blender 4.4.3</metadata><resources>`)
	model.WriteString(`<m:texture2d id="1" path="/3D/Textures/basecolor.jpg" contenttype="image/jpeg"/><m:texture2d id="2" path="/3D/Textures/roughness.png" contenttype="image/png"/><m:texture2d id="3" path="/3D/Textures/metallic.png" contenttype="image/png"/>`)
	model.WriteString(`<m:pbmetallictexturedisplayproperties id="4" basecolortextureid="1" roughnesstextureid="2" metallictextureid="3"/>`)
	model.WriteString(`<object id="5" type="model" p:UUID="00000001-61cb-4c03-9d28-80fed5dfa1dc"><mesh><vertices>`)
	for index := 0; index < 200; index++ {
		fmt.Fprintf(&model, `<vertex x="%d.5" y="%d.25" z="%d.125"/>`, index%97, index%89, index%83)
	}
	model.WriteString(`</vertices><triangles>`)
	for index := 0; index+2 < 200; index++ {
		fmt.Fprintf(&model, `<triangle v1="%d" v2="%d" v3="%d"/>`, index, index+1, index+2)
	}
	model.WriteString(`</triangles><t:trianglesets><t:triangleset name="face_0" identifier="face_0"><t:triangleref index="0"/></t:triangleset></t:trianglesets></mesh></object>`)
	model.WriteString(`</resources><build p:UUID="2c7c17d8-22b5-4d84-8835-1976022ea369"><item objectid="5" p:UUID="00000002-b1ec-4553-aec9-835e5b724bb4" transform="1 0 0 0 1 0 0 0 1 0 0 0" printable="1"/></build></model>`)
	parts := []struct {
		name string
		body []byte
	}{
		{"[Content_Types].xml", []byte(`<?xml version='1.0' encoding='UTF-8'?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="model" ContentType="application/vnd.ms-package.3dmanufacturing-3dmodel+xml"/><Default Extension="png" ContentType="image/png"/><Default Extension="jpg" ContentType="image/jpeg"/><Default Extension="config" ContentType="text/xml"/></Types>`)},
		{"_rels/.rels", []byte(`<?xml version='1.0' encoding='UTF-8'?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Target="/3D/3dmodel.model" Id="rel0" Type="http://schemas.microsoft.com/3dmanufacturing/2013/01/3dmodel"/><Relationship Target="/Metadata/thumbnail.png" Id="rel1" Type="http://schemas.openxmlformats.org/package/2006/relationships/metadata/thumbnail"/></Relationships>`)},
		{"3D/_rels/3dmodel.model.rels", []byte(`<?xml version='1.0' encoding='UTF-8'?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Target="/3D/Textures/basecolor.jpg" Id="rel1" Type="http://schemas.microsoft.com/3dmanufacturing/2013/01/3dmodeltexture"/><Relationship Target="/3D/Textures/roughness.png" Id="rel2" Type="http://schemas.microsoft.com/3dmanufacturing/2013/01/3dmodeltexture"/><Relationship Target="/3D/Textures/metallic.png" Id="rel3" Type="http://schemas.microsoft.com/3dmanufacturing/2013/01/3dmodeltexture"/></Relationships>`)},
		{"3D/3dmodel.model", []byte(model.String())},
		{"3D/Textures/basecolor.jpg", encode(syntheticTexture(64, 64, 1), true)},
		{"3D/Textures/roughness.png", encode(gray, false)},
		{"3D/Textures/metallic.png", encode(gray16, false)},
		{"Metadata/thumbnail.png", encode(syntheticTexture(32, 32, 2), false)},
		{"Metadata/Slic3r_PE.config", []byte("; generated by Blender\n; filament_colour = #FFFFFF;#FF0000\n")},
	}
	var body bytes.Buffer
	archive := zip.NewWriter(&body)
	for _, part := range parts {
		method := zip.Deflate
		if strings.HasPrefix(part.name, "3D/Textures/") || part.name == "Metadata/thumbnail.png" {
			method = zip.Store
		}
		entry, err := archive.CreateHeader(&zip.FileHeader{Name: part.name, Method: method})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(part.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	checkFormatSlot(t, body.Bytes(), "converted.3mf", "application/octet-stream", "")
}
