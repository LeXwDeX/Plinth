package local

import (
	"archive/zip"
	"bufio"
	"bytes"
	"compress/flate"
	"encoding/binary"
	"errors"
	"io"
	"sort"
	"strings"
)

const (
	zipLocalSignature      = 0x04034b50
	zipCentralSignature    = 0x02014b50
	zipEndSignature        = 0x06054b50
	zip64EndSignature      = 0x06064b50
	zip64LocatorSignature  = 0x07064b50
	zipDescriptorSignature = 0x08074b50
	zip32Sentinel          = uint64(1<<32 - 1)
	zip16Sentinel          = uint64(1<<16 - 1)
	zipFooterSearchBytes   = 22 + 65535
)

type zipDirectoryFrame struct {
	offset, size, count, footer uint64
}

type zipEntryFrame struct {
	name                 string
	directory            bool
	flags, method        uint16
	crc                  uint32
	compressed, expanded uint64
	offset               uint64
}

func looksLikeZIP(file io.ReaderAt, size int64, magic []byte, variant, mediaType string) (bool, error) {
	mediaType = strings.ToLower(strings.TrimSpace(strings.Split(mediaType, ";")[0]))
	variant = strings.ToLower(strings.TrimSpace(variant))
	if bytes.Equal(magic[:4], []byte("PK\x03\x04")) || is3MFFormat(variant, mediaType) || variant == "usdz" ||
		mediaType == "application/zip" || strings.HasSuffix(mediaType, "+zip") {
		return true, nil
	}
	// A ZIP reader may accept a self-extracting prefix. Locate the footer even
	// when the first bytes or the upstream filename do not identify a ZIP.
	length := min(size, int64(zipFooterSearchBytes))
	footer := make([]byte, length)
	if _, err := file.ReadAt(footer, size-length); err != nil {
		return false, err
	}
	return bytes.Contains(footer, []byte("PK\x05\x06")), nil
}

// archive/zip intentionally tolerates prefixes, trailers and gaps. Delivered
// model bytes need a stricter boundary: every byte belongs to a checked local
// record, payload, descriptor, directory or footer.
func validateZIPFraming(reader io.ReaderAt, fileSize int64) error {
	if fileSize < 22 {
		return errors.New("ZIP 文件尾被截断")
	}
	size := uint64(fileSize)
	directory, err := readZIPDirectoryFrame(reader, size)
	if err != nil {
		return err
	}
	if directory.count > maxArchiveFiles || directory.count > directory.size/46 {
		return errors.New("ZIP 文件数量或目录大小无效")
	}
	entries := make([]zipEntryFrame, 0, directory.count)
	position := directory.offset
	directoryEnd := directory.offset + directory.size
	for range directory.count {
		fixed, err := readZIPFrame(reader, position, 46, directoryEnd)
		if err != nil || binary.LittleEndian.Uint32(fixed[:4]) != zipCentralSignature {
			return errors.New("ZIP 中央目录无效")
		}
		nameLength := uint64(binary.LittleEndian.Uint16(fixed[28:30]))
		extraLength := uint64(binary.LittleEndian.Uint16(fixed[30:32]))
		commentLength := uint64(binary.LittleEndian.Uint16(fixed[32:34]))
		metadata, err := readZIPFrame(reader, position+46, nameLength+extraLength+commentLength, directoryEnd)
		if err != nil {
			return err
		}
		if containsExternalAddress(metadata) {
			return errors.New("ZIP 中央目录元数据含外部地址")
		}
		entry := zipEntryFrame{
			name: string(metadata[:nameLength]), flags: binary.LittleEndian.Uint16(fixed[8:10]),
			method: binary.LittleEndian.Uint16(fixed[10:12]), crc: binary.LittleEndian.Uint32(fixed[16:20]),
			compressed: uint64(binary.LittleEndian.Uint32(fixed[20:24])),
			expanded:   uint64(binary.LittleEndian.Uint32(fixed[24:28])),
			offset:     uint64(binary.LittleEndian.Uint32(fixed[42:46])),
		}
		entry.directory = isZIPDirectory(&zip.FileHeader{
			Name: entry.name, CreatorVersion: binary.LittleEndian.Uint16(fixed[4:6]),
			ExternalAttrs: binary.LittleEndian.Uint32(fixed[38:42]),
		})
		disk := uint64(binary.LittleEndian.Uint16(fixed[34:36]))
		if err := resolveZIP64Extra(metadata[nameLength:nameLength+extraLength],
			&entry.expanded, &entry.compressed, &entry.offset, &disk); err != nil {
			return err
		}
		if disk != 0 || entry.method != zip.Store && entry.method != zip.Deflate ||
			entry.flags & ^uint16(0x080e) != 0 || entry.method == zip.Store && entry.flags&6 != 0 {
			return errors.New("ZIP 使用不支持的分卷、加密或压缩结构")
		}
		if entry.directory && (entry.expanded != 0 || entry.crc != 0) {
			return errors.New("ZIP 目录不能携带未检查的内容")
		}
		entries = append(entries, entry)
		position += 46 + uint64(len(metadata))
	}
	if position != directoryEnd {
		return errors.New("ZIP 中央目录含未声明的内容")
	}
	sort.Slice(entries, func(left, right int) bool { return entries[left].offset < entries[right].offset })
	position = 0
	for index, entry := range entries {
		if entry.offset != position {
			return errors.New("ZIP 本地记录含前缀、间隙或重叠")
		}
		next := directory.offset
		if index+1 < len(entries) {
			next = entries[index+1].offset
		}
		if err := validateZIPLocalFrame(reader, size, entry, next); err != nil {
			return err
		}
		position = next
	}
	if position != directory.offset {
		return errors.New("ZIP 目录前含未声明的内容")
	}
	return nil
}

