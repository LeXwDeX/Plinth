package local

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"encoding/binary"
	"hash/crc32"
	"io"
	"strings"
	"testing"
)

// #386: Tripo's 3MF conversion declares the vendor namespace
// https://www.tripo3d.ai/ns/aigc/1.0 on the model root and carries one
// <metadata name="customXMLNS0:AIGC"> label under it. The isolation only knows
// the 3MF, OPC and slicer vocabularies, so it refused every real Tripo 3MF
// (observed 2026-11-19 on the 35,952,441-byte result of a real conversion:
// "模型 XML 含外部地址", reason external_address). The vendor identifier is
// listed now, as an exact string like the others: the same host with another
// path, scheme, port or query, and any other vendor, stay refused.
//
// The package below is built like the real one: the three parts it holds, in
// its order; the namespace set of its model root; the label with placeholder
// values; and the ZIP64 framing Tripo's writer uses for every member, however
// small it is (sizes in the 32-bit fields are the sentinel, the real sizes are
// in a ZIP64 extra field, the end record points to a ZIP64 end record).

const (
	tripoAIGCNamespace = "https://www.tripo3d.ai/ns/aigc/1.0"

	// The label is a JSON document, written escaped as the text of one metadata
	// element. Every value is a placeholder.
	tripoAIGCLabel = `{&quot;Label&quot;:&quot;1&quot;,&quot;ContentProducer&quot;:&quot;PLACEHOLDER-PRODUCER&quot;,&quot;ProduceID&quot;:&quot;00000000-0000-4000-8000-000000000001&quot;,&quot;ReservedCode1&quot;:&quot;&quot;,&quot;ContentPropagator&quot;:&quot;PLACEHOLDER-PRODUCER&quot;,&quot;PropagateID&quot;:&quot;00000000-0000-4000-8000-000000000002&quot;,&quot;ReservedCode2&quot;:&quot;&quot;}`

	tripoContentTypes = `<?xml version="1.0" encoding="utf-8"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
	<Default Extension="jpeg" ContentType="image/jpeg"/>
	<Default Extension="jpg" ContentType="image/jpeg"/>
	<Default Extension="model" ContentType="application/vnd.ms-package.3dmanufacturing-3dmodel+xml"/>
	<Default Extension="png" ContentType="image/png"/>
	<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
	<Default Extension="texture" ContentType="application/vnd.ms-package.3dmanufacturing-3dmodeltexture"/>
</Types>
`
	tripoRelationships = `<?xml version="1.0" encoding="utf-8"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
	<Relationship Type="http://schemas.microsoft.com/3dmanufacturing/2013/01/3dmodel" Target="/3D/3dmodel.model" Id="rel0"/>
</Relationships>
`
)

// tripoStyleModel is the model part with the namespace the vendor label is
// declared under; one object holds a tetrahedron.
func tripoStyleModel(aigcNamespace string) string {
	const gm = "http://schemas.microsoft.com/3dmanufacturing/"
	const tm = "http://schemas.3mf.io/3dmanufacturing/"
	return `<?xml version="1.0" encoding="utf-8"?>
<model xmlns="` + gm + `core/2015/02" unit="millimeter" xml:lang="en-US"` +
		` xmlns:m="` + gm + `material/2015/02" xmlns:p="` + gm + `production/2015/06"` +
		` xmlns:b="` + gm + `beamlattice/2017/02" xmlns:s="` + gm + `slice/2015/07"` +
		` xmlns:t="` + gm + `trianglesets/2021/07" xmlns:sc="` + gm + `securecontent/2019/04"` +
		` xmlns:v="` + tm + `volumetric/2022/01" xmlns:i="` + tm + `implicit/2023/12"` +
		` xmlns:customXMLNS0="` + aigcNamespace + `">
	<metadata name="customXMLNS0:AIGC" preserve="1">` + tripoAIGCLabel + `</metadata>
	<resources>
		<object id="1" name="tripo_node_00000000-0000-4000-8000-000000000003" type="model" p:UUID="00000000-0000-4000-8000-000000000004">
			<mesh>
				<vertices>
					<vertex x="0.000000" y="0.000000" z="0.000000" />
					<vertex x="10.000000" y="0.000000" z="0.000000" />
					<vertex x="0.000000" y="10.000000" z="0.000000" />
					<vertex x="0.000000" y="0.000000" z="10.000000" />
				</vertices>
				<triangles>
					<triangle v1="0" v2="2" v3="1" />
					<triangle v1="0" v2="1" v3="3" />
					<triangle v1="1" v2="2" v3="3" />
					<triangle v1="0" v2="3" v3="2" />
				</triangles>
			</mesh>
		</object>
	</resources>
	<build p:UUID="00000000-0000-4000-8000-000000000005">
		<item objectid="1" p:UUID="00000000-0000-4000-8000-000000000006"/>
	</build>
</model>
`
}

