package httpapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

var chineseCharacter = regexp.MustCompile(`[\x{3400}-\x{9fff}]`)

// chineseStrings lists the string leaves of a JSON body that contain Chinese.
func chineseStrings(t *testing.T, body []byte) []string {
	t.Helper()
	var document any
	if err := json.Unmarshal(body, &document); err != nil {
		t.Fatal(err)
	}
	var found []string
	var walk func(any)
	walk = func(node any) {
		switch value := node.(type) {
		case map[string]any:
			for _, child := range value {
				walk(child)
			}
		case []any:
			for _, child := range value {
				walk(child)
			}
		case string:
			if chineseCharacter.MatchString(value) {
				found = append(found, value)
			}
		}
	}
	walk(document)
	return found
}

func getWithLocale(t *testing.T, handler http.Handler, path, acceptLanguage string) []byte {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.Header.Set("X-User-ID", "owner-a")
	response := serveWithLocale(handler, request, acceptLanguage)
	if response.Code != http.StatusOK {
		t.Fatalf("GET %s = %d %s", path, response.Code, response.Body.String())
	}
	if vary := response.Header().Values("Vary"); len(vary) == 0 {
		t.Fatalf("GET %s does not vary by locale", path)
	}
	return response.Body.Bytes()
}

func TestOfferingsFollowTheRequestLocale(t *testing.T) {
	t.Parallel()
	_, english := englishBundle(t)
	_, handler, _ := newHTTPHarness(t)

	if leftover := chineseStrings(t, getWithLocale(t, handler, "/api/v1/offerings", "en-US")); len(leftover) > 0 {
		t.Fatalf("English offerings still carry Chinese: %q", leftover[:min(len(leftover), 5)])
	}
	body := getWithLocale(t, handler, "/api/v1/offerings", "")
	if len(chineseStrings(t, body)) == 0 {
		t.Fatal("default-locale offerings lost their Chinese copy")
	}

	type offeringLabels struct {
		Offerings []struct {
			ID         string `json:"id"`
			Parameters []struct {
				Label string `json:"label"`
			} `json:"parameters"`
		} `json:"offerings"`
	}
	var chinese, translated offeringLabels
	if err := json.Unmarshal(body, &chinese); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(getWithLocale(t, handler, "/api/v1/offerings", "en"), &translated); err != nil {
		t.Fatal(err)
	}
	checked := 0
	for index, offering := range chinese.Offerings {
		if translated.Offerings[index].ID != offering.ID {
			t.Fatalf("offering order changed with the locale: %s", offering.ID)
		}
		for position, parameter := range offering.Parameters {
			want, exists := english.OfferingTexts[parameter.Label]
			if !exists {
				continue
			}
			if got := translated.Offerings[index].Parameters[position].Label; got != want {
				t.Fatalf("%s label %q = %q, want %q", offering.ID, parameter.Label, got, want)
			}
			checked++
		}
	}
	if checked == 0 {
		t.Fatal("no translated parameter label was checked")
	}
}

func TestProviderDiscoveryFollowsTheRequestLocale(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarness(t)
	for _, path := range []string{
		"/api/providers?capability=text-to-image&projection=extended",
		"/api/providers?capability=rigging&projection=extended",
		"/api/providers?domain=model-3d",
	} {
		if leftover := chineseStrings(t, getWithLocale(t, handler, path, "en")); len(leftover) > 0 {
			t.Fatalf("English %s still carries Chinese: %q", path, leftover[:min(len(leftover), 5)])
		}
	}
}

// A chat turn freezes the UI locale of the request that sent it, so the
// Agent's reply and the server's own messages in the turn use it.
func TestChatTurnFreezesTheRequestLocale(t *testing.T) {
	t.Parallel()
	scheduler := &recordingChatScheduler{}
	_, handler, _ := newHTTPHarnessWithChatScheduler(t, scheduler, nil)
	var session map[string]any
	decodeChatRecorder(t, performChatRequest(handler, http.MethodPost, "/api/chat/sessions", `{}`), &session)
	send := func(locale string) {
		request := httptest.NewRequest(http.MethodPost, "/api/chat/sessions/"+session["id"].(string)+"/messages",
			strings.NewReader(`{"content":"draw a cat"}`))
		request.Header.Set("X-User-ID", "local-user")
		request.Header.Set("Content-Type", "application/json")
		if response := serveWithLocale(handler, request, locale); response.Code != http.StatusAccepted {
			t.Fatalf("send %s = %d %s", locale, response.Code, response.Body.String())
		}
	}
	send("en-GB")
	if len(scheduler.commands) != 1 || scheduler.commands[0].Options.Locale != "en" {
		t.Fatalf("commands = %+v", scheduler.commands)
	}
}
