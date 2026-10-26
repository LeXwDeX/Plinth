package assetdelivery

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

const (
	exifTagArtist    = uint16(0x013b)
	exifTagSoftware  = uint16(0x0131)
	exifTagCopyright = uint16(0x8298)
)

type ExifStamper struct {
	clock func() time.Time
}

func NewExifStamper(clock func() time.Time) *ExifStamper {
	if clock == nil {
		clock = time.Now
	}
	return &ExifStamper{clock: clock}
}

func (stamper *ExifStamper) Stamp(
	content []byte,
	mediaType string,
	metadata catalog.AssetProxyMetadataSchema,
) ([]byte, error) {
	copyright := strings.ReplaceAll(
		metadata.CopyrightTemplate,
		"{year}",
		strconv.Itoa(stamper.clock().Year()),
	)
	fields := exifFields{
		copyright: copyright,
		artist:    metadata.Artist,
		software:  metadata.Software,
	}
	switch normalizedMediaType(mediaType) {
	case "image/jpeg":
		return stampJPEG(content, fields)
	case "image/png":
		return stampPNG(content, fields)
	case "image/webp":
		return stampWebP(content, fields)
	case "image/tiff":
		return stampTIFF(content, fields)
	default:
		return content, nil
	}
}

type exifFields struct {
	copyright string
	artist    string
	software  string
}

func buildExifTIFF(fields exifFields) ([]byte, error) {
	header := make([]byte, 8)
	copy(header[:2], "II")
	binary.LittleEndian.PutUint16(header[2:4], 42)
	binary.LittleEndian.PutUint32(header[4:8], 8)
	ifd, err := buildIFD(
		binary.LittleEndian,
		nil,
		0,
		fields,
		8,
	)
	if err != nil {
		return nil, err
	}
	return append(header, ifd...), nil
}

func stampTIFF(content []byte, fields exifFields) ([]byte, error) {
	order, firstIFD, err := inspectTIFF(content)
	if err != nil {
		return nil, err
	}
	countOffset := int(firstIFD)
	count := int(order.Uint16(content[countOffset : countOffset+2]))
	entriesOffset := countOffset + 2
	entriesEnd := entriesOffset + count*12
	if entriesEnd+4 > len(content) {
		return nil, errors.New("assetdelivery: truncated TIFF IFD")
	}
	entries := make([][]byte, 0, count)
	for index := 0; index < count; index++ {
		entry := append(
			[]byte(nil),
			content[entriesOffset+index*12:entriesOffset+(index+1)*12]...,
		)
		tag := order.Uint16(entry[:2])
		if tag == exifTagArtist ||
			tag == exifTagSoftware ||
			tag == exifTagCopyright {
			continue
		}
		entries = append(entries, entry)
	}
	nextIFD := order.Uint32(content[entriesEnd : entriesEnd+4])
	result := append([]byte(nil), content...)
	if len(result)%2 != 0 {
		result = append(result, 0)
	}
	if len(result) > math.MaxUint32 {
		return nil, errors.New("assetdelivery: TIFF is too large")
	}
	newOffset := uint32(len(result))
	ifd, err := buildIFD(
		order,
		entries,
		nextIFD,
		fields,
		newOffset,
	)
	if err != nil {
		return nil, err
	}
	result = append(result, ifd...)
	order.PutUint32(result[4:8], newOffset)
	return result, nil
}

