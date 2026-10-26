package mock

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// claimPlan 描述一个要交付的产物：它是谁（角色、变体、序号）、什么格式、
// 什么媒体类型。内容在 OpenAsset 时按计划现画，所以 Observe 很轻。
type claimPlan struct {
	key       string
	role      string
	variant   string
	format    string // png、glb、gltf、obj、mtl、stl、fbx、usdz、3mf、blend
	index     int
	total     int
	mediaType string
	// attachment marks a file the result carries besides its required
	// outputs (a Meshy Rigging result's basic animations, #326).
	attachment bool
}

func newClaimPlan(role, variant string, index, total int, input map[string]any) claimPlan {
	plan := claimPlan{role: role, variant: variant, index: index, total: total}
	switch role {
	case schema.GaussianSplatRole:
		plan.format = schema.GaussianSplatVariant
	case "image", "thumbnail", "texture":
		plan.format = "png"
	default:
		plan.format = modelFormat(variant, input)
	}
	plan.mediaType = formatMediaType(plan.format)
	plan.key = fmt.Sprintf("%s-%s-%d", role, sanitizeKey(variant), index)
	return plan
}

func sanitizeKey(variant string) string {
	if variant == "" {
		return "_"
	}
	return strings.Map(func(r rune) rune {
		if r == '/' {
			return '_'
		}
		return r
	}, variant)
}

// modelFormat 从变体名推断格式。纯 "converted"（格式转换）看输入里的 format。
func modelFormat(variant string, input map[string]any) string {
	name := strings.ToLower(variant)
	if name == "" || name == "converted" {
		if format, ok := input["format"].(string); ok && strings.TrimSpace(format) != "" {
			name = strings.ToLower(strings.TrimSpace(format))
		}
	}
	for _, format := range []string{"usdz", "3mf", "gltf", "glb", "fbx", "obj", "mtl", "stl", "blend"} {
		if strings.Contains(name, format) {
			return format
		}
	}
	return "glb"
}

func formatMediaType(format string) string {
	switch format {
	case schema.GaussianSplatVariant:
		return schema.GaussianSplatMediaType
	case "png":
		return "image/png"
	case "glb":
		return "model/gltf-binary"
	case "gltf":
		return "model/gltf+json"
	case "obj", "mtl":
		return "text/plain"
	default:
		return "application/octet-stream"
	}
}

