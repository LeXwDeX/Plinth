package mock

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

type deliveredFile struct {
	claim   generation.ArtifactClaim
	content []byte
}

// complete 把一个作业推进到完成并读出全部产物。
func complete(t *testing.T, adapter *Adapter, offering generation.Offering, submission string, input any) []deliveredFile {
	t.Helper()
	payload, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	started, err := adapter.Start(context.Background(), generation.StartRequest{
		TaskID:        generation.TaskID("task-" + submission),
		AttemptID:     generation.AttemptID("attempt-" + submission),
		SubmissionKey: submission,
		Offering:      offering,
		Input:         payload,
	})
	if err != nil {
		t.Fatal(err)
	}
	observe := generation.ObserveRequest{
		TaskID:    generation.TaskID("task-" + submission),
		AttemptID: generation.AttemptID("attempt-" + submission),
		Offering:  offering,
		Job:       *started.Job,
		Input:     payload,
	}
	var observation generation.Observation
	for range 4 {
		observation, err = adapter.Observe(context.Background(), observe)
		if err != nil {
			t.Fatal(err)
		}
		if observation.Phase == generation.UpstreamCompleted {
			break
		}
	}
	if observation.Phase != generation.UpstreamCompleted {
		t.Fatalf("job did not complete: %#v", observation)
	}
	return open(t, adapter, offering, observe, observation.Artifacts)
}

func open(t *testing.T, adapter *Adapter, offering generation.Offering, observe generation.ObserveRequest, claims []generation.ArtifactClaim) []deliveredFile {
	t.Helper()
	delivered := make([]deliveredFile, 0, len(claims))
	for _, claim := range claims {
		body, metadata, err := adapter.OpenAsset(context.Background(), generation.OpenAssetRequest{
			TaskID: observe.TaskID, AttemptID: observe.AttemptID, Offering: offering, Job: &observe.Job, Claim: claim,
		})
		if err != nil {
			t.Fatalf("open %s/%s: %v", claim.Role, claim.Variant, err)
		}
		content, err := io.ReadAll(body)
		_ = body.Close()
		if err != nil || int64(len(content)) != metadata.Size {
			t.Fatalf("read %s/%s: %v (%d of %d)", claim.Role, claim.Variant, err, len(content), metadata.Size)
		}
		delivered = append(delivered, deliveredFile{claim: claim, content: content})
	}
	return delivered
}

func imageOffering(capability string) generation.Offering {
	one := 1
	return generation.Offering{
		ProviderID: "openai",
		Capability: generation.Capability(capability),
		Domain:     generation.DomainImage,
		Outputs:    []generation.AssetRequirement{{Role: "image", Minimum: &one, MediaTypes: []string{"image/*"}}},
	}
}

func decodeSize(t *testing.T, content []byte) (int, int) {
	t.Helper()
	config, err := png.DecodeConfig(bytes.NewReader(content))
	if err != nil {
		t.Fatalf("decode PNG: %v", err)
	}
	return config.Width, config.Height
}

func TestImageFollowsRequestedAspectRatioAndSizeClass(t *testing.T) {
	cases := []struct {
		name         string
		input        map[string]any
		wantLong     int
		wantW, wantH float64
	}{
		{name: "16:9", input: map[string]any{"prompt": "x", "aspect_ratio": "16:9"}, wantLong: 1024, wantW: 16, wantH: 9},
		{name: "2:3 portrait", input: map[string]any{"prompt": "x", "aspect_ratio": "2:3"}, wantLong: 1024, wantW: 2, wantH: 3},
		{name: "1:1 default", input: map[string]any{"prompt": "x"}, wantLong: 1024, wantW: 1, wantH: 1},
		{name: "explicit size", input: map[string]any{"prompt": "x", "size": "1536x1024"}, wantLong: 1536, wantW: 3, wantH: 2},
		{name: "huge size is capped", input: map[string]any{"prompt": "x", "size": "3840x2160"}, wantLong: 1536, wantW: 16, wantH: 9},
		{name: "small size is raised", input: map[string]any{"prompt": "x", "size": "512x512"}, wantLong: 768, wantW: 1, wantH: 1},
		{name: "2K class", input: map[string]any{"prompt": "x", "aspect_ratio": "4:3", "image_size": "2K"}, wantLong: 1536, wantW: 4, wantH: 3},
		{name: "4K class", input: map[string]any{"prompt": "x", "aspect_ratio": "9:16", "image_size": "4K"}, wantLong: 2048, wantW: 9, wantH: 16},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			files := complete(t, New(), imageOffering("text-to-image"), "dims-"+tc.name, tc.input)
			if len(files) != 1 {
				t.Fatalf("claims = %d", len(files))
			}
			w, h := decodeSize(t, files[0].content)
			long := max(w, h)
			if long < 768 {
				t.Fatalf("long side %d < 768", long)
			}
			if long < tc.wantLong-8 || long > tc.wantLong+8 {
				t.Fatalf("long side = %d, want about %d (%dx%d)", long, tc.wantLong, w, h)
			}
			got, want := float64(w)/float64(h), tc.wantW/tc.wantH
			if got < want*0.99 || got > want*1.01 {
				t.Fatalf("ratio = %.3f (%dx%d), want %.3f", got, w, h, want)
			}
		})
	}
}

