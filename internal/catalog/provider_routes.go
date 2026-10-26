package catalog

import (
	"encoding/json"
	"fmt"
	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
	"net/url"
	"path"
	"strings"
)

// validateProviderRoutes 按连接声明的 protocol 对发布路由做通用安全校验。
// 相对路径/占位符/host 规则与历史行为一致；adapter_key 未映射到连接的
// Offering 不做发布路由校验（与既有口径一致）。协议 → 校验形状：
//   - image-sync：endpoint 必须是安全相对路径，capability 属图像域；
//   - tripo：observe_endpoint 恰含一个 {task_id}，capability 必须在
//     连接声明的能力集合内；文生/图生 3D 生成形状另需 model 与
//     （图生）upload_endpoint。
func validateProviderRoutes(
	offering schemacontract.Offering,
	protocols map[string]string,
	connectionCapabilities map[string][]schemacontract.Capability,
) error {
	protocol, mapped := protocols[offering.AdapterKey]
	if !mapped {
		return nil
	}
	var options struct {
		Protocol         string `json:"protocol"`
		Endpoint         string `json:"endpoint"`
		ObserveEndpoint  string `json:"observe_endpoint"`
		DownloadEndpoint string `json:"download_endpoint"`
		UploadEndpoint   string `json:"upload_endpoint"`
		Model            string `json:"model"`
	}
	if err := json.Unmarshal(offering.ProviderOptions, &options); err != nil {
		return fmt.Errorf("Offering %q provider_options 无效: %w", offering.ID, err)
	}
	routes := map[string]string{"endpoint": options.Endpoint}
	declared := connectionCapabilities[offering.AdapterKey]
	switch protocol {
	case "image-sync":
		if !capabilityDeclared(declared, offering.Capability) {
			return fmt.Errorf(
				"Offering %q capability 与 image-sync 协议连接不匹配",
				offering.ID,
			)
		}
	case "tripo":
		if !capabilityDeclared(declared, offering.Capability) {
			return fmt.Errorf(
				"Offering %q capability 与 tripo 协议连接不匹配",
				offering.ID,
			)
		}
		if options.Protocol != "" && options.Protocol != "smartsegment" {
			return fmt.Errorf("Offering %q provider_options.protocol 未知", offering.ID)
		}
		if options.Protocol == "smartsegment" && options.Endpoint != "mesh/smartsegment" {
			return fmt.Errorf("Offering %q Smart Segmentation 端点无效", offering.ID)
		}
		// model 是 tripo 生成形状（文生/图生 3D）的协议要求；转换等
		// 非生成形状不消费 model，不做该要求（镜像适配器实际消费面）。
		if (offering.Capability == schemacontract.TextTo3D ||
			offering.Capability == schemacontract.ImageTo3D) && options.Protocol != "smartsegment" {
			if strings.TrimSpace(options.Model) == "" {
				return fmt.Errorf("Offering %q 缺少 provider_options.model", offering.ID)
			}
		}
		if strings.Count(options.ObserveEndpoint, "{task_id}") != 1 {
			return fmt.Errorf("Offering %q provider_options.observe_endpoint 必须包含一个 {task_id}", offering.ID)
		}
		routes["observe_endpoint"] = strings.ReplaceAll(options.ObserveEndpoint, "{task_id}", "task-id")
		if offering.Capability == schemacontract.ImageTo3D || options.UploadEndpoint != "" {
			routes["upload_endpoint"] = options.UploadEndpoint
		}
	case "hyper3d.rodin":
		if !capabilityDeclared(declared, offering.Capability) {
			return fmt.Errorf("Offering %q capability 与 hyper3d.rodin 协议连接不匹配", offering.ID)
		}
		routes["observe_endpoint"] = options.ObserveEndpoint
		routes["download_endpoint"] = options.DownloadEndpoint
	default:
		return nil
	}
	for name, value := range routes {
		parsed, err := url.Parse(value)
		if err != nil || parsed.IsAbs() || parsed.Host != "" || parsed.RawQuery != "" || parsed.Fragment != "" ||
			value == "" || strings.TrimSpace(value) != value || strings.HasPrefix(parsed.Path, "/") ||
			strings.ContainsAny(value, "\\{}") || path.Clean(parsed.Path) != parsed.Path ||
			strings.HasPrefix(parsed.Path, "../") || parsed.Path == "." || parsed.Path == ".." {
			return fmt.Errorf("Offering %q provider_options.%s 必须为安全的相对路径", offering.ID, name)
		}
	}
	return nil
}

// capabilityDeclared 判断能力是否被连接声明（含条件能力；路由校验只关
// 心声明集合，不重复计算条件可用性——那由连接注册路径负责）。
func capabilityDeclared(
	declared []schemacontract.Capability,
	capability schemacontract.Capability,
) bool {
	for _, candidate := range declared {
		if candidate == capability {
			return true
		}
	}
	return false
}
