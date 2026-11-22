package assetlist

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// sanitizeArchiveName decides the directory segment of one item, which the ZIP
// reader later extracts on the person's own filesystem: the characters Windows
// refuses and the device names it reserves must never reach it (#425).
func TestSanitizeArchiveName(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		raw   string
		limit int
		want  string
	}{
		{name: "plain ascii", raw: "Hero", want: "Hero"},
		{name: "chinese is preserved", raw: "主角", want: "主角"},
		{name: "path separators become dashes", raw: `a/b\c`, want: "a-b-c"},
		{name: "windows-forbidden characters become dashes", raw: `a:b*c?d"e<f>g|h`, want: "a-b-c-d-e-f-g-h"},
		{name: "control and format characters become dashes", raw: "a\x00b​c", want: "a-b-c"},
		{name: "surrounding spaces and dots are dropped", raw: "  Hero. . ", want: "Hero"},
		{name: "a trailing dot alone leaves the item unnamed", raw: " . ", want: "item"},
		{name: "an empty name falls back", raw: "", want: "item"},
		{name: "reserved device name is prefixed", raw: "CON", want: "_CON"},
		{name: "reserved device name is case-insensitive", raw: "prn", want: "_prn"},
		{name: "reserved device name with an extension is prefixed", raw: "Aux.txt", want: "_Aux.txt"},
		{name: "reserved device name with a trailing space is prefixed", raw: "NUL .txt", want: "_NUL .txt"},
		{name: "the first COM port is reserved", raw: "COM1", want: "_COM1"},
		{name: "a higher LPT port is reserved", raw: "lpt9", want: "_lpt9"},
		{name: "COM0 and LPT0 are not reserved", raw: "COM0", want: "COM0"},
		{name: "a name that only starts with a device name is kept", raw: "console", want: "console"},
		{name: "a name that only ends with a device name is kept", raw: "my-con", want: "my-con"},
		{name: "truncation keeps the value on a rune boundary", raw: "字word", limit: 6, want: "字wor"},
		{name: "truncation drops the spaces and dots it leaves", raw: "abcdefgh", limit: 3, want: "abc"},
		{name: "a truncated device name is still prefixed", raw: "contact sheet", limit: 3, want: "_co"},
	}
	for _, test := range cases {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := sanitizeArchiveName(test.raw, test.limit); got != test.want {
				t.Fatalf("sanitizeArchiveName(%q, %d) = %q; want %q", test.raw, test.limit, got, test.want)
			}
		})
	}
}

// #425: the batch ZIP's README is Schema copy, so a download reads in the
// language of the browser that asked for it, never in one fixed language.
func TestArchiveReadmeFollowsTheRequestLanguage(t *testing.T) {
	t.Parallel()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	texts, err := catalog.NewTextLocalizer(application)
	if err != nil {
		t.Fatal(err)
	}
	service := &Service{
		archive: &catalog.AssetListArchiveSchema{Readme: application.AssetList.Review.Archive.Readme},
		texts:   texts,
	}
	triangles, texture := int64(9000), int64(2048)
	manifest := ArchiveManifest{
		ListID: "alist_1", Title: "Chapter one", GeneratedAt: time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC),
		Items: []ArchiveManifestItem{{
			Position: 1, Name: "Hero", Kind: "character", Round: 2, TaskID: "task_1",
			Directory: "01-Hero", Formats: []string{"glb", "png"},
			Triangles: &triangles, TextureMaxEdge: &texture,
			Checks: []ArchiveManifestCheck{{Check: "triangles", Status: "pass", Budget: "max 30000"}},
			Files: []ArchiveManifestFile{
				{Path: "01-Hero/model.glb", Role: "model", Format: "glb", Size: 10, SHA256: "aa"},
			},
			Missing: []ArchiveManifestMissing{{AssetID: "asset_1", MediaType: "image/png"}},
		}},
	}
	for _, test := range []struct {
		locale string
		want   []string
	}{
		{"", []string{
			"资产清单：Chapter one", "清单 ID：alist_1", "共 1 项、1 个文件。",
			"01. Hero（character，第 2 版）", "目录：01-Hero", "来源任务：task_1",
			"格式：glb、png", "面数：9000", "贴图最大边：2048",
			"验收：triangles=pass（max 30000）",
			"- 01-Hero/model.glb（model，glb，10 字节，sha256 aa）",
			"缺失（存储中已不存在，未打包）：", "- asset_1（未知角色，image/png）",
		}},
		{"en", []string{
			"Asset list: Chapter one", "List ID: alist_1", "1 items, 1 files.",
			"01. Hero (character, round 2)", "Directory: 01-Hero", "Source task: task_1",
			"Formats: glb, png", "Triangles: 9000", "Maximum texture edge: 2048",
			"Acceptance: triangles=pass (max 30000)",
			"- 01-Hero/model.glb (model, glb, 10 bytes, sha256 aa)",
			"Missing (recorded, but no longer in storage; not packaged):",
			"- asset_1 (unknown role, image/png)",
		}},
	} {
		test := test
		t.Run(test.locale, func(t *testing.T) {
			ctx := context.Background()
			if test.locale != "" {
				ctx = catalog.WithLocale(ctx, test.locale)
			}
			readme := string(service.archiveReadme(ctx, manifest, 1))
			for _, want := range test.want {
				if !strings.Contains(readme, want) {
					t.Fatalf("%s README misses %q:\n%s", test.locale, want, readme)
				}
			}
		})
	}
}