func TestImagesDifferPerRequestButAreDeterministicPerSubmission(t *testing.T) {
	adapter := New()
	input := map[string]any{"prompt": "赛博朋克女刺客", "aspect_ratio": "2:3"}
	first := complete(t, adapter, imageOffering("text-to-image"), "same-key", input)[0].content
	// 新适配器、同一提交键：字节完全相同（适配器重启后重放也一致）。
	again := complete(t, New(), imageOffering("text-to-image"), "same-key", input)[0].content
	if !bytes.Equal(first, again) {
		t.Fatal("same submission key produced different bytes")
	}
	other := complete(t, adapter, imageOffering("text-to-image"), "other-key", input)[0].content
	if bytes.Equal(first, other) {
		t.Fatal("different submissions produced identical images")
	}
	decoded := func(content []byte) (uint32, uint32) {
		img, err := png.Decode(bytes.NewReader(content))
		if err != nil {
			t.Fatal(err)
		}
		r, g, b, _ := img.At(img.Bounds().Dx()/8, img.Bounds().Dy()/2).RGBA()
		return r<<16 | g, b
	}
	// 同一提交键、不同提示词：主色不同（提示词哈希决定颜色）。
	promptA := complete(t, New(), imageOffering("text-to-image"), "colour-a", map[string]any{"prompt": "红色披风"})[0].content
	promptB := complete(t, New(), imageOffering("text-to-image"), "colour-a", map[string]any{"prompt": "黄昏雨后的废墟街角"})[0].content
	ca, da := decoded(promptA)
	cb, db := decoded(promptB)
	if ca == cb && da == db {
		t.Fatal("prompt hash did not change the colour")
	}
}

func TestImageCountFollowsN(t *testing.T) {
	files := complete(t, New(), imageOffering("text-to-image"), "six", map[string]any{"prompt": "x", "n": 6})
	if len(files) != 6 {
		t.Fatalf("claims = %d, want 6", len(files))
	}
	seen := map[string]bool{}
	for _, file := range files {
		seen[string(file.content)] = true
		if file.claim.Role != "image" || file.claim.MediaType != "image/png" {
			t.Fatalf("claim = %#v", file.claim)
		}
	}
	if len(seen) != 6 {
		t.Fatalf("only %d distinct images among 6", len(seen))
	}
}

func TestBackgroundRemovalKeepsATransparentBackground(t *testing.T) {
	files := complete(t, New(), imageOffering("background-removal"), "cutout", map[string]any{})
	img, err := png.Decode(bytes.NewReader(files[0].content))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, alpha := img.At(5, img.Bounds().Dy()-5).RGBA(); alpha != 0 {
		t.Fatalf("corner alpha = %d, want transparent", alpha)
	}
}

func validateDelivered(t *testing.T, name string, role, variant, mediaType string, content []byte) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "asset")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := local.ValidateDeliveredFile(file, role, variant, mediaType); err != nil {
		t.Errorf("%s: %s/%s rejected by the delivery check: %v", name, role, variant, err)
	}
}

