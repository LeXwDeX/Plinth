package httpapi

import (
	"mime"
	"net/http"
	"regexp"
	"strings"
)

// Stored product bytes come from people and from upstream Providers, so they
// are untrusted content on this origin (#315, DLV-04). Every response that
// returns them says so the same way:
//
//   - X-Content-Type-Options: nosniff, so the declared type is final;
//   - Content-Security-Policy: sandbox, so a file opened as a document gets a
//     unique origin with scripts, forms and plugins off;
//   - Content-Disposition: inline only for the Schema's inline_media_types
//     (the pictures the UI previews); every other file is an attachment with
//     a safe file name.
//
// The header changes nothing for <img>, fetch or the 3D viewer: a response
// CSP governs only a document the bytes are opened as.
const sandboxPolicy = "sandbox"

var unsafeFilenameCharacters = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// safeDownloadFilename keeps a file name to [A-Za-z0-9._-], without leading
// dots or dashes, so it needs no quoting and cannot name a path.
func safeDownloadFilename(name string) string {
	name = unsafeFilenameCharacters.ReplaceAllString(name, "-")
	name = strings.TrimLeft(name, ".-")
	if name == "" {
		return "download"
	}
	return name
}

// storedAssetFilename is the attachment name of a product file that has no
// Schema format name: <role>-<task>.<extension>, the extension taken from a
// short variant (mtl, obj) or else from the media type.
func storedAssetFilename(role, taskID, variant, mediaType string) string {
	extension := ""
	if shortVariant.MatchString(variant) {
		extension = variant
	} else if extensions, _ := mime.ExtensionsByType(mediaType); len(extensions) > 0 {
		extension = strings.TrimPrefix(extensions[0], ".")
	}
	if extension == "" {
		extension = "bin"
	}
	if role == "" {
		role = "asset"
	}
	return safeDownloadFilename(role + "-" + taskID + "." + extension)
}

var shortVariant = regexp.MustCompile(`^[a-z0-9]{1,8}$`)

// setStoredAssetDeliveryHeaders writes nosniff, the sandbox policy and the
// disposition for one stored file; filename is used only for attachments.
func (s *Server) setStoredAssetDeliveryHeaders(writer http.ResponseWriter, mediaType, filename string) {
	header := writer.Header()
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Content-Security-Policy", sandboxPolicy)
	if slicesContains(s.application.Runtime.InputAssets.InlineMediaTypes, normalizedUploadMediaType(mediaType)) {
		header.Set("Content-Disposition", "inline")
		return
	}
	name := safeDownloadFilename(filename)
	header.Set("Content-Disposition", `attachment; filename="`+name+`"; filename*=UTF-8''`+name)
}

// setSandboxedBytesHeaders is for responses that already chose their own
// disposition (archives, admin exports, signed upstream fetches).
func setSandboxedBytesHeaders(writer http.ResponseWriter) {
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.Header().Set("Content-Security-Policy", sandboxPolicy)
}
