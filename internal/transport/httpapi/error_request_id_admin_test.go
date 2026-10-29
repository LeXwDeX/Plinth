package httpapi_test

import (
	"net/http"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #118: every administration route answers a forbidden or unauthenticated
// caller with an error body that names the request ID the access log records.
// Routes are read from the Schema so a new administration route is covered
// without editing this test.
func TestEveryAdministrationRouteErrorCarriesTheRequestID(t *testing.T) {
	t.Parallel()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	routes := reflect.ValueOf(product.Application().Runtime.HTTP.LegacyRoutes)
	parameter := regexp.MustCompile(`\{[^}]+\}`)
	var paths []string
	for index := 0; index < routes.NumField(); index++ {
		value, ok := routes.Field(index).Interface().(string)
		if ok && strings.HasPrefix(value, "/api/admin") {
			paths = append(paths, parameter.ReplaceAllString(value, "sample"))
		}
	}
	if len(paths) < 8 {
		t.Fatalf("expected the administration routes from the Schema, got %v", paths)
	}
	_, regular, _ := newHTTPHarnessWithRoles(t, []string{"user"})
	checked := 0
	for _, path := range paths {
		for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPatch, http.MethodDelete} {
			response := administrationRequest(regular, method, path, "", nil)
			if response.Code == http.StatusMethodNotAllowed || response.Code == http.StatusNotFound {
				continue
			}
			checked++
			requestID := response.Header().Get("X-Request-ID")
			if response.Code != http.StatusForbidden {
				t.Errorf("%s %s: status %d, want 403: %s", method, path, response.Code, response.Body)
				continue
			}
			body := decodeAdministrationBody(t, response)
			if requestID == "" || body["request_id"] != requestID {
				t.Errorf("%s %s: request_id %v does not match header %q: %s", method, path, body["request_id"], requestID, response.Body)
			}
		}
	}
	if checked < len(paths) {
		t.Fatalf("only %d method/route pairs reached a handler for %d routes", checked, len(paths))
	}
}
