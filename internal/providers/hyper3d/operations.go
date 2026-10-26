package hyper3d

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/imagewire"
)

func validateBangOffering(offering generation.Offering, options wireOptions) error {
	if !validSharedRoutes(options) || !noTemplateCollisions(offering, options) || offering.Capability != generation.Capability("mesh-segmentation") {
		return fmt.Errorf("Bang 必须声明安全路由与 mesh-segmentation capability")
	}
	if err := validateModelOutput(offering); err != nil {
		return err
	}
	if !validTemplateOutputFormat(offering, options) {
		return fmt.Errorf("Bang 模板输出格式必须与必需模型产物声明一致")
	}
	var taskSource, modelSource, imageSource bool
	for _, parameter := range offering.Parameters {
		wire := effectiveWireName(parameter)
		switch wire {
		case "asset_id":
			if parameter.Type != "task" || !parameter.SameProvider || !hasRodinCapability(parameter.TaskCapabilities) {
				return fmt.Errorf("Bang asset_id 必须引用 Hyper3D Rodin 任务")
			}
			taskSource = true
		case "model":
			if parameter.Type != "asset" || parameter.MaxBytes <= 0 || !allBangModelMedia(parameter.MediaTypes) {
				return fmt.Errorf("Bang model 必须声明单个有界的受支持模型文件")
			}
			modelSource = true
		case "image":
			if parameter.Type != "asset" || parameter.MaxBytes <= 0 || !allImageMedia(parameter.MediaTypes) {
				return fmt.Errorf("Bang image 必须声明为单个有界图片文件")
			}
			imageSource = true
		case "geometry_file_format":
			if !validFormatParameter(parameter) || !formatsMatchOutput(offering, parameter) {
				return fmt.Errorf("Bang 输出格式参数包含未受支持的格式")
			}
		case "prompt", "instruction":
			if parameter.Type != "text" {
				return fmt.Errorf("Bang %s 必须是 text 参数", wire)
			}
		case "strength":
			if !validNumericParameter(parameter, true, 1, 12) {
				return fmt.Errorf("Bang strength 必须限定在 1..12")
			}
		case "quality_override":
			if !validNumericParameter(parameter, true, 500, 2000000) {
				return fmt.Errorf("Bang quality_override 必须限定在 500..2000000")
			}
		case "seed":
			if !validNumericParameter(parameter, true, 0, 65535) {
				return fmt.Errorf("Bang seed 必须限定在 0..65535")
			}
		case "escore":
			if !validNumericParameter(parameter, false, 0, 10) {
				return fmt.Errorf("Bang escore 必须限定在 0..10")
			}
		case "reference_scale":
			if !validNumericParameter(parameter, false, 0, 1) {
				return fmt.Errorf("Bang reference_scale 必须限定在 0..1")
			}
		case "material":
			if !validSelectParameter(parameter, "PBR", "Shaded", "All", "None") {
				return fmt.Errorf("Bang material 选项无效")
			}
		case "resolution":
			if !validSelectParameter(parameter, "Basic", "High") {
				return fmt.Errorf("Bang resolution 选项无效")
			}
		case "mesh_mode":
			if !validSelectParameter(parameter, "Quad", "Raw") {
				return fmt.Errorf("Bang mesh_mode 选项无效")
			}
		default:
			return fmt.Errorf("Bang 参数 wire name %q 未受支持", wire)
		}
	}
	if !modelSource && !taskSource {
		return fmt.Errorf("Bang 必须声明本地模型来源或 Rodin 原生任务来源")
	}
	if modelSource && !imageSource {
		return fmt.Errorf("Bang 本地模型来源必须声明单张 image 参数")
	}
	return nil
}

