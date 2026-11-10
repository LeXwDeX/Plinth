package generationtool

import (
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

func TestResultPicturesSamplesTheVisionBudget(t *testing.T) {
	tool := catalog.ChatToolSchema{Capability: "image-to-image"}
	urls := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"}
	pictures := resultPictures(tool, urls, 4)
	if len(pictures) != 4 || !pictures[0].Fresh {
		t.Fatalf("pictures = %+v", pictures)
	}
	if pictures[0].URL != "a" || pictures[3].URL != "j" {
		t.Fatalf("sampled = %+v", pictures)
	}
	for _, picture := range pictures[1:] {
		if !picture.Fresh {
			t.Fatalf("picture not marked fresh: %+v", picture)
		}
	}
}

func TestResultPicturesIgnores3DAndEmptyResults(t *testing.T) {
	if pictures := resultPictures(catalog.ChatToolSchema{Capability: "text-to-3d"}, []string{"a"}, 4); pictures != nil {
		t.Fatalf("3D pictures = %+v", pictures)
	}
	if pictures := resultPictures(catalog.ChatToolSchema{Capability: "image-to-image"}, nil, 4); pictures != nil {
		t.Fatalf("empty pictures = %+v", pictures)
	}
}

func TestEvenlySpacedKeepsBothEnds(t *testing.T) {
	urls := []string{"a", "b", "c", "d", "e", "f", "g"}
	if got := evenlySpaced(urls, 3); len(got) != 3 || got[0] != "a" || got[2] != "g" {
		t.Fatalf("evenlySpaced = %v", got)
	}
	if got := evenlySpaced(urls, 1); len(got) != 1 || got[0] != "d" {
		t.Fatalf("single pick = %v", got)
	}
	if got := evenlySpaced(urls, 9); len(got) != len(urls) {
		t.Fatalf("over-limit pick = %v", got)
	}
}
