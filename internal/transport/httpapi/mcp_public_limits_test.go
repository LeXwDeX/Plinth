package httpapi_test

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// The JSON-RPC codes of the two 429 answers of the external listener, written
// out here and not read from the server: a client matches on them, so changing
// either is a change of the contract. Neither is a code the protocol or the
// rest of the listener uses (-32001, -32003, -32020, -32022, ...).
const (
	mcpCodeRateLimited   = -32090
	mcpCodeAuthThrottled = -32091
)

// mcpLimits declares the per-minute limits of the listener for one fixture:
// requests a key may make and failed authentications an address may make.
func mcpLimits(requests, failures int) func(*catalog.ApplicationSchema) {
	return func(application *catalog.ApplicationSchema) {
		application.AccessKeys.RateLimit = &catalog.AccessKeysRateLimitSchema{
			RequestsPerMinute: requests, AuthFailuresPerMinute: failures,
		}
	}
}

// fromPeer makes the request arrive from the TCP peer address.
func fromPeer(address string) func(*http.Request) {
	return func(request *http.Request) { request.RemoteAddr = address }
}

func withHeader(name, value string) func(*http.Request) {
	return func(request *http.Request) { request.Header.Set(name, value) }
}

// status calls the cheapest tool with the key from the peer and returns the raw answer.
func (f *mcpFixture) status(key string, mutate ...func(*http.Request)) *httptest.ResponseRecorder {
	f.t.Helper()
	return f.post(key, "tools/call", map[string]any{"name": "get_status", "arguments": map[string]any{}}, mutate...)
}

// download asks the listener for a product file with the key.
func (f *mcpFixture) download(key, path string, mutate ...func(*http.Request)) *httptest.ResponseRecorder {
	f.t.Helper()
	request := httptest.NewRequest(http.MethodGet, path, nil)
	if key != "" {
		request.Header.Set("Authorization", "Bearer "+key)
	}
	for _, change := range mutate {
		change(request)
	}
	recorder := httptest.NewRecorder()
	f.handler.ServeHTTP(recorder, request)
	return recorder
}

// requireLimited checks the one shape of both refusals: HTTP 429 with
// Retry-After, the JSON-RPC problem with the wait in its data and the request
// ID the access log records, and no credential challenge (the caller is not
// asked for a key: waiting is the remedy). It returns the detail text.
func requireLimited(t *testing.T, recorder *httptest.ResponseRecorder, rpcCode int, wantWait int) string {
	t.Helper()
	if recorder.Code != http.StatusTooManyRequests {
		// A download that was answered is bytes, not JSON: show the start only.
		answer := recorder.Body.String()
		if len(answer) > 200 {
			answer = answer[:200]
		}
		t.Fatalf("status = %d, want 429: %q", recorder.Code, answer)
	}
	if got := recorder.Header().Get("Retry-After"); got != strconv.Itoa(wantWait) {
		t.Fatalf("Retry-After = %q, want %d", got, wantWait)
	}
	if recorder.Header().Get("WWW-Authenticate") != "" || recorder.Header().Get("Content-Type") != "application/json" ||
		!strings.Contains(recorder.Header().Get("Cache-Control"), "no-store") || recorder.Header().Get("X-Request-ID") == "" {
		t.Fatalf("headers = %v", recorder.Header())
	}
	result := decodeToolResult(t, recorder)
	if result.rpcError == nil || result.rpcError["code"].(float64) != float64(rpcCode) {
		t.Fatalf("answer = %s, want JSON-RPC code %d", recorder.Body.String(), rpcCode)
	}
	data, _ := result.rpcError["data"].(map[string]any)
	if data["retry_after_seconds"] != float64(wantWait) || data["request_id"] != recorder.Header().Get("X-Request-ID") {
		t.Fatalf("data = %v, want retry_after_seconds %d and the request ID", data, wantWait)
	}
	detail, _ := data["detail"].(string)
	if !strings.Contains(detail, strconv.Itoa(wantWait)) || strings.Contains(detail, "{seconds}") {
		t.Fatalf("detail = %q, want the wait worded", detail)
	}
	return detail
}