func validateTextureOffering(offering generation.Offering, options wireOptions) error {
	if !validSharedRoutes(options) || !noTemplateCollisions(offering, options) || offering.Capability != generation.Retexture {
		return fmt.Errorf("Texture 必须声明安全路由与 retexture capability")
	}
	if err := validateModelOutput(offering); err != nil {
		return err
	}
	if !validTemplateOutputFormat(offering, options) {
		return fmt.Errorf("Texture 模板输出格式必须与必需模型产物声明一致")
	}
	model, image := false, false
	for _, parameter := range offering.Parameters {
		wire := effectiveWireName(parameter)
		switch wire {
		case "model":
			model = parameter.Type == "asset" && parameter.Required && parameter.MaxBytes > 0 && allTextureModelMedia(parameter.MediaTypes)
		case "image":
			image = parameter.Type == "asset" && parameter.Required && parameter.MaxBytes > 0 && allImageMedia(parameter.MediaTypes)
		case "geometry_file_format":
			if !validFormatParameter(parameter) || !formatsMatchOutput(offering, parameter) {
				return fmt.Errorf("Texture 输出格式参数包含未受支持的格式")
			}
		case "prompt":
			if parameter.Type != "text" {
				return fmt.Errorf("Texture prompt 必须是 text 参数")
			}
		case "escore":
			if !validNumericParameter(parameter, false, 0, 10) {
				return fmt.Errorf("Texture escore 必须限定在 0..10")
			}
		case "reference_scale":
			if !validNumericParameter(parameter, false, 0.5, 1) {
				return fmt.Errorf("Texture reference_scale 必须限定在 0.5..1")
			}
		case "seed":
			if !validNumericParameter(parameter, true, 0, 65535) {
				return fmt.Errorf("Texture seed 必须限定在 0..65535")
			}
		case "hd_texture", "texture_delight":
			if parameter.Type != "boolean" {
				return fmt.Errorf("Texture %s 必须是 boolean 参数", wire)
			}
		case "texture_mode":
			if !validSelectParameter(parameter, "legacy", "extreme-low", "low", "medium", "high", "extreme-high") {
				return fmt.Errorf("Texture texture_mode 选项无效")
			}
		case "material":
			if !validSelectParameter(parameter, "PBR", "Shaded", "All") {
				return fmt.Errorf("Texture material 选项无效")
			}
		case "resolution":
			if !validSelectParameter(parameter, "Basic", "High") {
				return fmt.Errorf("Texture resolution 选项无效")
			}
		default:
			return fmt.Errorf("Texture 参数 wire name %q 未受支持", wire)
		}
	}
	if !model || !image {
		return fmt.Errorf("Texture 必须声明本地 model 与 image 文件输入")
	}
	return nil
}

func validSharedRoutes(options wireOptions) bool {
	if !safeEndpoint(options.Endpoint) || !safeEndpoint(options.ObserveEndpoint) || !safeEndpoint(options.DownloadEndpoint) {
		return false
	}
	var template map[string]any
	if json.Unmarshal(options.RequestTemplate, &template) != nil || len(template) == 0 {
		return false
	}
	for _, field := range []string{"asset_id", "model", "image", "prompt", "instruction"} {
		if _, exists := template[field]; exists {
			return false
		}
	}
	for field := range template {
		if !isTemplateField(options.Endpoint, field) {
			return false
		}
		if !validTemplateValue(options.Endpoint, field, template[field]) {
			return false
		}
	}
	return true
}

func validTemplateValue(endpoint, field string, value any) bool {
	switch field {
	case "geometry_file_format":
		format, ok := value.(string)
		return ok && supportedOutputVariant(format)
	case "material":
		allowed := []string{"PBR", "Shaded", "All"}
		if endpoint == bangEndpoint {
			allowed = append(allowed, "None")
		}
		return oneOf(value, allowed...)
	case "resolution":
		return oneOf(value, "Basic", "High")
	case "mesh_mode":
		return oneOf(value, "Quad", "Raw")
	case "texture_mode":
		return oneOf(value, "legacy", "extreme-low", "low", "medium", "high", "extreme-high")
	case "hd_texture", "texture_delight":
		_, ok := value.(bool)
		return endpoint == textureEndpoint && ok
	case "strength":
		return endpoint == bangEndpoint && numberInRange(value, 1, 12, true)
	case "quality_override":
		return endpoint == bangEndpoint && numberInRange(value, 500, 2000000, true)
	case "seed":
		return numberInRange(value, 0, 65535, true)
	case "escore":
		return numberInRange(value, 0, 10, false)
	case "reference_scale":
		minimum := 0.0
		if endpoint == textureEndpoint {
			minimum = 0.5
		}
		return numberInRange(value, minimum, 1, false)
	default:
		return false
	}
}