func readZIPDirectoryFrame(reader io.ReaderAt, size uint64) (zipDirectoryFrame, error) {
	length := min(size, uint64(zipFooterSearchBytes))
	window, err := readZIPFrame(reader, size-length, length, size)
	if err != nil {
		return zipDirectoryFrame{}, err
	}
	index := -1
	for candidate := len(window) - 22; candidate >= 0; candidate-- {
		if binary.LittleEndian.Uint32(window[candidate:candidate+4]) == zipEndSignature &&
			candidate+22+int(binary.LittleEndian.Uint16(window[candidate+20:candidate+22])) == len(window) {
			index = candidate
			break
		}
	}
	if index < 0 {
		return zipDirectoryFrame{}, errors.New("ZIP 文件尾无效或含附加内容")
	}
	end := window[index:]
	endOffset := size - length + uint64(index)
	if binary.LittleEndian.Uint16(end[4:6]) != 0 || binary.LittleEndian.Uint16(end[6:8]) != 0 ||
		binary.LittleEndian.Uint16(end[8:10]) != binary.LittleEndian.Uint16(end[10:12]) || containsExternalAddress(end[22:]) {
		return zipDirectoryFrame{}, errors.New("ZIP 分卷、计数或文件尾注释无效")
	}
	directory := zipDirectoryFrame{
		offset: uint64(binary.LittleEndian.Uint32(end[16:20])), size: uint64(binary.LittleEndian.Uint32(end[12:16])),
		count: uint64(binary.LittleEndian.Uint16(end[10:12])), footer: endOffset,
	}
	requiresZIP64 := directory.offset == zip32Sentinel || directory.size == zip32Sentinel || directory.count == zip16Sentinel
	var locator []byte
	if endOffset >= 20 {
		locator, err = readZIPFrame(reader, endOffset-20, 20, size)
		if err != nil {
			return zipDirectoryFrame{}, err
		}
	}
	if len(locator) == 20 && binary.LittleEndian.Uint32(locator[:4]) == zip64LocatorSignature {
		if binary.LittleEndian.Uint32(locator[4:8]) != 0 || binary.LittleEndian.Uint32(locator[16:20]) != 1 {
			return zipDirectoryFrame{}, errors.New("ZIP64 不支持分卷")
		}
		offset := binary.LittleEndian.Uint64(locator[8:16])
		fixed, err := readZIPFrame(reader, offset, 56, endOffset-20)
		if err != nil || binary.LittleEndian.Uint32(fixed[:4]) != zip64EndSignature {
			return zipDirectoryFrame{}, errors.New("ZIP64 文件尾无效")
		}
		recordSize := binary.LittleEndian.Uint64(fixed[4:12])
		if recordSize < 44 || recordSize > 65535 || offset > endOffset-20 || recordSize+12 != endOffset-20-offset {
			return zipDirectoryFrame{}, errors.New("ZIP64 文件尾含间隙或不支持的扩展")
		}
		extension, err := readZIPFrame(reader, offset+56, recordSize-44, endOffset-20)
		if err != nil || containsExternalAddress(extension) {
			return zipDirectoryFrame{}, errors.New("ZIP64 文件尾扩展无效")
		}
		count := binary.LittleEndian.Uint64(fixed[32:40])
		if binary.LittleEndian.Uint32(fixed[16:20]) != 0 || binary.LittleEndian.Uint32(fixed[20:24]) != 0 ||
			binary.LittleEndian.Uint64(fixed[24:32]) != count {
			return zipDirectoryFrame{}, errors.New("ZIP64 分卷或计数无效")
		}
		wide := zipDirectoryFrame{offset: binary.LittleEndian.Uint64(fixed[48:56]), size: binary.LittleEndian.Uint64(fixed[40:48]), count: count, footer: offset}
		if directory.offset != zip32Sentinel && directory.offset != wide.offset ||
			directory.size != zip32Sentinel && directory.size != wide.size || directory.count != zip16Sentinel && directory.count != wide.count {
			return zipDirectoryFrame{}, errors.New("ZIP 与 ZIP64 文件尾不一致")
		}
		directory = wide
	} else if requiresZIP64 {
		return zipDirectoryFrame{}, errors.New("ZIP64 定位记录缺失")
	}
	if directory.offset > directory.footer || directory.size != directory.footer-directory.offset {
		return zipDirectoryFrame{}, errors.New("ZIP 中央目录边界不一致")
	}
	return directory, nil
}

