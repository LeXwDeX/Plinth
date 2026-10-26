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
		}
	case "www.w3.org":
		switch parsed.Path {
		case "/2001/XMLSchema", "/2001/XMLSchema-instance", "/XML/1998/namespace":
			return true
		}
	case "purl.org":
		return parsed.Path == "/dc/elements/1.1/" || parsed.Path == "/dc/terms/"
	case "schemas.bambulab.com":
		return parsed.Path == "/package/2021"
	}
	return false
}