// A key may make the Schema's number of requests a minute, in one window that
// both the JSON-RPC endpoint and the download route draw from. Past it the
// answer is 429 with the time to wait, which runs down with the clock; when the
// window ends the key works again, with a full window. Another key, of the
// same person or of another, is not affected: the count is per key.
func TestMCPKeyRequestsAreLimitedPerMinute(t *testing.T) {
	t.Parallel()
	const limit = 5
	f := newMCPFixture(t, nil, mcpLimits(limit, 1000))
	key, sameOwner, otherOwner := f.issueKey("owner-a", "read"), f.issueKey("owner-a", "read"), f.issueKey("owner-b", "read")
	for call := 1; call <= limit; call++ {
		if got := decodeToolResult(t, f.status(key)); got.code != http.StatusOK || got.isError {
			t.Fatalf("request %d, within the limit: %d %s", call, got.code, got.raw)
		}
	}
	detail := requireLimited(t, f.status(key), mcpCodeRateLimited, 60)
	if !strings.Contains(detail, "访问 Key") || strings.Contains(detail, key) {
		t.Fatalf("detail = %q", detail)
	}
	// Every kind of request of the listener is counted: a tools/list is refused too.
	requireLimited(t, f.post(key, "tools/list", nil), mcpCodeRateLimited, 60)
	for name, other := range map[string]string{"another key of the same person": sameOwner, "another person's key": otherOwner} {
		if got := decodeToolResult(t, f.status(other)); got.code != http.StatusOK || got.isError {
			t.Fatalf("%s: %d %s", name, got.code, got.raw)
		}
	}
	// The wait runs down with the window and is never rounded to zero.
	f.clock.Advance(30 * time.Second)
	requireLimited(t, f.status(key), mcpCodeRateLimited, 30)
	f.clock.Advance(29*time.Second + 500*time.Millisecond)
	requireLimited(t, f.status(key), mcpCodeRateLimited, 1)
	f.clock.Advance(500 * time.Millisecond)
	for call := 1; call <= limit; call++ {
		if got := decodeToolResult(t, f.status(key)); got.code != http.StatusOK || got.isError {
			t.Fatalf("request %d of the next window: %d %s", call, got.code, got.raw)
		}
	}
	requireLimited(t, f.status(key), mcpCodeRateLimited, 60)

	// The refusal is worded in the request's language, with the wait filled in.
	english := f.status(key, withHeader("Accept-Language", "en"))
	if got := requireLimited(t, english, mcpCodeRateLimited, 60); !strings.Contains(got, "60 seconds") || strings.Contains(got, "访问") {
		t.Fatalf("English detail = %q", got)
	}

	// The access log names the reason, the person and the key's public ID (the
	// part of a key that identifies it and opens nothing), and holds no key and
	// no Authorization header value.
	log := f.serverLog()
	for _, want := range []string{"status=429", "error_code=rate_limited", "owner_id=owner-a", "per-minute request limit", strings.Split(key, "_")[1]} {
		if !strings.Contains(log, want) {
			t.Fatalf("the access log lacks %q:\n%s", want, log)
		}
	}
	for _, secret := range []string{key, sameOwner, otherOwner, "Bearer plk_", strings.Split(key, "_")[2]} {
		if strings.Contains(log, secret) {
			t.Fatalf("the access log contains %q", secret)
		}
	}
}

