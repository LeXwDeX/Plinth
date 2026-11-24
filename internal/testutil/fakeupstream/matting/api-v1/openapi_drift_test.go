package mattingapiv1

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"
	"testing"
)

// I22: the matting service is the operator's own and publishes no documentation;
// its FastAPI OpenAPI page is the reference. The reference is held against the
// fake by comparing a committed snapshot of /openapi.json:
//
//	testdata/openapi.snapshot.json
//
// The snapshot is captured once, by the live round (a single read-only GET of
// /openapi.json, no key needed for the schema document itself, nothing to pay),
// and committed after privacy scrubbing: no `servers` host, no examples that
// carry real values. Until it exists the drift test skips and says so, so an
// absent snapshot is visible in the test output and in the Issue, never a
// silent pass. When it exists it must agree with the fake on
//
//   - the set of operations under /api/v1 (every route the fake serves is in the
//     snapshot, and the snapshot has no operation the fake does not know, apart
//     from the service's own health and documentation routes);
//   - the PipelineParams schema: the same field names, the same numeric bounds
//     and integer-ness, boolean fields boolean;
//   - the BatchStatus and TaskStatus enums: every status word the fake emits is a
//     member (the fake may emit fewer than the service has).
//
// The schema names below are the ones the endpoint notes quote from the service's
// FastAPI page (SubmitBatchResponse, TaskStatusItem, BatchStatus); the first real
// snapshot may need them adjusted, and that adjustment is the point of capturing
// it.
const openAPISnapshotFile = "testdata/openapi.snapshot.json"

const (
	pipelineSchemaName    = "PipelineParams"
	batchStatusSchemaName = "BatchStatus"
	taskStatusSchemaName  = "TaskStatus"
)

// serviceOwnRoutes are served by the service but are not part of the job API the
// Adapter calls.
var serviceOwnRoutes = []string{"/health", "/openapi.json", "/docs", "/redoc", "/docs/oauth2-redirect"}

