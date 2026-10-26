package httpapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
	providerMock "github.com/LeXwDeX/Plinth/internal/providers/mock"
)

// #329 follow-up A and C over the task API, the direct (non-Agent) path.
func TestDirectPreparationTellsWhichPicturesAndRefusesRetiredModels(t *testing.T) {
	t.Parallel()
	_, api, _ := newHTTPHarness(t)
	server := httptest.NewServer(api)
	defer server.Close()
	created := boundaryRequest(t, api, http.MethodPost, "/api/chat/sessions", "owner-a", "application/json", `{"title":"direct"}`)
	var session struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &session); err != nil || session.ID == "" {
		t.Fatalf("session = %d %s", created.Code, created.Body)
	}
	first := uploadInputAsset(t, server.URL, "owner-a")
	second := uploadInputAsset(t, server.URL, "owner-a")
	prepare := func(capability, offering, input string) (int, map[string]any) {
		t.Helper()
		response := doRequest(t, server.Client(), http.MethodPost, server.URL+"/api/v1/tasks/prepare", "owner-a", "application/json",
			strings.NewReader(`{"session_id":"`+session.ID+`","capability":"`+capability+`","offering_id":"`+offering+`","input":`+input+`}`))
		var body map[string]any
		_ = json.Unmarshal(readBody(t, response), &body)
		return response.StatusCode, body
	}
	// prepareIn is prepare in a UI language (Accept-Language).
	prepareIn := func(locale, capability, offering, input string) (int, map[string]any) {
		t.Helper()
		request, err := http.NewRequest(http.MethodPost, server.URL+"/api/v1/tasks/prepare",
			strings.NewReader(`{"session_id":"`+session.ID+`","capability":"`+capability+`","offering_id":"`+offering+`","input":`+input+`}`))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("X-User-ID", "owner-a")
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Accept-Language", locale)
		response, err := server.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		var body map[string]any
		_ = json.Unmarshal(readBody(t, response), &body)
		return response.StatusCode, body
	}

	// A: two pictures for Meshy 7.1 Image to 3D, which takes one: the first is
	// kept and the answer says so before any grant or charge.
	code, body := prepare("image-to-3d", "meshy/meshy-7.1/image-to-3d", `{"reference_assets":["`+first+`","`+second+`"]}`)
	note, _ := body["reference_note"].(string)
	if code != http.StatusCreated || !strings.Contains(note, "Meshy 7.1") || !strings.Contains(note, "2") {
		t.Fatalf("prepare two pictures = %d %v", code, body)
	}
	// #329 note 99647 (observations 8, 9): the one-picture wording of the one
	// Schema note, in the reader's language and worded for one picture.
	if !strings.Contains(note, "第 1 张") || strings.Contains(note, "前 1 张") {
		t.Fatalf("Chinese note = %q", note)
	}
	_, english := prepareIn("en", "image-to-3d", "meshy/meshy-7.1/image-to-3d", `{"reference_assets":["`+first+`","`+second+`"]}`)
	if note, _ := english["reference_note"].(string); !strings.Contains(note, "only the first is used") || strings.Contains(note, "(s)") || strings.Contains(note, "first 1 ") {
		t.Fatalf("English prepare = %v", english)
	}
	code, body = prepare("image-to-3d", "meshy/meshy-7.1/image-to-3d", `{"reference_assets":["`+first+`"]}`)
	if _, noted := body["reference_note"]; code != http.StatusCreated || noted {
		t.Fatalf("prepare one picture = %d %v", code, body)
	}

	// C: a retired model's Offering is refused as invalid input with a reason
	// naming the successor; nothing is prepared.
	code, body = prepare("text-to-3d", "meshy/meshy-7/text-to-3d", `{"prompt":"castle"}`)
	detail, _ := body["detail"].(string)
	if code != http.StatusBadRequest || body["code"] != "invalid_input" || body["user_facing"] != true || !strings.Contains(detail, "Meshy 7.1") {
		t.Fatalf("retired offering = %d %v", code, body)
	}
	// The reason reads in the requester's language, not the Schema's Chinese
	// with the names filled in (#329 note 99647, found by the English browser run).
	code, body = prepareIn("en", "text-to-3d", "meshy/meshy-7/text-to-3d", `{"prompt":"castle"}`)
	detail, _ = body["detail"].(string)
	if code != http.StatusBadRequest || body["user_facing"] != true || !strings.Contains(detail, "Meshy 7.1") ||
		!strings.Contains(detail, "is no longer offered") || strings.ContainsAny(detail, "已停用积分") {
		t.Fatalf("English retired offering = %d %v", code, body)
	}
}

