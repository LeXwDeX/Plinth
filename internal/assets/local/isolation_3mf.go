package local

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"strings"
)

const max3MFIdentityDepth = 256

var errNot3MF = errors.New("内容不是 3MF 包")

// is3MFFormat identifies a slot whose declared variant or media type is 3MF.
// The frozen output variant comes from the Product Schema; a URL or file name
// alone never selects or exempts this contract.
func is3MFFormat(variant, mediaType string) bool {
	variant = strings.ToLower(strings.TrimSpace(variant))
	mediaType = strings.ToLower(normalizeMediaType(mediaType))
	return variant == "3mf" || strings.HasSuffix(variant, ".3mf") ||
		mediaType == "model/3mf" || mediaType == packageModelContentType
}

// inspect3MFIdentity runs after the full archive isolation pass has accepted
// every member. It only establishes that the ZIP is a 3MF package: an OPC
// [Content_Types].xml document and the 3D model part (the start part named by
// _rels/.rels, else 3D/3dmodel.model) whose root element is <model>. Profiled 3MF (3mf-package-v1) keeps its stricter OPC checks.
// Geometry, printability and colors remain endpoint acceptance concerns.
func inspect3MFIdentity(file io.ReaderAt, size int64) error {
	archive, err := zip.NewReader(file, size)
	if err != nil {
		return errNot3MF
	}
	members := map[string]*zip.File{}
	for _, member := range archive.File {
		if isZIPDirectory(&member.FileHeader) {
			continue
		}
		// OPC part names compare case-insensitively.
		name := strings.ToLower(member.Name)
		if members[name] != nil {
			return errNot3MF
		}
		members[name] = member
	}
	modelPart := "3d/3dmodel.model"
	if rels := members["_rels/.rels"]; rels != nil {
		start, err := find3MFStartPart(rels)
		if err != nil {
			return err
		}
		if start != "" {
			modelPart = strings.ToLower(start)
		}
	}
	contentTypes, model := members["[content_types].xml"], members[modelPart]
	if contentTypes == nil || model == nil {
		return errNot3MF
	}
	if err := inspect3MFXMLRoot(contentTypes, "Types", maxPackageMetadataBytes); err != nil {
		return err
	}
	return inspect3MFXMLRoot(model, "model", maxArchiveXMLBytes)
}

// find3MFStartPart returns the package-root 3D model part named by the OPC
// start relationship, or "" when the relationships part names none. 3MF
// writers normally use /3D/3dmodel.model, which is the fallback.
func find3MFStartPart(member *zip.File) (string, error) {
	if member.UncompressedSize64 == 0 || member.UncompressedSize64 > maxPackageMetadataBytes {
		return "", errNot3MF
	}
	body, err := member.Open()
	if err != nil {
		return "", errNot3MF
	}
	defer body.Close()
	decoder := xml.NewDecoder(io.LimitReader(body, maxPackageMetadataBytes+1))
	start := ""
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return start, nil
		}
		if err != nil {
			return "", errNot3MF
		}
		switch node := token.(type) {
		case xml.Directive:
			return "", errNot3MF
		case xml.StartElement:
			if node.Name.Local != "Relationship" {
				continue
			}
			var kind, target string
			for _, attr := range node.Attr {
				switch attr.Name.Local {
				case "Type":
					kind = attr.Value
				case "Target":
					target = attr.Value
				}
			}
			if kind != packageStartPartType {
				continue
			}
			name, err := packagePartName(target)
			if err != nil || start != "" {
				return "", errNot3MF
			}
			start = name
		}
	}
}

// inspect3MFXMLRoot requires one well-formed XML document with the named root
// element. Directives (DTDs) are refused; external entities are never read.
func inspect3MFXMLRoot(member *zip.File, root string, limit uint64) error {
	if member.UncompressedSize64 == 0 || member.UncompressedSize64 > limit {
		return errNot3MF
	}
	body, err := member.Open()
	if err != nil {
		return errNot3MF
	}
	defer body.Close()
	decoder := xml.NewDecoder(io.LimitReader(body, int64(limit)+1))
	depth, roots := 0, 0
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			if roots == 1 && depth == 0 {
				return nil
			}
			return errNot3MF
		}
		if err != nil {
			return errNot3MF
		}
		switch node := token.(type) {
		case xml.StartElement:
			depth++
			if depth > max3MFIdentityDepth {
				return errNot3MF
			}
			if depth == 1 {
				roots++
				if roots != 1 || node.Name.Local != root {
					return errNot3MF
				}
			}
		case xml.EndElement:
			depth--
		case xml.Directive:
			return errNot3MF
		case xml.CharData:
			if depth == 0 && len(bytes.TrimSpace(node)) != 0 {
				return errNot3MF
			}
		}
	}
}
