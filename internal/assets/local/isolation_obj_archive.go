package local

import (
	"archive/zip"
	"errors"
	"io"
	"strings"
)

var errNotOBJArchive = errors.New("内容不是 OBJ 压缩包")

// isOBJArchiveFormat identifies a slot whose frozen declaration is an OBJ
// delivered as a ZIP (Tripo's native OBJ export holds the OBJ, its material
// library and textures, #185).
func isOBJArchiveFormat(variant, mediaType string) bool {
	return strings.EqualFold(strings.TrimSpace(variant), "obj") &&
		strings.EqualFold(normalizeMediaType(mediaType), "application/zip")
}

// inspectOBJArchiveIdentity runs after the full archive isolation pass has
// accepted every member. It only establishes that the archive carries an OBJ
// file; geometry and materials remain endpoint acceptance concerns.
func inspectOBJArchiveIdentity(file io.ReaderAt, size int64) error {
	archive, err := zip.NewReader(file, size)
	if err != nil {
		return errNotOBJArchive
	}
	for _, member := range archive.File {
		if !isZIPDirectory(&member.FileHeader) && strings.HasSuffix(strings.ToLower(member.Name), ".obj") {
			return nil
		}
	}
	return errNotOBJArchive
}
