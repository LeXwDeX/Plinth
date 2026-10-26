package generation_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// validGLB 是带真实 glTF 二进制魔数与 JSON chunk 头的最小载荷，
// 避免用假字节冒充模型产物。
func validGLB(label string) []byte {
	body := []byte(`{"asset":{"version":"2.0"},"extras":{"label":"` + label + `"}}`)
	for len(body)%4 != 0 {
		body = append(body, 0x20)
	}
	asset := make([]byte, 20+len(body))
	copy(asset[:4], "glTF")
	binary.LittleEndian.PutUint32(asset[4:8], 2)
	binary.LittleEndian.PutUint32(asset[8:12], uint32(len(asset)))
	binary.LittleEndian.PutUint32(asset[12:16], uint32(len(body)))
	copy(asset[16:20], "JSON")
	copy(asset[20:], body)
	return asset
}

// variantClaimsAdapter 按当前目录 rigging/animation 的声明产出带变体的
// 产物：required 主产物 + basic_animations. 集合项。可配置为只回集合项
// 或让集合项下载失败，用于驱动 required/background 门控行为。
type variantClaimsAdapter struct {
	collectionOnly    bool
	breakBackgroundDL bool
	omitBackground    bool
}

func primaryVariantFor(offering generation.Offering) string {
	for _, output := range offering.Outputs {
		if output.Variant != "" {
			return output.Variant
		}
		if output.VariantPrefix != "" {
			return strings.TrimRight(output.VariantPrefix, "_") + "_glb_url"
		}
	}
	return ""
}

func (a *variantClaimsAdapter) claims(
	request generation.StartRequest,
) []generation.ArtifactClaim {
	primary := generation.ArtifactClaim{
		ID:        string(request.AttemptID) + ":primary",
		Role:      "model",
		Variant:   primaryVariantFor(request.Offering),
		Inline:    validGLB("primary"),
		MediaType: "model/gltf-binary",
	}
	if request.Offering.Capability == generation.Animation {
		primary.Inline = fidelityFixtureGLB("animation")
	}
	if primary.Variant == "rigged_character_fbx_url" {
		primary.MediaType = "application/octet-stream"
	}
	background := generation.ArtifactClaim{
		ID:        string(request.AttemptID) + ":idle",
		Role:      "model",
		Variant:   "basic_animations.idle",
		Inline:    validGLB("idle"),
		MediaType: "model/gltf-binary",
	}
	if a.breakBackgroundDL {
		background.Inline = nil
		background.Locator = "broken://background"
	}
	if a.collectionOnly {
		return []generation.ArtifactClaim{background}
	}
	claims := []generation.ArtifactClaim{primary}
	if primary.Variant == "rigged_character_fbx_url" {
		claims = append(claims, generation.ArtifactClaim{
			ID: string(request.AttemptID) + ":glb", Role: "model",
			Variant: "rigged_character_glb_url", Inline: fidelityFixtureGLB("skeleton"),
			MediaType: "model/gltf-binary",
		})
	}
	if !a.omitBackground {
		claims = append(claims, background)
	}
	return claims
}

func (a *variantClaimsAdapter) Start(
	_ context.Context,
	request generation.StartRequest,
) (generation.Observation, error) {
	return generation.Observation{
		Phase: generation.UpstreamCompleted,
		Job: &generation.UpstreamJob{
			ProviderID: request.Offering.ProviderID,
			RemoteID:   "variant-remote-" + string(request.AttemptID),
		},
		Artifacts: a.claims(request),
	}, nil
}

func (*variantClaimsAdapter) Observe(
	context.Context,
	generation.ObserveRequest,
) (generation.Observation, error) {
	return generation.Observation{}, errors.New("unexpected Observe")
}

