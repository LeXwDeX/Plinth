package legacygeneration

import (
	"encoding/json"
	"net/url"
	"path/filepath"
	"strings"
)

// DiagnosticCandidate is private source metadata. Callers must hash its paths
// before serialization. It describes existing reader rules without opening a
// file, calling a native asset port, or constructing an HTTP request.
type DiagnosticCandidate struct {
	Resolver     string
	Path         string `json:"-"`
	MetadataPath string `json:"-"`
	MediaType    string `json:"-"`
	Invalid      bool
}

// DiagnosticCandidates preserves the reader's route precedence: a recognized
// generated/token route claims the reference, while generic cache lookup tries
// the configured path and URL hash keys. Audit fallback belongs to ETL.
func (reader *Reader) DiagnosticCandidates(source, generatedRoot string) []DiagnosticCandidate {
	source = strings.TrimSpace(source)
	if source == "" {
		return nil
	}
	if name, ok := reader.generatedImageFilename(source); ok {
		invalid := name == "" || name == "." || filepath.Base(name) != name || strings.ContainsAny(name, `/\`)
		return []DiagnosticCandidate{{Resolver: "generated_route", Path: filepath.Join(generatedRoot, name), MediaType: reader.mediaTypeForFilename(name), Invalid: invalid}}
	}
	parsed, err := url.Parse(source)
	if err == nil && parsed.Host == "" && strings.HasPrefix(parsed.Path, "/") {
		for _, download := range reader.assetDownloads {
			if !strings.HasPrefix(parsed.Path, download.route.prefix) || !strings.HasSuffix(parsed.Path, download.route.suffix) {
				continue
			}
			token := strings.TrimSuffix(strings.TrimPrefix(parsed.Path, download.route.prefix), download.route.suffix)
			name := strings.ReplaceAll(download.cacheFilenameTemplate, download.tokenPlaceholder, token)
			invalid := !safeAssetToken(token) || filepath.Base(name) != name || strings.ContainsAny(name, `/\`)
			return []DiagnosticCandidate{{Resolver: "legacy_download_route", Path: filepath.Join(reader.cacheRoot, download.cacheSubdirectory, name), MediaType: download.mediaType, Invalid: invalid}}
		}
	}
	candidates := []DiagnosticCandidate{}
	for _, hash := range reader.cacheHashes(source) {
		if len(hash) < reader.cachePrefixCharacters {
			continue
		}
		resolver := "cache_url_hash"
		if err == nil && hash == sourceHash(parsed.Path) && hash != sourceHash(source) {
			resolver = "cache_path_hash"
		}
		directory := filepath.Join(reader.cacheRoot, hash[:reader.cachePrefixCharacters])
		candidates = append(candidates, DiagnosticCandidate{Resolver: resolver, Path: filepath.Join(directory, hash+reader.sourceExtension(source)), MetadataPath: filepath.Join(directory, hash+reader.cacheMetadataSuffix), MediaType: reader.mediaTypeForFilename(hash + reader.sourceExtension(source))})
	}
	return candidates
}

// DiagnosticMediaType applies the reader's existing sidecar interpretation to
// already safely-read bytes; metadata is never opened or serialized here.
func (reader *Reader) DiagnosticMediaType(candidate DiagnosticCandidate, raw []byte) (string, bool) {
	if candidate.MetadataPath == "" {
		return candidate.MediaType, true
	}
	var metadata map[string]any
	if json.Unmarshal(raw, &metadata) != nil {
		return "", false
	}
	value, _ := metadata[reader.cacheContentTypeField].(string)
	value = administrationMediaType(value)
	if value == "" {
		value = candidate.MediaType
	}
	return value, true
}
