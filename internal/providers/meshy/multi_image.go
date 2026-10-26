package meshy

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

const multiImageEndpoint = "multi-image-to-3d"

// multiImageSourceParameters locates the two schema-declared, mutually
// exclusive source forms accepted by Meshy's multi-image endpoint.
func multiImageSourceParameters(offering generation.Offering) (task, images *schema.Parameter) {
	for index := range offering.Parameters {
		parameter := &offering.Parameters[index]
		wireName := strings.TrimSpace(parameter.WireName)
		if wireName == "" {
			wireName = parameter.Name
		}
		if parameter.Type == "task" && wireName == "input_task_id" && parameter.Semantic == schema.SemanticSourceTask {
			task = parameter
		}
		if parameter.Type == "asset-array" && wireName == "image_urls" && parameter.Semantic == schema.SemanticReferenceImage {
			images = parameter
		}
	}
	return task, images
}

func validateMultiImageOffering(offering generation.Offering, options wireOptions) error {
	if strings.TrimSpace(options.Endpoint) != multiImageEndpoint {
		return nil
	}
	if offering.Capability != generation.ImageTo3D || offering.IdempotentSubmission {
		return errors.New("multi-image-to-3d requires non-idempotent image-to-3d submission")
	}
	if options.APIVersion != "v1" || options.ModelParameter != "ai_model" {
		return errors.New("multi-image-to-3d requires API v1 and the ai_model field")
	}
	modelValue := strings.TrimSpace(options.ModelParameterValue)
	if modelValue == "" {
		modelValue = offering.ModelID
	}
	switch modelValue {
	// latest is not pinned and meshy-7 is deprecated (#329, Meshy changelog 2026-09-18).
	case "meshy-7.1", "meshy-6", "meshy-6-lite":
	default:
		return errors.New("multi-image-to-3d declares an unsupported or deprecated Meshy model")
	}
	modelMap := false
	scalarSlots := map[string]resultAssetProjection{}
	for _, projection := range options.ResultAssets {
		if projection.Role != "model" {
			continue
		}
		// 历史 Offering 用单个 map 投影交付全部格式；新 Offering 为每个精确槽位
		// 声明标量投影和 MIME，避免依赖 CDN 标签与宿主机 MIME 表。
		if projection.Mode == "map" && projection.Path == "model_urls" {
			modelMap = true
			continue
		}
		if projection.Mode != "scalar" || !strings.HasPrefix(projection.Path, "model_urls.") ||
			strings.TrimSpace(strings.TrimPrefix(projection.Path, "model_urls.")) == "" {
			continue
		}
		if _, duplicate := scalarSlots[projection.Path]; duplicate {
			return fmt.Errorf("multi-image-to-3d projects %q more than once", projection.Path)
		}
		scalarSlots[projection.Path] = projection
	}
	if !modelMap && len(scalarSlots) == 0 {
		return errors.New("multi-image-to-3d must project model_urls as a model map or exact scalar slots")
	}
	if modelMap && len(scalarSlots) > 0 {
		return errors.New("multi-image-to-3d must not mix the model_urls map with exact scalar slots")
	}
	// 标量投影在启动时就要覆盖每个必需模型槽位并声明精确 MIME；否则配置错误
	// 要等付费任务进入物化阶段才暴露。历史 map 投影覆盖全部格式，不做此检查。
	if len(scalarSlots) > 0 {
		for path, projection := range scalarSlots {
			if path != "model_urls."+projection.Variant {
				return fmt.Errorf("multi-image-to-3d projection %q does not match its variant %q", path, projection.Variant)
			}
		}
		for _, requirement := range offering.Outputs {
			if requirement.Role != "model" || requirement.Minimum == nil || *requirement.Minimum < 1 {
				continue
			}
			projection, ok := scalarSlots["model_urls."+requirement.Variant]
			if !ok {
				return fmt.Errorf("multi-image-to-3d must project required model slot %q as model_urls.%s", requirement.Variant, requirement.Variant)
			}
			if len(requirement.MediaTypes) != 1 || projection.MediaType != requirement.MediaTypes[0] {
				return fmt.Errorf("multi-image-to-3d projection %q must declare the exact media type of required slot %q", projection.Path, requirement.Variant)
			}
		}
	}
	if err := validateMultiImageTextureImages(offering, modelValue); err != nil {
		return err
	}
	task, images := multiImageSourceParameters(offering)
	if task == nil && images == nil {
		return errors.New("multi-image-to-3d requires a declared task or reference-image input")
	}
	if task != nil && (task.Required || task.TaskSourceKind != schema.TaskSourceUpstreamJob || !task.SameProvider || len(task.TaskCapabilities) == 0 || len(task.TaskOfferings) == 0) {
		return errors.New("multi-image input_task_id must declare an optional same-provider upstream task allowlist")
	}
	if task != nil {
		for _, capability := range task.TaskCapabilities {
			if capability != generation.TextToImage && capability != generation.ImageToImage {
				return errors.New("multi-image input_task_id only accepts image-generation task capabilities")
			}
		}
	}
	if images != nil {
		if images.Name != "reference_assets" || (images.Required && task != nil) || images.MinItems < 1 || images.MaxItems < images.MinItems || images.MaxItems > 4 || images.MaxBytes < 1 || images.MaxBytes > 20<<20 || len(images.MediaTypes) == 0 {
			return errors.New("multi-image image_urls must declare 1-4 images and a byte/media-type limit")
		}
		for _, mediaType := range images.MediaTypes {
			switch strings.ToLower(strings.TrimSpace(mediaType)) {
			case "image/jpeg", "image/png", "image/webp":
			default:
				return errors.New("multi-image image_urls declares an unsupported media type")
			}
		}
	}
	return nil
}

