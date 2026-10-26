package local

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"net/url"
	"os"
	"path"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/assets/splat"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

const (
	maxPackageMetadataBytes       = 1 << 20
	maxPackageMetadataElements    = 8192
	packageTypesNamespace         = "http://schemas.openxmlformats.org/package/2006/content-types"
	packageRelationshipsNamespace = "http://schemas.openxmlformats.org/package/2006/relationships"
	packageStartPartType          = "http://schemas.microsoft.com/3dmanufacturing/2013/01/3dmodel"
	packageModelContentType       = "application/vnd.ms-package.3dmanufacturing-3dmodel+xml"
	packageModelNamespace         = "http://schemas.microsoft.com/3dmanufacturing/core/2015/02"
)

func deliveryProfileError() error {
	return errors.New("asset delivery profile does not match its format identity")
}

// A profile identifies the container, not its useful geometry, printable
// topology, colors or motion tracks. Those require separate qualification.
func inspectProfiledAsset(file *os.File, profile, role, variant, mediaType string) error {
	if profile == "" {
		return inspectDeliveredAsset(file, role, variant, mediaType)
	}
	expectedVariant, expectedMIME, known := schema.DeliveryProfileFormat(profile)
	if !known || variant != expectedVariant || mediaType != expectedMIME {
		return deliveryProfileError()
	}
	if requiredRole := schema.DeliveryProfileRole(profile); requiredRole != "" && role != requiredRole {
		return deliveryProfileError()
	}
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	switch profile {
	case schema.DeliveryProfileBinaryFBXV1:
		err = inspectBinaryFBX(file, info.Size(), newInspectionWorkBudgetForSize(info.Size()))
	case schema.DeliveryProfile3MFPackageV1:
		err = inspect3MFPackage(file, info.Size())
	case schema.DeliveryProfileGaussianSplat32V1:
		err = splat.ValidateReader(file, info.Size())
	}
	if err != nil {
		return labelInspectionFailure("delivery_profile", err)
	}
	_, err = file.Seek(0, io.SeekStart)
	return err
}

func inspect3MFPackage(file io.ReaderAt, size int64) error {
	// Retain all existing framing, duplicate, path, decompression and external
	// dependency checks. No ZIP member is extracted or resolved outside the file.
	if size <= 0 || size > maxArchiveBytes {
		return deliveryProfileError()
	}
	if err := validateZIPFraming(file, size); err != nil {
		return err
	}
	archive, err := zip.NewReader(file, size)
	if err != nil {
		return deliveryProfileError()
	}
	if len(archive.File) > maxArchiveFiles {
		return deliveryProfileError()
	}
	members := make(map[string]*zip.File, len(archive.File))
	for _, member := range archive.File {
		if !isZIPDirectory(&member.FileHeader) {
			members[member.Name] = member
		}
	}
	defaults, overrides := map[string]string{}, map[string]string{}
	if err = readPackageMetadata(members["[Content_Types].xml"], "Types", packageTypesNamespace, func(node xml.StartElement) error {
		attrs, err := packageAttributes(node)
		if err != nil {
			return err
		}
		switch node.Name.Local {
		case "Default":
			if len(attrs) != 2 || attrs["ContentType"] == "" {
				return deliveryProfileError()
			}
			extension := strings.ToLower(attrs["Extension"])
			if extension == "" || len(extension) > 128 || strings.ContainsAny(extension, " ./\\:%?#") {
				return deliveryProfileError()
			}
			if _, dup := defaults[extension]; dup {
				return deliveryProfileError()
			}
			defaults[extension] = attrs["ContentType"]
		case "Override":
			if len(attrs) != 2 || attrs["ContentType"] == "" || !strings.HasPrefix(attrs["PartName"], "/") {
				return deliveryProfileError()
			}
			name, err := packagePartName(attrs["PartName"])
			if err != nil {
				return err
			}
			if _, dup := overrides[name]; dup {
				return deliveryProfileError()
			}
			overrides[name] = attrs["ContentType"]
		default:
			return deliveryProfileError()
		}
		return nil
	}); err != nil {
		return err
	}
	startPart := ""
	ids := map[string]bool{}
	if err = readPackageMetadata(members["_rels/.rels"], "Relationships", packageRelationshipsNamespace, func(node xml.StartElement) error {
		if node.Name.Local != "Relationship" {
			return deliveryProfileError()
		}
		attrs, err := packageAttributes(node)
		if err != nil {
			return err
		}
		for key := range attrs {
			if key != "Id" && key != "Type" && key != "Target" && key != "TargetMode" {
				return deliveryProfileError()
			}
		}
		if attrs["Id"] == "" || attrs["Type"] == "" || ids[attrs["Id"]] || (attrs["TargetMode"] != "" && attrs["TargetMode"] != "Internal") {
			return deliveryProfileError()
		}
		ids[attrs["Id"]] = true
		name, err := packagePartName(attrs["Target"])
		if err != nil {
			return err
		}
		if attrs["Type"] == packageStartPartType {
			if startPart != "" {
				return deliveryProfileError()
			}
			startPart = name
		}
		return nil
	}); err != nil {
		return err
	}
	model := members[startPart]
	if startPart == "" || model == nil {
		return deliveryProfileError()
	}
	contentType, override := overrides[startPart]
	if !override {
		contentType = defaults[strings.ToLower(strings.TrimPrefix(path.Ext(startPart), "."))]
	}
	if contentType != packageModelContentType {
		return deliveryProfileError()
	}
	if model.UncompressedSize64 > maxArchiveXMLBytes {
		return deliveryProfileError()
	}
	// Only packages with bounded identity metadata reach the full existing
	// isolation pass, which still checks every delivered archive member.
	if err := inspectArchiveWithXMLPart(file, size, startPart); err != nil {
		return err
	}
	body, err := model.Open()
	if err != nil {
		return deliveryProfileError()
	}
	defer body.Close()
	// Check document identity and well-formedness, without interpreting model
	// objects, topology or extension semantics. Token work is bounded by the
	// existing XML byte cap and an explicit nesting limit.
	decoder := xml.NewDecoder(io.LimitReader(body, maxArchiveXMLBytes+1))
	depth, roots := 0, 0
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			if roots == 1 && depth == 0 {
				return nil
			}
			return deliveryProfileError()
		}
		if err != nil {
			return deliveryProfileError()
		}
		switch node := token.(type) {
		case xml.StartElement:
			attributes := map[xml.Name]bool{}
			for _, a := range node.Attr {
				if attributes[a.Name] {
					return deliveryProfileError()
				}
				attributes[a.Name] = true
			}
			depth++
			if depth > 64 {
				return deliveryProfileError()
			}
			if depth == 1 {
				roots++
				if roots != 1 || node.Name.Local != "model" || node.Name.Space != packageModelNamespace {
					return deliveryProfileError()
				}
			}
		case xml.EndElement:
			depth--
		case xml.Directive:
			return deliveryProfileError()
		case xml.CharData:
			if depth == 0 && len(bytes.TrimSpace(node)) != 0 {
				return deliveryProfileError()
			}
		}
	}
}

