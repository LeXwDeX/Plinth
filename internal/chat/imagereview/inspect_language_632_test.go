package imagereview

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// #632: what inspect_image hands the Agent in a Chinese session carries no
// English phrase it could copy into its reply. Checks are measurements in the
// report's field names; every sentence is the Schema's Chinese copy. The
// golden results are exactly what Inspect returns, so this reads them.
func TestInspectResultsCarryNoEnglishPhrase(t *testing.T) {
	machineToken := regexp.MustCompile(`[A-Za-z0-9_@#./{}=<>%|-]*[_@#{}0-9=<>%][A-Za-z0-9_@#./{}=<>%|-]*`)
	phrase := regexp.MustCompile(`[A-Za-z][A-Za-z'’]*(?:[ ,;:()/-]+[A-Za-z][A-Za-z'’]*){2,}`)
	var walk func(value any, path string)
	paths, err := filepath.Glob(filepath.Join("testdata", "inspect", "*.json"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("golden results: %v %v", paths, err)
	}
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var record struct {
			AgentContent any `json:"agent_content"`
		}
		if err := json.Unmarshal(raw, &record); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		walk = func(value any, at string) {
			switch typed := value.(type) {
			case map[string]any:
				for key, child := range typed {
					walk(child, at+"."+key)
				}
			case []any:
				for _, child := range typed {
					walk(child, at+"[]")
				}
			case string:
				if match := phrase.FindString(machineToken.ReplaceAllString(typed, "|")); match != "" {
					t.Errorf("%s %s = %q carries the English phrase %q", filepath.Base(path), at, typed, match)
				}
			}
		}
		walk(record.AgentContent, "")
	}
}
