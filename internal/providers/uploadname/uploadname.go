// Package uploadname names files that Provider adapters upload as multipart
// parts. Several upstreams read the file format from the part's filename, so
// the name must follow the asset's verified media type. The mapping is an
// explicit project table: the host's mime table differs between machines and
// containers, and a generic ".bin" name tells the upstream nothing.
package uploadname

import (
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// ErrNoReliableExtension marks an asset whose verified format has no upload
// extension the target input declares. Callers refuse it before dispatch.
var ErrNoReliableExtension = errors.New("资产没有可靠的上传扩展名")

// extensions lists, canonical first, the filename extensions of each media
// type that Provider inputs accept. Only formats named here are uploaded.
var extensions = map[string][]string{
	"image/png":                    {".png"},
	"image/jpeg":                   {".jpg", ".jpeg"},
	"image/webp":                   {".webp"},
	"image/gif":                    {".gif"},
	"image/bmp":                    {".bmp"},
	"image/tiff":                   {".tiff", ".tif"},
	"image/avif":                   {".avif"},
	"image/heic":                   {".heic"},
	"image/heif":                   {".heif"},
	"image/jp2":                    {".jp2"},
	"model/gltf-binary":            {".glb"},
	"model/gltf+json":              {".gltf"},
	"model/obj":                    {".obj"},
	"model/stl":                    {".stl"},
	"application/sla":              {".stl"},
	"model/fbx":                    {".fbx"},
	"application/vnd.autodesk.fbx": {".fbx"},
	"model/vnd.usdz+zip":           {".usdz"},
	"model/usdz":                   {".usdz"},
	"model/vnd.usd":                {".usd"},
	"model/vnd.usda":               {".usda"},
	"model/vnd.usdc":               {".usdc"},
	"model/3mf":                    {".3mf"},
}

// Extension returns the upload extension (with its dot) of a verified media
// type: the first mapped one the input declares, or the canonical one when
// declared is empty. A type without such an extension returns
// ErrNoReliableExtension.
func Extension(mediaType string, declared []string) (string, error) {
	name, err := Filename(generation.InputAsset{MediaType: mediaType}, declared)
	if err != nil {
		return "", err
	}
	return path.Ext(name), nil
}

// Filename names one asset's upload part. declared is the input's Schema
// extension list (with or without dots); an empty list accepts any mapped
// extension. An original filename is kept when its extension already matches
// the verified format; otherwise its stem gets the verified extension, and an
// unnamed asset (a Task output) is called "upload" plus that extension.
//
// application/octet-stream names no format. Such an asset is accepted only
// when its saved variant or its filename carries an extension the input
// declares; anything else returns ErrNoReliableExtension.
func Filename(asset generation.InputAsset, declared []string) (string, error) {
	allowed := map[string]bool{}
	for _, extension := range declared {
		if extension = normalizeExtension(extension); extension != "" {
			allowed[extension] = true
		}
	}
	permitted := func(extension string) bool {
		return extension != "" && (len(allowed) == 0 || allowed[extension])
	}
	name := strings.TrimSpace(asset.OriginalFilename)
	current := normalizeExtension(path.Ext(name))
	stem := strings.TrimSuffix(name, path.Ext(name))
	if strings.TrimSpace(stem) == "" {
		stem = "upload"
	}
	mediaType := normalizeMediaType(asset.MediaType)
	if candidates := extensions[mediaType]; len(candidates) > 0 {
		for _, extension := range candidates {
			if extension == current && permitted(extension) {
				return name, nil
			}
		}
		for _, extension := range candidates {
			if permitted(extension) {
				return stem + extension, nil
			}
		}
		return "", fmt.Errorf("%w：媒体类型 %s 对应的扩展名 %s 不在输入声明的扩展名中", ErrNoReliableExtension, mediaType, strings.Join(candidates, "/"))
	}
	if mediaType == "application/octet-stream" && len(allowed) > 0 {
		if variant := normalizeExtension(asset.Variant); asset.Variant != "" && allowed[variant] {
			return stem + variant, nil
		}
		if allowed[current] {
			return name, nil
		}
	}
	if mediaType == "" {
		mediaType = "（空）"
	}
	return "", fmt.Errorf("%w：媒体类型 %s 没有项目内的扩展名映射", ErrNoReliableExtension, mediaType)
}

func normalizeMediaType(mediaType string) string {
	mediaType, _, _ = strings.Cut(mediaType, ";")
	return strings.ToLower(strings.TrimSpace(mediaType))
}

func normalizeExtension(extension string) string {
	extension = strings.ToLower(strings.TrimSpace(extension))
	if extension == "" || extension == "." {
		return ""
	}
	if !strings.HasPrefix(extension, ".") {
		extension = "." + extension
	}
	return extension
}
