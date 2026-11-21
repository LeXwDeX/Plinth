package objpackage_test

import (
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/modeldelivery/objpackage"
)

func spec() catalog.ModelObjPackageSchema {
	return catalog.ModelObjPackageSchema{
		FormatID: "obj", LibraryStatement: "mtllib", MaterialFileExtension: ".mtl",
		ReferenceScanBytes: 1 << 20,
		TextureBindings: []catalog.ModelTextureBinding{
			{VariantKey: "base_color", Statements: []string{"map_Kd", "map_Ka"}},
			{VariantKey: "normal", Statements: []string{"map_Bump"}},
		},
	}
}

func TestLibraryNameReadsTheFirstSafeReference(t *testing.T) {
	object := "# Blender\nmtllib temp.mtl\no Mesh\n"
	if name := objpackage.LibraryName(strings.NewReader(object), spec()); name != "temp.mtl" {
		t.Fatalf("library = %q; want temp.mtl", name)
	}
	// A name that would leave the extraction folder is never returned.
	for _, unsafe := range []string{"mtllib ../outside.mtl\n", "mtllib /abs.mtl\n", "mtllib C:\\\\abs.mtl\n"} {
		if name := objpackage.LibraryName(strings.NewReader(unsafe), spec()); name != "" {
			t.Fatalf("library of %q = %q; want none", unsafe, name)
		}
	}
	// A Schema without the keyword scans nothing instead of guessing.
	if name := objpackage.LibraryName(strings.NewReader(object), catalog.ModelObjPackageSchema{}); name != "" {
		t.Fatalf("library without a declaration = %q; want none", name)
	}
}

func TestTexturesReadsTheReferencedNamesRelativeToTheLibrary(t *testing.T) {
	library := "newmtl Material\nmap_Kd wood.png\nmap_Bump maps/normal.png\nmap_Kd wood.png\n"
	references := objpackage.Textures(strings.NewReader(library), spec(), "01-Hero/obj/model.mtl", "01-Hero/obj")
	if len(references) != 2 {
		t.Fatalf("references = %+v; want the two unique textures", references)
	}
	if references[0].Statement != "map_kd" || references[0].Path != "01-Hero/obj/wood.png" {
		t.Fatalf("first reference = %+v", references[0])
	}
	if references[1].Statement != "map_bump" || references[1].Path != "01-Hero/obj/maps/normal.png" {
		t.Fatalf("second reference = %+v", references[1])
	}
}

func TestAssignBindsAWholeFileNameVariantToItsStatement(t *testing.T) {
	references := []objpackage.Texture{
		{Statement: "map_kd", Path: "obj/wood.png"},
		{Statement: "map_bump", Path: "obj/normal.png"},
	}
	// The variant keys are the Schema's own binding keys; a texture that
	// repeats a reference already taken gets none, and VariantKey is how a
	// saved file name ("base_color.png") becomes its key ("png").
	chosen := objpackage.Assign(spec(), []string{"base_color", "normal", "base_color"},
		references)
	if len(chosen) != 3 || chosen[0] != 0 || chosen[1] != 1 || chosen[2] != -1 {
		t.Fatalf("assign = %v; want 0, 1, -1", chosen)
	}
	if key := objpackage.VariantKey("base_color.png"); key != "png" {
		t.Fatalf("variant key = %q", key)
	}
}

func TestRelativeNameRejectsEscapesAndAcceptsNestedNames(t *testing.T) {
	for input, want := range map[string]string{
		"wood.png":          "wood.png",
		"maps/normal.png":   "maps/normal.png",
		`textures\base.png`: "textures/base.png",
		`"quoted name.png"`: "quoted name.png",
	} {
		name, ok := objpackage.RelativeName(input)
		if !ok || name != want {
			t.Fatalf("RelativeName(%q) = %q, %v; want %q", input, name, ok, want)
		}
	}
	for _, unsafe := range []string{"", "..", "../x.png", "a/../../b.png", "/abs.png", "C:/x.png", "a\x00b.png"} {
		if name, ok := objpackage.RelativeName(unsafe); ok {
			t.Fatalf("RelativeName(%q) = %q; want refused", unsafe, name)
		}
	}
}
