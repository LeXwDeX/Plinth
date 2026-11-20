package fakeupstream

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"encoding/xml"
	"io"
	"sort"
	"strings"
	"testing"
)

// #386: the 3MF the Tripo fake delivers has the shape of the real one (see
// tripo3mf.go), so the delivery checks meet what only Tripo's writer produces.
// The facts below are those of the real file of 2026-11-19, kept as literals, not
// read back from the builder.

func tripoPackageBytes(t *testing.T) []byte {
	t.Helper()
	file, err := ModelFile("3mf:tripo")
	if err != nil {
		t.Fatal(err)
	}
	return file.Data
}

func TestTripoStyle3MFIsFramedLikeTheRealFile(t *testing.T) {
	body := tripoPackageBytes(t)
	archive, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range archive.File {
		names = append(names, entry.Name)
		if entry.Method != zip.Deflate || entry.Flags != 0 {
			t.Errorf("%s: method %d, flags %#x; want Deflate and no data descriptor", entry.Name, entry.Method, entry.Flags)
		}
		// The central directory entry holds the sentinel sizes and offset and a
		// ZIP64 extra field of the three values (tag 1, 24 bytes).
		if !bytes.HasPrefix(entry.Extra, []byte{1, 0, 24, 0}) || len(entry.Extra) != 28 {
			t.Errorf("%s: central directory extra field % x, want one 24-byte ZIP64 field", entry.Name, entry.Extra)
		}
		// The local header: both 32-bit sizes are the sentinel, a 20-byte ZIP64
		// extra field (tag 1, 16 bytes) follows the name.
		dataOffset, err := entry.DataOffset()
		if err != nil {
			t.Fatal(err)
		}
		start := dataOffset - 30 - int64(len(entry.Name)) - 20
		header := body[start : start+30]
		extra := body[start+30+int64(len(entry.Name)) : dataOffset]
		if binary.LittleEndian.Uint32(header[0:4]) != 0x04034b50 || binary.LittleEndian.Uint16(header[4:6]) != 45 ||
			binary.LittleEndian.Uint32(header[18:22]) != 0xffffffff || binary.LittleEndian.Uint32(header[22:26]) != 0xffffffff ||
			binary.LittleEndian.Uint16(header[10:12]) != 0 || binary.LittleEndian.Uint16(header[12:14]) != 0 {
			t.Errorf("%s: local header % x", entry.Name, header)
		}
		if !bytes.HasPrefix(extra, []byte{1, 0, 16, 0}) || len(extra) != 20 ||
			binary.LittleEndian.Uint64(extra[4:12]) != entry.UncompressedSize64 || binary.LittleEndian.Uint64(extra[12:20]) != entry.CompressedSize64 {
			t.Errorf("%s: local extra field % x", entry.Name, extra)
		}
	}
	if strings.Join(names, ",") != "3D/3dmodel.model,[Content_Types].xml,_rels/.rels" {
		t.Errorf("members = %v", names)
	}

	// The footer: end record (directory offset is the sentinel, size and count
	// are real), the locator and the ZIP64 end record right before it.
	end := body[len(body)-22:]
	locator := body[len(body)-42 : len(body)-22]
	if binary.LittleEndian.Uint32(end[0:4]) != 0x06054b50 || binary.LittleEndian.Uint16(end[10:12]) != 3 ||
		binary.LittleEndian.Uint32(end[16:20]) != 0xffffffff || binary.LittleEndian.Uint32(end[12:16]) == 0xffffffff {
		t.Errorf("end record % x", end)
	}
	zip64End := binary.LittleEndian.Uint64(locator[8:16])
	record := body[zip64End : zip64End+56]
	if binary.LittleEndian.Uint32(locator[0:4]) != 0x07064b50 || binary.LittleEndian.Uint32(locator[16:20]) != 1 ||
		binary.LittleEndian.Uint32(record[0:4]) != 0x06064b50 || binary.LittleEndian.Uint64(record[4:12]) != 44 ||
		binary.LittleEndian.Uint64(record[32:40]) != 3 || zip64End+56 != uint64(len(body)-42) {
		t.Errorf("ZIP64 locator % x, record % x", locator, record)
	}
}

