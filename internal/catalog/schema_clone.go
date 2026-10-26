package catalog

// Independent copies of schema values returned to callers.
import (
	"encoding/json"
	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
	"slices"
)

func cloneStringMap(source map[string]string) map[string]string {
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func cloneLegacyConstraintOverrides(
	source ProviderDiscoveryLegacyConstraintOverrideSchema,
) ProviderDiscoveryLegacyConstraintOverrideSchema {
	result := source
	if source.PromptMaxLength != nil {
		value := *source.PromptMaxLength
		result.PromptMaxLength = &value
	}
	if source.NegativePromptMaxLength != nil {
		value := *source.NegativePromptMaxLength
		result.NegativePromptMaxLength = &value
	}
	if source.ReferenceImageMin != nil {
		value := *source.ReferenceImageMin
		result.ReferenceImageMin = &value
	}
	if source.ReferenceImageMax != nil {
		value := *source.ReferenceImageMax
		result.ReferenceImageMax = &value
	}
	if source.ReferenceImageMaxSize != nil {
		value := *source.ReferenceImageMaxSize
		result.ReferenceImageMaxSize = &value
	}
	if source.PolycountMin != nil {
		value := *source.PolycountMin
		result.PolycountMin = &value
	}
	if source.PolycountMax != nil {
		value := *source.PolycountMax
		result.PolycountMax = &value
	}
	return result
}

func cloneApplication(source ApplicationSchema) ApplicationSchema {
	result := source
	if source.Terminology != nil {
		terminology := *source.Terminology
		terminology.UserFacing = make([]TerminologyTermSchema, len(source.Terminology.UserFacing))
		for index, term := range source.Terminology.UserFacing {
			terminology.UserFacing[index] = term
			terminology.UserFacing[index].Rejected = append([]string(nil), term.Rejected...)
		}
		result.Terminology = &terminology
	}
	if source.TaskHistory.AssetRecovery != nil {
		copy := *source.TaskHistory.AssetRecovery
		result.TaskHistory.AssetRecovery = &copy
	}
	result.Runtime.TaskRetention.TaskStates = append([]string(nil), source.Runtime.TaskRetention.TaskStates...)
	result.Chat.Harness.TaskSourceModes = append([]HarnessSourceKind(nil), source.Chat.Harness.TaskSourceModes...)
	result.Chat.Harness.SourceKinds = append([]HarnessSourceKind(nil), source.Chat.Harness.SourceKinds...)
	result.Runtime.InputAssets.ModelProfile = cloneModelInputProfile(source.Runtime.InputAssets.ModelProfile)
	fitting := &result.Runtime.InputAssets.ImageFitting
	fitting.ParameterSemantics = append([]string(nil), source.Runtime.InputAssets.ImageFitting.ParameterSemantics...)
	fitting.Steps = append([]string(nil), source.Runtime.InputAssets.ImageFitting.Steps...)
	fitting.JPEGQualities = append([]int(nil), source.Runtime.InputAssets.ImageFitting.JPEGQualities...)
	if source.Runtime.InputAssets.GeneratedModelProfile != nil {
		generated := *source.Runtime.InputAssets.GeneratedModelProfile
		result.Runtime.InputAssets.GeneratedModelProfile = &generated
	}
	result.AgentConfiguration = cloneAgentConfiguration(source.AgentConfiguration)
	result.LLMSettings = cloneLLMSettings(source.LLMSettings)
	result.Chat.Skills.Categories = make([]ChatSkillCategorySchema, len(source.Chat.Skills.Categories))
	for i, category := range source.Chat.Skills.Categories {
		result.Chat.Skills.Categories[i] = category
		result.Chat.Skills.Categories[i].HiddenCapabilities = append([]schemacontract.Capability(nil), category.HiddenCapabilities...)
	}
	result.Chat.Skills.Recipes = make([]ChatSkillRecipeSchema, len(source.Chat.Skills.Recipes))
	for i, recipe := range source.Chat.Skills.Recipes {
		result.Chat.Skills.Recipes[i] = recipe
		result.Chat.Skills.Recipes[i].Steps = make([]ChatSkillRecipeStepSchema, len(recipe.Steps))
		for j, step := range recipe.Steps {
			result.Chat.Skills.Recipes[i].Steps[j] = step
			if step.StaticInput != nil {
				result.Chat.Skills.Recipes[i].Steps[j].StaticInput = make(map[string]json.RawMessage, len(step.StaticInput))
				for key, value := range step.StaticInput {
					result.Chat.Skills.Recipes[i].Steps[j].StaticInput[key] = append(json.RawMessage(nil), value...)
				}
			}
		}
	}
	result.Runtime.TaskEvents.PublicProjection = make(
		[]PublicEventProjectionSchema,
		len(source.Runtime.TaskEvents.PublicProjection),
	)
	for index, entry := range source.Runtime.TaskEvents.PublicProjection {
		cloned := entry
		cloned.Fields = append([]string(nil), entry.Fields...)
		result.Runtime.TaskEvents.PublicProjection[index] = cloned
	}
	result.TaskHistory.Assets.RigCheckReport.FieldLabels = cloneStringMap(source.TaskHistory.Assets.RigCheckReport.FieldLabels)
	result.TaskHistory.Assets.RigCheckReport.BooleanLabels = cloneStringMap(source.TaskHistory.Assets.RigCheckReport.BooleanLabels)
	result.TaskHistory.Assets.RigCheckReport.SpeciesLabels = cloneStringMap(source.TaskHistory.Assets.RigCheckReport.SpeciesLabels)
	result.TaskHistory.Assets.PreviewRoles = append(
		[]string(nil),
		source.TaskHistory.Assets.PreviewRoles...,
	)
	result.TaskHistory.Labels.Models = cloneStringMap(
		source.TaskHistory.Labels.Models,
	)
	result.TaskHistory.Labels.Parameters = make(
		map[string]map[string]string,
		len(source.TaskHistory.Labels.Parameters),
	)
	for capability, parameters := range source.TaskHistory.Labels.Parameters {
		result.TaskHistory.Labels.Parameters[capability] = cloneStringMap(
			parameters,
		)
	}
	result.TaskHistory.HiddenFields = make(
		map[string][]string,
		len(source.TaskHistory.HiddenFields),
	)
	for capability, fields := range source.TaskHistory.HiddenFields {
		result.TaskHistory.HiddenFields[capability] = append(
			[]string(nil),
			fields...,
		)
	}
	result.ProviderDiscovery.LegacyModels = append(
		[]ProviderDiscoveryLegacyModel(nil),
		source.ProviderDiscovery.LegacyModels...,
	)
	for index, model := range source.ProviderDiscovery.LegacyModels {
		if model.Recommended != nil {
			recommended := *model.Recommended
			result.ProviderDiscovery.LegacyModels[index].Recommended =
				&recommended
		}
		result.ProviderDiscovery.LegacyModels[index].ParameterProjections =
			make(
				map[schemacontract.Capability]ProviderDiscoveryLegacyParameterProjection,
				len(model.ParameterProjections),
			)
		for capability, projection := range model.ParameterProjections {
			cloned := projection
			cloned.Order = append([]string(nil), projection.Order...)
			cloned.Omit = append([]string(nil), projection.Omit...)
			cloned.Fields = make(
				map[string]ProviderDiscoveryLegacyParameterFieldSchema,
				len(projection.Fields),
			)
			for parameter, fields := range projection.Fields {
				clonedFields := fields
				clonedFields.Omit = append([]string(nil), fields.Omit...)
				if fields.Required != nil {
					required := *fields.Required
					clonedFields.Required = &required
				}
				cloned.Fields[parameter] = clonedFields
			}
			result.ProviderDiscovery.LegacyModels[index].
				ParameterProjections[capability] = cloned
		}
		result.ProviderDiscovery.LegacyModels[index].ConstraintProjections =
			make(
				map[schemacontract.Capability]ProviderDiscoveryLegacyConstraintProjection,
				len(model.ConstraintProjections),
			)
		for capability, projection := range model.ConstraintProjections {
			cloned := projection
			cloned.Omit = append([]string(nil), projection.Omit...)
			cloned.Overrides = cloneLegacyConstraintOverrides(
				projection.Overrides,
			)
			result.ProviderDiscovery.LegacyModels[index].
				ConstraintProjections[capability] = cloned
		}
	}
	result.ProviderDiscovery.LegacyConstraintOptionLabels =
		make(map[string]map[string]string,
			len(source.ProviderDiscovery.LegacyConstraintOptionLabels))
	for modelID, labels := range source.ProviderDiscovery.
		LegacyConstraintOptionLabels {
		result.ProviderDiscovery.LegacyConstraintOptionLabels[modelID] =
			cloneStringMap(labels)
	}
	result.ProviderWebhooks = append(
		[]ProviderWebhookSchema(nil),
		source.ProviderWebhooks...,
	)
	for index := range result.ProviderWebhooks {
		result.ProviderWebhooks[index].KeyHeaders = append(
			[]string(nil),
			source.ProviderWebhooks[index].KeyHeaders...,
		)
		result.ProviderWebhooks[index].PayloadJobIDFields = append(
			[]string(nil),
			source.ProviderWebhooks[index].PayloadJobIDFields...,
		)
	}
	result.Administration.Navigation = append(
		[]AdministrationNavigationSchema(nil),
		source.Administration.Navigation...,
	)
	result.TaskStream.Aliases = append(
		[]TaskStreamCapabilityAlias(nil),
		source.TaskStream.Aliases...,
	)
	result.Administration.Users.Presentation.DateTimeOptions = make(
		map[string]string,
		len(source.Administration.Users.Presentation.DateTimeOptions),
	)
	for key, value := range source.Administration.Users.Presentation.
		DateTimeOptions {
		result.Administration.Users.Presentation.DateTimeOptions[key] = value
	}
	result.Administration.Users.Presentation.Text = make(
		map[string]string,
		len(source.Administration.Users.Presentation.Text),
	)
	for key, value := range source.Administration.Users.Presentation.Text {
		result.Administration.Users.Presentation.Text[key] = value
	}
	result.Administration.AuditLogs.Presentation.DateTimeOptions = cloneStringMap(
		source.Administration.AuditLogs.Presentation.DateTimeOptions,
	)
	result.Administration.AuditLogs.Presentation.ActionBadgeClasses = cloneStringMap(
		source.Administration.AuditLogs.Presentation.ActionBadgeClasses,
	)
	result.Administration.AuditLogs.Presentation.ActionPrefixBadgeClasses = cloneStringMap(
		source.Administration.AuditLogs.Presentation.ActionPrefixBadgeClasses,
	)
	result.Administration.AuditLogs.Presentation.Text = cloneStringMap(
		source.Administration.AuditLogs.Presentation.Text,
	)
	result.Administration.Generations.Presentation.DateTimeOptions =
		cloneStringMap(
			source.Administration.Generations.Presentation.DateTimeOptions,
		)
	result.Administration.Generations.Presentation.Text = cloneStringMap(
		source.Administration.Generations.Presentation.Text,
	)
	result.Administration.Generations.Assets.PreviewImageExtensions = append(
		[]string(nil),
		source.Administration.Generations.Assets.PreviewImageExtensions...,
	)
	result.Administration.Generations.Assets.MediaTypeExtensions =
		cloneStringMap(
			source.Administration.Generations.Assets.
				MediaTypeExtensions,
		)
	result.Administration.Generations.Assets.DownloadExcludedRoles = append(
		[]string(nil),
		source.Administration.Generations.Assets.DownloadExcludedRoles...,
	)
	result.Administration.Usage.Presentation.QuickRanges = append(
		[]AdministrationUsageRangeSchema(nil),
		source.Administration.Usage.Presentation.QuickRanges...,
	)
	result.Administration.Usage.Presentation.DateTimeOptions = cloneStringMap(
		source.Administration.Usage.Presentation.DateTimeOptions,
	)
	result.Administration.Usage.Presentation.Text = cloneStringMap(
		source.Administration.Usage.Presentation.Text,
	)
	result.Administration.UserAssets.Filters.DownloadActions = append(
		[]AdministrationUserAssetsDownloadActionSchema(nil),
		source.Administration.UserAssets.Filters.DownloadActions...,
	)
	result.Administration.UserAssets.Presentation.DateTimeOptions =
		cloneStringMap(
			source.Administration.UserAssets.Presentation.DateTimeOptions,
		)
	result.Administration.UserAssets.Presentation.SizeUnits = append(
		[]string(nil),
		source.Administration.UserAssets.Presentation.SizeUnits...,
	)
	result.Administration.UserAssets.Presentation.AssetTypeLabels =
		cloneStringMap(
			source.Administration.UserAssets.Presentation.AssetTypeLabels,
		)
	result.Administration.UserAssets.Presentation.Text = cloneStringMap(
		source.Administration.UserAssets.Presentation.Text,
	)
	result.Administration.BackfillStatus.SyncProviderIDs = append(
		[]string(nil),
		source.Administration.BackfillStatus.SyncProviderIDs...,
	)
	result.Canvas.Presentation.Text = make(
		map[string]string,
		len(source.Canvas.Presentation.Text),
	)
	for key, value := range source.Canvas.Presentation.Text {
		result.Canvas.Presentation.Text[key] = value
	}
	result.Canvas.Frontend.Export.Frame.LineDash = append(
		[]float64(nil),
		source.Canvas.Frontend.Export.Frame.LineDash...,
	)
	result.Canvas.Frontend.Artboard.FrameLineDash = append(
		[]float64(nil),
		source.Canvas.Frontend.Artboard.FrameLineDash...,
	)
	result.Canvas.Frontend.Artboard.Resize.Anchors = append(
		[]string(nil),
		source.Canvas.Frontend.Artboard.Resize.Anchors...,
	)
	result.Canvas.Frontend.Artboard.RatioPixels = make(
		map[string]CanvasPixelSize,
		len(source.Canvas.Frontend.Artboard.RatioPixels),
	)
	for ratio, size := range source.Canvas.Frontend.Artboard.RatioPixels {
		result.Canvas.Frontend.Artboard.RatioPixels[ratio] = size
	}
	result.Canvas.Frontend.Artboard.RatioPixelTiers = make(
		map[string]map[string]map[string]CanvasPixelSize,
		len(source.Canvas.Frontend.Artboard.RatioPixelTiers),
	)
	for parameter, tiers := range source.Canvas.Frontend.Artboard.RatioPixelTiers {
		clonedTiers := make(map[string]map[string]CanvasPixelSize, len(tiers))
		for tier, ratios := range tiers {
			clonedRatios := make(map[string]CanvasPixelSize, len(ratios))
			for ratio, size := range ratios {
				clonedRatios[ratio] = size
			}
			clonedTiers[tier] = clonedRatios
		}
		result.Canvas.Frontend.Artboard.RatioPixelTiers[parameter] = clonedTiers
	}
	result.Canvas.Frontend.Style.PresetColors = append(
		[]string(nil),
		source.Canvas.Frontend.Style.PresetColors...,
	)
	result.Canvas.Frontend.Style.StrokeWidths = append(
		[]float64(nil),
		source.Canvas.Frontend.Style.StrokeWidths...,
	)
	result.Canvas.Frontend.Style.FontSizes = append(
		[]float64(nil),
		source.Canvas.Frontend.Style.FontSizes...,
	)
	result.Canvas.Frontend.Backgrounds = append(
		[]CanvasBackgroundSchema(nil),
		source.Canvas.Frontend.Backgrounds...,
	)
	result.Canvas.Frontend.Keyboard.DeleteKeys = append(
		[]string(nil),
		source.Canvas.Frontend.Keyboard.DeleteKeys...,
	)
	result.Canvas.Frontend.Keyboard.ShapeCycle = append(
		[]string(nil),
		source.Canvas.Frontend.Keyboard.ShapeCycle...,
	)
	result.Canvas.Frontend.Selection.AntsDash = append(
		[]float64(nil),
		source.Canvas.Frontend.Selection.AntsDash...,
	)
	// ProcessingAction has only scalar presentation fields, copied by the initial value assignment.
	result.Canvas.Frontend.StageActions = append(
		[]CanvasStageActionSchema(nil),
		source.Canvas.Frontend.StageActions...,
	)
	for index := range result.Canvas.Frontend.StageActions {
		action := &result.Canvas.Frontend.StageActions[index]
		action.Kinds = append([]string(nil), action.Kinds...)
	}
	result.Canvas.Frontend.Tools = append(
		[]CanvasToolSchema(nil),
		source.Canvas.Frontend.Tools...,
	)
	for index := range result.Canvas.Frontend.Tools {
		tool := &result.Canvas.Frontend.Tools[index]
		tool.Modifiers = append([]string(nil), tool.Modifiers...)
	}
	result.Canvas.Frontend.PanelTabs.Tabs = append(
		[]CanvasPanelTabSchema(nil),
		source.Canvas.Frontend.PanelTabs.Tabs...,
	)
	result.Canvas.Frontend.PanelTabs.Kinds = append(
		[]CanvasPanelKindSchema(nil),
		source.Canvas.Frontend.PanelTabs.Kinds...,
	)
	for index := range result.Canvas.Frontend.PanelTabs.Kinds {
		kind := &result.Canvas.Frontend.PanelTabs.Kinds[index]
		kind.Tabs = append([]string(nil), kind.Tabs...)
	}
	result.Chat.API.PromptEnhance.CapabilityProfiles = make(
		map[schemacontract.Capability]string,
		len(source.Chat.API.PromptEnhance.CapabilityProfiles),
	)
	for capability, profile := range source.Chat.API.PromptEnhance.
		CapabilityProfiles {
		result.Chat.API.PromptEnhance.CapabilityProfiles[capability] = profile
	}
	result.Chat.API.PromptEnhance.Profiles = make(
		map[string]ChatPromptEnhanceProfileSchema,
		len(source.Chat.API.PromptEnhance.Profiles),
	)
	for profileID, profile := range source.Chat.API.PromptEnhance.Profiles {
		result.Chat.API.PromptEnhance.Profiles[profileID] = profile
	}
	result.Chat.Context.HandlePrefixes = cloneStringMap(source.Chat.Context.HandlePrefixes)
	result.Chat.Context.AttachmentLabelTemplates = cloneStringMap(source.Chat.Context.AttachmentLabelTemplates)
	result.Chat.Context.KindLabels = cloneStringMap(source.Chat.Context.KindLabels)
	result.Chat.Context.UnpicturedAttachmentTemplates = cloneStringMap(source.Chat.Context.UnpicturedAttachmentTemplates)
	result.Chat.Stream.Headers = make(
		map[string]string,
		len(source.Chat.Stream.Headers),
	)
	for name, value := range source.Chat.Stream.Headers {
		result.Chat.Stream.Headers[name] = value
	}
	if source.Chat.Agent.Thinking != nil {
		thinking := *source.Chat.Agent.Thinking
		thinking.ModelProfiles = nil
		for _, profile := range source.Chat.Agent.Thinking.ModelProfiles {
			copied := profile
			copied.ModelPrefixes = slices.Clone(profile.ModelPrefixes)
			copied.EffortLevels = slices.Clone(profile.EffortLevels)
			if profile.StreamIncludeUsage != nil {
				include := *profile.StreamIncludeUsage
				copied.StreamIncludeUsage = &include
			}
			copied.RequestFields = make(map[string]json.RawMessage, len(profile.RequestFields))
			for field, value := range profile.RequestFields {
				copied.RequestFields[field] = slices.Clone(value)
			}
			thinking.ModelProfiles = append(thinking.ModelProfiles, copied)
		}
		result.Chat.Agent.Thinking = &thinking
	}
	result.Chat.ContextCompaction.RoleLabels = cloneStringMap(source.Chat.ContextCompaction.RoleLabels)
	if source.Chat.Presentation.Messages.GenerationFailedByClass != nil {
		result.Chat.Presentation.Messages.GenerationFailedByClass = cloneStringMap(source.Chat.Presentation.Messages.GenerationFailedByClass)
	}
	if source.Chat.Presentation.Messages.GenerationFailedMeteredByClass != nil {
		result.Chat.Presentation.Messages.GenerationFailedMeteredByClass = cloneStringMap(source.Chat.Presentation.Messages.GenerationFailedMeteredByClass)
	}
	result.Chat.AgentScope.Modes = make([]ChatAgentScopeMode, len(source.Chat.AgentScope.Modes))
	for index, mode := range source.Chat.AgentScope.Modes {
		result.Chat.AgentScope.Modes[index] = mode
		result.Chat.AgentScope.Modes[index].Allows = append([]string(nil), mode.Allows...)
		result.Chat.AgentScope.Modes[index].WorkspaceDomains = append([]string(nil), mode.WorkspaceDomains...)
	}
	result.Chat.AgentTools = make([]ChatAgentToolSchema, len(source.Chat.AgentTools))
	for index, tool := range source.Chat.AgentTools {
		result.Chat.AgentTools[index] = tool
		result.Chat.AgentTools[index].Execution = cloneChatToolExecution(tool.Execution)
	}
	result.Chat.Tools = make([]ChatToolSchema, len(source.Chat.Tools))
	for index, tool := range source.Chat.Tools {
		result.Chat.Tools[index] = tool
		if tool.AcceptReferenceImages != nil {
			accept := *tool.AcceptReferenceImages
			result.Chat.Tools[index].AcceptReferenceImages = &accept
		}
		result.Chat.Tools[index].Execution = cloneChatToolExecution(tool.Execution)
		result.Chat.Tools[index].Parameters = append(
			json.RawMessage(nil),
			tool.Parameters...,
		)
		result.Chat.Tools[index].ArgumentMappings = make(
			map[string]string,
			len(tool.ArgumentMappings),
		)
		for argument, parameter := range tool.ArgumentMappings {
			result.Chat.Tools[index].ArgumentMappings[argument] = parameter
		}
		result.Chat.Tools[index].OverrideMappings = make(
			map[string]string,
			len(tool.OverrideMappings),
		)
		for semantic, parameter := range tool.OverrideMappings {
			result.Chat.Tools[index].OverrideMappings[semantic] = parameter
		}
		result.Chat.Tools[index].DefaultParameters = make(
			map[string]json.RawMessage,
			len(tool.DefaultParameters),
		)
		for parameter, value := range tool.DefaultParameters {
			result.Chat.Tools[index].DefaultParameters[parameter] = append(
				json.RawMessage(nil),
				value...,
			)
		}
		result.Chat.Tools[index].ResultAssetRoles = append(
			[]string(nil),
			tool.ResultAssetRoles...,
		)
		if tool.Processing != nil {
			processing := *tool.Processing
			if processing.Plan != nil {
				plan := *processing.Plan
				processing.Plan = &plan
			}
			if processing.Choice != nil {
				choice := *processing.Choice
				processing.Choice = &choice
			}
			processing.Steps = make(map[string]schemacontract.Capability, len(tool.Processing.Steps))
			for step, capability := range tool.Processing.Steps {
				processing.Steps[step] = capability
			}
			processing.ParameterArguments = make(map[string]string, len(tool.Processing.ParameterArguments))
			for argument, parameter := range tool.Processing.ParameterArguments {
				processing.ParameterArguments[argument] = parameter
			}
			processing.SourceVariantPreference = append([]string(nil), tool.Processing.SourceVariantPreference...)
			result.Chat.Tools[index].Processing = &processing
		}
	}
	result.Runtime.HTTP.ThumbnailAssetRoles = append([]string(nil), source.Runtime.HTTP.ThumbnailAssetRoles...)
	result.Runtime.HTTP.CandidateProxy.LegacyRouteKeys = append(
		[]string(nil),
		source.Runtime.HTTP.CandidateProxy.LegacyRouteKeys...,
	)
	result.Runtime.LegacyMigration.CacheOnlyTaskExclusions = append(
		[]LegacyTaskExclusionSchema(nil),
		source.Runtime.LegacyMigration.CacheOnlyTaskExclusions...,
	)
	result.Runtime.LegacyMigration.TaskOutputExcludedRoles = append(
		[]string(nil),
		source.Runtime.LegacyMigration.TaskOutputExcludedRoles...,
	)
	result.Runtime.LegacyMigration.ThumbnailBackfill.OfferingMappings = append(
		[]LegacyThumbnailOfferingMappingSchema(nil),
		source.Runtime.LegacyMigration.ThumbnailBackfill.OfferingMappings...,
	)
	result.Runtime.LegacyMigration.ThumbnailBackfill.
		AcknowledgedSourceLosses = append(
		[]LegacyThumbnailSourceLossSchema(nil),
		source.Runtime.LegacyMigration.ThumbnailBackfill.
			AcknowledgedSourceLosses...,
	)
	result.Runtime.ProviderAccounts = make(
		map[string]ProviderAccountSchema,
		len(source.Runtime.ProviderAccounts),
	)
	for accountID, account := range source.Runtime.ProviderAccounts {
		result.Runtime.ProviderAccounts[accountID] = account
	}
	result.Runtime.AssetDelivery.LegacyGeneratedImage.AssetRoles = append(
		[]string(nil),
		source.Runtime.AssetDelivery.LegacyGeneratedImage.AssetRoles...,
	)
	result.Runtime.AssetDelivery.LegacyGeneratedImage.ExtensionMediaTypes = make(
		map[string]string,
		len(source.Runtime.AssetDelivery.LegacyGeneratedImage.ExtensionMediaTypes),
	)
	for extension, mediaType := range source.Runtime.AssetDelivery.
		LegacyGeneratedImage.ExtensionMediaTypes {
		result.Runtime.AssetDelivery.LegacyGeneratedImage.
			ExtensionMediaTypes[extension] = mediaType
	}
	result.Runtime.AssetDelivery.Proxy.Security.AllowedSchemes = append(
		[]string(nil),
		source.Runtime.AssetDelivery.Proxy.Security.AllowedSchemes...,
	)
	result.Runtime.AssetDelivery.Proxy.Security.AllowedHosts = append(
		[]string(nil),
		source.Runtime.AssetDelivery.Proxy.Security.AllowedHosts...,
	)
	result.Runtime.AssetDelivery.Proxy.Metadata.SupportedMediaTypes = append(
		[]string(nil),
		source.Runtime.AssetDelivery.Proxy.Metadata.SupportedMediaTypes...,
	)
	result.Runtime.AssetDelivery.Proxy.MediaTypeExtensions = cloneStringMap(
		source.Runtime.AssetDelivery.Proxy.MediaTypeExtensions,
	)
	result.Runtime.InputAssets.TypeRules = make(
		[]InputAssetTypeRule,
		len(source.Runtime.InputAssets.TypeRules),
	)
	result.Runtime.Frontend.RetiredGenerationModels = append([]RetiredGenerationModel(nil), source.Runtime.Frontend.RetiredGenerationModels...)
	result.ProviderDeprecations = cloneProviderDeprecations(source.ProviderDeprecations)
	result.Runtime.Frontend.ImageUpload.Conversions = make(
		[]ImageUploadConversion, len(source.Runtime.Frontend.ImageUpload.Conversions),
	)
	for index, conversion := range source.Runtime.Frontend.ImageUpload.Conversions {
		conversion.SourceMediaTypes = append([]string{}, conversion.SourceMediaTypes...)
		conversion.SourceExtensions = append([]string{}, conversion.SourceExtensions...)
		result.Runtime.Frontend.ImageUpload.Conversions[index] = conversion
	}
	for index, rule := range source.Runtime.InputAssets.TypeRules {
		result.Runtime.InputAssets.TypeRules[index] = rule
		result.Runtime.InputAssets.TypeRules[index].MediaTypes = append(
			[]string(nil),
			rule.MediaTypes...,
		)
		result.Runtime.InputAssets.TypeRules[index].Extensions = append(
			[]string(nil),
			rule.Extensions...,
		)
	}
	result.Runtime.Configuration.Settings = make(
		map[string]RuntimeSettingSchema,
		len(source.Runtime.Configuration.Settings),
	)
	for name, setting := range source.Runtime.Configuration.Settings {
		setting.Environment = append(
			[]EnvironmentSourceSchema(nil),
			setting.Environment...,
		)
		setting.DefaultByEnvironment = make(
			map[string]string,
			len(setting.DefaultByEnvironment),
		)
		for environment, value := range source.Runtime.Configuration.
			Settings[name].DefaultByEnvironment {
			setting.DefaultByEnvironment[environment] = value
		}
		setting.AllowedValues = append(
			[]string(nil),
			setting.AllowedValues...,
		)
		result.Runtime.Configuration.Settings[name] = setting
	}
	result.Content.Home.Links = append(
		[]HomeLinkPresentation(nil),
		source.Content.Home.Links...,
	)
	result.Content.Navigation = append(
		[]NavigationPresentation(nil),
		source.Content.Navigation...,
	)
	result.Content.AssetKinds = append(
		[]ValueLabel(nil),
		source.Content.AssetKinds...,
	)
	result.Content.AssetKindPatterns = append(
		[]AssetKindPatternPresentation(nil),
		source.Content.AssetKindPatterns...,
	)
	result.Content.Messages = make(map[string]string, len(source.Content.Messages))
	for key, value := range source.Content.Messages {
		result.Content.Messages[key] = value
	}
	result.Localization = cloneLocalizationSchema(source.Localization)
	result.Capabilities = append([]CapabilityPresentation(nil), source.Capabilities...)
	for index, capability := range source.Capabilities {
		if capability.WorkspaceModelSelector != nil {
			value := *capability.WorkspaceModelSelector
			result.Capabilities[index].WorkspaceModelSelector = &value
		}
		if capability.Workflow != nil {
			workflow := *capability.Workflow
			if capability.Workflow.DefaultAutoRemesh != nil {
				defaultAutoRemesh := *capability.Workflow.DefaultAutoRemesh
				workflow.DefaultAutoRemesh = &defaultAutoRemesh
			}
			result.Capabilities[index].Workflow = &workflow
		}
		if capability.LegacyAPI != nil {
			legacyAPI := *capability.LegacyAPI
			if capability.LegacyAPI.Multipart != nil {
				multipart := *capability.LegacyAPI.Multipart
				multipart.OfferingIDs = append([]schemacontract.OfferingID(nil), multipart.OfferingIDs...)
				legacyAPI.Multipart = &multipart
			}
			if capability.LegacyAPI.AssetDownload != nil {
				assetDownload := *capability.LegacyAPI.AssetDownload
				legacyAPI.AssetDownload = &assetDownload
			}
			legacyAPI.Fields = append(
				[]LegacyFieldSchema(nil),
				capability.LegacyAPI.Fields...,
			)
			for i := range legacyAPI.Fields {
				legacyAPI.Fields[i].OfferingIDs = append([]schemacontract.OfferingID(nil), legacyAPI.Fields[i].OfferingIDs...)
			}
			legacyAPI.AssetAliases = append(
				[]LegacyAssetAliasSchema(nil),
				capability.LegacyAPI.AssetAliases...,
			)
			for i := range legacyAPI.AssetAliases {
				legacyAPI.AssetAliases[i].OfferingIDs = append([]schemacontract.OfferingID(nil), legacyAPI.AssetAliases[i].OfferingIDs...)
			}
			legacyAPI.TaskAliases = append(
				[]LegacyTaskAliasSchema(nil),
				capability.LegacyAPI.TaskAliases...,
			)
			for i := range legacyAPI.TaskAliases {
				legacyAPI.TaskAliases[i].OfferingIDs = append([]schemacontract.OfferingID(nil), legacyAPI.TaskAliases[i].OfferingIDs...)
			}
			legacyAPI.OutputProjections = append(
				[]LegacyOutputProjectionSchema(nil),
				capability.LegacyAPI.OutputProjections...,
			)
			for projectionIndex := range legacyAPI.OutputProjections {
				projection := &legacyAPI.OutputProjections[projectionIndex]
				projection.RecordFields = append(
					[]LegacyRecordFieldSchema(nil),
					projection.RecordFields...,
				)
				for fieldIndex := range projection.RecordFields {
					field := &projection.RecordFields[fieldIndex]
					field.Literal = append(
						json.RawMessage(nil),
						field.Literal...,
					)
				}
			}
			legacyAPI.ResultAssets = make(
				[]LegacyResultAssetSchema,
				len(capability.LegacyAPI.ResultAssets),
			)
			for resultIndex, asset := range capability.LegacyAPI.ResultAssets {
				legacyAPI.ResultAssets[resultIndex] = asset
				legacyAPI.ResultAssets[resultIndex].OfferingIDs = append([]schemacontract.OfferingID(nil), asset.OfferingIDs...)
				if asset.OfferingVariants != nil {
					legacyAPI.ResultAssets[resultIndex].OfferingVariants = make(map[schemacontract.OfferingID]string, len(asset.OfferingVariants))
					for id, variant := range asset.OfferingVariants {
						legacyAPI.ResultAssets[resultIndex].OfferingVariants[id] = variant
					}
				}
				legacyAPI.ResultAssets[resultIndex].MediaTypes = append(
					[]string(nil),
					asset.MediaTypes...,
				)
			}
			legacyAPI.ResultCollections = append(
				[]LegacyResultCollectionSchema(nil),
				capability.LegacyAPI.ResultCollections...,
			)
			for i := range legacyAPI.ResultCollections {
				legacyAPI.ResultCollections[i].OfferingIDs = append([]schemacontract.OfferingID(nil), legacyAPI.ResultCollections[i].OfferingIDs...)
			}
			result.Capabilities[index].LegacyAPI = &legacyAPI
		}
		if capability.PromptComposition != nil {
			composition := *capability.PromptComposition
			result.Capabilities[index].PromptComposition = &composition
		}
	}
	result.TaskStates = append([]TaskStatePresentation(nil), source.TaskStates...)
	result.AuditActions = append([]ValueLabel(nil), source.AuditActions...)
	result.ModelDelivery = cloneModelDelivery(source.ModelDelivery)
	result.Authentication.PublicPaths = append(
		[]string(nil),
		source.Authentication.PublicPaths...,
	)
	result.Authentication.Permissions = append(
		[]PermissionSchema(nil),
		source.Authentication.Permissions...,
	)
	result.Authentication.Roles = make([]RoleSchema, len(source.Authentication.Roles))
	for index, role := range source.Authentication.Roles {
		result.Authentication.Roles[index] = role
		result.Authentication.Roles[index].Permissions = append(
			[]string(nil),
			role.Permissions...,
		)
	}
	return result
}

func cloneChatToolExecution(source ChatToolExecutionSchema) ChatToolExecutionSchema {
	result := source
	result.Effects = append([]string(nil), source.Effects...)
	result.Domains = append([]string(nil), source.Domains...)
	result.Capabilities = append([]schemacontract.Capability(nil), source.Capabilities...)
	return result
}

// clonePostSuccessPolicy 深拷贝成功后观察声明（§6.1 快照内嵌）。
func clonePostSuccessPolicy(source *schemacontract.PostSuccessPolicy) *schemacontract.PostSuccessPolicy {
	if source == nil {
		return nil
	}
	clone := *source
	return &clone
}

// cloneCostDeclaration 深拷贝成本声明（含条件切片），保证冻结快照与
// 目录实例互不影响。
func cloneCostDeclaration(source *schemacontract.CostDeclaration) *schemacontract.CostDeclaration {
	if source == nil {
		return nil
	}
	clone := *source
	clone.ExtraWhen = append([]schemacontract.ParameterCondition(nil), source.ExtraWhen...)
	return &clone
}

func cloneCostPolicy(source *schemacontract.CostPolicy) *schemacontract.CostPolicy {
	if source == nil {
		return nil
	}
	clone := *source
	return &clone
}

func cloneInputRules(source []schemacontract.InputRule) []schemacontract.InputRule {
	result := make([]schemacontract.InputRule, len(source))
	for index, rule := range source {
		result[index] = rule
		result[index].Parameters = append([]string(nil), rule.Parameters...)
	}
	return result
}

func cloneAgentConfiguration(source AgentConfigurationSchema) AgentConfigurationSchema {
	result := source
	result.UI = cloneStringMap(source.UI)
	result.MCPConnections = make(map[string]AgentMCPConnectionSchema, len(source.MCPConnections))
	for key, connection := range source.MCPConnections {
		connection.AllowedOrigins = append([]string(nil), connection.AllowedOrigins...)
		result.MCPConnections[key] = connection
	}
	result.MCPPresets = make([]AgentMCPPresetSchema, len(source.MCPPresets))
	for i, preset := range source.MCPPresets {
		result.MCPPresets[i] = preset
		result.MCPPresets[i].ToolNames = append([]string(nil), preset.ToolNames...)
		result.MCPPresets[i].Tools = make([]AgentMCPToolSchema, len(preset.Tools))
		for j, tool := range preset.Tools {
			tool.Parameters = append(json.RawMessage(nil), tool.Parameters...)
			tool.Execution = cloneChatToolExecution(tool.Execution)
			result.MCPPresets[i].Tools[j] = tool
		}
	}
	return result
}
