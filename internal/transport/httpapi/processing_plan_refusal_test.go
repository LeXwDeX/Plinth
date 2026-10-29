package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/processing"
)

// #121: a plan refused with a Schema reason (a Remesh budget over a later
// step's source limit) shows that reason with a 400; any other invalid plan
// keeps the generic message.
func TestProcessingPlanRefusalShowsTheSchemaReasonOnlyWhenItHasOne(t *testing.T) {
	t.Parallel()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{application: product.Application()}
	messages := product.Application().Runtime.GenerationMessages
	reason := messages.PlanSourceFaceLimitRefusal("Meshy Remesh", "Meshy Rigging", 600000, 320000, "quad ≤ 160000 / triangle ≤ 320000")
	if reason == nil {
		t.Fatal("the default Catalog has no plan refusal text")
	}
	reason.Cause = processing.ErrInvalid

	recorder := httptest.NewRecorder()
	server.writeProcessingError(recorder, reason)
	var body struct {
		Code       string `json:"code"`
		Detail     string `json:"detail"`
		UserFacing bool   `json:"user_facing"`
	}
	if recorder.Code != http.StatusBadRequest || json.Unmarshal(recorder.Body.Bytes(), &body) != nil || body.Code != "invalid_input" ||
		!body.UserFacing || !strings.Contains(body.Detail, "600000") || !strings.Contains(body.Detail, "quad ≤ 160000 / triangle ≤ 320000") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	generic := httptest.NewRecorder()
	server.writeProcessingError(generic, errors.Join(processing.ErrInvalid, &generation.Error{Code: generation.ErrInvalidInput, Detail: "internal parameter name"}))
	if generic.Code != http.StatusBadRequest || strings.Contains(generic.Body.String(), "internal parameter name") || strings.Contains(generic.Body.String(), "600000") {
		t.Fatalf("generic invalid plan: status=%d body=%s", generic.Code, generic.Body.String())
	}
}
