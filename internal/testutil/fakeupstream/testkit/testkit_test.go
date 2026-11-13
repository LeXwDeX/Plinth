package testkit

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
)

// RouteHost is how an Adapter that reads artifacts only from public HTTPS hosts
// still gets its bytes from the fake: the named host reaches the fixture's
// server (over plain HTTP, the way httptest serves it) and no other host does.
func TestRouteHostReachesTheFakeForTheNamedHostOnly(t *testing.T) {
	f := New(t, fakeupstream.Config{}, fakeupstream.Version{
		Provider: "probe", API: "v1", Prefix: "/probe",
		Handler: func(*fakeupstream.Env) (http.Handler, error) {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprintf(w, "%s %s?%s host=%s", r.Method, r.URL.Path, r.URL.RawQuery, r.Host)
			}), nil
		},
	})
	client := &http.Client{Transport: f.RouteHost("assets.example")}

	for _, address := range []string{"https://assets.example/probe/model.glb?fake-issue=1", "https://ASSETS.example/probe/model.glb?fake-issue=1"} {
		response, err := client.Get(address)
		if err != nil {
			t.Fatalf("%s: %v", address, err)
		}
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		want := "GET /model.glb?fake-issue=1 host=" + strings.TrimPrefix(f.Server.URL, "http://")
		if response.StatusCode != http.StatusOK || string(body) != want {
			t.Fatalf("%s answered %d %q, want %q", address, response.StatusCode, body, want)
		}
	}

	for _, address := range []string{"https://other.example/probe/model.glb", "https://assets.example.evil.test/probe/model.glb"} {
		response, err := client.Get(address)
		if err == nil {
			response.Body.Close()
			t.Fatalf("%s reached the fake", address)
		}
		if !strings.Contains(err.Error(), "only routes") {
			t.Fatalf("%s: unexpected refusal %v", address, err)
		}
	}
}

// RouteHosts serves an Adapter that sends its API requests and reads its
// artifacts through one client: both named hosts reach the fake, any other one
// is refused before a socket is opened, and the refusal names every routed host.
func TestRouteHostsReachesTheFakeForEveryNamedHostOnly(t *testing.T) {
	f := New(t, fakeupstream.Config{}, fakeupstream.Version{
		Provider: "probe", API: "v1", Prefix: "/probe",
		Handler: func(*fakeupstream.Env) (http.Handler, error) {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprintf(w, "%s %s", r.Method, r.URL.Path)
			}), nil
		},
	})
	client := &http.Client{Transport: f.RouteHosts("api.example", "files.example")}

	for _, address := range []string{"https://api.example/probe/status", "https://FILES.example/probe/model.glb"} {
		response, err := client.Get(address)
		if err != nil {
			t.Fatalf("%s: %v", address, err)
		}
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != http.StatusOK || !strings.HasPrefix(string(body), "GET /") {
			t.Fatalf("%s answered %d %q", address, response.StatusCode, body)
		}
	}
	response, err := client.Get("https://third.example/probe/status")
	if err == nil {
		response.Body.Close()
		t.Fatal("a host nobody named reached the fake")
	}
	if !strings.Contains(err.Error(), `"api.example, files.example"`) {
		t.Fatalf("the refusal does not name the routed hosts: %v", err)
	}
}