func readPart(t *testing.T, body []byte, name string) []byte {
	t.Helper()
	archive, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range archive.File {
		if entry.Name == name {
			reader, err := entry.Open()
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			data, err := io.ReadAll(reader)
			if err != nil {
				t.Fatal(err)
			}
			return data
		}
	}
	t.Fatalf("no member %s", name)
	return nil
}

func TestTripoStyle3MFHasTheRealModelHeaderAndLabel(t *testing.T) {
	model := readPart(t, tripoPackageBytes(t), "3D/3dmodel.model")
	decoder := xml.NewDecoder(bytes.NewReader(model))
	var path []string
	counts := map[string]int{}
	var rootAttrs map[string]string
	var label string
	var metadataAttrs, objectAttrs, buildAttrs, itemAttrs map[string]string
	// The decoder reports a namespaced attribute with its namespace URL as the
	// space, xmlns declarations with the space "xmlns" and xml:lang with the XML
	// namespace; the names below are the prefixed ones the file spells.
	attrs := func(element xml.StartElement) map[string]string {
		out := map[string]string{}
		for _, attr := range element.Attr {
			name := attr.Name.Local
			switch attr.Name.Space {
			case "":
			case "xmlns":
				name = "xmlns:" + name
			case "http://www.w3.org/XML/1998/namespace":
				name = "xml:" + name
			default:
				name = attr.Name.Space + ":" + name
			}
			out[name] = attr.Value
		}
		return out
	}
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		switch node := token.(type) {
		case xml.StartElement:
			path = append(path, node.Name.Local)
			counts[strings.Join(path, "/")]++
			switch strings.Join(path, "/") {
			case "model":
				rootAttrs = attrs(node)
			case "model/metadata":
				metadataAttrs = attrs(node)
			case "model/resources/object":
				objectAttrs = attrs(node)
			case "model/build":
				buildAttrs = attrs(node)
			case "model/build/item":
				itemAttrs = attrs(node)
			}
		case xml.CharData:
			if strings.Join(path, "/") == "model/metadata" {
				label += string(node)
			}
		case xml.EndElement:
			path = path[:len(path)-1]
		}
	}

	wantRoot := map[string]string{
		"xmlns": "http://schemas.microsoft.com/3dmanufacturing/core/2015/02", "unit": "millimeter", "xml:lang": "en-US",
		"xmlns:m":            "http://schemas.microsoft.com/3dmanufacturing/material/2015/02",
		"xmlns:p":            "http://schemas.microsoft.com/3dmanufacturing/production/2015/06",
		"xmlns:b":            "http://schemas.microsoft.com/3dmanufacturing/beamlattice/2017/02",
		"xmlns:s":            "http://schemas.microsoft.com/3dmanufacturing/slice/2015/07",
		"xmlns:t":            "http://schemas.microsoft.com/3dmanufacturing/trianglesets/2021/07",
		"xmlns:sc":           "http://schemas.microsoft.com/3dmanufacturing/securecontent/2019/04",
		"xmlns:v":            "http://schemas.3mf.io/3dmanufacturing/volumetric/2022/01",
		"xmlns:i":            "http://schemas.3mf.io/3dmanufacturing/implicit/2023/12",
		"xmlns:customXMLNS0": "https://www.tripo3d.ai/ns/aigc/1.0",
	}
	if len(rootAttrs) != len(wantRoot) {
		t.Errorf("root attributes = %v", rootAttrs)
	}
	for name, want := range wantRoot {
		if rootAttrs[name] != want {
			t.Errorf("root %s = %q, want %q", name, rootAttrs[name], want)
		}
	}

	if metadataAttrs["name"] != "customXMLNS0:AIGC" || metadataAttrs["preserve"] != "1" || len(metadataAttrs) != 2 {
		t.Errorf("metadata attributes = %v", metadataAttrs)
	}
	// The label is JSON of seven fields; the fake's values are placeholders, never
	// the real producer or task ids.
	var fields map[string]string
	if err := json.Unmarshal([]byte(label), &fields); err != nil {
		t.Fatalf("label %q: %v", label, err)
	}
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if strings.Join(keys, ",") != "ContentProducer,ContentPropagator,Label,ProduceID,PropagateID,ReservedCode1,ReservedCode2" {
		t.Errorf("label fields = %v", keys)
	}
	if fields["Label"] != "1" || fields["ReservedCode1"] != "" || fields["ReservedCode2"] != "" ||
		fields["ContentProducer"] != fields["ContentPropagator"] || fields["ProduceID"] != fields["PropagateID"] ||
		!strings.HasPrefix(fields["ContentProducer"], "PLACEHOLDER") || !strings.HasPrefix(fields["ProduceID"], "00000000-0000-4000-8000-") {
		t.Errorf("label values = %v", fields)
	}

	const productionUUID = "http://schemas.microsoft.com/3dmanufacturing/production/2015/06:UUID"
	placeholder := func(value string) bool { return strings.HasPrefix(value, "00000000-0000-4000-8000-") }
	if len(objectAttrs) != 4 || objectAttrs["id"] != "1" || objectAttrs["type"] != "model" ||
		!strings.HasPrefix(objectAttrs["name"], "tripo_node_") || !placeholder(strings.TrimPrefix(objectAttrs["name"], "tripo_node_")) || !placeholder(objectAttrs[productionUUID]) {
		t.Errorf("object attributes = %v", objectAttrs)
	}
	if len(buildAttrs) != 1 || !placeholder(buildAttrs[productionUUID]) || len(itemAttrs) != 2 || itemAttrs["objectid"] != "1" || !placeholder(itemAttrs[productionUUID]) {
		t.Errorf("build %v, item %v", buildAttrs, itemAttrs)
	}
	// One object with one mesh, and nothing else in the model: no textures, no
	// components, no second object.
	for path, want := range map[string]int{
		"model": 1, "model/metadata": 1, "model/resources": 1, "model/resources/object": 1, "model/resources/object/mesh": 1,
		"model/resources/object/mesh/vertices": 1, "model/resources/object/mesh/triangles": 1, "model/build": 1, "model/build/item": 1,
	} {
		if counts[path] != want {
			t.Errorf("%s occurs %d times, want %d", path, counts[path], want)
		}
	}
	// The mesh is the fixture's: as many vertices and triangles as the generic
	// package writes.
	generic, err := ModelFile("3mf")
	if err != nil {
		t.Fatal(err)
	}
	genericModel := readPart(t, generic.Data, "3D/3dmodel.model")
	for element, tag := range map[string]string{"model/resources/object/mesh/vertices/vertex": "<vertex ", "model/resources/object/mesh/triangles/triangle": "<triangle "} {
		if want := bytes.Count(genericModel, []byte(tag)); want == 0 || counts[element] != want {
			t.Errorf("%s occurs %d times, want the fixture's %d", element, counts[element], want)
		}
	}
	if !bytes.Contains(model, []byte("\n\t\t\t\t\t<vertex x=\"")) || !bytes.HasSuffix(model, []byte("</model>\n")) {
		t.Error("the model part is not written one tab-indented element per line like the real one")
	}

	// The other two parts are the real ones.
	if got := readPart(t, tripoPackageBytes(t), "[Content_Types].xml"); len(got) != 587 || !bytes.Contains(got, []byte(`Extension="texture"`)) {
		t.Errorf("[Content_Types].xml: %d bytes", len(got))
	}
	if got := readPart(t, tripoPackageBytes(t), "_rels/.rels"); len(got) != 263 || !bytes.Contains(got, []byte(`Target="/3D/3dmodel.model" Id="rel0"`)) {
		t.Errorf("_rels/.rels: %d bytes", len(got))
	}
}

// The generic 3MF fixture, which the Mock provider and the other fakes share,
// stays a plain package: only Tripo's conversion carries Tripo's vendor namespace.
func TestGeneric3MFFixtureStaysPlain(t *testing.T) {
	generic, err := ModelFile("3mf")
	if err != nil {
		t.Fatal(err)
	}
	model := readPart(t, generic.Data, "3D/3dmodel.model")
	if bytes.Contains(model, []byte("tripo3d")) || bytes.Contains(model, []byte("customXMLNS0")) {
		t.Error("the generic 3MF fixture carries Tripo's vendor namespace")
	}
	if bytes.Equal(generic.Data, tripoPackageBytes(t)) {
		t.Error("the Tripo package is the generic fixture")
	}
}
