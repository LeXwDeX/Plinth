package architecture_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The open-source runtime carries no organization's domains, brand, internal
// hosts or server paths (#249); deployments provide them through runtime
// settings. The Go module path is renamed separately (#250) and is ignored.
// The markers are assembled from pieces so that this guard itself, and the
// public snapshot scan (scripts/check-public-tree.sh, #261), never meet them
// in plain text.
var organizationIdentifiers = []string{
	"yc" + "game",
	"yun" + "chan",
	"云" + "蟾",
	"192." + "168.33.",
	"192." + "168.34.",
	"33." + "110",
	"33." + "146",
	"/data/" + "appdata",
}

const goModulePath = "github.com/LeXwDeX/Plinth"

// Guards build their markers from pieces (see above), so no source file is
// exempt from the scan.
var organizationIdentifierGuards = map[string]bool{}

func TestRuntimeSourcesCarryNoOrganizationIdentifiers(t *testing.T) {
	root := filepath.Join("..", "..")
	for _, directory := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(
			filepath.Join(root, directory),
			func(path string, entry fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if entry.IsDir() || !scannedSource(entry.Name()) {
					return nil
				}
				relative, err := filepath.Rel(root, path)
				if err != nil {
					return err
				}
				if organizationIdentifierGuards[relative] {
					return nil
				}
				content, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				text := strings.ToLower(
					strings.ReplaceAll(string(content), goModulePath, ""),
				)
				for _, marker := range organizationIdentifiers {
					if strings.Contains(text, marker) {
						t.Errorf("%s contains %q", relative, marker)
					}
				}
				return nil
			},
		)
		if err != nil {
			t.Fatal(err)
		}
	}
}

func scannedSource(name string) bool {
	switch filepath.Ext(name) {
	case ".go", ".json", ".sql", ".yaml", ".yml", ".tmpl", ".html":
		return true
	}
	return false
}