type zip64Member struct {
	name string
	data string
}

// tripoStylePackage builds the three-part package. model is the model part's
// text; the order of the members is the one the real file has.
func tripoStylePackage(t *testing.T, model string) []byte {
	t.Helper()
	return zip64Package(t, []zip64Member{
		{"3D/3dmodel.model", model},
		{"[Content_Types].xml", tripoContentTypes},
		{"_rels/.rels", tripoRelationships},
	})
}

// zip64Package writes members the way the ZIP writer of Tripo's converter
// frames them, which archive/zip cannot produce for a small member (it only
// writes ZIP64 fields past 4 GiB): Deflate, no data descriptor, zero DOS time
// and date, and for every member
//
//   - a local header whose two 32-bit sizes are 0xffffffff, with a ZIP64 extra
//     field holding the expanded and compressed sizes;
//   - a central directory entry whose sizes and local header offset are
//     0xffffffff, with a ZIP64 extra field holding the three values;
//
// then a ZIP64 end record and locator, and an end record whose directory offset
// is 0xffffffff while its size and count are real.
func zip64Package(t *testing.T, members []zip64Member) []byte {
	t.Helper()
	le := binary.LittleEndian
	type placed struct {
		name                 string
		crc                  uint32
		compressed, expanded uint64
		offset               uint64
	}
	var out bytes.Buffer
	var placedMembers []placed
	for _, member := range members {
		var deflated bytes.Buffer
		writer, err := flate.NewWriter(&deflated, flate.DefaultCompression)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(writer, member.data); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		entry := placed{
			name: member.name, crc: crc32.ChecksumIEEE([]byte(member.data)),
			compressed: uint64(deflated.Len()), expanded: uint64(len(member.data)), offset: uint64(out.Len()),
		}
		header := make([]byte, 30)
		le.PutUint32(header[0:], zipLocalSignature)
		le.PutUint16(header[4:], 45) // version needed
		le.PutUint16(header[8:], zip.Deflate)
		le.PutUint32(header[14:], entry.crc)
		le.PutUint32(header[18:], uint32(zip32Sentinel))
		le.PutUint32(header[22:], uint32(zip32Sentinel))
		le.PutUint16(header[26:], uint16(len(member.name)))
		extra := zipWireExtra(1, zipWireUint64(entry.expanded, entry.compressed))
		le.PutUint16(header[28:], uint16(len(extra)))
		out.Write(header)
		out.WriteString(member.name)
		out.Write(extra)
		out.Write(deflated.Bytes())
		placedMembers = append(placedMembers, entry)
	}
	directory := uint64(out.Len())
	for _, entry := range placedMembers {
		header := make([]byte, 46)
		le.PutUint32(header[0:], zipCentralSignature)
		le.PutUint16(header[4:], 45) // version made by (MS-DOS)
		le.PutUint16(header[6:], 45) // version needed
		le.PutUint16(header[10:], zip.Deflate)
		le.PutUint32(header[16:], entry.crc)
		le.PutUint32(header[20:], uint32(zip32Sentinel))
		le.PutUint32(header[24:], uint32(zip32Sentinel))
		le.PutUint16(header[28:], uint16(len(entry.name)))
		extra := zipWireExtra(1, zipWireUint64(entry.expanded, entry.compressed, entry.offset))
		le.PutUint16(header[30:], uint16(len(extra)))
		le.PutUint32(header[38:], 0x80) // external attributes
		le.PutUint32(header[42:], uint32(zip32Sentinel))
		out.Write(header)
		out.WriteString(entry.name)
		out.Write(extra)
	}
	size := uint64(out.Len()) - directory
	zip64End := uint64(out.Len())
	record := make([]byte, 56)
	le.PutUint32(record[0:], zip64EndSignature)
	le.PutUint64(record[4:], 44)
	le.PutUint16(record[12:], 45)
	le.PutUint16(record[14:], 45)
	le.PutUint64(record[24:], uint64(len(members)))
	le.PutUint64(record[32:], uint64(len(members)))
	le.PutUint64(record[40:], size)
	le.PutUint64(record[48:], directory)
	out.Write(record)
	locator := make([]byte, 20)
	le.PutUint32(locator[0:], zip64LocatorSignature)
	le.PutUint64(locator[8:], zip64End)
	le.PutUint32(locator[16:], 1)
	out.Write(locator)
	end := make([]byte, 22)
	le.PutUint32(end[0:], zipEndSignature)
	le.PutUint16(end[8:], uint16(len(members)))
	le.PutUint16(end[10:], uint16(len(members)))
	le.PutUint32(end[12:], uint32(size))
	le.PutUint32(end[16:], uint32(zip32Sentinel))
	out.Write(end)
	return out.Bytes()
}