func oneOf(value any, allowed ...string) bool {
	stringValue, ok := value.(string)
	if !ok {
		return false
	}
	for _, candidate := range allowed {
		if stringValue == candidate {
			return true
		}
	}
	return false
}

func numberInRange(value any, minimum, maximum float64, integer bool) bool {
	var number float64
	switch typed := value.(type) {
	case float64:
		number = typed
	case json.Number:
		parsed, err := typed.Float64()
		if err != nil {
			return false
		}
		number = parsed
	case int:
		number = float64(typed)
	default:
		return false
	}
	return number >= minimum && number <= maximum && (!integer || number == float64(int64(number)))
}

func isTemplateField(endpoint, field string) bool {
	switch endpoint {
	case bangEndpoint:
		switch field {
		case "geometry_file_format", "strength", "material", "resolution", "seed", "escore", "reference_scale", "mesh_mode", "quality_override":
			return true
		}
	case textureEndpoint:
		switch field {
		case "geometry_file_format", "escore", "reference_scale", "hd_texture", "texture_delight", "texture_mode", "seed", "material", "resolution":
			return true
		}
	}
	return false
}

func validNumericParameter(parameter generation.Parameter, integer bool, minimum, maximum float64) bool {
	if parameter.Type != "number" && parameter.Type != "integer" {
		return false
	}
	if integer && parameter.Type != "integer" && !parameter.Integer {
		return false
	}
	if parameter.Minimum == nil || parameter.Maximum == nil || *parameter.Minimum < minimum || *parameter.Maximum > maximum || *parameter.Minimum > *parameter.Maximum {
		return false
	}
	if len(parameter.Default) > 0 {
		var value float64
		if json.Unmarshal(parameter.Default, &value) != nil || value < minimum || value > maximum || integer && value != float64(int64(value)) {
			return false
		}
	}
	return true
}

func validSelectParameter(parameter generation.Parameter, allowed ...string) bool {
	if parameter.Type != "select" || len(parameter.Options) == 0 {
		return false
	}
	valid := func(value string) bool {
		for _, candidate := range allowed {
			if value == candidate {
				return true
			}
		}
		return false
	}
	for _, option := range parameter.Options {
		if !valid(option.Value) {
			return false
		}
	}
	if len(parameter.Default) > 0 {
		var value string
		if json.Unmarshal(parameter.Default, &value) != nil || !valid(value) {
			return false
		}
	}
	return true
}

func noTemplateCollisions(offering generation.Offering, options wireOptions) bool {
	var template map[string]any
	if json.Unmarshal(options.RequestTemplate, &template) != nil {
		return false
	}
	for _, parameter := range offering.Parameters {
		if _, exists := template[effectiveWireName(parameter)]; exists {
			return false
		}
	}
	return true
}

func hasSupportedModelMedia(values []string) bool {
	for _, value := range values {
		if _, _, ok := modelFileKind(value); ok {
			return true
		}
	}
	return false
}

func hasRodinCapability(values []generation.Capability) bool {
	if len(values) == 0 {
		return false
	}
	for _, value := range values {
		if value != generation.TextTo3D && value != generation.ImageTo3D {
			return false
		}
	}
	return true
}

func containsCapability(values []generation.Capability, required generation.Capability) bool {
	for _, value := range values {
		if value == required {
			return true
		}
	}
	return false
}

func allBangModelMedia(values []string) bool {
	if len(values) == 0 {
		return false
	}
	for _, value := range values {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "model/gltf-binary", "model/obj", "model/stl", "application/sla", "model/fbx", "application/vnd.autodesk.fbx", "model/vnd.usd", "model/vnd.usda", "model/vnd.usdz+zip", "model/vnd.usdc":
		default:
			return false
		}
	}
	return true
}