// 默认 Catalog 里每个启用的 3D 与处理 Offering，都能交付自己声明的全部必需
// 格式，且每份字节都通过仓库的交付检查；任务因此可以真正成功。
func TestEveryEnabledModelOfferingDeliversValidRequiredFormats(t *testing.T) {
	loaded, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, offering := range loaded.List("") {
		if !offering.Enabled || (offering.Domain != generation.DomainModel3D && offering.Domain != generation.DomainProcessing) {
			continue
		}
		offering := offering
		t.Run(string(offering.ID), func(t *testing.T) {
			input := map[string]any{"prompt": "a cheerful knight", "source_task": "task-source", "action_id": 0, "format": "FBX"}
			files := complete(t, New(), offering, "models-"+string(offering.ID), input)
			store, err := local.New(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			for index, file := range files {
				profile, fidelity := "", ""
				for _, output := range offering.Outputs {
					if output.Role == file.claim.Role && output.Variant == file.claim.Variant {
						profile, fidelity = output.DeliveryProfile, output.Fidelity
					}
				}
				asset, err := store.Put(context.Background(), generation.AssetWrite{
					AssetID: generation.AssetID(fmt.Sprintf("asset-%d", index)), OwnerID: "owner", TaskID: "task", AttemptID: "attempt",
					Role: file.claim.Role, Variant: file.claim.Variant, MediaType: file.claim.MediaType,
					DeliveryProfile: profile, MaxBytes: int64(len(file.content)), Body: bytes.NewReader(file.content),
				})
				if err != nil {
					t.Fatalf("%s/%s delivery rejected: %v", file.claim.Role, file.claim.Variant, err)
				}
				// The declared skeleton/animation fidelity is checked by the same
				// store method Generation calls during materialization (#265).
				if err := store.CheckFidelity(context.Background(), asset, fidelity); err != nil {
					t.Errorf("%s/%s fails declared fidelity %q: %v", file.claim.Role, file.claim.Variant, fidelity, err)
				}
				body, err := store.Open(context.Background(), asset)
				if err != nil {
					t.Fatal(err)
				}
				actual, readErr := io.ReadAll(body)
				_ = body.Close()
				if readErr != nil || !bytes.Equal(actual, file.content) {
					t.Fatal("delivery changed bytes", readErr)
				}
			}
			for _, output := range offering.Outputs {
				if !blocksSuccess(output.Delivery) {
					continue
				}
				minimum := 1
				if output.Minimum != nil {
					minimum = *output.Minimum
				}
				if output.Delivery == generation.AssetDeliveryRequired && output.Minimum != nil && *output.Minimum == 0 {
					continue
				}
				matched := 0
				for _, file := range files {
					if file.claim.Role != output.Role {
						continue
					}
					switch {
					case output.Variant != "" && file.claim.Variant != output.Variant:
						continue
					case output.VariantPrefix != "" && len(file.claim.Variant) < len(output.VariantPrefix):
						continue
					case output.VariantPrefix != "" && file.claim.Variant[:len(output.VariantPrefix)] != output.VariantPrefix:
						continue
					}
					matched++
				}
				if matched < minimum {
					t.Errorf("output %s/%s%s: delivered %d, need %d", output.Role, output.Variant, output.VariantPrefix, matched, minimum)
				}
			}
			checked++
		})
	}
	if checked < 5 {
		t.Fatalf("only %d model offerings were checked", checked)
	}
}

func TestPostSuccessDeliversPNGThumbnails(t *testing.T) {
	loaded, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering, ok := loaded.Get("meshy/meshy-7.1/image-to-3d")
	if !ok {
		t.Fatal("meshy 7.1 image-to-3d missing from catalog")
	}
	adapter := New()
	files := complete(t, adapter, offering, "thumb", map[string]any{"prompt": "knight"})
	if len(files) == 0 {
		t.Fatal("no claims")
	}
	job := generation.UpstreamJob{ProviderID: offering.ProviderID, RemoteID: adapter.jobs[adapter.submissions["thumb"]].remoteID}
	result, err := adapter.Observe(context.Background(), generation.ObserveRequest{
		TaskID: "task-thumb", Offering: offering, Job: job, PostSuccess: true, Input: []byte(`{"prompt":"knight"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	thumbnails := 0
	for _, claim := range result.Artifacts {
		if claim.Role != "thumbnail" {
			continue
		}
		thumbnails++
		if claim.MediaType != "image/png" {
			t.Fatalf("thumbnail media type = %q", claim.MediaType)
		}
		validateDelivered(t, "thumbnail", claim.Role, claim.Variant, claim.MediaType, claim.Inline)
		if w, h := decodeSize(t, claim.Inline); w < 256 || h < 256 {
			t.Fatalf("thumbnail %dx%d", w, h)
		}
	}
	if thumbnails != 1 {
		t.Fatalf("thumbnails = %d", thumbnails)
	}
}

func TestRetopologyHasFewerFacesThanTheOriginal(t *testing.T) {
	original := buildHumanoid(digest("g"), 8)
	remeshed := buildHumanoid(digest("g"), 4)
	if remeshed.triangleCount() >= original.triangleCount()/2 {
		t.Fatalf("remesh %d tris vs original %d", remeshed.triangleCount(), original.triangleCount())
	}
}

func TestOldPlaceholderGLBIsRejectedByTheDeliveryCheck(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.glb")
	if err := os.WriteFile(path, []byte("glTF\x02\x00\x00\x00mock-generated-model:task"), 0o600); err != nil {
		t.Fatal(err)
	}
	file, _ := os.Open(path)
	defer file.Close()
	if err := local.ValidateDeliveredFile(file, "model", "glb", "model/gltf-binary"); err == nil {
		t.Fatal("the delivery check no longer rejects the old placeholder; this test guards the reason for real GLBs")
	}
}

func TestInsufficientBalanceMarkerReturnsTheProviderBalanceFailure(t *testing.T) {
	_, err := New().Start(context.Background(), generation.StartRequest{
		TaskID: "t", AttemptID: "a", SubmissionKey: "balance",
		Offering: imageOffering("text-to-image"),
		Input:    []byte(`{"prompt":"画一个骑士 ` + MarkerInsufficientBalance + `"}`),
	})
	var providerErr *generation.ProviderError
	if !errors.As(err, &providerErr) || providerErr.Failure == nil {
		t.Fatalf("err = %v, want a ProviderError", err)
	}
	if providerErr.Failure.Class != generation.FailureInsufficientBalance || providerErr.Failure.Retryable {
		t.Fatalf("failure = %#v", providerErr.Failure)
	}
	// 没有记号的同类请求照常成功。
	if _, err := New().Start(context.Background(), generation.StartRequest{
		TaskID: "t", AttemptID: "a", SubmissionKey: "balance-ok",
		Offering: imageOffering("text-to-image"), Input: []byte(`{"prompt":"画一个骑士"}`),
	}); err != nil {
		t.Fatal(err)
	}
}

func TestSlowMarkerStretchesProgressOverTheConfiguredTime(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	adapter := NewWithOptions(Options{SlowLatency: 20 * time.Second, Now: func() time.Time { return now }})
	offering := imageOffering("text-to-image")
	started, err := adapter.Start(context.Background(), generation.StartRequest{
		TaskID: "t", AttemptID: "a", SubmissionKey: "slow", Offering: offering,
		Input: []byte(`{"prompt":"慢任务 ` + MarkerSlow + `"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	observe := func() generation.Observation {
		result, err := adapter.Observe(context.Background(), generation.ObserveRequest{TaskID: "t", AttemptID: "a", Offering: offering, Job: *started.Job})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	if first := observe(); first.Phase != generation.UpstreamRunning || first.Progress > 10 {
		t.Fatalf("t=0 observation = %#v", first)
	}
	now = now.Add(10 * time.Second)
	middle := observe()
	if middle.Phase != generation.UpstreamRunning || middle.Progress < 40 || middle.Progress > 60 {
		t.Fatalf("t=10s observation = %#v", middle)
	}
	now = now.Add(11 * time.Second)
	if done := observe(); done.Phase != generation.UpstreamCompleted || len(done.Artifacts) != 1 {
		t.Fatalf("t=21s observation = %#v", done)
	}
}

func TestDevelopmentLatencyApplies(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	options := DevelopmentOptions()
	options.Now = func() time.Time { return now }
	adapter := NewWithOptions(options)
	offering := imageOffering("text-to-image")
	offering.Domain = generation.DomainModel3D
	started, err := adapter.Start(context.Background(), generation.StartRequest{
		TaskID: "t", AttemptID: "a", SubmissionKey: "dev", Offering: offering, Input: []byte(`{"prompt":"x"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	observation, err := adapter.Observe(context.Background(), generation.ObserveRequest{TaskID: "t", AttemptID: "a", Offering: offering, Job: *started.Job})
	if err != nil || observation.Phase != generation.UpstreamRunning {
		t.Fatalf("3D job finished instantly: %#v %v", observation, err)
	}
	now = now.Add(options.ModelLatency + time.Second)
	observation, err = adapter.Observe(context.Background(), generation.ObserveRequest{TaskID: "t", AttemptID: "a", Offering: offering, Job: *started.Job})
	if err != nil || observation.Phase != generation.UpstreamCompleted {
		t.Fatalf("3D job did not finish after its latency: %#v %v", observation, err)
	}
}

// 前端把按产物 ID 排序后序号最小的模型文件交给查看器；GLB 必须排在最前。
func TestViewableGLBSortsFirstAmongModelClaims(t *testing.T) {
	loaded, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"meshy/meshy-7.1/image-to-3d", "meshy/processing/remesh", "meshy/processing/rigging", "meshy/processing/animation", "tripo/tripo-v3.1-full/image-to-3d"} {
		offering, ok := loaded.Get(generation.OfferingID(id))
		if !ok {
			t.Fatalf("%s missing", id)
		}
		files := complete(t, New(), offering, "order-"+id, map[string]any{"prompt": "x", "source_task": "task-source", "action_id": 0})
		first := ""
		for _, file := range files { // 已按 ID 排序的交付顺序即内核编号顺序
			if file.claim.Role == "model" || file.claim.Role == "converted_model" {
				first = file.claim.MediaType
				break
			}
		}
		if first != "model/gltf-binary" {
			t.Errorf("%s: first model claim has media type %q, want a GLB", id, first)
		}
	}
}