func (a *variantClaimsAdapter) OpenAsset(
	_ context.Context,
	request generation.OpenAssetRequest,
) (io.ReadCloser, generation.AssetMetadata, error) {
	if request.Claim.Locator == "broken://background" {
		return nil, generation.AssetMetadata{}, &generation.ProviderError{
			Failure: &generation.Failure{
				Class:      generation.FailureUpstreamUnavailable,
				Retryable:  true,
				SafeDetail: "background artifact unavailable",
			},
		}
	}
	return io.NopCloser(bytes.NewReader(request.Claim.Inline)), generation.AssetMetadata{
		MediaType: request.Claim.MediaType,
		Size:      int64(len(request.Claim.Inline)),
	}, nil
}

func submitRigging(
	t *testing.T,
	harness *harness,
) generation.TaskID {
	t.Helper()
	source, err := harness.inputs.Upload(context.Background(), "owner-a",
		bytes.NewReader(validGLB("source")), "model/gltf-binary", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := harness.submitApproved(t, context.Background(), generation.SubmitCommand{
		OwnerID:    "owner-a",
		Capability: generation.Rigging,
		OfferingID: "meshy/processing/rigging",
		Input:      []byte(`{"source_asset":"` + string(source.ID) + `"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	return accepted.TaskID
}

// TestRequiredPrefixAndBackgroundCollectionMaterialize 覆盖 §3.5：主产物
// 前缀命中 required，集合项命中 background；成功后 required 标记只落在
// 主产物上，一对一分配不允许集合项顶替主产物槽位。
func TestRequiredPrefixAndBackgroundCollectionMaterialize(t *testing.T) {
	harness := newHarness(t, &variantClaimsAdapter{}, nil)
	task := harness.runUntilTerminal(t, "owner-a", submitRigging(t, harness))
	if task.State != generation.TaskSucceeded {
		t.Fatalf("task = %#v", task)
	}
	if len(task.Assets) != 3 {
		t.Fatalf("assets = %#v", task.Assets)
	}
	required := map[string]bool{}
	for _, asset := range task.Assets {
		if asset.Role != "model" {
			t.Fatalf("asset role = %q", asset.Role)
		}
		required[asset.Variant] = asset.Required
	}
	if !required["rigged_character_glb_url"] || !required["rigged_character_fbx_url"] {
		t.Fatalf("primary formats not required: %#v", required)
	}
	if required["basic_animations.idle"] {
		t.Fatalf("minimum-zero animation should not be mandatory when absent: %#v", required)
	}
}

// TestCollectionOnlyResponseFailsRequiredPrefix 是 §11 证伪器：仅含
// basic_animations.* 的响应不得被误判成功，required 计数不足必须进入
// FAILED（integrity）路径且不落任何资产。
func TestCollectionOnlyResponseFailsRequiredPrefix(t *testing.T) {
	harness := newHarness(t, &variantClaimsAdapter{collectionOnly: true}, nil)
	task := harness.runUntilTerminal(t, "owner-a", submitRigging(t, harness))
	if task.State != generation.TaskFailed ||
		task.Failure == nil ||
		task.Failure.Class != generation.FailureIntegrity {
		t.Fatalf("task = %#v", task)
	}
	if len(task.Assets) != 0 {
		t.Fatalf("failed task persisted assets: %#v", task.Assets)
	}
	if !strings.Contains(task.Failure.SafeDetail, "rigged_character") {
		t.Fatalf("failure detail = %q", task.Failure.SafeDetail)
	}
}

// A missing optional animation may complete, but a claimed animation must be
// cached successfully under the required/minimum=0 contract.
func TestClaimedAnimationFailureBlocksSuccess(t *testing.T) {
	harness := newHarness(
		t,
		&variantClaimsAdapter{breakBackgroundDL: true},
		nil,
	)
	task := harness.runUntilTerminal(t, "owner-a", submitRigging(t, harness))
	if task.State != generation.TaskFailed {
		t.Fatalf("task = %#v", task)
	}
}

func TestMissingOptionalAnimationAllowsSuccess(t *testing.T) {
	harness := newHarness(t, &variantClaimsAdapter{omitBackground: true}, nil)
	task := harness.runUntilTerminal(t, "owner-a", submitRigging(t, harness))
	if task.State != generation.TaskSucceeded {
		t.Fatalf("task = %#v", task)
	}
	if len(task.Assets) != 2 {
		t.Fatalf("assets = %#v", task.Assets)
	}
}

// TestAnimationPrimaryPrefixUsesSameRoleDistinctVariants 覆盖 §3.3：
// animation 声明 animation_ required + basic_animations. background，
// 同 role 不同变体各自命中自己的槽位；且 source_task 依赖链在带变体的
// rigging 产物上仍可解析。
func TestAnimationPrimaryPrefixUsesSameRoleDistinctVariants(t *testing.T) {
	harness := newHarness(t, &variantClaimsAdapter{}, nil)
	riggingTask := harness.runUntilTerminal(t, "owner-a", submitRigging(t, harness))
	if riggingTask.State != generation.TaskSucceeded {
		t.Fatalf("rigging task = %#v", riggingTask)
	}
	accepted, err := harness.submitApproved(t, context.Background(), generation.SubmitCommand{
		OwnerID:    "owner-a",
		Capability: generation.Animation,
		OfferingID: "meshy/processing/animation",
		Input: []byte(
			`{"source_task":"` + string(riggingTask.ID) + `","action_id":3}`,
		),
	})
	if err != nil {
		t.Fatal(err)
	}
	task := harness.runUntilTerminal(t, "owner-a", accepted.TaskID)
	if task.State != generation.TaskSucceeded {
		t.Fatalf("task = %#v", task)
	}
	required := map[string]bool{}
	for _, asset := range task.Assets {
		required[asset.Variant] = asset.Required
	}
	if !required["animation_glb_url"] || required["basic_animations.idle"] {
		t.Fatalf("required flags = %#v", required)
	}
}

// 已停用的 Meshy 7 仍可留在 Catalog 中供历史记录解释，但不能创建新任务。
func TestDisabledMeshy7OfferingCannotAcceptNewTasks(t *testing.T) {
	harness := newHarness(t, &legacyRoleOnlyAdapter{}, nil)
	_, err := harness.submitApproved(t, context.Background(), generation.SubmitCommand{
		OwnerID:    "owner-a",
		Capability: generation.TextTo3D,
		OfferingID: "meshy/meshy-7/text-to-3d",
		Input:      []byte(`{"prompt":"castle"}`),
	})
	// #329 follow-up: Meshy 7 is retired, so the refusal is invalid input with
	// the Schema reason naming Meshy 7.1 rather than an unknown Offering.
	if !generation.IsCode(err, generation.ErrInvalidInput) || !strings.Contains(generation.UserFacingDetail(err), "Meshy 7.1") {
		t.Fatalf("disabled Meshy 7 offer accepted new task, err=%v", err)
	}
}

type legacyRoleOnlyAdapter struct{}

func (*legacyRoleOnlyAdapter) Start(
	_ context.Context,
	request generation.StartRequest,
) (generation.Observation, error) {
	return generation.Observation{
		Phase: generation.UpstreamCompleted,
		Job: &generation.UpstreamJob{
			ProviderID: request.Offering.ProviderID,
			RemoteID:   "legacy-remote-" + string(request.AttemptID),
		},
		Artifacts: []generation.ArtifactClaim{{
			ID:        string(request.AttemptID) + ":model",
			Role:      "model",
			Inline:    validGLB("legacy"),
			MediaType: "model/gltf-binary",
		}},
	}, nil
}

func (*legacyRoleOnlyAdapter) Observe(
	context.Context,
	generation.ObserveRequest,
) (generation.Observation, error) {
	return generation.Observation{}, errors.New("unexpected Observe")
}

func (*legacyRoleOnlyAdapter) OpenAsset(
	_ context.Context,
	request generation.OpenAssetRequest,
) (io.ReadCloser, generation.AssetMetadata, error) {
	return io.NopCloser(bytes.NewReader(request.Claim.Inline)), generation.AssetMetadata{
		MediaType: request.Claim.MediaType,
		Size:      int64(len(request.Claim.Inline)),
	}, nil
}
