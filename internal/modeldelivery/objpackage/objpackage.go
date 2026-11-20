// Package objpackage holds the OBJ material-library and texture reference
// rules shared by the single-task model delivery (#330) and the production
// list batch archive (#329c).
//
// The Product Schema declares the keywords and bindings
// (application.model_delivery.archive.obj_package); this package only reads
// the files and matches names, so both ZIP layouts can keep an OBJ loadable
// after extraction without a second copy of the rules.
package objpackage

import (
	"bufio"
	"io"
	"path"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// LibraryName reads the first material library name the OBJ references
// (spec.LibraryStatement, normally "mtllib"). It returns "" when the scanned
// bytes name none or the name would leave the folder it is extracted to.
func LibraryName(reader io.Reader, spec catalog.ModelObjPackageSchema) string {
	if strings.TrimSpace(spec.LibraryStatement) == "" {
		return ""
	}
	scanner := NewLineScanner(io.LimitReader(reader, scanLimit(spec)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		keyword, rest, found := strings.Cut(line, " ")
		if !found || !strings.EqualFold(keyword, spec.LibraryStatement) {
			continue
		}
		if name, ok := RelativeName(strings.TrimSpace(rest)); ok {
			return name
		}
	}
	return ""
}

// Texture is one texture file a material library references, with the
// statement that references it.
type Texture struct {
	Statement string
	Path      string
}

// Textures reads the texture file names a material library uses, in order,
// with the statement that references each. Names are placed relative to the
// library's folder, the way a viewer resolves them.
func Textures(reader io.Reader, spec catalog.ModelObjPackageSchema, materialPath, directory string) []Texture {
	base := path.Dir(materialPath)
	if base == "." || base == "" {
		base = directory
	}
	var references []Texture
	seen := map[string]bool{}
	scanner := NewLineScanner(io.LimitReader(reader, scanLimit(spec)))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		statement := strings.ToLower(fields[0])
		if !strings.HasPrefix(statement, "map_") && statement != "bump" && statement != "norm" &&
			statement != "disp" && statement != "decal" && statement != "refl" {
			continue
		}
		name, ok := RelativeName(fields[len(fields)-1])
		if !ok {
			continue
		}
		full := path.Join(base, name)
		if seen[full] {
			continue
		}
		seen[full] = true
		references = append(references, Texture{Statement: statement, Path: full})
	}
	return references
}

// Assign matches saved texture variants to the material library's references,
// in order: a texture takes the first reference not taken yet whose statement
// its variant key binds (spec.TextureBindings). Every result is the index into
// references, or -1 when nothing binds.
func Assign(spec catalog.ModelObjPackageSchema, variants []string, references []Texture) []int {
	chosen := make([]int, len(variants))
	taken := make([]bool, len(references))
	for index := range chosen {
		chosen[index] = -1
	}
	for texture, variant := range variants {
		for reference, candidate := range references {
			if taken[reference] || !Binds(spec, variant, candidate.Statement) {
				continue
			}
			chosen[texture] = reference
			taken[reference] = true
			break
		}
	}
	return chosen
}

// Binds reports whether the material statement belongs to the texture variant
// key the Schema declares.
func Binds(spec catalog.ModelObjPackageSchema, variantKey, statement string) bool {
	for _, binding := range spec.TextureBindings {
		if !strings.EqualFold(binding.VariantKey, variantKey) {
			continue
		}
		for _, candidate := range binding.Statements {
			if strings.EqualFold(candidate, statement) {
				return true
			}
		}
	}
	return false
}

// VariantKey is the last dotted segment of a saved texture variant
// ("base_color.png" → "png").
func VariantKey(variant string) string {
	key := variant
	if cut := strings.LastIndex(key, "."); cut >= 0 {
		key = key[cut+1:]
	}
	return key
}

// RelativeName accepts only a name that stays inside the folder it is
// extracted to. Backslashes become slashes and surrounding quotes are dropped.
func RelativeName(name string) (string, bool) {
	name = strings.ReplaceAll(name, "\\", "/")
	name = strings.Trim(name, "\"")
	if name == "" || strings.ContainsRune(name, 0) || strings.HasPrefix(name, "/") ||
		strings.Contains(name, ":") {
		return "", false
	}
	cleaned := path.Clean(name)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", false
	}
	return cleaned, true
}

// NewLineScanner reads long material lines (a texture name list can exceed the
// scanner's default token size).
func NewLineScanner(reader io.Reader) *bufio.Scanner {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	return scanner
}

// scanLimit bounds how much of an OBJ or material library is read. A Schema
// without the value keeps the historical one-mebibyte bound.
func scanLimit(spec catalog.ModelObjPackageSchema) int64 {
	if spec.ReferenceScanBytes > 0 {
		return spec.ReferenceScanBytes
	}
	return 1 << 20
}
