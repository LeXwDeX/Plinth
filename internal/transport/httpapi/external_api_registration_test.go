package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

type externalAPIMatrixRoute struct {
	Method string `json:"method"`
	Path   string `json:"path"`
	Status string `json:"status"`
	GoPath string `json:"goPath"`
}

var externalAPIPathParameter = regexp.MustCompile(`\{[^}]+\}`)

func TestEveryGoCompatibleExternalAPIIsRegistered(t *testing.T) {
	t.Parallel()
	productCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{
		application: productCatalog.Application(),
		mux:         http.NewServeMux(),
	}
	server.routes()

	routes := readExternalAPIMatrix(t)
	compatibleCount := 0
	for _, route := range routes {
		if route.Status != "go-compatible" {
			continue
		}
		compatibleCount++
		t.Run(route.Method+" "+route.Path, func(t *testing.T) {
			requestPath := externalAPIPathParameter.ReplaceAllString(
				route.GoPath,
				"matrix-contract-id",
			)
			request := httptest.NewRequest(route.Method, requestPath, nil)
			_, matchedPattern := server.mux.Handler(request)
			if matchedPattern == "" {
				t.Fatalf(
					"%s %s is classified as compatible but is not registered",
					route.Method,
					route.GoPath,
				)
			}
			if !strings.HasPrefix(matchedPattern, route.Method+" ") {
				t.Fatalf(
					"%s %s matched the wrong method pattern %q",
					route.Method,
					route.GoPath,
					matchedPattern,
				)
			}
			if !isConcreteExternalAPIMatch(route, matchedPattern) {
				t.Fatalf(
					"%s %s was masked by unrelated route pattern %q",
					route.Method,
					route.GoPath,
					matchedPattern,
				)
			}
		})
	}
	if compatibleCount != len(routes) {
		t.Fatalf(
			"compatible route count = %d, want all %d external routes",
			compatibleCount,
			len(routes),
		)
	}
}

func TestExternalAPIRegistrationRejectsCatchAllFalsePositives(t *testing.T) {
	t.Parallel()
	route := externalAPIMatrixRoute{
		Method: "GET",
		Path:   "/api/example",
		Status: "go-compatible",
		GoPath: "/api/example",
	}
	if isConcreteExternalAPIMatch(route, "GET /") {
		t.Fatal("method catch-all must not prove a concrete API is registered")
	}
}

func isConcreteExternalAPIMatch(
	route externalAPIMatrixRoute,
	matchedPattern string,
) bool {
	if route.Path == "/[webhookSecret]/wbh" {
		return matchedPattern == route.Method+" /"
	}
	expectedPattern := route.Method + " " + route.GoPath
	return externalAPIPathParameter.ReplaceAllString(
		matchedPattern,
		"{}",
	) == externalAPIPathParameter.ReplaceAllString(expectedPattern, "{}")
}

func readExternalAPIMatrix(t *testing.T) []externalAPIMatrixRoute {
	t.Helper()
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve test source path")
	}
	matrixPath := filepath.Join(
		filepath.Dir(sourceFile),
		"..",
		"..",
		"..",
		"docs",
		"api-migration-matrix.json",
	)
	payload, err := os.ReadFile(matrixPath)
	if err != nil {
		t.Fatal(err)
	}
	var routes []externalAPIMatrixRoute
	if err := json.Unmarshal(payload, &routes); err != nil {
		t.Fatal(err)
	}
	return routes
}