func resolveZIP64Extra(extra []byte, expanded, compressed, offset, disk *uint64) error {
	needs := []*uint64{}
	for _, value := range []*uint64{expanded, compressed, offset} {
		if value != nil && *value == zip32Sentinel {
			needs = append(needs, value)
		}
	}
	needsDisk := disk != nil && *disk == zip16Sentinel
	found := false
	for len(extra) > 0 {
		if len(extra) < 4 {
			return errors.New("ZIP 扩展字段被截断")
		}
		tag, length := binary.LittleEndian.Uint16(extra[:2]), int(binary.LittleEndian.Uint16(extra[2:4]))
		if length > len(extra)-4 {
			return errors.New("ZIP 扩展字段长度无效")
		}
		body := extra[4 : 4+length]
		extra = extra[4+length:]
		if tag != 1 {
			continue
		}
		if found {
			return errors.New("ZIP64 扩展字段重复")
		}
		found = true
		for _, value := range needs {
			if len(body) < 8 {
				return errors.New("ZIP64 扩展字段被截断")
			}
			*value = binary.LittleEndian.Uint64(body[:8])
			body = body[8:]
		}
		if needsDisk {
			if len(body) < 4 {
				return errors.New("ZIP64 分卷字段被截断")
			}
			*disk = uint64(binary.LittleEndian.Uint32(body[:4]))
			body = body[4:]
		}
		if len(body) != 0 {
			return errors.New("ZIP64 含未声明的扩展值")
		}
	}
	if (len(needs) != 0 || needsDisk) && !found {
		return errors.New("ZIP64 扩展字段缺失")
	}
	return nil
}

