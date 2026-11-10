package generationtool

import (
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

func TestResultPicturesKeepEveryCandidateForTheContactSheet(t *testing.T) {
	tool := catalog.ChatToolSchema{Capability: "image-to-image"}
	urls := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"}
	pictures := resultPictures(tool, urls)
	if len(pictures) != len(urls) {
		t.Fatalf("pictures = %+v; every candidate must reach the vision planner (#247)", pictures)
	}
	for index, picture := range pictures {
		if !picture.Fresh || picture.Purpose != "result" || picture.URL != urls[index] {
			t.Fatalf("picture %d = %+v", index, picture)
		}
	}
}

func TestResultPicturesIgnores3DAndEmptyResults(t *testing.T) {
	if pictures := resultPictures(catalog.ChatToolSchema{Capability: "text-to-3d"}, []string{"a"}); pictures != nil {
		t.Fatalf("3D pictures = %+v", pictures)
	}
	if pictures := resultPictures(catalog.ChatToolSchema{Capability: "image-to-image"}, nil); pictures != nil {
		t.Fatalf("empty pictures = %+v", pictures)
	}
}