// The download route draws from the same per-key window as the endpoint, and a
// refused download carries no file bytes.
func TestMCPDownloadRouteCountsAgainstTheKeyLimit(t *testing.T) {
	t.Parallel()
	const limit = 4
	f := newMCPFixture(t, nil, mcpLimits(limit, 1000))
	key := f.issueKey("owner-a", "read")
	task := f.succeededTask("owner-a", "", "limits-download")
	path := "/mcp/tasks/" + string(task.ID) + "/assets/" + string(task.Assets[0].ID)
	for call := 1; call <= limit/2; call++ {
		if got := decodeToolResult(t, f.status(key)); got.code != http.StatusOK || got.isError {
			t.Fatalf("request %d: %d %s", call, got.code, got.raw)
		}
		if got := f.download(key, path); got.Code != http.StatusOK || got.Body.Len() == 0 {
			t.Fatalf("download %d = %d", call, got.Code)
		}
	}
	refused := f.download(key, path)
	requireLimited(t, refused, mcpCodeRateLimited, 60)
	if strings.Contains(refused.Header().Get("Content-Disposition"), "filename") || refused.Header().Get("Content-Security-Policy") == "sandbox" {
		t.Fatalf("a refused download carries delivery headers: %v", refused.Header())
	}
	// The endpoint is out of requests too: one window, two routes.
	requireLimited(t, f.status(key), mcpCodeRateLimited, 60)
	f.clock.Advance(time.Minute)
	if got := f.download(key, path); got.Code != http.StatusOK || got.Body.Len() == 0 {
		t.Fatalf("download after the window = %d", got.Code)
	}
}

// Requests that arrive at once are counted exactly: a key at its limit of N
// gets N answers and the rest are refused, never N plus the ones that raced.
func TestMCPKeyLimitHoldsUnderConcurrentRequests(t *testing.T) {
	t.Parallel()
	const limit, callers = 10, 40
	f := newMCPFixture(t, nil, mcpLimits(limit, 1000))
	key := f.issueKey("owner-a", "read")
	codes := make([]int, callers)
	var group sync.WaitGroup
	for index := range callers {
		group.Add(1)
		go func() {
			defer group.Done()
			codes[index] = f.post(key, "tools/list", nil).Code
		}()
	}
	group.Wait()
	answered, refused := 0, 0
	for _, code := range codes {
		switch code {
		case http.StatusOK:
			answered++
		case http.StatusTooManyRequests:
			refused++
		}
	}
	if answered != limit || refused != callers-limit {
		t.Fatalf("answered %d and refused %d of %d requests at a limit of %d (codes %v)", answered, refused, callers, limit, codes)
	}
}

// Valid keys are never throttled by the failure limit, however many are in
// flight from one address at once: only failed authentications count. Failed
// attempts in flight at once may all be looked up (the documented overshoot),
// but once they have failed the address is throttled.
func TestMCPConcurrentAttemptsAndTheFailureLimit(t *testing.T) {
	t.Parallel()
	const limit, callers = 5, 40
	f := newMCPFixture(t, nil, mcpLimits(1000, limit))
	valid := f.issueKey("owner-a", "read")
	wrong := valid[:len(valid)-1] + map[bool]string{true: "b", false: "a"}[valid[len(valid)-1] == 'a']
	run := func(key string) []int {
		codes := make([]int, callers)
		var group sync.WaitGroup
		for index := range callers {
			group.Add(1)
			go func() {
				defer group.Done()
				codes[index] = f.post(key, "tools/list", nil).Code
			}()
		}
		group.Wait()
		return codes
	}
	for index, code := range run(valid) {
		if code != http.StatusOK {
			t.Fatalf("valid request %d of %d in flight = %d; valid keys must not be throttled", index+1, callers, code)
		}
	}
	unauthenticated := 0
	for _, code := range run(wrong) {
		switch code {
		case http.StatusUnauthorized:
			unauthenticated++
		case http.StatusTooManyRequests:
		default:
			t.Fatalf("a failed attempt = %d", code)
		}
	}
	if unauthenticated < limit {
		t.Fatalf("only %d of %d failed attempts were looked up at a limit of %d", unauthenticated, callers, limit)
	}
	if code := f.post(valid, "tools/list", nil).Code; code != http.StatusTooManyRequests {
		t.Fatalf("after the burst the address answered %d, want it throttled", code)
	}
}

