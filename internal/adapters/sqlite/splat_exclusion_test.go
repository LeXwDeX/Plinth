package sqlite

import (
	"encoding/json"
	"errors"
	"github.com/LeXwDeX/Plinth/internal/capture"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/scene"
	"github.com/LeXwDeX/Plinth/internal/schema"
	"testing"
)

func TestSplatCannotMasqueradeAsSceneOrCapture(t *testing.T) {
	for _, marker := range []string{"all", "profile", "role", "mime"} {
		t.Run(marker, func(t *testing.T) {
			repo, captures, cmd, _ := captureFixture(t)
			role, variant, mime, profile := "model", "glb", "model/gltf-binary", ""
			if marker == "all" || marker == "profile" {
				profile = schema.DeliveryProfileGaussianSplat32V1
			}
			if marker == "all" || marker == "role" {
				role = schema.GaussianSplatRole
			}
			if marker == "all" || marker == "mime" {
				mime = schema.GaussianSplatMediaType
			}
			if marker == "all" {
				variant = schema.GaussianSplatVariant
			}
			if _, err := repo.db.Exec(`UPDATE generation_assets SET role=?,variant=?,media_type=?,delivery_profile=? WHERE id=?`, role, variant, mime, profile, cmd.ModelAssetID); err != nil {
				t.Fatal(err)
			}
			sr, err := repo.SceneRepository(SQLiteSceneEvidence{})
			if err != nil {
				t.Fatal(err)
			}
			for _, kind := range []scene.Kind{scene.Bitmap, scene.Model3D, scene.Vector, scene.Capture} {
				_, err = sr.Create(t.Context(), scene.Create{Object: scene.Object{ID: "object", OwnerID: cmd.OwnerID, SessionID: cmd.SessionID, Rect: scene.Rect{Width: 10, Height: 10}}, Initial: scene.Representation{ID: "representation", ObjectID: "object", Kind: kind, AssetID: cmd.ModelAssetID, MetadataSchemaRevision: "v1"}})
				if !errors.Is(err, scene.ErrNotFound) {
					t.Fatalf("Scene kind %s admitted: %v", kind, err)
				}
			}
			if marker == "all" {
				raw, _ := json.Marshal(generation.Offering{Outputs: []generation.AssetRequirement{{Role: role, Variant: variant, MediaTypes: []string{mime}, DeliveryProfile: profile}}})
				if _, err = repo.db.Exec(`UPDATE generation_tasks SET offering_json=? WHERE id=?`, string(raw), cmd.SourceTaskID); err != nil {
					t.Fatal(err)
				}
				if _, err = captures.Record(t.Context(), cmd); !errors.Is(err, capture.ErrNotFound) {
					t.Fatalf("capture accepted Splat: %v", err)
				}
			}
		})
	}
}