func inspectTIFF(
	content []byte,
) (binary.ByteOrder, uint32, error) {
	if len(content) < 10 {
		return nil, 0, errors.New("assetdelivery: truncated TIFF")
	}
	var order binary.ByteOrder
	switch string(content[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return nil, 0, errors.New("assetdelivery: invalid TIFF byte order")
	}
	if order.Uint16(content[2:4]) != 42 {
		return nil, 0, errors.New("assetdelivery: invalid TIFF marker")
	}
	firstIFD := order.Uint32(content[4:8])
	if firstIFD > uint32(len(content)-2) {
		return nil, 0, errors.New("assetdelivery: invalid TIFF IFD offset")
	}
	return order, firstIFD, nil
}

func buildIFD(
	order binary.ByteOrder,
	existing [][]byte,
	nextIFD uint32,
	fields exifFields,
	baseOffset uint32,
) ([]byte, error) {
	entries := make([][]byte, 0, len(existing)+3)
	entries = append(entries, existing...)
	values := []struct {
		tag   uint16
		value string
	}{
		{tag: exifTagSoftware, value: fields.software},
		{tag: exifTagArtist, value: fields.artist},
		{tag: exifTagCopyright, value: fields.copyright},
	}
	for _, value := range values {
		entry := make([]byte, 12)
		order.PutUint16(entry[:2], value.tag)
		order.PutUint16(entry[2:4], 2)
		entries = append(entries, entry)
	}
	if len(entries) > math.MaxUint16 {
		return nil, errors.New("assetdelivery: too many TIFF IFD entries")
	}
	sort.SliceStable(entries, func(left int, right int) bool {
		return order.Uint16(entries[left][:2]) <
			order.Uint16(entries[right][:2])
	})
	fixedLength := 2 + len(entries)*12 + 4
	if uint64(baseOffset)+uint64(fixedLength) > math.MaxUint32 {
		return nil, errors.New("assetdelivery: TIFF IFD offset overflow")
	}
	data := make([]byte, 0)
	valueByTag := map[uint16]string{
		exifTagSoftware:  fields.software,
		exifTagArtist:    fields.artist,
		exifTagCopyright: fields.copyright,
	}
	for _, entry := range entries {
		tag := order.Uint16(entry[:2])
		value, configured := valueByTag[tag]
		if !configured {
			continue
		}
		encoded := append([]byte(value), 0)
		order.PutUint32(entry[4:8], uint32(len(encoded)))
		if len(encoded) <= 4 {
			copy(entry[8:12], encoded)
			continue
		}
		offset := uint64(baseOffset) +
			uint64(fixedLength) +
			uint64(len(data))
		if offset > math.MaxUint32 {
			return nil, errors.New(
				"assetdelivery: TIFF metadata offset overflow",
			)
		}
		order.PutUint32(entry[8:12], uint32(offset))
		data = append(data, encoded...)
	}
	result := make([]byte, fixedLength, fixedLength+len(data))
	order.PutUint16(result[:2], uint16(len(entries)))
	position := 2
	for _, entry := range entries {
		copy(result[position:position+12], entry)
		position += 12
	}
	order.PutUint32(result[position:position+4], nextIFD)
	result = append(result, data...)
	return result, nil
}

func stampJPEG(content []byte, fields exifFields) ([]byte, error) {
	if len(content) < 4 ||
		content[0] != 0xff ||
		content[1] != 0xd8 {
		return nil, errors.New("assetdelivery: invalid JPEG")
	}
	for position := 2; position+4 <= len(content); {
		if content[position] != 0xff {
			break
		}
		markerPosition := position
		for position < len(content) && content[position] == 0xff {
			position++
		}
		if position >= len(content) {
			break
		}
		marker := content[position]
		position++
		if marker == 0xda || marker == 0xd9 {
			break
		}
		if marker == 0x01 || marker >= 0xd0 && marker <= 0xd7 {
			continue
		}
		if position+2 > len(content) {
			break
		}
		length := int(binary.BigEndian.Uint16(
			content[position : position+2],
		))
		if length < 2 || position+length > len(content) {
			break
		}
		payloadStart := position + 2
		payloadEnd := position + length
		if marker == 0xe1 &&
			payloadEnd-payloadStart >= 6 &&
			bytes.Equal(
				content[payloadStart:payloadStart+6],
				[]byte("Exif\x00\x00"),
			) {
			merged, err := stampTIFF(
				content[payloadStart+6:payloadEnd],
				fields,
			)
			if err == nil {
				segment, segmentErr := jpegExifSegment(merged)
				if segmentErr != nil {
					return nil, segmentErr
				}
				return joinBytes(
					content[:markerPosition],
					segment,
					content[payloadEnd:],
				), nil
			}
		}
		position += length
	}
	tiff, err := buildExifTIFF(fields)
	if err != nil {
		return nil, err
	}
	segment, err := jpegExifSegment(tiff)
	if err != nil {
		return nil, err
	}
	return joinBytes(content[:2], segment, content[2:]), nil
}

func jpegExifSegment(tiff []byte) ([]byte, error) {
	payload := append([]byte("Exif\x00\x00"), tiff...)
	if len(payload)+2 > math.MaxUint16 {
		return nil, errors.New("assetdelivery: JPEG Exif is too large")
	}
	segment := make([]byte, 4, 4+len(payload))
	segment[0], segment[1] = 0xff, 0xe1
	binary.BigEndian.PutUint16(segment[2:4], uint16(len(payload)+2))
	return append(segment, payload...), nil
}

func stampPNG(content []byte, fields exifFields) ([]byte, error) {
	signature := []byte{
		0x89, 0x50, 0x4e, 0x47,
		0x0d, 0x0a, 0x1a, 0x0a,
	}
	if len(content) < len(signature) ||
		!bytes.Equal(content[:len(signature)], signature) {
		return nil, errors.New("assetdelivery: invalid PNG")
	}
	tiff, err := buildExifTIFF(fields)
	if err != nil {
		return nil, err
	}
	exifChunk := buildPNGChunk("eXIf", tiff)
	result := append([]byte(nil), signature...)
	inserted := false
	for position := len(signature); position+12 <= len(content); {
		length := int(binary.BigEndian.Uint32(
			content[position : position+4],
		))
		end := position + 12 + length
		if length < 0 || end > len(content) {
			return nil, errors.New("assetdelivery: truncated PNG chunk")
		}
		chunkType := string(content[position+4 : position+8])
		if chunkType == "IEND" && !inserted {
			result = append(result, exifChunk...)
			inserted = true
		}
		if chunkType != "eXIf" {
			result = append(result, content[position:end]...)
		}
		position = end
		if chunkType == "IEND" {
			break
		}
	}
	if !inserted {
		return nil, errors.New("assetdelivery: PNG is missing IEND")
	}
	return result, nil
}

func buildPNGChunk(chunkType string, payload []byte) []byte {
	result := make([]byte, 8, 12+len(payload))
	binary.BigEndian.PutUint32(result[:4], uint32(len(payload)))
	copy(result[4:8], chunkType)
	result = append(result, payload...)
	checksum := crc32.ChecksumIEEE(result[4:])
	result = append(result, make([]byte, 4)...)
	binary.BigEndian.PutUint32(result[len(result)-4:], checksum)
	return result
}

type webPChunk struct {
	fourCC  string
	payload []byte
}

func stampWebP(content []byte, fields exifFields) ([]byte, error) {
	chunks, err := parseWebPChunks(content)
	if err != nil {
		return nil, err
	}
	tiff, err := buildExifTIFF(fields)
	if err != nil {
		return nil, err
	}
	exifPayload := append([]byte("Exif\x00\x00"), tiff...)
	filtered := make([]webPChunk, 0, len(chunks)+2)
	hasExtendedHeader := false
	for _, chunk := range chunks {
		if chunk.fourCC == "EXIF" {
			continue
		}
		if chunk.fourCC == "VP8X" {
			if len(chunk.payload) < 10 {
				return nil, errors.New(
					"assetdelivery: truncated WebP VP8X",
				)
			}
			chunk.payload = append([]byte(nil), chunk.payload...)
			chunk.payload[0] |= 0x08
			hasExtendedHeader = true
		}
		filtered = append(filtered, chunk)
	}
	if !hasExtendedHeader {
		width, height, alpha, dimensionErr :=
			simpleWebPDimensions(filtered)
		if dimensionErr != nil {
			return nil, dimensionErr
		}
		payload := make([]byte, 10)
		if alpha {
			payload[0] |= 0x10
		}
		payload[0] |= 0x08
		putUint24(payload[4:7], uint32(width-1))
		putUint24(payload[7:10], uint32(height-1))
		filtered = append(
			[]webPChunk{{fourCC: "VP8X", payload: payload}},
			filtered...,
		)
	}
	filtered = append(filtered, webPChunk{
		fourCC:  "EXIF",
		payload: exifPayload,
	})
	return encodeWebPChunks(filtered)
}

func parseWebPChunks(content []byte) ([]webPChunk, error) {
	if len(content) < 12 ||
		string(content[:4]) != "RIFF" ||
		string(content[8:12]) != "WEBP" {
		return nil, errors.New("assetdelivery: invalid WebP container")
	}
	declaredEnd := uint64(binary.LittleEndian.Uint32(content[4:8])) + 8
	if declaredEnd > uint64(len(content)) || declaredEnd < 12 {
		return nil, errors.New("assetdelivery: truncated WebP container")
	}
	chunks := make([]webPChunk, 0)
	for position := 12; uint64(position) < declaredEnd; {
		if uint64(position+8) > declaredEnd {
			return nil, errors.New("assetdelivery: truncated WebP chunk")
		}
		size := uint64(binary.LittleEndian.Uint32(
			content[position+4 : position+8],
		))
		payloadStart := uint64(position + 8)
		payloadEnd := payloadStart + size
		paddedEnd := payloadEnd + size%2
		if payloadEnd > declaredEnd || paddedEnd > declaredEnd {
			return nil, errors.New("assetdelivery: invalid WebP chunk size")
		}
		chunks = append(chunks, webPChunk{
			fourCC: string(content[position : position+4]),
			payload: append(
				[]byte(nil),
				content[payloadStart:payloadEnd]...,
			),
		})
		position = int(paddedEnd)
	}
	return chunks, nil
}

func simpleWebPDimensions(
	chunks []webPChunk,
) (int, int, bool, error) {
	for _, chunk := range chunks {
		switch chunk.fourCC {
		case "VP8 ":
			if len(chunk.payload) < 10 ||
				chunk.payload[0]&1 != 0 ||
				!bytes.Equal(
					chunk.payload[3:6],
					[]byte{0x9d, 0x01, 0x2a},
				) {
				return 0, 0, false, errors.New(
					"assetdelivery: invalid WebP VP8 header",
				)
			}
			width := int(binary.LittleEndian.Uint16(
				chunk.payload[6:8],
			) & 0x3fff)
			height := int(binary.LittleEndian.Uint16(
				chunk.payload[8:10],
			) & 0x3fff)
			if width <= 0 || height <= 0 {
				return 0, 0, false, errors.New(
					"assetdelivery: invalid WebP canvas",
				)
			}
			return width, height, false, nil
		case "VP8L":
			if len(chunk.payload) < 5 || chunk.payload[0] != 0x2f {
				return 0, 0, false, errors.New(
					"assetdelivery: invalid WebP VP8L header",
				)
			}
			header := binary.LittleEndian.Uint32(chunk.payload[1:5])
			width := int(header&0x3fff) + 1
			height := int((header>>14)&0x3fff) + 1
			alpha := header>>28&1 != 0
			return width, height, alpha, nil
		}
	}
	return 0, 0, false, errors.New(
		"assetdelivery: WebP image chunk is missing",
	)
}

func encodeWebPChunks(chunks []webPChunk) ([]byte, error) {
	var body bytes.Buffer
	body.WriteString("WEBP")
	for _, chunk := range chunks {
		if len(chunk.fourCC) != 4 ||
			uint64(len(chunk.payload)) > math.MaxUint32 {
			return nil, errors.New("assetdelivery: invalid WebP chunk")
		}
		body.WriteString(chunk.fourCC)
		if err := binary.Write(
			&body,
			binary.LittleEndian,
			uint32(len(chunk.payload)),
		); err != nil {
			return nil, err
		}
		body.Write(chunk.payload)
		if len(chunk.payload)%2 != 0 {
			body.WriteByte(0)
		}
	}
	if uint64(body.Len()) > math.MaxUint32 {
		return nil, errors.New("assetdelivery: WebP is too large")
	}
	var result bytes.Buffer
	result.WriteString("RIFF")
	if err := binary.Write(
		&result,
		binary.LittleEndian,
		uint32(body.Len()),
	); err != nil {
		return nil, err
	}
	result.Write(body.Bytes())
	return result.Bytes(), nil
}

func putUint24(target []byte, value uint32) {
	target[0] = byte(value)
	target[1] = byte(value >> 8)
	target[2] = byte(value >> 16)
}

func joinBytes(parts ...[]byte) []byte {
	length := 0
	for _, part := range parts {
		length += len(part)
	}
	result := make([]byte, 0, length)
	for _, part := range parts {
		result = append(result, part...)
	}
	return result
}
