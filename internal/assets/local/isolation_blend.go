package local

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"strings"
)

// A .blend is an opaque Blender container: raw files start with "BLENDER",
// Blender 3.0+ compresses with Zstandard and older releases with gzip. The
// compressed or DNA/block payload is high-entropy binary, so the general text
// address scan (protocol-relative "//host/") false-positives on it at random
// (about 85% of 8 MiB of random bytes). Only the unambiguous absolute
// http(s):// scan applies to such bytes, as for the numeric part of binary STL.
func isBlendFormat(variant, mediaType string) bool {
	variant = strings.ToLower(strings.TrimSpace(variant))
	mediaType = strings.ToLower(strings.TrimSpace(strings.Split(mediaType, ";")[0]))
	return variant == "blend" || strings.HasSuffix(variant, ".blend") ||
		mediaType == "application/x-blender" || mediaType == "application/x-blend"
}

// blendMagicKnown accepts only the three Blender container headers. The gzip
// check also requires the deflate method byte and zero reserved flag bits so a
// two-byte prefix glued onto arbitrary text does not pass as a container.
func blendMagicKnown(magic []byte) bool {
	if bytes.HasPrefix(magic, []byte("BLENDER")) ||
		bytes.HasPrefix(magic, []byte{0x28, 0xb5, 0x2f, 0xfd}) {
		return true
	}
	return len(magic) >= 4 && magic[0] == 0x1f && magic[1] == 0x8b &&
		magic[2] == 0x08 && magic[3]&0xe0 == 0
}

func inspectBlend(reader io.Reader, size int64, magic []byte) error {
	if size < 12 || !blendMagicKnown(magic) {
		return rejectInspection("blend_magic_missing", errors.New("BLEND 文件头不是 Blender 容器"))
	}
	scanner := addressScanner{initial: absoluteAddress, continuation: absoluteAddress}
	buffered := bufio.NewReaderSize(reader, isolationChunkBytes)
	buffer := make([]byte, isolationChunkBytes)
	for {
		count, err := buffered.Read(buffer)
		if count > 0 && scanner.contains(buffer[:count]) {
			return rejectInspection("external_address", errors.New("BLEND 内容含外部地址"))
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}
