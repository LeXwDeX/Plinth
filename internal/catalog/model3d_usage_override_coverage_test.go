package catalog

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDefaultCatalogCoversEveryRecommendedOfferingWithAnOverrideOrNotNeeded(t *testing.T) {
	product, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	usage := product.Application().Chat.Model3DUsage
	if !usage.OverrideCoverageRequired {
		t.Fatal("shipped catalog does not require override coverage")
	}
	for _, profile := range usage.Profiles {
		recommended := map[string]bool{}
		for _, ids := range profile.RecommendedOfferings {
			for _, id := range ids {
				recommended[id] = true
			}
		}
		notNeeded := map[string]bool{}
		for _, id := range profile.OverridesNotNeeded {
			notNeeded[id] = true
		}
		for id := range recommended {
			if len(profile.ParameterOverrides[id]) == 0 && !notNeeded[id] {
				t.Fatalf("profile %q recommends %q without an override or overrides_not_needed", profile.ID, id)
			}
		}
	}
	// #249: the multiview low-poly model now carries a purpose override, so a
	// game asset can pick it and still get a controlled face count.
	for _, id := range []string{"game_character", "game_prop", "web_ar"} {
		profile := purposeProfile(t, usage, id)
		override := profile.ParameterOverrides["tripo/tripo-v3.1-multiview/image-to-3d"]
		// Triangles at the 20000 ceiling smart low poly allows. #275 halved it to
		// 10000 for a character while quad output was on; #294 took the quad
		// override back (P2 quad output delivers an FBX only), so every purpose
		// asks for the same triangle count again.
		const wantFaces = 20000
		var faceLimit int
		if err := json.Unmarshal(override["face_limit"], &faceLimit); err != nil || faceLimit != wantFaces {
			t.Fatalf("profile %q multiview face_limit = %s (%v), want %d", id, override["face_limit"], err, wantFaces)
		}
		var lowPoly bool
		if err := json.Unmarshal(override["smart_low_poly"], &lowPoly); err != nil || !lowPoly {
			t.Fatalf("profile %q multiview smart_low_poly = %s", id, override["smart_low_poly"])
		}
	}
}

func TestOverrideCoverageRejectsMissingOverlapAndUnknownEntries(t *testing.T) {
	for name, tc := range map[string]struct {
		change func(profile map[string]any)
		want   string
	}{
		"missing coverage": {
			change: func(profile map[string]any) { delete(profile, "overrides_not_needed") },
			want:   "缺少 parameter_overrides",
		},
		"non-recommended entry": {
			change: func(profile map[string]any) {
				profile["overrides_not_needed"] = append(profile["overrides_not_needed"].([]any), "tripo/tripo-p2/text-to-3d")
			},
			want: "不在推荐列表中",
		},
		"both override and not needed": {
			change: func(profile map[string]any) {
				profile["overrides_not_needed"] = append(profile["overrides_not_needed"].([]any), "tripo/tripo-v3.1-full/text-to-3d")
				overrides, _ := profile["parameter_overrides"].(map[string]any)
				if overrides == nil {
					overrides = map[string]any{}
					profile["parameter_overrides"] = overrides
				}
				overrides["tripo/tripo-v3.1-full/text-to-3d"] = map[string]any{"face_limit": 100000}
			},
			want: "同时出现在 parameter_overrides 与 overrides_not_needed",
		},
	} {
		t.Run(name, func(t *testing.T) {
			err := mutateDefaultCatalog(t, func(document map[string]any) {
				chat := document["application"].(map[string]any)["chat"].(map[string]any)
				usage := chat["model3d_usage"].(map[string]any)
				for _, item := range usage["profiles"].([]any) {
					profile := item.(map[string]any)
					if profile["id"] == "render" {
						tc.change(profile)
						return
					}
				}
				t.Fatal("render profile missing")
			})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func purposeProfile(t *testing.T, usage ChatModel3DUsageSchema, id string) ChatModel3DUsageProfile {
	t.Helper()
	for _, profile := range usage.Profiles {
		if profile.ID == id {
			return profile
		}
	}
	t.Fatalf("profile %q missing", id)
	return ChatModel3DUsageProfile{}
}
