package fakeupstream

import (
	"encoding/binary"
	"fmt"

	"github.com/LeXwDeX/Plinth/internal/testutil/wirefixture"
)

// renderXMP is an ordinary XMP packet. Every packet names its schemas by
// http:// namespace URIs.
const renderXMP = `<x:xmpmeta xmlns:x="adobe:ns:meta/"><rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#"><rdf:Description xmlns:xmp="http://ns.adobe.com/xap/1.0/" xmp:CreatorTool="fake renderer"/></rdf:RDF></x:xmpmeta>`

// RenderWebP returns the fixed render a 3D provider delivers as WebP: an
// extended WebP (VP8X) whose picture is the 1x1 lossless image of the
// webp-1x1 wire fixture and which carries an XMP metadata chunk.
//
// #576: a real Tripo render (rendered_image, WebP, 209802 bytes, served as
// binary/octet-stream) was refused by Plinth's asset store as content_rejected:
// WebP had no raster check, so the generic address scan read the file as text
// and found something address-shaped. The recording substituted the bytes, so
// what tripped the scan (metadata or compressed pixels) is not known; this
// fixture is constructed so that the old path refuses it the same way (the XMP
// namespace URIs are addresses to that scan) while the bytes are a valid WebP
// any decoder reads. The 1x1 fixture keeps serving wire fixtures that only
// need decodable bytes.
func RenderWebP() (File, error) {
	_, data, err := wirefixture.SyntheticMedia("webp-1x1")
	if err != nil {
		return File{}, err
	}
	if len(data) < 20 || string(data[12:16]) != "VP8L" {
		return File{}, fmt.Errorf("fakeupstream: webp-1x1 is not a simple lossless WebP")
	}
	body := []byte("WEBP")
	// VP8X: XMP flag (0x04), canvas 1x1 (width-1 and height-1 as 24-bit values).
	body = append(body, renderWebPChunk("VP8X", []byte{0x04, 0, 0, 0, 0, 0, 0, 0, 0, 0})...)
	body = append(body, data[12:]...) // the VP8L chunk, padding included
	body = append(body, renderWebPChunk("XMP ", []byte(renderXMP))...)
	file := append([]byte("RIFF"), binary.LittleEndian.AppendUint32(nil, uint32(len(body)))...)
	return File{MediaType: "image/webp", Data: append(file, body...)}, nil
}

func renderWebPChunk(kind string, payload []byte) []byte {
	chunk := append([]byte(kind), binary.LittleEndian.AppendUint32(nil, uint32(len(payload)))...)
	chunk = append(chunk, payload...)
	if len(payload)%2 == 1 {
		chunk = append(chunk, 0)
	}
	return chunk
}
