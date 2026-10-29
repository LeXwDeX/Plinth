package align

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

// A step whose file is an earlier step's artifact (File.FromVar) uploads bytes
// the official run downloaded once. The offline export replays the saved request
// and takes those bytes from it: it needs neither the network nor a still-valid
// URL, and sends the same bytes the official request carried.
func TestOfflineExportReusesTheSavedUploadInsteadOfDownloading(t *testing.T) {
	artifact := []byte("model-bytes-the-official-run-downloaded")
	var uploaded []byte
	mux := http.NewServeMux()
	var server *httptest.Server
	mux.HandleFunc("POST /make", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"url":"`+server.URL+`/model.glb"}`)
	})
	mux.HandleFunc("GET /model.glb", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(artifact) })
	mux.HandleFunc("POST /texture", func(w http.ResponseWriter, r *http.Request) {
		reader, err := r.MultipartReader()
		if err != nil {
			t.Error(err)
			return
		}
		for {
			part, err := reader.NextPart()
			if err != nil {
				break
			}
			if part.FileName() != "" {
				uploaded, _ = io.ReadAll(part)
			}
		}
		_, _ = io.WriteString(w, `{"ok":true}`)
	})
	server = httptest.NewServer(mux)
	defer server.Close()
	steps := []Step{
		{Name: "make", Method: "POST", Path: "/make", Body: map[string]any{}, Capture: map[string]string{"model": "url"}},
		{Name: "texture", Method: "POST", Path: "/texture", RequireVar: "model", Paid: true,
			Form:  []Field{{Name: "prompt", Value: "p"}},
			Files: []File{{Field: "model", Filename: "model.glb", MediaType: "model/gltf-binary", FromVar: "model"}}},
	}
	tee := NewTee(nil)
	if _, err := Run(context.Background(), "p", "v1", Target{Name: "official", BaseURL: server.URL, Key: "k",
		Client: &http.Client{Transport: tee}}, steps, nil, nil); err != nil {
		t.Fatal(err)
	}
	if string(uploaded) != string(artifact) {
		t.Fatalf("the official upload carried %q", uploaded)
	}
	saved := filepath.Join(t.TempDir(), "raw.json")
	if err := tee.Save(saved); err != nil {
		t.Fatal(err)
	}
	replay, err := LoadReplay(saved)
	if err != nil {
		t.Fatal(err)
	}
	// The network is gone and the artifact URL no longer answers.
	server.Close()
	offline := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("offline")
	})}
	bundle, err := Run(context.Background(), "p", "v1", Target{Name: "replay", BaseURL: server.URL, Key: "k",
		Client: &http.Client{Transport: replay}, Download: offline, SkipFetch: true, NoPollDelay: true,
		FileHook: replay.MultipartFile}, steps, nil, nil)
	if err != nil {
		t.Fatalf("offline replay: %v", err)
	}
	if bundle.Obs[1].Status != http.StatusOK {
		t.Fatalf("texture replay status = %d", bundle.Obs[1].Status)
	}
	// Without the hook the same replay would have to download, and fails offline.
	if _, err := Run(context.Background(), "p", "v1", Target{Name: "replay", BaseURL: server.URL, Key: "k",
		Client: &http.Client{Transport: replay}, Download: offline, SkipFetch: true}, steps, nil, nil); err == nil {
		t.Fatal("expected the download to fail without the file hook")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