func (a *Adapter) addMultiImageInputs(
	ctx context.Context,
	request generation.StartRequest,
	body map[string]any,
) error {
	taskParameter, imageParameter := multiImageSourceParameters(request.Offering)
	if taskParameter == nil && imageParameter == nil {
		return providerFailure(generation.FailureInvalidInput, false, "Meshy 多图 Offering 缺少来源参数声明")
	}
	rawTaskID, taskKeyPresent := body["input_task_id"]
	taskID, taskIsString := rawTaskID.(string)
	taskPresent := taskKeyPresent && taskIsString && strings.TrimSpace(taskID) != ""
	if taskKeyPresent && (!taskIsString || !taskPresent || taskParameter == nil) {
		return providerFailure(generation.FailureInvalidInput, false, "Meshy 多图任务来源未在 Offering Schema 中声明")
	}
	var images []generation.InputAsset
	if imageParameter != nil {
		images = request.InputAssets[imageParameter.Name]
		if len(images) > 0 && (len(images) < imageParameter.MinItems || len(images) > imageParameter.MaxItems) {
			return providerFailure(generation.FailureInvalidInput, false, "Meshy 多图输入数量超出 Offering 限制")
		}
		for _, asset := range images {
			if request.OwnerID == "" || asset.OwnerID != request.OwnerID {
				return providerFailure(generation.FailureInvalidInput, false, "Meshy 多图参考资产所有权不一致")
			}
			allowed := false
			for _, mediaType := range imageParameter.MediaTypes {
				allowed = allowed || strings.EqualFold(strings.TrimSpace(mediaType), strings.TrimSpace(asset.MediaType))
			}
			if !allowed {
				return providerFailure(generation.FailureInvalidInput, false, "Meshy 多图参考图格式不在 Offering Schema 限制内")
			}
		}
	}
	if taskPresent == (len(images) > 0) {
		return providerFailure(generation.FailureInvalidInput, false, "Meshy 多图输入必须且只能选择一种来源")
	}
	if taskPresent {
		dependency, ok := request.TaskDependencies[taskParameter.Name]
		if !ok || dependency.TaskID == "" || !slices.Contains(taskParameter.TaskCapabilities, dependency.Capability) || !slices.Contains(taskParameter.TaskOfferings, dependency.OfferingID) || dependency.ProviderID != request.Offering.ProviderID || dependency.UpstreamJob.ProviderID != request.Offering.ProviderID || dependency.UpstreamJob.RemoteID != taskID {
			return providerFailure(generation.FailureInvalidInput, false, "Meshy 多图任务来源与解析后的任务依赖不一致")
		}
		delete(body, "image_urls")
		delete(body, "image_url")
		delete(body, "reference_assets")
		return nil
	}
	if imageParameter.Name != "reference_assets" {
		return providerFailure(generation.FailureInvalidInput, false, "Meshy 多图参考资产参数未被实现")
	}
	dataURIs, err := a.referenceDataURIs(ctx, request)
	if err != nil {
		return err
	}
	delete(body, "reference_assets")
	delete(body, "image_url")
	body["image_urls"] = dataURIs
	return nil
}

// validateMultiImageTextureImages checks an optional texture_image_urls list
// against Meshy's Multi-Image to 3D documentation (#329): one to four
// JPEG/PNG/WebP images of at most 20 MB, only with meshy-7.1, and never
// together with texture_image_url or texture_prompt.
func validateMultiImageTextureImages(offering generation.Offering, modelValue string) error {
	var textures *schema.Parameter
	exclusive := false
	for index := range offering.Parameters {
		parameter := &offering.Parameters[index]
		wireName := strings.TrimSpace(parameter.WireName)
		if wireName == "" {
			wireName = parameter.Name
		}
		switch wireName {
		case "texture_image_urls":
			textures = parameter
		case "texture_image_url", "texture_prompt":
			exclusive = true
		}
	}
	if textures == nil {
		return nil
	}
	if textures.Type != "asset-array" || textures.Required || textures.MinItems < 1 || textures.MaxItems < textures.MinItems ||
		textures.MaxItems > 4 || textures.MaxBytes < 1 || textures.MaxBytes > 20_000_000 || len(textures.MediaTypes) == 0 {
		return errors.New("multi-image texture_image_urls must be an optional list of 1-4 images with a byte/media-type limit")
	}
	for _, mediaType := range textures.MediaTypes {
		switch strings.ToLower(strings.TrimSpace(mediaType)) {
		case "image/jpeg", "image/png", "image/webp":
		default:
			return errors.New("multi-image texture_image_urls declares an unsupported media type")
		}
	}
	if modelValue != "meshy-7.1" {
		return errors.New("multi-image texture_image_urls requires meshy-7.1")
	}
	if exclusive {
		return errors.New("multi-image texture_image_urls cannot be declared with texture_image_url or texture_prompt")
	}
	return nil
}