// The helper itself must produce what it claims to, or the acceptance below
// would not say anything about the real framing: archive/zip reads the package
// back, and every member is framed with the sentinel sizes and a ZIP64 extra.
func TestTripoStylePackageIsFramedLikeTheRealOne(t *testing.T) {
	body := tripoStylePackage(t, tripoStyleModel(tripoAIGCNamespace))
	archive, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range archive.File {
		names = append(names, entry.Name)
		if entry.Method != zip.Deflate || entry.Flags != 0 {
			t.Errorf("%s: method %d flags %#x, want Deflate with no data descriptor", entry.Name, entry.Method, entry.Flags)
		}
		if entry.UncompressedSize64 == 0 || entry.CompressedSize64 == 0 {
			t.Errorf("%s: sizes %d/%d were not resolved from the ZIP64 extra field", entry.Name, entry.UncompressedSize64, entry.CompressedSize64)
		}
		content, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.Copy(io.Discard, content); err != nil {
			t.Errorf("%s: %v", entry.Name, err)
		}
		_ = content.Close()
		// The central directory's own 32-bit fields are the sentinel; the sizes
		// only exist in the extra field.
		if !bytes.Contains(entry.Extra, []byte{1, 0, 24, 0}) {
			t.Errorf("%s: the central directory entry carries no ZIP64 extra field: % x", entry.Name, entry.Extra)
		}
	}
	if strings.Join(names, ",") != "3D/3dmodel.model,[Content_Types].xml,_rels/.rels" {
		t.Errorf("members = %v", names)
	}
	// Every local header is framed the same way: the sentinel in both 32-bit
	// sizes and a 20-byte ZIP64 extra field after the name.
	for _, entry := range archive.File {
		dataOffset, err := entry.DataOffset()
		if err != nil {
			t.Fatal(err)
		}
		start := dataOffset - 30 - int64(len(entry.Name)) - 20
		header := body[start : start+30]
		if binary.LittleEndian.Uint32(header[0:4]) != zipLocalSignature ||
			binary.LittleEndian.Uint32(header[18:22]) != uint32(zip32Sentinel) || binary.LittleEndian.Uint32(header[22:26]) != uint32(zip32Sentinel) ||
			binary.LittleEndian.Uint16(header[28:30]) != 20 {
			t.Errorf("%s: local header % x is not framed with the sentinel sizes and a 20-byte ZIP64 extra field", entry.Name, header)
		}
	}
	end := body[len(body)-22:]
	if binary.LittleEndian.Uint32(end[16:20]) != uint32(zip32Sentinel) || binary.LittleEndian.Uint16(end[10:12]) != 3 {
		t.Errorf("end record %x: want the sentinel directory offset and three entries", end)
	}
}