func validateZIPLocalFrame(reader io.ReaderAt, size uint64, entry zipEntryFrame, next uint64) error {
	if next > size || entry.offset >= next {
		return errors.New("ZIP 本地记录重叠")
	}
	fixed, err := readZIPFrame(reader, entry.offset, 30, next)
	if err != nil || binary.LittleEndian.Uint32(fixed[:4]) != zipLocalSignature {
		return errors.New("ZIP 本地文件头无效")
	}
	nameLength := uint64(binary.LittleEndian.Uint16(fixed[26:28]))
	extraLength := uint64(binary.LittleEndian.Uint16(fixed[28:30]))
	metadata, err := readZIPFrame(reader, entry.offset+30, nameLength+extraLength, next)
	if err != nil {
		return err
	}
	if string(metadata[:nameLength]) != entry.name || containsExternalAddress(metadata) ||
		binary.LittleEndian.Uint16(fixed[6:8]) != entry.flags || binary.LittleEndian.Uint16(fixed[8:10]) != entry.method {
		return errors.New("ZIP 本地文件头与中央目录不一致或含外部地址")
	}
	expanded, compressed := uint64(binary.LittleEndian.Uint32(fixed[22:26])), uint64(binary.LittleEndian.Uint32(fixed[18:22]))
	if err := resolveZIP64Extra(metadata[nameLength:], &expanded, &compressed, nil, nil); err != nil {
		return err
	}
	descriptor := entry.flags&8 != 0
	crc := binary.LittleEndian.Uint32(fixed[14:18])
	if crc != entry.crc && !(descriptor && crc == 0) ||
		expanded != entry.expanded && !(descriptor && expanded == 0) ||
		compressed != entry.compressed && !(descriptor && compressed == 0) {
		return errors.New("ZIP 本地校验值或大小与中央目录不一致")
	}
	dataOffset := entry.offset + 30 + uint64(len(metadata))
	if dataOffset > next || entry.compressed > next-dataOffset {
		return errors.New("ZIP 压缩内容被截断或重叠")
	}
	payloadEnd := dataOffset + entry.compressed
	if entry.directory && entry.compressed != 0 {
		// archive/zip skips directory payloads. Some valid exporters deflate an
		// empty stream here, so inspect it directly instead of skipping bytes.
		if entry.method != zip.Deflate {
			return errors.New("ZIP 目录含未检查的 Stored 字节")
		}
		body := inspectZIPDeflate(io.NewSectionReader(reader, int64(dataOffset), int64(entry.compressed)))
		content, err := io.ReadAll(io.LimitReader(body, 1))
		_ = body.Close()
		if err != nil || len(content) != 0 {
			return errors.New("ZIP 目录压缩内容不为空或含未检查的字节")
		}
	}
	if !descriptor {
		if payloadEnd != next {
			return errors.New("ZIP 压缩内容之后含未声明的字节")
		}
		return nil
	}
	length := next - payloadEnd
	if length != 12 && length != 16 && length != 20 && length != 24 {
		return errors.New("ZIP 数据描述记录长度无效")
	}
	footer, err := readZIPFrame(reader, payloadEnd, length, next)
	if err != nil {
		return err
	}
	if length == 16 || length == 24 {
		if binary.LittleEndian.Uint32(footer[:4]) != zipDescriptorSignature {
			return errors.New("ZIP 数据描述记录签名无效")
		}
		footer = footer[4:]
	}
	if binary.LittleEndian.Uint32(footer[:4]) != entry.crc {
		return errors.New("ZIP 数据描述记录校验值不一致")
	}
	var declaredCompressed, declaredExpanded uint64
	if len(footer) == 12 {
		declaredCompressed, declaredExpanded = uint64(binary.LittleEndian.Uint32(footer[4:8])), uint64(binary.LittleEndian.Uint32(footer[8:12]))
	} else {
		declaredCompressed, declaredExpanded = binary.LittleEndian.Uint64(footer[4:12]), binary.LittleEndian.Uint64(footer[12:20])
	}
	if declaredCompressed != entry.compressed || declaredExpanded != entry.expanded {
		return errors.New("ZIP 数据描述记录大小不一致")
	}
	return nil
}

// Match archive/zip's UNIX, DOS and filename-based directory interpretation.
// Framing validation must inspect every payload the semantic pass will skip.
func isZIPDirectory(header *zip.FileHeader) bool {
	return header.Mode().IsDir()
}

func readZIPFrame(reader io.ReaderAt, offset, length, limit uint64) ([]byte, error) {
	if offset > limit || length > limit-offset || length > 30+3*65535 {
		return nil, errors.New("ZIP 记录越界或被截断")
	}
	frame := make([]byte, int(length))
	if _, err := reader.ReadAt(frame, int64(offset)); err != nil {
		return nil, err
	}
	return frame, nil
}

type exactZIPDeflateReader struct {
	io.ReadCloser
	compressed io.Reader
	verified   bool
}

func (reader *exactZIPDeflateReader) Read(buffer []byte) (int, error) {
	count, err := reader.ReadCloser.Read(buffer)
	if err == io.EOF && !reader.verified {
		reader.verified = true
		var remainder [1]byte
		if trailing, trailingErr := reader.compressed.Read(remainder[:]); trailing != 0 || trailingErr != io.EOF {
			return count, errors.New("ZIP Deflate 内容之后含未检查的压缩字节")
		}
	}
	return count, err
}

func inspectZIPDeflate(reader io.Reader) io.ReadCloser {
	// Share the buffer with the remainder check. flate may leave unused bytes
	// already prefetched here, even when the underlying SectionReader is at EOF.
	compressed := bufio.NewReader(reader)
	return &exactZIPDeflateReader{ReadCloser: flate.NewReader(compressed), compressed: compressed}
}
