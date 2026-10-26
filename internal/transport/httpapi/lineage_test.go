package httpapi_test

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func lineagePath(sessionID string) string {
	return "/api/v1/chat/sessions/" + sessionID + "/lineage"
}

func TestChatSessionLineageIsOwnerOnly(t *testing.T) {
	t.Parallel()
	handler, cookie, sessionID := grantTestBrowser(t)
	own := grantTestRequest(t, handler, cookie, http.MethodGet, lineagePath(sessionID), "", "")
	if own.Code != http.StatusOK || strings.TrimSpace(own.Body.String()) != `{"nodes":[],"edges":[],"truncated":false}` {
		t.Fatalf("own empty session: %d %s", own.Code, own.Body.String())
	}
	if cache := own.Header().Get("Cache-Control"); !strings.Contains(cache, "private") {
		t.Fatalf("lineage must not be shared-cacheable: %q", cache)
	}
	// A session that is not the caller's and one that does not exist are the same 404.
	for _, id := range []string{"foreign-session", "no-such-session"} {
		response := grantTestRequest(t, handler, cookie, http.MethodGet, lineagePath(id), "", "")
		if response.Code != http.StatusNotFound || strings.Contains(response.Body.String(), "nodes") {
			t.Fatalf("%s: %d %s", id, response.Code, response.Body.String())
		}
	}
	anonymous := grantTestRequest(t, handler, nil, http.MethodGet, lineagePath(sessionID), "", "")
	// Without the owner's login the caller is someone else: never the chain.
	if anonymous.Code != http.StatusUnauthorized && anonymous.Code != http.StatusNotFound || strings.Contains(anonymous.Body.String(), "nodes") {
		t.Fatalf("anonymous: %d %s", anonymous.Code, anonymous.Body.String())
	}
	if post := grantTestRequest(t, handler, cookie, http.MethodPost, lineagePath(sessionID), "{}", ""); post.Code == http.StatusOK {
		t.Fatalf("lineage is read-only: %d", post.Code)
	}
}

func TestChatSessionLineagePagingIsOptInAndValidatesInput(t *testing.T) {
	t.Parallel()
	handler, cookie, sessionID := grantTestBrowser(t)
	page := grantTestRequest(t, handler, cookie, http.MethodGet, lineagePath(sessionID)+"?cursor=", "", "")
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), `"next_cursor":""`) {
		t.Fatalf("empty page: %d %s", page.Code, page.Body.String())
	}
	for _, query := range []string{"?cursor=broken!", "?limit=0", "?limit=100000", "?limit=x"} {
		response := grantTestRequest(t, handler, cookie, http.MethodGet, lineagePath(sessionID)+query, "", "")
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d %s", query, response.Code, response.Body.String())
		}
	}
	for _, id := range []string{"foreign-session", "no-such-session"} {
		response := grantTestRequest(t, handler, cookie, http.MethodGet, lineagePath(id)+"?cursor=broken!", "", "")
		if response.Code != http.StatusNotFound {
			t.Fatalf("foreign page: %d %s", response.Code, response.Body.String())
		}
	}
}

func TestLineageHTTPNativeCursorRoundTripAndCrossOwner(t *testing.T) {
	t.Parallel()
	// The existing retry fixture has four failed tasks and a real foreign session.
	handler, cookie := retryBrowser(t)
	type pageWire struct {
		Nodes []struct {
			ID      string `json:"id"`
			State   string `json:"state"`
			Version int    `json:"version"`
		} `json:"nodes"`
		NextCursor string `json:"next_cursor"`
	}
	cursor := ""
	seen := map[string]bool{}
	for i := 0; i < 4; i++ {
		response := grantTestRequest(t, handler, cookie, http.MethodGet, lineagePath(retrySessionID)+"?limit=1&cursor="+url.QueryEscape(cursor), "", "")
		if response.Code != http.StatusOK {
			t.Fatalf("page: %d %s", response.Code, response.Body.String())
		}
		var page pageWire
		if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		if len(page.Nodes) != 1 || seen[page.Nodes[0].ID] || page.Nodes[0].State != "FAILED" || page.Nodes[0].Version != 0 {
			t.Fatalf("page = %#v", page)
		}
		seen[page.Nodes[0].ID] = true
		cursor = page.NextCursor
		if (i < 3) != (cursor != "") {
			t.Fatalf("cursor on page %d = %q", i, cursor)
		}
	}
	foreign := grantTestRequest(t, handler, cookie, http.MethodGet, lineagePath("foreign-session")+"?limit=1", "", "")
	if foreign.Code != http.StatusNotFound {
		t.Fatalf("foreign: %d %s", foreign.Code, foreign.Body.String())
	}
	before := base64.RawURLEncoding.EncodeToString([]byte("2000-01-01T00:00:00Z\x00old"))
	raw, _ := json.Marshal(map[string]string{"owner": "owner-a", "session": retrySessionID, "before": before})
	emptyCursor := base64.RawURLEncoding.EncodeToString(raw)
	empty := grantTestRequest(t, handler, cookie, http.MethodGet, lineagePath(retrySessionID)+"?cursor="+emptyCursor, "", "")
	if empty.Code != http.StatusOK || strings.TrimSpace(empty.Body.String()) != `{"nodes":[],"edges":[],"truncated":false,"next_cursor":""}` {
		t.Fatalf("empty older page: %d %s", empty.Code, empty.Body.String())
	}
	for _, before := range []string{
		"broken!",
		base64.RawURLEncoding.EncodeToString([]byte("no separator")),
		base64.RawURLEncoding.EncodeToString([]byte("not-a-time\x00task")),
		base64.RawURLEncoding.EncodeToString([]byte("2000-01-01T00:00:00Z\x00")),
	} {
		raw, _ = json.Marshal(map[string]string{"owner": "owner-a", "session": retrySessionID, "before": before})
		bad := grantTestRequest(t, handler, cookie, http.MethodGet, lineagePath(retrySessionID)+"?cursor="+base64.RawURLEncoding.EncodeToString(raw), "", "")
		if bad.Code != http.StatusBadRequest {
			t.Fatalf("malformed nested cursor: %d %s", bad.Code, bad.Body.String())
		}
	}
}