func allTextureModelMedia(values []string) bool {
	if len(values) == 0 {
		return false
	}
	for _, value := range values {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "model/gltf-binary", "model/gltf+json", "model/obj", "model/stl", "application/sla", "model/fbx", "application/vnd.autodesk.fbx", "model/vnd.usdz+zip", "model/usdz":
		default:
			return false
		}
	}
	return true
}

func allImageMedia(values []string) bool {
	if len(values) == 0 {
		return false
	}
	for _, value := range values {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "image/jpeg", "image/png", "image/webp", "image/tiff", "image/gif", "image/avif", "image/heic", "image/heif", "image/bmp", "image/jp2":
		default:
			return false
		}
	}
	return true
}

func validFormatParameter(parameter generation.Parameter) bool {
	if parameter.Type != "select" || len(parameter.Options) == 0 {
		return false
	}
	for _, option := range parameter.Options {
		if role, _, _ := fileKind("format." + strings.TrimPrefix(strings.TrimSpace(option.Value), ".")); role != "model" || !supportedOutputVariant(option.Value) {
			return false
		}
	}
	return true
}

func formatsMatchOutput(offering generation.Offering, parameter generation.Parameter) bool {
	for _, option := range parameter.Options {
		if !hasModelFormatOutput(offering, option.Value) {
			return false
		}
	}
	if len(parameter.Default) > 0 {
		var value string
		if json.Unmarshal(parameter.Default, &value) != nil || !hasModelFormatOutput(offering, value) {
			return false
		}
	}
	return true
}

func validTemplateOutputFormat(offering generation.Offering, options wireOptions) bool {
	var template map[string]any
	if json.Unmarshal(options.RequestTemplate, &template) != nil {
		return false
	}
	value, exists := template["geometry_file_format"]
	if !exists {
		return true
	}
	format, ok := value.(string)
	return ok && hasModelFormatOutput(offering, format)
}

func hasModelFormatOutput(offering generation.Offering, value string) bool {
	role, _, variant := fileKind("output." + strings.TrimPrefix(strings.TrimSpace(value), "."))
	if role != "model" || !supportedOutputVariant(value) {
		return false
	}
	_, ok := declaredOutputMedia(offering, "model", variant)
	return ok
}

func supportedOutputVariant(value string) bool {
	switch strings.ToLower(strings.TrimPrefix(strings.TrimSpace(value), ".")) {
	case "glb", "usdz", "fbx", "obj", "stl":
		return true
	default:
		return false
	}
}

func validateModelOutput(offering generation.Offering) error {
	for _, output := range offering.Outputs {
		if output.Role != "model" || output.Minimum != nil && *output.Minimum < 1 {
			continue
		}
		if len(output.MediaTypes) == 0 {
			continue
		}
		allSupported := true
		for _, mediaType := range output.MediaTypes {
			if !isSupportedOutputMedia(mediaType) {
				allSupported = false
				break
			}
		}
		if allSupported {
			return nil
		}
	}
	return fmt.Errorf("Hyper3D 处理 Offering 必须声明必需的受支持模型输出")
}

func isSupportedOutputMedia(mediaType string) bool {
	for _, supported := range []string{"model/gltf-binary", "model/vnd.usdz+zip", "model/usdz", "model/fbx", "application/vnd.autodesk.fbx", "model/obj", "model/stl", "application/sla"} {
		if strings.EqualFold(strings.TrimSpace(mediaType), supported) {
			return true
		}
	}
	return false
}