// An address that has failed to authenticate the Schema's number of times in a
// minute is refused with 429 before any key is looked up: from then on even a
// valid key gets no answer from it, so guessing keys is capped per address. The
// failure count belongs to the address as the product resolves it (an IPv6
// /64 is one address; the listener trusts no proxy, so a forwarding header
// never names it),
// a success does not clear it, and the next window starts clean.
func TestMCPFailedAuthenticationsThrottleTheClientAddress(t *testing.T) {
	t.Parallel()
	const failures = 3
	f := newMCPFixture(t, nil, mcpLimits(1000, failures))
	valid := f.issueKey("owner-a", "read")
	wrong := valid[:len(valid)-1] + map[bool]string{true: "b", false: "a"}[valid[len(valid)-1] == 'a']
	const addressA, addressB, addressC, addressD = "198.51.100.7:4100", "198.51.100.8:4100", "198.51.100.9:4100", "198.51.100.10:4100"

	for attempt := 1; attempt <= failures; attempt++ {
		if got := f.status(wrong, fromPeer(addressA)); got.Code != http.StatusUnauthorized {
			t.Fatalf("failed attempt %d = %d, want 401", attempt, got.Code)
		}
	}
	detail := requireLimited(t, f.status(wrong, fromPeer(addressA)), mcpCodeAuthThrottled, 60)
	if !strings.Contains(detail, "鉴权失败") || strings.Contains(detail, wrong) {
		t.Fatalf("detail = %q", detail)
	}
	// The key is not looked up at all: the valid key gets the same refusal.
	requireLimited(t, f.status(valid, fromPeer(addressA)), mcpCodeAuthThrottled, 60)
	if got := f.download(valid, "/mcp/tasks/task_x/assets/asset_x", fromPeer(addressA)); got.Code != http.StatusTooManyRequests {
		t.Fatalf("download by a throttled address = %d, want 429", got.Code)
	}
	// Another address is not affected, neither for a valid key nor for its own failures.
	if got := decodeToolResult(t, f.status(valid, fromPeer(addressB))); got.code != http.StatusOK || got.isError {
		t.Fatalf("another address: %d %s", got.code, got.raw)
	}
	if got := f.status(wrong, fromPeer(addressB)); got.Code != http.StatusUnauthorized {
		t.Fatalf("another address's first failure = %d, want 401", got.Code)
	}
	// A forwarded address is client input unless the peer is a trusted proxy:
	// it neither frees a throttled peer nor throttles the address it names.
	requireLimited(t, f.status(valid, fromPeer(addressA), withHeader("X-Forwarded-For", "198.51.100.8"), withHeader("X-Real-IP", "198.51.100.8")),
		mcpCodeAuthThrottled, 60)
	if got := decodeToolResult(t, f.status(valid, fromPeer(addressB), withHeader("X-Forwarded-For", "198.51.100.7"))); got.code != http.StatusOK || got.isError {
		t.Fatalf("a request naming a throttled address in X-Forwarded-For: %d %s", got.code, got.raw)
	}
	// A path that never asks for a key is answered as before.
	if got := f.post(valid, "tools/list", nil, fromPeer(addressA), func(r *http.Request) { r.Method = http.MethodGet }); got.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET /mcp from a throttled address = %d, want 405", got.Code)
	}

	// An IPv6 subscriber holds a whole /64: its addresses are one address.
	for attempt := 1; attempt <= failures; attempt++ {
		f.status(wrong, fromPeer("[2001:db8:1:2::1]:4100"))
	}
	requireLimited(t, f.status(valid, fromPeer("[2001:db8:1:2:ffff::9]:4100")), mcpCodeAuthThrottled, 60)
	if got := decodeToolResult(t, f.status(valid, fromPeer("[2001:db8:1:3::1]:4100"))); got.code != http.StatusOK || got.isError {
		t.Fatalf("another /64: %d %s", got.code, got.raw)
	}

	// A success does not clear the count: guessing is not made free by holding
	// one valid key. The third failure still sits in the same window.
	f.status(wrong, fromPeer(addressC))
	f.status(wrong, fromPeer(addressC))
	if got := decodeToolResult(t, f.status(valid, fromPeer(addressC))); got.code != http.StatusOK || got.isError {
		t.Fatalf("a valid key between failures: %d %s", got.code, got.raw)
	}
	if got := f.status(wrong, fromPeer(addressC)); got.Code != http.StatusUnauthorized {
		t.Fatalf("third failure = %d, want 401", got.Code)
	}
	requireLimited(t, f.status(valid, fromPeer(addressC)), mcpCodeAuthThrottled, 60)

	// A request without any key is a failed authentication as well.
	for attempt := 1; attempt <= failures; attempt++ {
		if got := f.status("", fromPeer(addressD)); got.Code != http.StatusUnauthorized {
			t.Fatalf("request %d without a key = %d, want 401", attempt, got.Code)
		}
	}
	requireLimited(t, f.status("", fromPeer(addressD)), mcpCodeAuthThrottled, 60)

	// The wait runs down with the window; when it ends the address starts clean.
	f.clock.Advance(45 * time.Second)
	requireLimited(t, f.status(valid, fromPeer(addressA)), mcpCodeAuthThrottled, 15)
	f.clock.Advance(15 * time.Second)
	if got := decodeToolResult(t, f.status(valid, fromPeer(addressA))); got.code != http.StatusOK || got.isError {
		t.Fatalf("the address after the window: %d %s", got.code, got.raw)
	}
	for attempt := 1; attempt <= failures; attempt++ {
		if got := f.status(wrong, fromPeer(addressA)); got.Code != http.StatusUnauthorized {
			t.Fatalf("failed attempt %d of the next window = %d, want 401", attempt, got.Code)
		}
	}
	english := f.status(valid, fromPeer(addressA), withHeader("Accept-Language", "en"))
	if got := requireLimited(t, english, mcpCodeAuthThrottled, 60); !strings.Contains(got, "60 seconds") || strings.Contains(got, "鉴权") {
		t.Fatalf("English detail = %q", got)
	}

	// The access log records the throttling with its reason, and holds no key,
	// no Authorization header and no attempted secret.
	log := f.serverLog()
	for _, want := range []string{"status=429", "error_code=auth_throttled", "failed authentications"} {
		if !strings.Contains(log, want) {
			t.Fatalf("the access log lacks %q:\n%s", want, log)
		}
	}
	for _, secret := range []string{valid, wrong, "Bearer plk_", strings.Split(valid, "_")[2]} {
		if strings.Contains(log, secret) {
			t.Fatalf("the access log contains %q", secret)
		}
	}
}

// A Catalog that declares no limits limits nothing: the listener lets every
// request and every failed authentication through, as it did before the
// limits existed.
func TestMCPWithoutDeclaredLimitsLimitsNothing(t *testing.T) {
	t.Parallel()
	f := newMCPFixture(t, nil, func(application *catalog.ApplicationSchema) { application.AccessKeys.RateLimit = nil })
	valid := f.issueKey("owner-a", "read")
	for call := 0; call < 150; call++ {
		if got := f.post(valid, "tools/list", nil, fromPeer("198.51.100.7:4100")); got.Code != http.StatusOK {
			t.Fatalf("request %d = %d, want 200", call+1, got.Code)
		}
	}
	for attempt := 0; attempt < 40; attempt++ {
		if got := f.status("plk_"+strings.Repeat("a", 16)+"_"+strings.Repeat("b", 52), fromPeer("198.51.100.8:4100")); got.Code != http.StatusUnauthorized {
			t.Fatalf("failed attempt %d = %d, want 401", attempt+1, got.Code)
		}
	}
	if got := f.status(valid, fromPeer("198.51.100.8:4100")); got.Code != http.StatusOK {
		t.Fatalf("a valid key after many failures = %d, want 200", got.Code)
	}
}