func TestTripoAIGC3MFIsAccepted(t *testing.T) {
	body := tripoStylePackage(t, tripoStyleModel(tripoAIGCNamespace))
	// Stored at ingest and delivered again from the stored blob.
	checkFormatSlot(t, body, "converted.3mf", "application/octet-stream", "")
	checkFormatSlot(t, body, "converted.3mf", "binary/octet-stream", "")
	checkFormatSlot(t, body, "3mf", "model/3mf", "")
	// The check the real file was run through.
	assertIsolationBoundary(t, body, "converted.3mf", "binary/octet-stream", true)
}

func TestTripoAIGC3MFStillRefusesEveryOtherAddress(t *testing.T) {
	for name, namespace := range map[string]string{
		"unlisted vendor":            "https://evil.example/ns",
		"unlisted vendor, same path": "https://evil.example/ns/aigc/1.0",
		"other version":              "https://www.tripo3d.ai/ns/aigc/2.0",
		"other path":                 "https://www.tripo3d.ai/ns/aigc",
		"suffix":                     "https://www.tripo3d.ai/ns/aigc/1.0/https://evil.example/x",
		"trailing slash":             "https://www.tripo3d.ai/ns/aigc/1.0/",
		"other case":                 "https://www.tripo3d.ai/NS/AIGC/1.0",
		"query":                      "https://www.tripo3d.ai/ns/aigc/1.0?sig=secret",
		"empty query":                "https://www.tripo3d.ai/ns/aigc/1.0?",
		"fragment":                   "https://www.tripo3d.ai/ns/aigc/1.0#x",
		"port":                       "https://www.tripo3d.ai:8443/ns/aigc/1.0",
		"default port":               "https://www.tripo3d.ai:443/ns/aigc/1.0",
		"user information":           "https://user@www.tripo3d.ai/ns/aigc/1.0",
		"plain http":                 "http://www.tripo3d.ai/ns/aigc/1.0",
		"apex host":                  "https://tripo3d.ai/ns/aigc/1.0",
		"sibling host":               "https://api.tripo3d.ai/ns/aigc/1.0",
		"look-alike host":            "https://www.tripo3d.ai.example/ns/aigc/1.0",
		"look-alike suffix host":     "https://evil-www.tripo3d.ai/ns/aigc/1.0",
		"percent-encoded path":       "https://www.tripo3d.ai/ns%2Faigc/1.0",
		"relative network address":   "//www.tripo3d.ai/ns/aigc/1.0",
		"host without a domain":      "https://localhost/ns/aigc/1.0",
	} {
		t.Run(name, func(t *testing.T) {
			if namespace == tripoAIGCNamespace {
				t.Fatal("fixture is the listed identifier")
			}
			checkFormatSlot(t, tripoStylePackage(t, tripoStyleModel(namespace)), "converted.3mf", "application/octet-stream", "external_address")
		})
	}
}

// The identifier is a namespace name. Anywhere else, including the label that
// sits under the namespace, an address is refused as before.
func TestTripoAIGC3MFRefusesAddressesOutsideNamespaceDeclarations(t *testing.T) {
	model := tripoStyleModel(tripoAIGCNamespace)
	for name, change := range map[string]func(string) string{
		"the identifier as the label text": func(model string) string {
			return strings.Replace(model, tripoAIGCLabel, tripoAIGCNamespace, 1)
		},
		"an address in the label": func(model string) string {
			return strings.Replace(model, "PLACEHOLDER-PRODUCER", "http://evil.example/x", 1)
		},
		"the identifier as an attribute value": func(model string) string {
			return strings.Replace(model, `<metadata name="customXMLNS0:AIGC"`, `<metadata name="customXMLNS0:AIGC" href="`+tripoAIGCNamespace+`"`, 1)
		},
		"a second vendor namespace": func(model string) string {
			return strings.Replace(model, ` xmlns:customXMLNS0=`, ` xmlns:other="https://evil.example/ns" xmlns:customXMLNS0=`, 1)
		},
		"the identifier in a comment": func(model string) string {
			return strings.Replace(model, "<resources>", "<!-- "+tripoAIGCNamespace+" --><resources>", 1)
		},
	} {
		t.Run(name, func(t *testing.T) {
			changed := change(model)
			if changed == model {
				t.Fatal("fixture was not changed")
			}
			checkFormatSlot(t, tripoStylePackage(t, changed), "converted.3mf", "application/octet-stream", "external_address")
		})
	}
}