func (a *Adapter) startProcessing(ctx context.Context, request generation.StartRequest, options wireOptions) (generation.Observation, error) {
	input, err := decodeInput(request.Input)
	if err != nil {
		return generation.Observation{}, err
	}
	if options.Endpoint == bangEndpoint {
		var source *generation.Parameter
		for i := range request.Offering.Parameters {
			if effectiveWireName(request.Offering.Parameters[i]) == "asset_id" && request.Offering.Parameters[i].Type == "task" {
				source = &request.Offering.Parameters[i]
			}
		}
		modelSource := offeringAssetParameter(request.Offering, "model")
		dependency, hasDependency := generation.TaskDependency{}, false
		if source != nil {
			dependency, hasDependency = request.TaskDependencies[source.Name]
		}
		if hasDependency {
			sourceValue, ok := input[source.Name].(string)
			if !ok || strings.TrimSpace(sourceValue) == "" || generation.TaskID(sourceValue) != dependency.TaskID || len(request.TaskDependencies) != 1 || hasAnyInputAssets(request.InputAssets) ||
				dependency.ProviderID != request.Offering.ProviderID || dependency.UpstreamJob.ProviderID != request.Offering.ProviderID ||
				len(source.TaskOfferings) != 0 && !slices.Contains(source.TaskOfferings, dependency.OfferingID) ||
				strings.TrimSpace(dependency.UpstreamJob.RemoteID) == "" || !containsCapability(source.TaskCapabilities, dependency.Capability) {
				return generation.Observation{}, fail(generation.FailureInvalidInput, false, "Bang 仅接受已交付的 Hyper3D Rodin 原生任务")
			}
			input[source.Name] = dependency.UpstreamJob.RemoteID
			for _, parameter := range request.Offering.Parameters {
				wire := effectiveWireName(parameter)
				if wire == "model" || wire == "image" || wire == "prompt" {
					if value := input[parameter.Name]; !emptyInput(value) {
						return generation.Observation{}, fail(generation.FailureInvalidInput, false, "Bang 原生任务模式不得混入 model、image 或 prompt")
					}
					delete(input, parameter.Name)
				}
			}
			encoded, _ := json.Marshal(input)
			request.Input = encoded
		} else {
			foundModel := false
			if len(request.TaskDependencies) != 0 {
				return generation.Observation{}, fail(generation.FailureInvalidInput, false, "Bang 任务来源与本地模型来源不能混用")
			}
			for _, parameter := range request.Offering.Parameters {
				count := len(request.InputAssets[parameter.Name])
				switch effectiveWireName(parameter) {
				case "model":
					if count != 1 || input[parameter.Name] == nil || !validateInputAssets(request.OwnerID, parameter, request.InputAssets[parameter.Name], true) {
						return generation.Observation{}, fail(generation.FailureInvalidInput, false, "Bang 本地模型来源必须是单个已归属文件")
					}
					foundModel = true
				case "image":
					if count > 1 || count > 0 && !validateInputAssets(request.OwnerID, parameter, request.InputAssets[parameter.Name], false) {
						return generation.Observation{}, fail(generation.FailureInvalidInput, false, "Bang 只接受一个有效的 image 文件")
					}
					if count == 0 {
						delete(input, parameter.Name)
					}
				case "asset_id":
					if count > 0 || input[parameter.Name] != nil {
						return generation.Observation{}, fail(generation.FailureInvalidInput, false, "Bang 本地模型模式不接受原生任务 ID")
					}
				}
			}
			if !foundModel || modelSource == nil {
				return generation.Observation{}, fail(generation.FailureInvalidInput, false, "Bang 本地模型模式必须上传一个本地模型")
			}
			imageName := offeringAssetParameterName(request.Offering, "image")
			imageCount := len(request.InputAssets[imageName])
			if imageCount == 0 && !emptyInput(input[imageName]) {
				return generation.Observation{}, fail(generation.FailureInvalidInput, false, "Bang image 引用没有对应的本地文件")
			}
			if imageCount == 0 && bangImageRequired(request.Offering, input, options.RequestTemplate) || imageCount > 0 && imageCount != 1 {
				return generation.Observation{}, fail(generation.FailureInvalidInput, false, "Bang custom model request requires exactly one reference image")
			}
		}
	} else {
		if len(request.TaskDependencies) != 0 {
			return generation.Observation{}, fail(generation.FailureInvalidInput, false, "Texture 不接受任务或远程资产来源")
		}
		for _, parameter := range request.Offering.Parameters {
			count := len(request.InputAssets[parameter.Name])
			switch effectiveWireName(parameter) {
			case "model", "image":
				if count != 1 || input[parameter.Name] == nil || !validateInputAssets(request.OwnerID, parameter, request.InputAssets[parameter.Name], effectiveWireName(parameter) == "model") {
					return generation.Observation{}, fail(generation.FailureInvalidInput, false, "Texture 必须上传一个本地模型和一张本地参考图")
				}
			case "asset_id":
				if count != 0 {
					return generation.Observation{}, fail(generation.FailureInvalidInput, false, "Texture 不接受额外资产来源")
				}
			}
		}
	}
	if hasAnyInputAssets(request.InputAssets) && a.inputs == nil {
		return generation.Observation{}, fail(generation.FailureInvalidInput, false, "本地文件输入不可用")
	}
	body, contentType, err := imagewire.EncodeMultipart(ctx, request, a.inputs)
	if err != nil {
		return generation.Observation{}, multipartInputFailure("Hyper3D 本地文件输入无效", err)
	}
	stage := "bang.create"
	if options.Endpoint == textureEndpoint {
		stage = "texture.create"
	}
	return a.submitMultipart(ctx, request, options, body, contentType, stage)
}

