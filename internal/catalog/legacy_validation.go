package catalog

// Legacy API projection and migration compatibility contracts.
import (
	"encoding/json"
	"errors"
	"fmt"
	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
	"path/filepath"
	"strings"
)

func validateLegacyConstraintOverrides(
	capability schemacontract.Capability,
	overrides ProviderDiscoveryLegacyConstraintOverrideSchema,
) error {
	if overrides.PromptMaxLength != nil && *overrides.PromptMaxLength <= 0 ||
		overrides.NegativePromptMaxLength != nil &&
			*overrides.NegativePromptMaxLength <= 0 ||
		overrides.ReferenceImageMin != nil && *overrides.ReferenceImageMin < 0 ||
		overrides.ReferenceImageMax != nil && *overrides.ReferenceImageMax <= 0 ||
		overrides.ReferenceImageMaxSize != nil &&
			*overrides.ReferenceImageMaxSize <= 0 ||
		overrides.PolycountMin != nil && *overrides.PolycountMin < 0 ||
		overrides.PolycountMax != nil && *overrides.PolycountMax <= 0 {
		return fmt.Errorf(
			"constraint_projections.%s.overrides 含无效数值",
			capability,
		)
	}
	return nil
}

func validateLegacyMigrationRuntime(
	schema LegacyMigrationRuntimeSchema,
) error {
	backfillCache := strings.TrimSpace(
		schema.ThumbnailBackfill.SourceCacheSubdirectory,
	)
	if backfillCache == "" ||
		backfillCache == "." ||
		backfillCache == ".." ||
		filepath.IsAbs(backfillCache) ||
		strings.ContainsAny(backfillCache, `/\`) {
		return fmt.Errorf(
			"thumbnail_backfill.source_cache_subdirectory 无效: %q",
			backfillCache,
		)
	}
	if schema.ThumbnailBackfill.BatchSize <= 0 ||
		schema.ThumbnailBackfill.BatchSize > 100 {
		return fmt.Errorf(
			"thumbnail_backfill.batch_size 必须在 1..100: %d",
			schema.ThumbnailBackfill.BatchSize,
		)
	}
	if schema.ThumbnailBackfill.MaximumAssetBytes <= 0 {
		return fmt.Errorf(
			"thumbnail_backfill.maximum_asset_bytes 必须大于 0: %d",
			schema.ThumbnailBackfill.MaximumAssetBytes,
		)
	}
	seenOfferingMappings := make(
		map[string]struct{},
		len(schema.ThumbnailBackfill.OfferingMappings),
	)
	for index, mapping := range schema.ThumbnailBackfill.OfferingMappings {
		providerID := strings.TrimSpace(mapping.LegacyProviderID)
		offeringID := strings.TrimSpace(string(mapping.OfferingID))
		if providerID == "" ||
			!mapping.Capability.Valid() ||
			offeringID == "" {
			return fmt.Errorf(
				"thumbnail_backfill.offering_mappings[%d] 配置不完整",
				index,
			)
		}
		key := providerID + "\x00" + string(mapping.Capability)
		if _, exists := seenOfferingMappings[key]; exists {
			return fmt.Errorf(
				"thumbnail_backfill.offering_mappings[%d] 重复声明 %q/%q",
				index,
				providerID,
				mapping.Capability,
			)
		}
		seenOfferingMappings[key] = struct{}{}
	}
	seenSourceLosses := make(
		map[string]struct{},
		len(schema.ThumbnailBackfill.AcknowledgedSourceLosses),
	)
	for index, sourceLoss := range schema.ThumbnailBackfill.
		AcknowledgedSourceLosses {
		taskID := strings.TrimSpace(sourceLoss.TaskID)
		reason := strings.TrimSpace(sourceLoss.Reason)
		if taskID == "" || reason == "" {
			return fmt.Errorf(
				"thumbnail_backfill.acknowledged_source_losses[%d] 配置不完整",
				index,
			)
		}
		if _, exists := seenSourceLosses[taskID]; exists {
			return fmt.Errorf(
				"thumbnail_backfill.acknowledged_source_losses[%d] 重复声明 %q",
				index,
				taskID,
			)
		}
		seenSourceLosses[taskID] = struct{}{}
	}
	if len(schema.TaskOutputExcludedRoles) == 0 {
		return errors.New("task_output_excluded_roles 不能为空")
	}
	seenRoles := make(
		map[string]struct{},
		len(schema.TaskOutputExcludedRoles),
	)
	for index, role := range schema.TaskOutputExcludedRoles {
		role = strings.TrimSpace(role)
		if role == "" {
			return fmt.Errorf(
				"task_output_excluded_roles[%d] 不能为空",
				index,
			)
		}
		if _, exists := seenRoles[role]; exists {
			return fmt.Errorf(
				"task_output_excluded_roles[%d] 重复声明 %q",
				index,
				role,
			)
		}
		seenRoles[role] = struct{}{}
	}
	auditRoot := strings.TrimSpace(
		schema.AuditAssetRecovery.RootSubdirectory,
	)
	if auditRoot == "" ||
		auditRoot == "." ||
		auditRoot == ".." ||
		filepath.IsAbs(auditRoot) ||
		strings.ContainsAny(auditRoot, `/\`) {
		return fmt.Errorf(
			"audit_asset_recovery.root_subdirectory 无效: %q",
			auditRoot,
		)
	}
	template := strings.TrimSpace(
		schema.AuditAssetRecovery.FilenameTemplate,
	)
	if template == "" ||
		filepath.IsAbs(template) ||
		strings.ContainsAny(template, `/\`) {
		return fmt.Errorf(
			"audit_asset_recovery.filename_template 无效: %q",
			template,
		)
	}
	for _, placeholder := range []string{
		"{task_id}",
		"{source_stem}",
		"{source_extension}",
	} {
		if strings.Count(template, placeholder) != 1 {
			return fmt.Errorf(
				"audit_asset_recovery.filename_template 必须且只能包含一次 %s",
				placeholder,
			)
		}
		template = strings.Replace(template, placeholder, "", 1)
	}
	if strings.ContainsAny(template, "{}") {
		return errors.New(
			"audit_asset_recovery.filename_template 含未知占位符",
		)
	}

	seen := make(map[string]struct{}, len(schema.CacheOnlyTaskExclusions))
	for index, exclusion := range schema.CacheOnlyTaskExclusions {
		taskID := strings.TrimSpace(exclusion.TaskID)
		externalType := strings.TrimSpace(exclusion.ExternalType)
		reason := strings.TrimSpace(exclusion.Reason)
		if taskID == "" || externalType == "" || reason == "" {
			return fmt.Errorf(
				"cache_only_task_exclusions[%d] 的 task_id、external_type、reason 不能为空",
				index,
			)
		}
		key := taskID + "\x00" + externalType
		if _, exists := seen[key]; exists {
			return fmt.Errorf(
				"cache_only_task_exclusions[%d] 重复声明 %q/%q",
				index,
				taskID,
				externalType,
			)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func validateLegacyThumbnailOfferingMappings(
	mappings []LegacyThumbnailOfferingMappingSchema,
	offerings generationMap,
) error {
	for index, mapping := range mappings {
		offering, exists := offerings[mapping.OfferingID]
		if !exists {
			return fmt.Errorf(
				"offering_mappings[%d] 引用了未知 Offering %q",
				index,
				mapping.OfferingID,
			)
		}
		legacyProvider := strings.ToLower(strings.TrimSpace(mapping.LegacyProviderID))
		provider := strings.ToLower(offering.ProviderID)
		providerMatches := legacyProvider == provider ||
			strings.HasPrefix(legacyProvider, provider+"-") ||
			strings.HasPrefix(legacyProvider, provider+"/")
		// Historical migration may refer to a disabled Offering. Its stable ID
		// still records the original provider and capability, unlike a current
		// submission route which must be enabled.
		if offering.Capability != mapping.Capability ||
			!providerMatches ||
			!strings.HasPrefix(string(offering.ID), offering.ProviderID+"/") {
			return fmt.Errorf(
				"offering_mappings[%d] 的 Offering %q 与来源 Provider 或能力 %q 不匹配",
				index,
				mapping.OfferingID,
				mapping.Capability,
			)
		}
	}
	return nil
}

func validateLegacyTaskAPI(schema LegacyTaskAPISchema) error {
	if !strings.HasPrefix(schema.Path, "/api/") ||
		strings.ContainsAny(schema.Path, "{}:*") {
		return fmt.Errorf("path 必须是无参数的 /api/ 路径，当前为 %q", schema.Path)
	}
	if strings.TrimSpace(schema.ModelParameter) == "" {
		return errors.New("model_parameter 不能为空")
	}
	if schema.OutputKind != "image" && schema.OutputKind != "model" {
		return fmt.Errorf("output_kind 仅支持 image 或 model，当前为 %q", schema.OutputKind)
	}
	if len(schema.OutputProjections) == 0 {
		return errors.New("output_projections 不能为空")
	}
	outputFields := make(map[string]struct{}, len(schema.OutputProjections))
	for index, projection := range schema.OutputProjections {
		if strings.TrimSpace(projection.Name) == "" ||
			strings.TrimSpace(projection.Role) == "" {
			return fmt.Errorf(
				"output_projections[%d] 的 name 和 role 不能为空",
				index,
			)
		}
		switch projection.Mode {
		case "array", "map", "scalar", "array-map", "records":
		default:
			return fmt.Errorf(
				"output_projections[%d].mode 仅支持 array、map、scalar、array-map 或 records，当前为 %q",
				index,
				projection.Mode,
			)
		}
		if projection.DefaultKey != "" && projection.Mode != "map" {
			return fmt.Errorf(
				"output_projections[%d].default_key 仅允许用于 map",
				index,
			)
		}
		if projection.Mode == "records" &&
			len(projection.RecordFields) == 0 {
			return fmt.Errorf(
				"output_projections[%d].record_fields 对 records 必填",
				index,
			)
		}
		if projection.Mode == "records" &&
			!knownLegacyRecordCollectionSource(projection.RecordSource) {
			return fmt.Errorf(
				"output_projections[%d].record_source 非法: %q",
				index,
				projection.RecordSource,
			)
		}
		if projection.Mode != "records" &&
			len(projection.RecordFields) > 0 {
			return fmt.Errorf(
				"output_projections[%d].record_fields 只允许用于 records",
				index,
			)
		}
		if projection.Mode != "records" &&
			projection.RecordSource != "" {
			return fmt.Errorf(
				"output_projections[%d].record_source 只允许用于 records",
				index,
			)
		}
		recordFields := make(
			map[string]struct{},
			len(projection.RecordFields),
		)
		recordSemantics := make(
			map[string]struct{},
			len(projection.RecordFields),
		)
		for fieldIndex, field := range projection.RecordFields {
			if strings.TrimSpace(field.Name) == "" {
				return fmt.Errorf(
					"output_projections[%d].record_fields[%d].name 不能为空",
					index,
					fieldIndex,
				)
			}
			hasSource := strings.TrimSpace(field.Source) != ""
			hasLiteral := len(field.Literal) > 0
			if hasSource == hasLiteral {
				return fmt.Errorf(
					"output_projections[%d].record_fields[%d] 必须且只能声明 source 或 literal",
					index,
					fieldIndex,
				)
			}
			if hasSource && !knownLegacyRecordSource(field.Source) {
				return fmt.Errorf(
					"output_projections[%d].record_fields[%d].source 未知: %q",
					index,
					fieldIndex,
					field.Source,
				)
			}
			if hasSource &&
				!legacyRecordSourceMatchesCollection(
					projection.RecordSource,
					field.Source,
				) {
				return fmt.Errorf(
					"output_projections[%d].record_fields[%d].source %q 不属于 record_source %q",
					index,
					fieldIndex,
					field.Source,
					projection.RecordSource,
				)
			}
			if !knownLegacyRecordSemantic(field.Semantic) {
				return fmt.Errorf(
					"output_projections[%d].record_fields[%d].semantic 未知: %q",
					index,
					fieldIndex,
					field.Semantic,
				)
			}
			if hasLiteral && !json.Valid(field.Literal) {
				return fmt.Errorf(
					"output_projections[%d].record_fields[%d].literal 不是合法 JSON",
					index,
					fieldIndex,
				)
			}
			if _, exists := recordFields[field.Name]; exists {
				return fmt.Errorf(
					"output_projections[%d] record field 重复: %q",
					index,
					field.Name,
				)
			}
			recordFields[field.Name] = struct{}{}
			if _, exists := recordSemantics[field.Semantic]; exists {
				return fmt.Errorf(
					"output_projections[%d] record semantic 重复: %q",
					index,
					field.Semantic,
				)
			}
			recordSemantics[field.Semantic] = struct{}{}
		}
		if _, exists := outputFields[projection.Name]; exists {
			return fmt.Errorf("output projection 字段重复: %q", projection.Name)
		}
		outputFields[projection.Name] = struct{}{}
	}
	fields := map[string]struct{}{}
	for index, field := range schema.Fields {
		if strings.TrimSpace(field.Name) == "" {
			return fmt.Errorf("fields[%d].name 不能为空", index)
		}
		if field.TargetParameter == field.Name {
			return fmt.Errorf(
				"fields[%d].target_parameter 不能与 name 相同",
				index,
			)
		}
		if field.Forwarding != "always" && field.Forwarding != "if-supported" {
			return fmt.Errorf(
				"fields[%d].forwarding 仅支持 always 或 if-supported，当前为 %q",
				index,
				field.Forwarding,
			)
		}
		if _, exists := fields[field.Name]; exists {
			return fmt.Errorf("field 重复: %q", field.Name)
		}
		fields[field.Name] = struct{}{}
	}
	aliases := map[string]struct{}{}
	for index, alias := range schema.AssetAliases {
		if strings.TrimSpace(alias.Name) == "" {
			return fmt.Errorf("asset_aliases[%d].name 不能为空", index)
		}
		if !knownParameterSemantic(alias.Semantic) {
			return fmt.Errorf(
				"asset_aliases[%d].semantic 无效: %q",
				index,
				alias.Semantic,
			)
		}
		if alias.Cardinality != "one" && alias.Cardinality != "many" {
			return fmt.Errorf(
				"asset_aliases[%d].cardinality 仅支持 one 或 many，当前为 %q",
				index,
				alias.Cardinality,
			)
		}
		if alias.RestoreMinimumItems < 0 {
			return fmt.Errorf("asset_aliases[%d].restore_minimum_items 不能为负数", index)
		}
		if _, exists := aliases[alias.Name]; exists {
			return fmt.Errorf("asset alias 重复: %q", alias.Name)
		}
		aliases[alias.Name] = struct{}{}
	}
	for index, alias := range schema.TaskAliases {
		if strings.TrimSpace(alias.Name) == "" ||
			strings.TrimSpace(alias.TargetParameter) == "" ||
			alias.Name == alias.TargetParameter {
			return fmt.Errorf(
				"task_aliases[%d] 的 name 或 target_parameter 无效",
				index,
			)
		}
		if _, exists := aliases[alias.Name]; exists {
			return fmt.Errorf("legacy alias 重复: %q", alias.Name)
		}
		if _, exists := fields[alias.Name]; exists {
			return fmt.Errorf("task alias 与 field 重复: %q", alias.Name)
		}
		if alias.Name == schema.ModelParameter {
			return fmt.Errorf("task alias 与 model_parameter 重复: %q", alias.Name)
		}
		aliases[alias.Name] = struct{}{}
	}
	if schema.Multipart != nil {
		multipart := schema.Multipart
		if strings.TrimSpace(multipart.FileField) == "" ||
			strings.TrimSpace(multipart.ParamsField) == "" ||
			strings.TrimSpace(multipart.TargetParameter) == "" {
			return errors.New(
				"multipart 的 file_field、params_field 与 target_parameter 不能为空",
			)
		}
		if multipart.FileField == multipart.ParamsField {
			return errors.New("multipart 的 file_field 与 params_field 不能相同")
		}
		if multipart.FileField == schema.ModelParameter ||
			multipart.ParamsField == schema.ModelParameter {
			return errors.New("multipart 字段不能与 model_parameter 相同")
		}
		if multipart.Cardinality != "one" &&
			multipart.Cardinality != "many" {
			return fmt.Errorf(
				"multipart.cardinality 必须是 one 或 many，当前为 %q",
				multipart.Cardinality,
			)
		}
		if multipart.Cardinality == "many" &&
			strings.TrimSpace(multipart.InvalidCountMessageKey) == "" {
			return errors.New(
				"multipart.invalid_count_message_key 对 many 必填",
			)
		}
	}
	if schema.AssetDownload != nil {
		download := schema.AssetDownload
		placeholder := "{" + download.TokenParameter + "}"
		if !strings.HasPrefix(download.Path, "/api/") ||
			strings.TrimSpace(download.TokenParameter) == "" ||
			!strings.Contains(download.Path, placeholder) ||
			strings.TrimSpace(download.FilenameTemplate) == "" ||
			strings.TrimSpace(download.MediaType) == "" ||
			strings.TrimSpace(download.CacheSubdirectory) == "" ||
			strings.TrimSpace(download.CacheFilenameTemplate) == "" ||
			!strings.Contains(
				download.CacheFilenameTemplate,
				placeholder,
			) {
			return errors.New(
				"asset_download 必须声明路由、token、文件名、media_type 和缓存位置",
			)
		}
		if (download.CacheHeader == "") !=
			(download.CacheValue == "") {
			return errors.New(
				"asset_download.cache_header 与 cache_value 必须同时声明",
			)
		}
	}
	resultProjectionCount := len(schema.ResultAssets) +
		len(schema.ResultCollections)
	if (strings.TrimSpace(schema.ResultField) == "") !=
		(resultProjectionCount == 0) {
		return errors.New(
			"result_field 与 result_assets/result_collections 必须同时配置",
		)
	}
	resultFields := map[string]struct{}{}
	for index, asset := range schema.ResultAssets {
		if strings.TrimSpace(asset.Name) == "" ||
			strings.TrimSpace(asset.Role) == "" {
			return fmt.Errorf(
				"result_assets[%d] 的 name 或 role 不能为空",
				index,
			)
		}
		if strings.TrimSpace(asset.Variant) == "" &&
			len(asset.MediaTypes) == 0 {
			return fmt.Errorf(
				"result_assets[%d] 必须配置 variant 或 media_types",
				index,
			)
		}
		if _, exists := resultFields[asset.Name]; exists {
			return fmt.Errorf("result asset 字段重复: %q", asset.Name)
		}
		resultFields[asset.Name] = struct{}{}
		for mediaIndex, mediaType := range asset.MediaTypes {
			if strings.TrimSpace(mediaType) == "" {
				return fmt.Errorf(
					"result_assets[%d].media_types[%d] 不能为空",
					index,
					mediaIndex,
				)
			}
		}
	}
	for index, collection := range schema.ResultCollections {
		if strings.TrimSpace(collection.Name) == "" ||
			strings.TrimSpace(collection.Role) == "" ||
			strings.TrimSpace(collection.VariantPrefix) == "" {
			return fmt.Errorf(
				"result_collections[%d] 的 name、role 或 variant_prefix 不能为空",
				index,
			)
		}
		if _, exists := resultFields[collection.Name]; exists {
			return fmt.Errorf(
				"result collection 字段重复: %q",
				collection.Name,
			)
		}
		resultFields[collection.Name] = struct{}{}
	}
	return nil
}

func validateLegacyTaskMessageKeys(
	content ContentSchema,
	schema LegacyTaskAPISchema,
) error {
	if schema.Multipart == nil {
		return nil
	}
	for name, key := range map[string]string{
		"missing_file_message_key":      schema.Multipart.MissingFileMessageKey,
		"invalid_extension_message_key": schema.Multipart.InvalidExtensionMessageKey,
		"invalid_params_message_key":    schema.Multipart.InvalidParamsMessageKey,
	} {
		if strings.TrimSpace(key) == "" ||
			strings.TrimSpace(content.Messages[key]) == "" {
			return fmt.Errorf(
				"multipart.%s 必须指向非空 content.messages 文本",
				name,
			)
		}
	}
	return nil
}

func validateLegacyTaskAliasTargets(
	application ApplicationSchema,
	offerings generationMap,
) error {
	for _, capability := range application.Capabilities {
		if capability.LegacyAPI == nil ||
			len(capability.LegacyAPI.TaskAliases) == 0 {
			continue
		}
		for _, alias := range capability.LegacyAPI.TaskAliases {
			foundOffering := false
			for _, offering := range offerings {
				if offering.Capability != capability.ID || !LegacyOfferingApplies(alias.OfferingIDs, offering.ID) {
					continue
				}
				foundOffering = true
				foundTarget := false
				for _, parameter := range offering.Parameters {
					if parameter.Name == alias.TargetParameter &&
						parameter.Type == "task" {
						foundTarget = true
						break
					}
				}
				if !foundTarget {
					return fmt.Errorf(
						"能力 %q 的 task alias %q 在 Offering %q 中没有对应 task 参数 %q",
						capability.ID,
						alias.Name,
						offering.ID,
						alias.TargetParameter,
					)
				}
			}
			if !foundOffering {
				return fmt.Errorf(
					"能力 %q 的 task alias %q 没有可验证的 Offering",
					capability.ID,
					alias.Name,
				)
			}
		}
	}
	return nil
}

func validateLegacyFieldTargets(
	application ApplicationSchema,
	offerings generationMap,
) error {
	for _, capability := range application.Capabilities {
		if capability.LegacyAPI == nil {
			continue
		}
		for _, field := range capability.LegacyAPI.Fields {
			// A renamed field always names its target. A field kept under its
			// own name and forwarded always must exist as well (#330/#313: a
			// removed Remesh parameter left behind made old clients fail); an
			// if-supported field may be absent.
			target := field.TargetParameter
			if target == "" {
				if field.Forwarding != "always" {
					continue
				}
				target = field.Name
			}
			foundOffering := false
			declaredOffering := false
			for _, offering := range offerings {
				if offering.Capability != capability.ID || !LegacyOfferingApplies(field.OfferingIDs, offering.ID) {
					continue
				}
				declaredOffering = true
				// Disabled candidates have no executable legacy input contract.
				// Enabling one restores full target validation at load time.
				if !offering.Enabled {
					continue
				}
				foundOffering = true
				if _, exists := offeringParameter(
					offering.Parameters,
					target,
				); !exists {
					return fmt.Errorf(
						"能力 %q 的 field %q 在 Offering %q 中没有对应参数 %q",
						capability.ID,
						field.Name,
						offering.ID,
						target,
					)
				}
			}
			if !foundOffering && !declaredOffering {
				return fmt.Errorf(
					"能力 %q 的 field %q 没有可验证的 Offering",
					capability.ID,
					field.Name,
				)
			}
		}
	}
	return nil
}

func validateLegacyMultipartTargets(
	application ApplicationSchema,
	offerings generationMap,
) error {
	for _, capability := range application.Capabilities {
		if capability.LegacyAPI == nil ||
			capability.LegacyAPI.Multipart == nil {
			continue
		}
		targetName := capability.LegacyAPI.Multipart.TargetParameter
		targetType := "asset"
		if capability.LegacyAPI.Multipart.Cardinality == "many" {
			targetType = "asset-array"
		}
		foundOffering := false
		declaredOffering := false
		for _, offering := range offerings {
			if offering.Capability != capability.ID || !LegacyOfferingApplies(capability.LegacyAPI.Multipart.OfferingIDs, offering.ID) {
				continue
			}
			declaredOffering = true
			if !offering.Enabled {
				continue
			}
			foundOffering = true
			target, exists := offeringParameter(offering.Parameters, targetName)
			if !exists ||
				target.Type != targetType ||
				target.MaxBytes <= 0 ||
				len(target.Extensions) == 0 {
				return fmt.Errorf(
					"能力 %q 的 multipart target_parameter %q 在 Offering %q 中必须对应带大小和扩展名约束的 %s 参数",
					capability.ID,
					targetName,
					offering.ID,
					targetType,
				)
			}
		}
		if !foundOffering && !declaredOffering {
			return fmt.Errorf(
				"能力 %q 的 multipart 协议没有可验证的 Offering",
				capability.ID,
			)
		}
	}
	return nil
}

func validateLegacyResultAssetTargets(
	application ApplicationSchema,
	offerings generationMap,
) error {
	for _, capability := range application.Capabilities {
		if capability.LegacyAPI == nil ||
			(len(capability.LegacyAPI.ResultAssets) == 0 &&
				len(capability.LegacyAPI.ResultCollections) == 0) {
			continue
		}
		projections := make([]struct {
			name        string
			role        string
			offeringIDs []schemacontract.OfferingID
		}, 0, len(capability.LegacyAPI.ResultAssets)+
			len(capability.LegacyAPI.ResultCollections))
		for _, resultAsset := range capability.LegacyAPI.ResultAssets {
			projections = append(projections, struct {
				name        string
				role        string
				offeringIDs []schemacontract.OfferingID
			}{name: resultAsset.Name, role: resultAsset.Role, offeringIDs: resultAsset.OfferingIDs})
		}
		for _, collection := range capability.LegacyAPI.ResultCollections {
			projections = append(projections, struct {
				name        string
				role        string
				offeringIDs []schemacontract.OfferingID
			}{name: collection.Name, role: collection.Role, offeringIDs: collection.OfferingIDs})
		}
		for _, projection := range projections {
			foundOffering := false
			for _, offering := range offerings {
				if offering.Capability != capability.ID || !LegacyOfferingApplies(projection.offeringIDs, offering.ID) {
					continue
				}
				foundOffering = true
				foundRole := false
				for _, output := range offering.Outputs {
					if output.Role == projection.role {
						foundRole = true
						break
					}
				}
				if !foundRole {
					return fmt.Errorf(
						"能力 %q 的 result asset %q 在 Offering %q 中没有对应输出角色 %q",
						capability.ID,
						projection.name,
						offering.ID,
						projection.role,
					)
				}
			}
			if !foundOffering {
				return fmt.Errorf(
					"能力 %q 的 result asset %q 没有可验证的 Offering",
					capability.ID,
					projection.name,
				)
			}
		}
	}
	return nil
}

func knownParameterSemantic(semantic schemacontract.ParameterSemantic) bool {
	switch semantic {
	case schemacontract.SemanticPrompt,
		schemacontract.SemanticNegativePrompt,
		schemacontract.SemanticReferenceImage,
		schemacontract.SemanticPolycount,
		schemacontract.SemanticOutputSize:
		return true
	default:
		return false
	}
}

func knownLegacyRecordSource(value string) bool {
	switch value {
	case "asset-token",
		"asset-url",
		"asset-id",
		"asset-role",
		"asset-variant",
		"asset-media-type",
		"asset-ordinal",
		"legacy-download-url",
		"provider-record-identifier",
		"provider-record-status",
		"provider-record-downloadable",
		"provider-record-source-name",
		"provider-record-error-message",
		"provider-record-invalid-reason",
		"legacy-download-url-by-successful-ordinal":
		return true
	default:
		return false
	}
}

func knownLegacyRecordCollectionSource(value string) bool {
	return value == "assets" || value == "provider-data"
}

func legacyRecordSourceMatchesCollection(
	collection string,
	source string,
) bool {
	if collection == "provider-data" {
		return strings.HasPrefix(source, "provider-record-") ||
			source == "legacy-download-url-by-successful-ordinal"
	}
	return !strings.HasPrefix(source, "provider-record-") &&
		source != "legacy-download-url-by-successful-ordinal"
}

func knownLegacyRecordSemantic(value string) bool {
	switch value {
	case "task-id",
		"status",
		"downloadable",
		"download-path",
		"source-name",
		"error-message",
		"invalid-reason":
		return true
	default:
		return false
	}
}
