package localmodel_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	inputassets "github.com/LeXwDeX/Plinth/internal/assets/input"
	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/assets/modelinput/modelinputtest"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/modeldelivery"
	"github.com/LeXwDeX/Plinth/internal/processing"
	"github.com/LeXwDeX/Plinth/internal/providers"
	"github.com/LeXwDeX/Plinth/internal/providers/localmodel"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// This fixture models a historical provider's GLB contract. Its actual output
// still traverses the kernel's normal materialization and SQLite Task lifecycle.
// It deliberately does not claim the new static-model profile.
type historicalModelPort struct{ body []byte }

func (p historicalModelPort) Start(context.Context, generation.StartRequest) (generation.Observation, error) {
	return generation.Observation{Phase: generation.UpstreamCompleted, Progress: 100, Artifacts: []generation.ArtifactClaim{{ID: "historical-model", Role: "model", Variant: "glb", MediaType: "model/gltf-binary", Inline: p.body}}}, nil
}
func (historicalModelPort) Observe(context.Context, generation.ObserveRequest) (generation.Observation, error) {
	return generation.Observation{}, errors.New("synchronous source cannot be polled")
}
func (p historicalModelPort) OpenAsset(context.Context, generation.OpenAssetRequest) (io.ReadCloser, generation.AssetMetadata, error) {
	return io.NopCloser(bytes.NewReader(p.body)), generation.AssetMetadata{MediaType: "model/gltf-binary", Size: int64(len(p.body))}, nil
}

type deliveryPlanCatalog struct {
	fixtureCatalog
	module *generation.Module
}

func (c deliveryPlanCatalog) OfferingAvailable(id generation.OfferingID) bool {
	return c.module.OfferingAvailable(id)
}

func databaseObjectIDs(t *testing.T, path string) map[string][]string {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	result := map[string][]string{}
	for _, entry := range []struct{ table, column string }{{"generation_prepared_inputs", "id"}, {"generation_tasks", "id"}, {"execution_grants", "request_id"}, {"processing_plans", "id"}} {
		rows, err := db.Query("SELECT " + entry.column + " FROM " + entry.table + " ORDER BY " + entry.column)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				t.Fatal(err)
			}
			result[entry.table] = append(result[entry.table], id)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		_ = rows.Close()
	}
	return result
}

func TestLocalModelDeliveryCoverageChecksRealBytesWithoutWriting(t *testing.T) {
	for _, valid := range []bool{true, false} {
		name := "static"
		if !valid {
			name = "historical-animation"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			root := t.TempDir()
			path := filepath.Join(root, "db")
			repo, err := sqlite.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer repo.Close()
			blobs, err := local.New(filepath.Join(root, "cas"))
			if err != nil {
				t.Fatal(err)
			}
			defer blobs.Close()
			native, err := inputassets.New(repo, blobs)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := repo.CreateSession(ctx, chat.Session{ID: "local-session", OwnerID: "owner", Title: "coverage fixture", CreatedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
				t.Fatal(err)
			}
			document, binary := modelinputtest.Document(modelinputtest.Options{Indexed: true})
			if !valid {
				document["animations"] = []any{map[string]any{}}
			}
			raw := modelinputtest.GLB(document, binary)
			forward := offering(schema.ModelConversionGLBToGLTF)
			forward.Enabled = true
			one := 1
			sourceOffering := generation.Offering{ID: "legacy/model-source", Revision: "legacy-v1", Enabled: true, ProviderID: "legacy", AdapterKey: "legacy", Capability: generation.FormatConversion, Domain: generation.DomainProcessing, Outputs: []generation.AssetRequirement{{Role: "model", Variant: "glb", MediaTypes: []string{"model/gltf-binary"}, MaxBytes: 2 << 20, Minimum: &one}}}
			product := fixtureCatalog{offerings: map[generation.OfferingID]generation.Offering{forward.ID: forward, sourceOffering.ID: sourceOffering}}
			registry := providers.NewRegistry()
			if err := registry.Register("legacy", historicalModelPort{body: raw}); err != nil {
				t.Fatal(err)
			}
			port, err := localmodel.New(localmodel.Config{Inputs: native, Assets: blobs})
			if err != nil {
				t.Fatal(err)
			}
			if err := registry.Register(forward.AdapterKey, port); err != nil {
				t.Fatal(err)
			}
			module, err := generation.New(generation.Dependencies{Repository: repo, Catalog: product, Providers: registry, Assets: blobs, Inputs: native})
			if err != nil {
				t.Fatal(err)
			}
			accepted := submitApprovedLocal(t, module, repo, sourceOffering, json.RawMessage(`{}`), "materialized-legacy-source")
			task := runLocalTask(t, module, accepted.TaskID)
			if len(task.Assets) != 1 {
				t.Fatal("source did not materialize actual model")
			}
			plans, err := processing.New(repo, deliveryPlanCatalog{fixtureCatalog: product, module: module}, module, nil)
			if err != nil {
				t.Fatal(err)
			}
			defaultProduct, err := catalog.LoadDefault()
			if err != nil {
				t.Fatal(err)
			}
			declaration := defaultProduct.Application().ModelDelivery
			declaration.Conversions = []catalog.ModelConversionSchema{{OfferingID: string(forward.ID), Provides: []string{"gltf"}, SourceFormats: []string{"glb"}}}
			delivery, err := modeldelivery.New(declaration, module, plans, nil)
			if err != nil {
				t.Fatal(err)
			}
			// Providers without an optional validator retain their historical read
			// projection path, without normalization or owner-source resolution.
			if err := module.ValidateOfferingInput(ctx, "owner", sourceOffering.ID, sourceOffering.Revision, json.RawMessage(`not-json`)); err != nil {
				t.Fatalf("legacy projection behavior changed: %v", err)
			}
			before := databaseObjectIDs(t, path)
			if len(before["generation_tasks"]) != 1 || len(before["execution_grants"]) != 1 || len(before["generation_prepared_inputs"]) != 1 {
				t.Fatal("fixture did not establish a real existing source lifecycle")
			}
			for repeat := 0; repeat < 2; repeat++ {
				coverage, err := delivery.Coverage(ctx, generation.OwnTasks("owner"), task.ID)
				if err != nil {
					t.Fatal(err)
				}
				var row modeldelivery.Format
				for _, format := range coverage.Formats {
					if format.ID == "gltf" {
						row = format
					}
				}
				expected := modeldelivery.StatusUnavailable
				if valid {
					expected = modeldelivery.StatusObtainable
				}
				if row.Status != expected || coverage.CanComplete != valid {
					t.Fatalf("%s bytes coverage=%s can_complete=%t", name, row.Status, coverage.CanComplete)
				}
			}
			if !valid {
				if _, err := delivery.Complete(ctx, "owner", task.ID); !errors.Is(err, modeldelivery.ErrNothingToComplete) {
					t.Fatalf("unsupported source created a completion plan: %v", err)
				}
			}
			if after := databaseObjectIDs(t, path); !reflect.DeepEqual(before, after) {
				t.Fatalf("read-only coverage/unsupported completion changed persisted IDs: before=%v after=%v", before, after)
			}
		})
	}
}
