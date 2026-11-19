package local

import (
	"strings"
	"testing"
)

// #386: the isolation already treats the OPC core-properties relationship type
// as a specification identifier, yet the part that relationship points to
// declares the core-properties namespace (and the DCMI type vocabulary), which
// were not on the same list. A package carrying a standard docProps/core.xml was
// therefore rejected as holding "external addresses" and, before the archive
// rules were named, reported only archive_rejected. These tests keep the two
// halves consistent: the standard namespace identifiers are accepted, and every
// address in a value, a text node or a foreign namespace is still refused.

const opcCoreProperties = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:dcterms="http://purl.org/dc/terms/" xmlns:dcmitype="http://purl.org/dc/dcmitype/" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">
  <dc:title>Model</dc:title>
  <dc:creator>Converter</dc:creator>
  <cp:lastModifiedBy>Converter</cp:lastModifiedBy>
  <dcterms:created xsi:type="dcterms:W3CDTF">2026-11-16T10:04:20Z</dcterms:created>
  <dcterms:modified xsi:type="dcterms:W3CDTF">2026-11-16T10:04:20Z</dcterms:modified>
</cp:coreProperties>`

// with3MFCoreProperties is the standard 3MF package plus an OPC core-properties
// part: the part itself, its content type override and the package relationship
// that names it.
func with3MFCoreProperties(coreProperties string) map[string]string {
	return with3MFEntries(map[string]string{
		"[Content_Types].xml": `<?xml version="1.0" encoding="UTF-8"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="model" ContentType="application/vnd.ms-package.3dmanufacturing-3dmodel+xml"/><Override PartName="/docProps/core.xml" ContentType="application/vnd.openxmlformats-package.core-properties+xml"/></Types>`,
		"_rels/.rels":         `<?xml version="1.0" encoding="UTF-8"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Target="/3D/3dmodel.model" Id="rel0" Type="http://schemas.microsoft.com/3dmanufacturing/2013/01/3dmodel"/><Relationship Target="/docProps/core.xml" Id="rel1" Type="http://schemas.openxmlformats.org/package/2006/relationships/metadata/core-properties"/></Relationships>`,
		"docProps/core.xml":   coreProperties,
	})
}

func TestOPCCoreProperties3MFIsAccepted(t *testing.T) {
	checkFormatSlot(t, zipModel(t, with3MFCoreProperties(opcCoreProperties)), "converted.3mf", "application/octet-stream", "")
	// The same package through the strict 3MF slot spelling.
	checkFormatSlot(t, zipModel(t, with3MFCoreProperties(opcCoreProperties)), "3mf", "model/3mf", "")
}

func TestOPCCoreProperties3MFStillRefusesAddresses(t *testing.T) {
	for _, test := range []struct{ name, core string }{
		{"URL as a property value", strings.Replace(opcCoreProperties, "<dc:title>Model</dc:title>", "<dc:title>http://evil.example/x</dc:title>", 1)},
		{"URL inside a date value", strings.Replace(opcCoreProperties, "2026-11-16T10:04:20Z</dcterms:created>", "http://evil.example/x</dcterms:created>", 1)},
		{"URL in an attribute value", strings.Replace(opcCoreProperties, "<dc:title>", `<dc:title lang="http://evil.example/x">`, 1)},
		{"foreign namespace", strings.Replace(opcCoreProperties, `xmlns:dc=`, `xmlns:evil="http://evil.example/x" xmlns:dc=`, 1)},
		{"foreign namespace replaces a standard one", strings.Replace(opcCoreProperties, "http://purl.org/dc/dcmitype/", "http://evil.example/x", 1)},
		{"standard identifier with a suffix", strings.Replace(opcCoreProperties, "core-properties\"", "core-properties/https://evil.example/x\"", 1)},
		{"standard identifier as text", strings.Replace(opcCoreProperties, "<dc:title>Model</dc:title>", "<dc:title>http://purl.org/dc/dcmitype/</dc:title>", 1)},
		{"standard identifier as an attribute value", strings.Replace(opcCoreProperties, "<dc:title>", `<dc:title href="http://purl.org/dc/dcmitype/">`, 1)},
		{"relative network address", strings.Replace(opcCoreProperties, "<dc:title>Model</dc:title>", "<dc:title>//localhost/x</dc:title>", 1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.core == opcCoreProperties {
				t.Fatal("fixture was not changed")
			}
			checkFormatSlot(t, zipModel(t, with3MFCoreProperties(test.core)), "converted.3mf", "application/octet-stream", "external_address")
		})
	}
}

// Slic3r and PrusaSlicer write their own vocabulary into the model part, under
// the slic3rpe prefix (src/slic3r-shared/src/Slic3r/Biz/Format/3mf/Model3mf.cpp,
// XMLNS_SLIC3R_VALUE).
func TestSlic3rPE3MFNamespaceIsAccepted(t *testing.T) {
	model := strings.Replace(standard3MFEntries()["3D/3dmodel.model"],
		`xmlns="http://schemas.microsoft.com/3dmanufacturing/core/2015/02">`,
		`xmlns="http://schemas.microsoft.com/3dmanufacturing/core/2015/02" xmlns:slic3rpe="http://schemas.slic3r.org/3mf/2017/06">
 <metadata name="slic3rpe:Version3mf">1</metadata>`, 1)
	entries := with3MFEntries(map[string]string{
		"3D/3dmodel.model":                model,
		"Metadata/Slic3r_PE_model.config": `<?xml version="1.0" encoding="UTF-8"?><config><object id="1" instances_count="1"><metadata type="object" key="name" value="model"/></object></config>`,
	})
	checkFormatSlot(t, zipModel(t, entries), "converted.3mf", "application/octet-stream", "")

	for name, namespace := range map[string]string{
		"suffix":         "http://schemas.slic3r.org/3mf/2017/06/https://evil.example/x",
		"other revision": "http://schemas.slic3r.org/3mf/2018/06",
		"other host":     "http://schemas.slic3r.example/3mf/2017/06",
	} {
		t.Run(name, func(t *testing.T) {
			changed := strings.Replace(model, "http://schemas.slic3r.org/3mf/2017/06", namespace, 1)
			checkFormatSlot(t, zipModel(t, with3MFEntries(map[string]string{"3D/3dmodel.model": changed})), "converted.3mf", "application/octet-stream", "external_address")
		})
	}
}
