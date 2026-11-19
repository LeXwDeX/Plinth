package local

import (
	"net/url"
	"regexp"
	"strings"
)

// 3MF extension identifiers have a named extension, dated namespace and an
// optional relationship name. No component can carry an encoded address.
var manufacturingSpecificationPath = regexp.MustCompile(`^/3dmanufacturing/(?:[a-z][a-z0-9-]*/){0,3}[0-9]{4}/(?:0[1-9]|1[0-2])(?:/[a-z0-9][a-z0-9-]*)?$`)

func publicMetadataURI(raw string) (*url.URL, bool) {
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.User != nil || parsed.Port() != "" || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" ||
		strings.ContainsAny(raw, "?#\\\x00\r\n") || strings.ContainsAny(parsed.Path, ":%?&#\\\x00\r\n") ||
		strings.Contains(parsed.Path, "//") || len(parsed.Path) > 512 {
		return nil, false
	}
	return parsed, true
}

func standardSpecificationURI(value string) bool {
	parsed, valid := publicMetadataURI(value)
	if !valid {
		return false
	}
	switch strings.ToLower(parsed.Hostname()) {
	case "schemas.microsoft.com", "schemas.3mf.io":
		return manufacturingSpecificationPath.MatchString(parsed.Path)
	case "schemas.openxmlformats.org":
		switch parsed.Path {
		case "/package/2006/content-types", "/package/2006/relationships", "/package/2006/relationships/metadata/thumbnail",
			"/package/2006/relationships/metadata/core-properties", "/package/2006/relationships/mustpreserve":
			return true
		// The namespace of the core-properties part that the relationship type
		// above names (ECMA-376 Part 2, 11.1). Listing only the relationship would
		// admit the pointer and refuse the part it points to. This is consistency of
		// the standard vocabulary found while investigating #386; it is not what
		// refused the real Tripo 3MF (see the Tripo namespace below).
		case "/package/2006/metadata/core-properties":
			return true
		}
	case "www.w3.org":
		switch parsed.Path {
		case "/2001/XMLSchema", "/2001/XMLSchema-instance", "/XML/1998/namespace":
			return true
		}
	case "purl.org":
		// Dublin Core elements and terms, and the DCMI type vocabulary the core
		// properties part declares next to them.
		return parsed.Path == "/dc/elements/1.1/" || parsed.Path == "/dc/terms/" || parsed.Path == "/dc/dcmitype/"
	case "schemas.bambulab.com":
		return parsed.Path == "/package/2021"
	case "schemas.slic3r.org":
		// Slic3r and PrusaSlicer's slic3rpe vocabulary (XMLNS_SLIC3R_VALUE in
		// PrusaSlicer's Model3mf.cpp).
		return parsed.Path == "/3mf/2017/06"
	case "www.tripo3d.ai":
		// Tripo's AI-generated-content label: every 3MF Tripo converts declares
		// this namespace on the model root for one <metadata name="…:AIGC"> entry
		// whose value is a JSON label (producer and produce ids, no address). It is
		// a namespace name, never fetched. Without it the real conversion of
		// 2026-11-19 was refused as holding an external address (#386). Unlike the
		// dated paths above it is one literal string: scheme, host case, path
		// encoding and a trailing slash are all part of the name, so no variant of
		// it is exempt.
		return value == "https://www.tripo3d.ai/ns/aigc/1.0"
	}
	return false
}