// planClaims 把 Offering 的输出声明展开成要交付的产物：
//   - 阻断成功的声明（没写 delivery，或 required）都交付，逐项变体各一份；
//   - 只有角色的图片声明按输入里的张数 n 交付 n 张；
//   - 前缀声明交付一个有代表性的变体；
//   - required 且 minimum 为 0 的 mtl 与贴图顺带交付，OBJ 才带得上材质；
//   - 冻结后最低数为 0 的条件必需声明（required_when 未满足）不交付；
//   - background 的缩略图与主产物一起交付；其余 background 与 on-demand 声明
//     不在这里交付（成功后观察仍可交付它们）。
func planClaims(offering generation.Offering, input map[string]any) []claimPlan {
	var plans []claimPlan
	seen := map[string]bool{}
	add := func(plan claimPlan) {
		// Strict delivery profiles declare their exact wire identity. Preserve
		// legacy media types for offerings without a profile.
		for _, output := range offering.Outputs {
			if output.Role != plan.role || (output.Variant != "" && output.Variant != plan.variant) ||
				(output.VariantPrefix != "" && !strings.HasPrefix(plan.variant, output.VariantPrefix)) {
				continue
			}
			if format, mime, known := schema.DeliveryProfileFormat(output.DeliveryProfile); known && format == plan.format {
				plan.mediaType = mime
			}
		}
		if !seen[plan.key] {
			seen[plan.key] = true
			plans = append(plans, plan)
		}
	}
	for _, output := range offering.Outputs {
		// 缩略图是 background 声明，但上游（Meshy）与主产物一起给出，mock 照做，
		// 版本条和结果卡才有真实的模型预览。其余 background/on-demand 声明不交付。
		if !blocksSuccess(output.Delivery) {
			if output.Delivery == generation.AssetDeliveryBackground && output.Role == "thumbnail" && output.Variant != "" {
				add(newClaimPlan(output.Role, output.Variant, 0, 1, input))
			}
			continue
		}
		// 条件必需的槽位（如四视角编辑里没点名的视角）在冻结 Offering 中最低数为 0：
		// 本次请求不要它，mock 像上游一样不交付。
		if output.RequiredWhen != nil && output.Minimum != nil && *output.Minimum == 0 {
			continue
		}
		switch {
		case output.VariantFrom != "":
			values, _ := input[output.VariantFrom].([]any)
			for _, value := range values {
				text, _ := value.(string)
				if variant := generation.SelectionVariant(output, text); variant != "" {
					add(newClaimPlan(output.Role, variant, 0, 1, input))
				}
			}
		case output.Variant != "":
			add(newClaimPlan(output.Role, output.Variant, 0, 1, input))
		case output.VariantPrefix != "":
			for _, variant := range prefixVariants(output.VariantPrefix, output.Minimum) {
				add(newClaimPlan(output.Role, variant, 0, 1, input))
			}
			if offering.Capability == generation.Rigging && output.VariantPrefix == meshyBasicAnimations {
				for _, variant := range meshyRiggingAttachments {
					plan := newClaimPlan(output.Role, output.VariantPrefix+variant, 0, 1, input)
					plan.attachment = true
					add(plan)
				}
			}
		default:
			count := 1
			if output.Role == "image" {
				count = imageCount(input)
			}
			if output.Minimum != nil {
				count = max(count, *output.Minimum)
			}
			for index := 0; index < count; index++ {
				add(newClaimPlan(output.Role, "", index, count, input))
			}
		}
	}
	// 内核按产物 ID 排序后给资产编号，前端把序号最小的模型文件当作主文件交给
	// 查看器。这里让可直接查看的 GLB 排在最前，其余保持声明顺序。
	// A Meshy Rigging result lists its basic-animation attachments before the
	// rigged character (dev-test task_8dd90530, #326): attachments go first.
	sort.SliceStable(plans, func(i, j int) bool {
		if plans[i].attachment != plans[j].attachment {
			return plans[i].attachment
		}
		return viewRank(plans[i]) < viewRank(plans[j])
	})
	return plans
}

// meshyBasicAnimations is the variant prefix of the walking and running
// previews Meshy Rigging returns with every rigged character. The mock
// delivers the six files the real result has (#326), so the stage, the 加工
// panel and the Agent can be checked against attachments listed first.
const meshyBasicAnimations = "basic_animations."

var meshyRiggingAttachments = []string{
	"running_armature_glb_url", "running_fbx_url", "running_glb_url",
	"walking_armature_glb_url", "walking_fbx_url", "walking_glb_url",
}

func viewRank(plan claimPlan) int {
	switch {
	case plan.role == "model" && plan.format == "glb":
		return 0
	case plan.role == "model" && plan.format == "gltf":
		return 1
	}
	return 2
}

// prefixVariants：必需的前缀槽位给出代表性变体；minimum 为 0 的前缀槽位只有
// 贴图值得交付（模型的 basic_animations. 之类跳过）。
func prefixVariants(prefix string, minimum *int) []string {
	switch {
	case prefix == "texture.":
		return []string{"texture.base_color"}
	case minimum != nil && *minimum == 0:
		return nil
	case strings.HasSuffix(prefix, "_"):
		return []string{prefix + "glb_url", prefix + "fbx_url"}
	default:
		return []string{prefix + "glb_url"}
	}
}

func imageCount(input map[string]any) int {
	for _, name := range []string{"n", "count", "num_images"} {
		switch value := input[name].(type) {
		case float64:
			if value >= 1 && value <= 16 && value == math.Trunc(value) {
				return int(value)
			}
		case string:
			if parsed, err := strconv.Atoi(value); err == nil && parsed >= 1 && parsed <= 16 {
				return parsed
			}
		}
	}
	return 1
}