func offeringAssetParameter(offering generation.Offering, wire string) *generation.Parameter {
	for i := range offering.Parameters {
		parameter := &offering.Parameters[i]
		if effectiveWireName(*parameter) == wire && parameter.Type == "asset" {
			return parameter
		}
	}
	return nil
}

func offeringAssetParameterName(offering generation.Offering, wire string) string {
	if parameter := offeringAssetParameter(offering, wire); parameter != nil {
		return parameter.Name
	}
	return ""
}

func bangImageRequired(offering generation.Offering, input map[string]any, rawTemplate json.RawMessage) bool {
	material := resolvedParameterValue(offering, input, rawTemplate, "material")
	instruction := resolvedParameterValue(offering, input, rawTemplate, "instruction")
	materialValue, ok := material.(string)
	materialDisabled := ok && materialValue == "None"
	return !materialDisabled || !emptyInput(instruction)
}

func resolvedParameterValue(offering generation.Offering, input map[string]any, rawTemplate json.RawMessage, wire string) any {
	for _, parameter := range offering.Parameters {
		if effectiveWireName(parameter) != wire {
			continue
		}
		if value, exists := input[parameter.Name]; exists {
			return value
		}
		if len(parameter.Default) > 0 {
			var value any
			if json.Unmarshal(parameter.Default, &value) == nil {
				return value
			}
		}
	}
	var template map[string]any
	if json.Unmarshal(rawTemplate, &template) == nil {
		return template[wire]
	}
	return nil
}

func emptyInput(value any) bool {
	switch typed := value.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(typed) == ""
	case []any:
		return len(typed) == 0
	case map[string]any:
		return len(typed) == 0
	default:
		return false
	}
}

func hasAnyInputAssets(assets map[string][]generation.InputAsset) bool {
	for _, values := range assets {
		if len(values) > 0 {
			return true
		}
	}
	return false
}

func validateInputAssets(owner generation.OwnerID, parameter generation.Parameter, assets []generation.InputAsset, model bool) bool {
	if strings.TrimSpace(string(owner)) == "" || parameter.MaxBytes <= 0 || len(assets) == 0 {
		return false
	}
	for _, asset := range assets {
		if asset.OwnerID != owner || asset.Size > parameter.MaxBytes {
			return false
		}
		mediaType := strings.ToLower(strings.TrimSpace(asset.MediaType))
		allowed := false
		for _, candidate := range parameter.MediaTypes {
			if strings.EqualFold(strings.TrimSpace(candidate), mediaType) {
				allowed = true
				break
			}
		}
		if !allowed || model && !hasSupportedModelMedia([]string{mediaType}) || !model && !strings.HasPrefix(mediaType, "image/") {
			return false
		}
	}
	return true
}

func decodeInput(raw json.RawMessage) (map[string]any, error) {
	var input map[string]any
	if err := json.Unmarshal(raw, &input); err != nil || input == nil {
		return nil, fail(generation.FailureInvalidInput, false, "输入不是 JSON 对象")
	}
	return input, nil
}