// Root relationships are relative to the package root. OPC permits the
// canonical root-absolute form /directory/part; no host or filesystem is used.
func packagePartName(raw string) (string, error) {
	if raw == "" || strings.TrimSpace(raw) != raw || strings.ContainsAny(raw, "\\\x00\r\n\t") {
		return "", deliveryProfileError()
	}
	u, err := url.Parse(raw)
	if err != nil || u.IsAbs() || u.Host != "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" || strings.HasPrefix(raw, "//") {
		return "", deliveryProfileError()
	}
	lower := strings.ToLower(u.RawPath)
	if strings.Contains(lower, "%2f") || strings.Contains(lower, "%5c") {
		return "", deliveryProfileError()
	}
	name := strings.TrimPrefix(u.Path, "/")
	if name == "" || strings.HasSuffix(name, "/") || strings.ContainsAny(name, "\\\x00\r\n\t") {
		return "", deliveryProfileError()
	}
	for _, segment := range strings.Split(name, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return "", deliveryProfileError()
		}
	}
	return name, nil
}
func packageAttributes(node xml.StartElement) (map[string]string, error) {
	attrs := map[string]string{}
	seen := map[xml.Name]bool{}
	for _, attr := range node.Attr {
		if seen[attr.Name] {
			return nil, deliveryProfileError()
		}
		seen[attr.Name] = true
		if attr.Name.Space == "xmlns" || (attr.Name.Space == "" && attr.Name.Local == "xmlns") {
			continue
		}
		if attr.Name.Space != "" {
			return nil, deliveryProfileError()
		}
		if _, dup := attrs[attr.Name.Local]; dup {
			return nil, deliveryProfileError()
		}
		attrs[attr.Name.Local] = attr.Value
	}
	return attrs, nil
}
func readPackageMetadata(member *zip.File, root, namespace string, visit func(xml.StartElement) error) error {
	if member == nil || member.UncompressedSize64 == 0 || member.UncompressedSize64 > maxPackageMetadataBytes {
		return deliveryProfileError()
	}
	body, err := member.Open()
	if err != nil {
		return deliveryProfileError()
	}
	raw, readErr := io.ReadAll(io.LimitReader(body, maxPackageMetadataBytes+1))
	closeErr := body.Close()
	if readErr != nil || closeErr != nil || uint64(len(raw)) != member.UncompressedSize64 {
		return deliveryProfileError()
	}
	decoder := xml.NewDecoder(bytes.NewReader(raw))
	depth, elements := 0, 0
	seen := false
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			if seen && depth == 0 {
				return nil
			}
			return deliveryProfileError()
		}
		if err != nil {
			return deliveryProfileError()
		}
		switch node := token.(type) {
		case xml.StartElement:
			depth++
			elements++
			if depth > 2 || elements > maxPackageMetadataElements || node.Name.Space != namespace {
				return deliveryProfileError()
			}
			if depth == 1 {
				if seen || node.Name.Local != root {
					return deliveryProfileError()
				}
				seen = true
				attributes, err := packageAttributes(node)
				if err != nil || len(attributes) != 0 {
					return deliveryProfileError()
				}
			} else if err = visit(node); err != nil {
				return err
			}
		case xml.EndElement:
			depth--
		case xml.CharData:
			if len(bytes.TrimSpace(node)) != 0 {
				return deliveryProfileError()
			}
		case xml.Directive:
			return deliveryProfileError()
		}
	}
}

// This uses the same expansion work, exact-length and CRC boundary as ordinary
// archive members. The caller established the XML media contract via OPC.
func inspectDeclaredXMLMember(entry *zip.File, work *archiveInspectionWork) error {
	body, reader, err := openArchiveInspectionEntry(entry, work)
	if err != nil {
		return err
	}
	limited := &io.LimitedReader{R: reader, N: int64(entry.UncompressedSize64) + 1}
	inspection := scanArchiveXML(limited, int64(entry.UncompressedSize64))
	if inspection == nil {
		var extra [1]byte
		n, readErr := limited.Read(extra[:])
		if n != 0 || readErr != io.EOF || limited.N != 1 {
			inspection = deliveryProfileError()
			if readErr != nil && readErr != io.EOF {
				inspection = readErr
			}
		}
	}
	return closeArchiveInspectionEntry(body, inspection)
}