// #329 follow-up C: a session whose last 3D turn used the retired Meshy 7
// reads as Meshy 7.1, and names the retired model so the workspace says it
// switched. The frozen turn itself is not rewritten.
func TestChatSessionReadReportsRetiredModelsAsTheirSuccessor(t *testing.T) {
	t.Parallel()
	module, handler, cookie, sessionID := grantTestBrowserWithApplication(t, nil, []string{"user"})
	value, ok := fixtureExecutions.Load(module)
	if !ok {
		t.Fatal("missing fixture repository")
	}
	repository := value.(*fixtureExecutionState).repository
	recordTurn(t, repository, "owner-a", sessionID, "turn-old", time.Now().UTC().Add(-time.Hour), chat.TurnOptions{
		CreativeDomain: "model-3d", AIModel: "meshy-7",
	})
	response := grantTestRequest(t, handler, cookie, http.MethodGet, "/api/chat/sessions/"+sessionID, "", "")
	var body map[string]any
	_ = json.Unmarshal(response.Body.Bytes(), &body)
	models, _ := body["generationModels"].(map[string]any)
	retired, _ := body["generationModelsRetired"].(map[string]any)
	if response.Code != http.StatusOK || models["model-3d"] != "meshy-7.1" || retired["model-3d"] != "meshy-7" {
		t.Fatalf("session read = %d %v", response.Code, body)
	}
}

