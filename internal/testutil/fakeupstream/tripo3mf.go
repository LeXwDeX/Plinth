package fakeupstream

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"math"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/providers/mock/modelfiles"
)

// The 3MF package Tripo's POST /models/convert delivers for format 3MF, as the
// real result of 2026-11-19 shows it (#386: a 35,952,441-byte file of 959,045
// vertices and 1,918,081 triangles, the 3MF export of a multiview model). The
// generic fixture (modelfiles.Build3MF, which the Mock provider and the other
// fakes share) is a plain package: no vendor namespace, ordinary ZIP framing. A
// delivery check run against it cannot meet what only Tripo's writer produces,
// and the first real file was refused for exactly that, so the Tripo fake delivers
// this shape instead:
//
//   - three ZIP members in this order: 3D/3dmodel.model, [Content_Types].xml,
//     _rels/.rels. No thumbnail, no core-properties part, no texture, no
//     metadata folder. Deflate, no data descriptor, zero DOS time and date;
//   - every member framed with ZIP64 however small it is: the two 32-bit sizes
//     of the local header are 0xffffffff and a ZIP64 extra field holds the real
//     ones; the central directory entry has the sizes and the local header offset
//     as 0xffffffff with a ZIP64 extra field of the three values; a ZIP64 end
//     record and locator precede an end record whose directory offset is
//     0xffffffff (its size and count are real);
//   - the model root declares the core namespace as the default one, unit
//     "millimeter", xml:lang "en-US", the namespaces m (material), p (production),
//     b (beam lattice), s (slice), t (triangle sets) and sc (secure content) of
//     schemas.microsoft.com, v (volumetric) and i (implicit) of schemas.3mf.io,
//     and the vendor namespace customXMLNS0 = https://www.tripo3d.ai/ns/aigc/1.0;
//   - the first child of the root is <metadata name="customXMLNS0:AIGC"
//     preserve="1"> holding an escaped JSON label of seven fields: Label,
//     ContentProducer, ProduceID, ReservedCode1, ContentPropagator, PropagateID,
//     ReservedCode2. The real label names the producer and the task; every value
//     here is a placeholder;
//   - one <object id="1" name="tripo_node_…" type="model" p:UUID="…"> with a
//     mesh (vertices as x, y and z with six decimals, triangles as v1, v2 and v3,
//     each element on its own tab-indented line) and a <build p:UUID="…"> with
//     one <item objectid="1" p:UUID="…"/>.
//
// The mesh is the fixture's humanoid, scaled to a longest side of 100 millimeters
// (the real model is about that size). What the real file holds beyond that (a
// million vertices, the real ids) is not modelled: the fake promises structure,
// not realism.
const (
	tripoAIGCNamespace = "https://www.tripo3d.ai/ns/aigc/1.0"

	tripoPlaceholderProducer = "PLACEHOLDER-PRODUCER"
	tripoPlaceholderTaskID   = "00000000-0000-4000-8000-000000000386"
	tripoPlaceholderObject   = "00000000-0000-4000-8000-000000000001"
	tripoPlaceholderBuild    = "00000000-0000-4000-8000-000000000002"
	tripoPlaceholderItem     = "00000000-0000-4000-8000-000000000003"

	tripoModelEdge = 100.0 // millimeters

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

// tripoStyle3MF writes the package for mesh.
func tripoStyle3MF(mesh modelfiles.Mesh) ([]byte, error) {
	return zip64Package([]zip64Member{
		{name: "3D/3dmodel.model", data: tripoModelPart(mesh)},
		{name: "[Content_Types].xml", data: tripoContentTypes},
		{name: "_rels/.rels", data: tripoRelationships},
	})
}

// tripoModelPart is the model part: the real file's header and tags around the
// mesh, in the 3MF coordinate system (Z up, as the generic fixture writes it).
func tripoModelPart(mesh modelfiles.Mesh) string {
	const microsoft = "http://schemas.microsoft.com/3dmanufacturing/"
	const consortium = "http://schemas.3mf.io/3dmanufacturing/"
	// The label is JSON written as the text of one element, so its quotes are
	// escaped; the order of its fields is the real one.
	label := fmt.Sprintf(`{&quot;Label&quot;:&quot;1&quot;,&quot;ContentProducer&quot;:&quot;%[1]s&quot;,&quot;ProduceID&quot;:&quot;%[2]s&quot;,`+
		`&quot;ReservedCode1&quot;:&quot;&quot;,&quot;ContentPropagator&quot;:&quot;%[1]s&quot;,&quot;PropagateID&quot;:&quot;%[2]s&quot;,&quot;ReservedCode2&quot;:&quot;&quot;}`,
		tripoPlaceholderProducer, tripoPlaceholderTaskID)

	var model strings.Builder
	model.WriteString(`<?xml version="1.0" encoding="utf-8"?>` + "\n")
	model.WriteString(`<model xmlns="` + microsoft + `core/2015/02" unit="millimeter" xml:lang="en-US"` +
		` xmlns:m="` + microsoft + `material/2015/02" xmlns:p="` + microsoft + `production/2015/06"` +
		` xmlns:b="` + microsoft + `beamlattice/2017/02" xmlns:s="` + microsoft + `slice/2015/07"` +
		` xmlns:t="` + microsoft + `trianglesets/2021/07" xmlns:sc="` + microsoft + `securecontent/2019/04"` +
		` xmlns:v="` + consortium + `volumetric/2022/01" xmlns:i="` + consortium + `implicit/2023/12"` +
		` xmlns:customXMLNS0="` + tripoAIGCNamespace + `">` + "\n")
	model.WriteString("\t" + `<metadata name="customXMLNS0:AIGC" preserve="1">` + label + `</metadata>` + "\n")
	model.WriteString("\t<resources>\n")
	fmt.Fprintf(&model, "\t\t"+`<object id="1" name="tripo_node_%s" type="model" p:UUID="%s">`+"\n", tripoPlaceholderTaskID, tripoPlaceholderObject)
	model.WriteString("\t\t\t<mesh>\n\t\t\t\t<vertices>\n")

	// Z up: the fixture's y becomes z, its z becomes -y (the transform of
	// modelfiles.Build3MF), then the longest side is scaled to tripoModelEdge.
	low, high := [3]float64{math.Inf(1), math.Inf(1), math.Inf(1)}, [3]float64{math.Inf(-1), math.Inf(-1), math.Inf(-1)}
	points := make([][3]float64, mesh.VertexCount())
	for i := range points {
		points[i] = [3]float64{float64(mesh.Position[3*i]), -float64(mesh.Position[3*i+2]), float64(mesh.Position[3*i+1])}
		for axis, value := range points[i] {
			low[axis], high[axis] = math.Min(low[axis], value), math.Max(high[axis], value)
		}
	}
	scale := 1.0
	if longest := math.Max(high[0]-low[0], math.Max(high[1]-low[1], high[2]-low[2])); longest > 0 {
		scale = tripoModelEdge / longest
	}
	for _, point := range points {
		fmt.Fprintf(&model, "\t\t\t\t\t"+`<vertex x="%.6f" y="%.6f" z="%.6f" />`+"\n", point[0]*scale, point[1]*scale, point[2]*scale)
	}
	model.WriteString("\t\t\t\t</vertices>\n\t\t\t\t<triangles>\n")
	for i := 0; i+2 < len(mesh.Index); i += 3 {
		fmt.Fprintf(&model, "\t\t\t\t\t"+`<triangle v1="%d" v2="%d" v3="%d" />`+"\n", mesh.Index[i], mesh.Index[i+1], mesh.Index[i+2])
	}
	model.WriteString("\t\t\t\t</triangles>\n\t\t\t</mesh>\n\t\t</object>\n\t</resources>\n")
	fmt.Fprintf(&model, "\t"+`<build p:UUID="%s">`+"\n\t\t"+`<item objectid="1" p:UUID="%s"/>`+"\n\t</build>\n</model>\n", tripoPlaceholderBuild, tripoPlaceholderItem)
	return model.String()
}

type zip64Member struct {
	name string
	data string
}

const (
	zip32Max = 1<<32 - 1 // the sentinel a 32-bit ZIP field holds when the real value is in a ZIP64 extra field
)

// zip64Package writes members framed as Tripo's converter frames them (see the
// description at the top of this file). archive/zip cannot write this for a
// small member: it only uses ZIP64 fields from 4 GiB on.
func zip64Package(members []zip64Member) ([]byte, error) {
	le := binary.LittleEndian
	extra := func(values ...uint64) []byte {
		out := make([]byte, 4, 4+8*len(values))
		le.PutUint16(out[0:], 1) // the ZIP64 extra field
		le.PutUint16(out[2:], uint16(8*len(values)))
		for _, value := range values {
			out = le.AppendUint64(out, value)
		}
		return out
	}
	type placed struct {
		name                 string
		crc                  uint32
		compressed, expanded uint64
		offset               uint64
	}
	var out bytes.Buffer
	placedMembers := make([]placed, 0, len(members))
	for _, member := range members {
		var deflated bytes.Buffer
		writer, err := flate.NewWriter(&deflated, flate.DefaultCompression)
		if err != nil {
			return nil, err
		}
		if _, err := writer.Write([]byte(member.data)); err != nil {
			return nil, err
		}
		if err := writer.Close(); err != nil {
			return nil, err
		}
		entry := placed{
			name: member.name, crc: crc32.ChecksumIEEE([]byte(member.data)),
			compressed: uint64(deflated.Len()), expanded: uint64(len(member.data)), offset: uint64(out.Len()),
		}
		header := make([]byte, 30)
		le.PutUint32(header[0:], 0x04034b50)
		le.PutUint16(header[4:], 45) // version needed to extract
		le.PutUint16(header[8:], 8)  // Deflate
		le.PutUint32(header[14:], entry.crc)
		le.PutUint32(header[18:], zip32Max)
		le.PutUint32(header[22:], zip32Max)
		le.PutUint16(header[26:], uint16(len(member.name)))
		local := extra(entry.expanded, entry.compressed)
		le.PutUint16(header[28:], uint16(len(local)))
		out.Write(header)
		out.WriteString(member.name)
		out.Write(local)
		out.Write(deflated.Bytes())
		placedMembers = append(placedMembers, entry)
	}
	directory := uint64(out.Len())
	for _, entry := range placedMembers {
		header := make([]byte, 46)
		le.PutUint32(header[0:], 0x02014b50)
		le.PutUint16(header[4:], 45) // version made by, MS-DOS
		le.PutUint16(header[6:], 45) // version needed to extract
		le.PutUint16(header[10:], 8)
		le.PutUint32(header[16:], entry.crc)
		le.PutUint32(header[20:], zip32Max)
		le.PutUint32(header[24:], zip32Max)
		le.PutUint16(header[28:], uint16(len(entry.name)))
		central := extra(entry.expanded, entry.compressed, entry.offset)
		le.PutUint16(header[30:], uint16(len(central)))
		le.PutUint32(header[38:], 0x80) // external attributes, as the real file
		le.PutUint32(header[42:], zip32Max)
		out.Write(header)
		out.WriteString(entry.name)
		out.Write(central)
	}
	size := uint64(out.Len()) - directory
	zip64End := uint64(out.Len())
	record := make([]byte, 56)
	le.PutUint32(record[0:], 0x06064b50)
	le.PutUint64(record[4:], 44)
	le.PutUint16(record[12:], 45)
	le.PutUint16(record[14:], 45)
	le.PutUint64(record[24:], uint64(len(members)))
	le.PutUint64(record[32:], uint64(len(members)))
	le.PutUint64(record[40:], size)
	le.PutUint64(record[48:], directory)
	out.Write(record)
	locator := make([]byte, 20)
	le.PutUint32(locator[0:], 0x07064b50)
	le.PutUint64(locator[8:], zip64End)
	le.PutUint32(locator[16:], 1)
	out.Write(locator)
	end := make([]byte, 22)
	le.PutUint32(end[0:], 0x06054b50)
	le.PutUint16(end[8:], uint16(len(members)))
	le.PutUint16(end[10:], uint16(len(members)))
	le.PutUint32(end[12:], uint32(size))
	le.PutUint32(end[16:], zip32Max)
	out.Write(end)
	return out.Bytes(), nil
}
