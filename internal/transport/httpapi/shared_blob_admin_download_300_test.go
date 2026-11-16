package httpapi_test

import (
	"archive/zip"
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/mock"
)

func readZipEntries(t *testing.T, body []byte) map[string][]byte {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("response is not a ZIP: %v", err)
	}
	entries := map[string][]byte{}
	for _, file := range reader.File {
		handle, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		entries[file.Name], _ = io.ReadAll(handle)
		_ = handle.Close()
	}
	return entries
}

// #300: the administration download of a task whose model and converted.gltf slots
// share one file packs that file once and explains the other slot in a README, in
// the words of the one-click ZIP: the same heading and the same line for a repeat.
func TestAdministrationDownloadHoldsASharedFileOnceAndNamesItLikeTheOneClickZip(t *testing.T) {
	adapter := &sharedGLBTripoAdapter{&httpMeshyFormatFixtureAdapter{Adapter: mock.New()}}
	module, handler, _, _ := newHTTPHarnessWithCatalogAndProviders(t, []string{"admin"}, nil, nil, nil, nil, nil,
		map[string]generation.ProviderPort{"tripo.v3": adapter})
	task := createSucceededTripoModelTask(t, module, "owner-a")
	var shared []byte
	for _, asset := range task.Assets {
		if asset.Role == "model" && asset.Variant == "" {
			response := modelDeliveryRequest(handler, http.MethodGet, "/api/v1/tasks/"+string(task.ID)+"/assets/"+string(asset.ID), "owner-a")
			if response.Code != http.StatusOK {
				t.Fatalf("model asset = %d", response.Code)
			}
			shared = response.Body.Bytes()
		}
	}
	if len(shared) == 0 {
		t.Fatal("the task saved no model file")
	}

	oneClick := modelDeliveryRequest(handler, http.MethodGet, "/api/v1/tasks/"+string(task.ID)+"/archive", "owner-a")
	if oneClick.Code != http.StatusOK {
		t.Fatalf("one-click archive = %d %s", oneClick.Code, oneClick.Body)
	}
	oneClickEntries := readZipEntries(t, oneClick.Body.Bytes())

	admin := httptest.NewRecorder()
	handler.ServeHTTP(admin, httptest.NewRequest(http.MethodGet, "/api/admin/generations/"+string(task.ID)+"/download", nil))
	if admin.Code != http.StatusOK || admin.Header().Get("Content-Type") != "application/zip" {
		t.Fatalf("admin download = %d %v %s", admin.Code, admin.Header(), admin.Body)
	}
	entries := readZipEntries(t, admin.Body.Bytes())

	// The shared bytes are in the package once.
	holders := 0
	for _, body := range entries {
		if bytes.Equal(body, shared) {
			holders++
		}
	}
	if holders != 1 {
		names := make([]string, 0, len(entries))
		for name := range entries {
			names = append(names, name)
		}
		t.Fatalf("the shared file is in the admin package %d times: %v", holders, names)
	}

	// The README says it in the one-click ZIP's words. The one-click ZIP names the
	// repeat the same way, so the heading and the shape of the line come from it.
	readme := string(entries["README.txt"])
	oneClickReadme := string(oneClickEntries["README.txt"])
	const heading = "内容相同的文件：\n"
	if !strings.Contains(oneClickReadme, heading) || !strings.HasPrefix(readme, heading) {
		t.Fatalf("headings differ:\none-click:\n%s\nadmin:\n%s", oneClickReadme, readme)
	}
	line := regexp.MustCompile(`(?m)^- (\S+) 与 (\S+) 内容相同，只收录一份$`)
	repeat := line.FindStringSubmatch(readme)
	if repeat == nil || line.FindStringSubmatch(oneClickReadme) == nil {
		t.Fatalf("a README has no repeat line:\none-click:\n%s\nadmin:\n%s", oneClickReadme, readme)
	}
	// The line names an entry that is in the package and holds the shared bytes.
	if body, found := entries[repeat[2]]; !found || !bytes.Equal(body, shared) {
		t.Fatalf("the README names %q, which does not hold the shared file (entries: %d)", repeat[2], len(entries))
	}
}