// #329 notes 99868 and 99888 over the direct task API: the answer to a
// refused preparation says which public input was too large, how many were
// given and how many fit, or that extra credits need explicit consent. The
// input is refused, not cut to fit, and nothing is frozen, approved, submitted
// or sent upstream.
func TestPreparationRefusalsNameWhatToChange(t *testing.T) {
	t.Parallel()
	_, api, _, _ := newHTTPHarnessWithCatalogAndProviders(t, nil, nil, nil, nil, nil, nil,
		map[string]generation.ProviderPort{"hyper3d.rodin.v1": providerMock.New()})
	server := httptest.NewServer(api)
	defer server.Close()
	created := boundaryRequest(t, api, http.MethodPost, "/api/chat/sessions", "owner-a", "application/json", `{"title":"direct"}`)
	var session struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &session); err != nil || session.ID == "" {
		t.Fatalf("session = %d %s", created.Code, created.Body)
	}
	pictures := make([]string, 0, 5)
	for range 5 {
		pictures = append(pictures, uploadInputAsset(t, server.URL, "owner-a"))
	}
	// prepareIn is prepare in a UI language (Accept-Language).
	prepareIn := func(locale, capability, offering, input string) (int, map[string]any) {
		t.Helper()
		request, err := http.NewRequest(http.MethodPost, server.URL+"/api/v1/tasks/prepare",
			strings.NewReader(`{"session_id":"`+session.ID+`","capability":"`+capability+`","offering_id":"`+offering+`","input":`+input+`}`))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("X-User-ID", "owner-a")
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Accept-Language", locale)
		response, err := server.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		var body map[string]any
		_ = json.Unmarshal(readBody(t, response), &body)
		return response.StatusCode, body
	}
	refuse := func(locale, capability, offering, input string) string {
		t.Helper()
		code, body := prepareIn(locale, capability, offering, input)
		detail, _ := body["detail"].(string)
		if code != http.StatusBadRequest || body["code"] != "invalid_input" || body["user_facing"] != true || detail == "" {
			t.Fatalf("%s refusal = %d %v", locale, code, body)
		}
		for _, private := range append([]string{"reference_assets", "authorize_extra_credits", "/api/v1/assets/"}, pictures...) {
			if strings.Contains(detail, private) {
				t.Fatalf("%s refusal leaked %q: %v", locale, private, body)
			}
		}
		return detail
	}

	// Meshy 7.1 Multiview preserves the selected views, so five pictures are
	// refused rather than silently reduced to four.
	five := `{"reference_assets":["` + strings.Join(pictures, `","`) + `"]}`
	chinese := refuse("zh-CN", "image-to-3d", "meshy/meshy-7.1-multiview/image-to-3d", five)
	for _, fact := range []string{"参考图", "5", "4"} {
		if !strings.Contains(chinese, fact) {
			t.Fatalf("Chinese reason %q must state %q", chinese, fact)
		}
	}
	english := refuse("en", "image-to-3d", "meshy/meshy-7.1-multiview/image-to-3d", five)
	for _, fact := range []string{"Reference images", "at most 4 pictures", "5 were provided"} {
		if !strings.Contains(english, fact) {
			t.Fatalf("English reason %q must state %q", english, fact)
		}
	}
	if strings.Contains(english, "参考图") || strings.Contains(english, "最多") {
		t.Fatalf("English reason still has Chinese copy: %q", english)
	}

	// Rodin Gen-2.5 always needs explicit extra-credit consent; the answer
	// names that next step without the internal parameter.
	rodin := `{"reference_assets":["` + pictures[0] + `"]}`
	chinese = refuse("zh-CN", "image-to-3d", "hyper3d/rodin-gen-2.5/image-to-3d", rodin)
	for _, fact := range []string{"额外积分", "同意"} {
		if !strings.Contains(chinese, fact) {
			t.Fatalf("Chinese reason %q must state %q", chinese, fact)
		}
	}
	english = refuse("en", "image-to-3d", "hyper3d/rodin-gen-2.5/image-to-3d", rodin)
	if !strings.Contains(strings.ToLower(english), "extra credits") || !strings.Contains(strings.ToLower(english), "approve") {
		t.Fatalf("English reason = %q", english)
	}

	// Refusal left no Task and no pending approval.
	tasks := doRequest(t, server.Client(), http.MethodGet, server.URL+"/api/v1/tasks", "owner-a", "", nil)
	var taskList struct {
		Tasks []json.RawMessage `json:"tasks"`
	}
	if err := json.Unmarshal(readBody(t, tasks), &taskList); err != nil || tasks.StatusCode != http.StatusOK || len(taskList.Tasks) != 0 {
		t.Fatalf("refusals left tasks: %d %+v", tasks.StatusCode, taskList.Tasks)
	}
	approvals := doRequest(t, server.Client(), http.MethodGet,
		server.URL+"/api/v1/chat/sessions/"+session.ID+"/pending-approvals", "owner-a", "", nil)
	var pending struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(readBody(t, approvals), &pending); err != nil || approvals.StatusCode != http.StatusOK || len(pending.Items) != 0 {
		t.Fatalf("refusals left approvals: %d %+v", approvals.StatusCode, pending.Items)
	}

	// The declared boundaries prepare as before: four preserved views, and
	// Rodin once the person gives the consent it asks for.
	four := `{"reference_assets":["` + strings.Join(pictures[:4], `","`) + `"],"authorize_extra_credits":true}`
	code, body := prepareIn("zh-CN", "image-to-3d", "meshy/meshy-7.1-multiview/image-to-3d", four)
	if code != http.StatusCreated || body["prepared_input_id"] == nil {
		t.Fatalf("four pictures = %d %v", code, body)
	}
	if _, noted := body["reference_note"]; noted {
		t.Fatalf("preserved four pictures reported a cut: %v", body)
	}
	code, body = prepareIn("zh-CN", "image-to-3d", "hyper3d/rodin-gen-2.5/image-to-3d",
		`{"reference_assets":["`+pictures[0]+`"],"authorize_extra_credits":true}`)
	if code != http.StatusCreated || body["prepared_input_id"] == nil {
		t.Fatalf("Rodin with consent = %d %v", code, body)
	}
}