// openAPIProblems compares an OpenAPI document with the fake and returns what
// disagrees, sorted.
func openAPIProblems(raw []byte) []string {
	var document struct {
		Servers []struct {
			URL string `json:"url"`
		} `json:"servers"`
		Paths      map[string]map[string]json.RawMessage `json:"paths"`
		Components struct {
			Schemas map[string]map[string]any `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		return []string{"the snapshot is not an OpenAPI JSON document: " + err.Error()}
	}
	var problems []string
	for _, server := range document.Servers {
		if strings.Contains(server.URL, "://") {
			problems = append(problems, "the snapshot keeps a server address; scrub it before committing: "+server.URL)
		}
	}
	// Operations.
	operations := map[string]bool{}
	for path, methods := range document.Paths {
		for method := range methods {
			switch method {
			case "get", "post", "put", "patch", "delete":
				operations[strings.ToUpper(method)+" "+path] = true
			}
		}
	}
	served := map[string]bool{}
	for _, endpoint := range endpoints {
		key := endpoint.Method + " " + samplePrefix + endpoint.Path
		served[key] = true
		if !operations[key] {
			problems = append(problems, "the fake serves "+key+", the snapshot has no such operation")
		}
	}
	for operation := range operations {
		_, path, _ := strings.Cut(operation, " ")
		if served[operation] || slices.Contains(serviceOwnRoutes, path) || !strings.HasPrefix(path, samplePrefix+"/") {
			continue
		}
		problems = append(problems, "the snapshot has "+operation+", which the fake does not serve")
	}
	// PipelineParams.
	pipelineSchema, ok := document.Components.Schemas[pipelineSchemaName]
	if !ok {
		problems = append(problems, "the snapshot has no "+pipelineSchemaName+" schema")
	} else {
		properties, _ := pipelineSchema["properties"].(map[string]any)
		fields := map[string]bool{}
		for _, limit := range PipelineLimits() {
			fields[limit.Field] = true
			property, ok := properties[limit.Field].(map[string]any)
			if !ok {
				problems = append(problems, pipelineSchemaName+" has no field "+limit.Field)
				continue
			}
			problems = append(problems, propertyProblems(limit, property)...)
		}
		for name := range properties {
			if !fields[name] {
				problems = append(problems, pipelineSchemaName+" has field "+name+", which the fake does not know")
			}
		}
	}
	// Enums.
	for _, check := range []struct {
		schema string
		words  []string
	}{
		{batchStatusSchemaName, append(RealBatchStatuses(), ConstructedBatchStatuses()...)},
		{taskStatusSchemaName, append(RealTaskStatuses(), ConstructedTaskStatuses()...)},
	} {
		entry, ok := document.Components.Schemas[check.schema]
		if !ok {
			problems = append(problems, "the snapshot has no "+check.schema+" schema")
			continue
		}
		members, _ := entry["enum"].([]any)
		for _, word := range check.words {
			if !slices.Contains(members, any(word)) {
				problems = append(problems, fmt.Sprintf("the fake emits %s %q, which is not in the snapshot's enum %v", check.schema, word, members))
			}
		}
	}
	sort.Strings(problems)
	return problems
}

// propertyProblems compares one PipelineParams property with the fake's limit.
// pydantic writes an optional field as anyOf [value schema, null].
func propertyProblems(limit PipelineLimit, property map[string]any) []string {
	value := property
	if options, ok := property["anyOf"].([]any); ok {
		for _, option := range options {
			if candidate, ok := option.(map[string]any); ok && candidate["type"] != "null" {
				value = candidate
			}
		}
	}
	name := pipelineSchemaName + "." + limit.Field
	kind, _ := value["type"].(string)
	if limit.Kind == KindBoolean {
		if kind != "boolean" {
			return []string{name + " is " + kind + " in the snapshot, the fake treats it as a boolean"}
		}
		return nil
	}
	var problems []string
	switch {
	case limit.Integer && kind != "integer":
		problems = append(problems, name+" is "+kind+" in the snapshot, the fake treats it as an integer")
	case !limit.Integer && kind != "number" && kind != "integer":
		problems = append(problems, name+" is "+kind+" in the snapshot, the fake treats it as a number")
	}
	for bound, want := range map[string]float64{"minimum": limit.Min, "maximum": limit.Max} {
		got, ok := value[bound].(float64)
		if !ok || got != want {
			problems = append(problems, fmt.Sprintf("%s %s is %v in the snapshot, %v in the fake", name, bound, value[bound], want))
		}
	}
	return problems
}

func TestSnapshotComparisonCatchesEveryKindOfDrift(t *testing.T) {
	good := syntheticSnapshot(t)
	if problems := openAPIProblems(good); len(problems) != 0 {
		t.Fatalf("a snapshot built from the fake's own tables disagrees with it: %v", problems)
	}
	mutate := func(edit func(document map[string]any)) []string {
		var document map[string]any
		if err := json.Unmarshal(good, &document); err != nil {
			t.Fatal(err)
		}
		edit(document)
		raw, _ := json.Marshal(document)
		return openAPIProblems(raw)
	}
	schemas := func(document map[string]any) map[string]any {
		return document["components"].(map[string]any)["schemas"].(map[string]any)
	}
	properties := func(document map[string]any) map[string]any {
		return schemas(document)[pipelineSchemaName].(map[string]any)["properties"].(map[string]any)
	}
	for name, test := range map[string]struct {
		edit func(document map[string]any)
		want string
	}{
		"an operation is missing": {func(d map[string]any) { delete(d["paths"].(map[string]any), samplePrefix+"/jobs") }, "the snapshot has no such operation"},
		"an unknown operation": {func(d map[string]any) {
			d["paths"].(map[string]any)[samplePrefix+"/batches"] = map[string]any{"get": map[string]any{}}
		}, "which the fake does not serve"},
		"the health route is the service's own": {func(d map[string]any) {
			d["paths"].(map[string]any)["/health"] = map[string]any{"get": map[string]any{}}
		}, ""},
		"a bound moved": {func(d map[string]any) {
			properties(d)["expand"].(map[string]any)["maximum"] = 2000
		}, "expand maximum is 2000"},
		"an optional field is read through anyOf": {func(d map[string]any) {
			properties(d)["birefnet_process_res"] = map[string]any{"anyOf": []any{map[string]any{"type": "integer", "minimum": 512, "maximum": 8192}, map[string]any{"type": "null"}}}
		}, "birefnet_process_res maximum is 8192"},
		"a field turns into another type": {func(d map[string]any) {
			properties(d)["mask_refine"] = map[string]any{"type": "string"}
		}, "mask_refine is string"},
		"a field the fake does not know": {func(d map[string]any) {
			properties(d)["new_knob"] = map[string]any{"type": "integer"}
		}, "has field new_knob"},
		"a field is gone": {func(d map[string]any) { delete(properties(d), "sensitivity") }, "has no field sensitivity"},
		"an enum member is gone": {func(d map[string]any) {
			schemas(d)[batchStatusSchemaName].(map[string]any)["enum"] = []any{"READY", "RUNNING", "SUCCESS", "FAILED", "CANCELED"}
		}, `"PARTIAL_SUCCESS"`},
		"an enum is missing": {func(d map[string]any) { delete(schemas(d), taskStatusSchemaName) }, "has no TaskStatus schema"},
		"a server address is kept": {func(d map[string]any) {
			d["servers"] = []any{map[string]any{"url": "http://10.0.0.1:8001"}}
		}, "scrub it before committing"},
	} {
		problems := mutate(test.edit)
		if test.want == "" {
			if len(problems) != 0 {
				t.Errorf("%s: %v", name, problems)
			}
			continue
		}
		if !strings.Contains(strings.Join(problems, "\n"), test.want) {
			t.Errorf("%s: %v, want one naming %q", name, problems, test.want)
		}
	}
	if problems := openAPIProblems([]byte("not json")); len(problems) != 1 {
		t.Errorf("a non-document: %v", problems)
	}
}

// syntheticSnapshot is an OpenAPI document written from the fake's own tables,
// in the shape FastAPI writes it. It exists only to test the comparison; it is
// never committed and proves nothing about the real service.
func syntheticSnapshot(t *testing.T) []byte {
	t.Helper()
	paths := map[string]any{}
	for _, endpoint := range endpoints {
		path := samplePrefix + endpoint.Path
		entry, _ := paths[path].(map[string]any)
		if entry == nil {
			entry = map[string]any{}
			paths[path] = entry
		}
		entry[strings.ToLower(endpoint.Method)] = map[string]any{}
	}
	properties := map[string]any{}
	for _, limit := range PipelineLimits() {
		switch {
		case limit.Kind == KindBoolean:
			properties[limit.Field] = map[string]any{"type": "boolean"}
		case limit.NullOK:
			properties[limit.Field] = map[string]any{"anyOf": []any{
				map[string]any{"type": "integer", "minimum": limit.Min, "maximum": limit.Max}, map[string]any{"type": "null"}}}
		default:
			kind := "number"
			if limit.Integer {
				kind = "integer"
			}
			properties[limit.Field] = map[string]any{"type": kind, "minimum": limit.Min, "maximum": limit.Max}
		}
	}
	document := map[string]any{
		"openapi": "3.1.0",
		"paths":   paths,
		"components": map[string]any{"schemas": map[string]any{
			pipelineSchemaName:    map[string]any{"type": "object", "properties": properties},
			batchStatusSchemaName: map[string]any{"enum": toAny(append(RealBatchStatuses(), ConstructedBatchStatuses()...))},
			taskStatusSchemaName:  map[string]any{"enum": toAny(append(RealTaskStatuses(), ConstructedTaskStatuses()...))},
		}},
	}
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func toAny(words []string) []any {
	list := make([]any, len(words))
	for i, word := range words {
		list[i] = word
	}
	return list
}

// The drift test proper. With no committed snapshot it skips, loudly: the
// snapshot is the live round's to capture (a single GET /openapi.json).
func TestFakeAgreesWithTheCommittedOpenAPISnapshot(t *testing.T) {
	raw, err := os.ReadFile(openAPISnapshotFile)
	if errors.Is(err, os.ErrNotExist) {
		t.Skipf("PENDING CAPTURE: %s is not committed yet; the live round captures it with one read-only GET /openapi.json (#538)", openAPISnapshotFile)
	}
	if err != nil {
		t.Fatal(err)
	}
	if problems := openAPIProblems(raw); len(problems) != 0 {
		t.Fatalf("the fake and the committed OpenAPI snapshot disagree:\n  %s", strings.Join(problems, "\n  "))
	}
}
