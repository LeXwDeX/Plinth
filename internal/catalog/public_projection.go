package catalog

// Public application projection and event visibility contracts.
func (source ApplicationSchema) Public() PublicApplicationSchema {
	cloned := cloneApplication(source)
	proxy := cloned.Runtime.AssetDelivery.Proxy
	publicProxy := PublicAssetProxyRuntimeSchema{}
	publicProxy.API.Query = proxy.API.Query
	publicProxy.Security.AllowedSchemes = proxy.Security.AllowedSchemes
	publicProxy.Security.AllowedHosts = proxy.Security.AllowedHosts
	publicProxy.Thumbnail.RequestValue = proxy.Thumbnail.RequestValue
	chatTools := make([]PublicChatToolSchema, 0, len(cloned.Chat.Tools))
	for _, tool := range cloned.Chat.Tools {
		chatTools = append(chatTools, PublicChatToolSchema{
			RequireUniqueOffering: tool.RequireUniqueOffering,
			Name:                  tool.Name,
			PendingCapability:     tool.PendingCapability,
			Capability:            tool.Capability,
			FallbackCapability:    tool.FallbackCapability,
			WorkspaceDomain:       firstOrEmpty(tool.Execution.Domains),
		})
	}
	publicChat := PublicChatSchema{
		Defaults: cloned.Chat.Defaults,
		Roles:    cloned.Chat.Roles,
		State:    cloned.Chat.State,
		Frontend: cloned.Chat.Frontend,
		Stream: PublicChatStreamSchema{
			EventFields: cloned.Chat.Stream.EventFields,
			EventTypes:  cloned.Chat.Stream.EventTypes,
		},
		Tools:        chatTools,
		Presentation: cloned.Chat.Presentation,
	}
	publicChat.Harness = cloned.Chat.Harness.Public()
	publicChat.AgentStatus.Available = !cloned.Chat.AgentNotConfigured
	publicChat.AgentLimits.MaxToolCallsPerReply = cloned.Chat.Agent.MaxToolCallsPerReply
	publicChat.ThreeViewLimits.QueueLimitSeconds = cloned.Chat.ThreeView.QueueLimitSeconds
	// #103: the conversation recognises review calls by these public tool
	// names; the brief panel and approval card label purposes and fields from
	// the public purpose table.
	if tools := cloned.Chat.ModelReview.Tools; tools != (ChatModelReviewToolsSchema{}) {
		publicChat.ModelReview = &PublicChatModelReviewSchema{Tools: tools}
	}
	// #246: the browser recognises the self-check tools and renders the three
	// delivery states by role. Thresholds and prompt text stay server-only.
	if selfCheck := cloned.Chat.SelfCheck; selfCheck.Tools != (ChatSelfCheckToolsSchema{}) ||
		len(selfCheck.Statuses) > 0 || selfCheck.Limits != (ChatSelfCheckLimitsSchema{}) ||
		len(selfCheck.Checkpoints) > 0 {
		statuses := make([]PublicChatSelfCheckStatus, 0, len(selfCheck.Statuses))
		for _, status := range selfCheck.Statuses {
			statuses = append(statuses, PublicChatSelfCheckStatus{ID: status.ID, Role: status.Role, Label: status.Label})
		}
		checkpoints := make([]PublicChatSelfCheckCheckpoint, 0, len(selfCheck.Checkpoints))
		for _, checkpoint := range selfCheck.Checkpoints {
			checkpoints = append(checkpoints, PublicChatSelfCheckCheckpoint{ID: checkpoint.ID, Label: checkpoint.Label})
		}
		publicChat.SelfCheck = &PublicChatSelfCheckSchema{
			Tools:    selfCheck.Tools,
			Statuses: statuses,
			Limits: PublicChatSelfCheckLimits{
				MaxRoundsPerResult:      selfCheck.Limits.MaxRoundsPerResult,
				MaxCorrectionsPerResult: selfCheck.Limits.MaxCorrectionsPerResult,
				MaxRendersPerRound:      selfCheck.Limits.MaxRendersPerRound,
			},
			Checkpoints: checkpoints,
		}
	}
	if usage := cloned.Chat.Model3DUsage; len(usage.Profiles) > 0 || len(usage.Brief.Fields) > 0 {
		profiles := make([]PublicChatModel3DUsageProfile, 0, len(usage.Profiles))
		for _, profile := range usage.Profiles {
			profiles = append(profiles, PublicChatModel3DUsageProfile{ID: profile.ID, Label: profile.Label})
		}
		publicChat.Model3DUsage = &PublicChatModel3DUsageSchema{
			Profiles: profiles,
			Brief:    PublicChatProductionBriefSchema{Fields: append([]ChatProductionBriefField(nil), usage.Brief.Fields...)},
		}
	}
	if usage := cloned.Chat.Image2DUsage; len(usage.Profiles) > 0 {
		profiles := make([]PublicChatImage2DUsageProfile, 0, len(usage.Profiles))
		for _, profile := range usage.Profiles {
			profiles = append(profiles, PublicChatImage2DUsageProfile{ID: profile.ID, Label: profile.Label})
		}
		publicChat.Image2DUsage = &PublicChatImage2DUsageSchema{Profiles: profiles}
	}
	publicChat.ApprovalPolicy = PublicChatApprovalPolicy{
		DefaultMode:                cloned.Chat.ApprovalPolicy.DefaultMode,
		Modes:                      cloned.Chat.ApprovalPolicy.Modes,
		ManualApprovalCapabilities: cloned.Chat.ApprovalPolicy.ManualApprovalCapabilities,
	}
	for _, state := range cloned.Chat.ApprovalPolicy.Budget.States {
		publicChat.ApprovalPolicy.BudgetStates = append(publicChat.ApprovalPolicy.BudgetStates,
			PublicChatApprovalBudgetState{ID: state.ID, Role: state.Role})
	}
	publicChat.ContextCompaction = PublicChatContextCompaction{
		Enabled:            cloned.Chat.ContextCompaction.Enabled,
		AutoThresholdRatio: cloned.Chat.ContextCompaction.AutoThresholdRatio,
		WarningRatio:       cloned.Chat.ContextCompaction.WarningRatio,
		KeepRecentRounds:   cloned.Chat.ContextCompaction.KeepRecentRounds,
	}
	publicChat.AgentScope.DefaultMode = cloned.Chat.AgentScope.DefaultMode
	publicChat.AgentScope.Modes = make([]PublicChatAgentScopeMode, 0, len(cloned.Chat.AgentScope.Modes))
	for _, mode := range cloned.Chat.AgentScope.Modes {
		publicChat.AgentScope.Modes = append(publicChat.AgentScope.Modes, PublicChatAgentScopeMode{
			ID: mode.ID, WorkspaceDomains: mode.WorkspaceDomains,
		})
	}
	publicChat.API.Fields = cloned.Chat.API.Fields
	publicChat.API.ResponseFields = cloned.Chat.API.ResponseFields
	publicChat.API.Attachment = cloned.Chat.API.Attachment
	publicChat.API.Preference = cloned.Chat.API.Preference
	publicChat.API.StyleLock = cloned.Chat.API.StyleLock
	// #241: the browser needs the bounds and the declared reason ids; the
	// framing templates, markers, item templates and prompt paragraph stay
	// server-only.
	if preferences := cloned.Chat.Preferences; preferences.MaximumEntries > 0 {
		ids := make([]string, 0, len(preferences.ReasonPresets))
		for _, preset := range preferences.ReasonPresets {
			ids = append(ids, preset.ID)
		}
		publicChat.Preferences = &PublicChatPreferencesSchema{
			MaximumEntries:             preferences.MaximumEntries,
			MaximumDirectionCharacters: preferences.MaximumDirectionCharacters,
			MaximumReasonCharacters:    preferences.MaximumReasonCharacters,
			ReasonPresets:              ids,
		}
	}
	publicChat.StyleLock.MaximumTextCharacters = cloned.Chat.StyleLock.MaximumTextCharacters
	publicChat.StyleLock.MaximumReferenceImages = cloned.Chat.StyleLock.MaximumReferenceImages
	publicChat.StyleLock.Sources = append([]string(nil), cloned.Chat.StyleLock.Sources...)
	publicChat.API.PromptEnhance.PromptField = cloned.Chat.API.PromptEnhance.PromptField
	publicChat.API.PromptEnhance.CapabilityField = cloned.Chat.API.PromptEnhance.CapabilityField
	publicChat.API.PromptEnhance.ModelField = cloned.Chat.API.PromptEnhance.ModelField
	publicChat.API.PromptEnhance.ResultField = cloned.Chat.API.PromptEnhance.ResultField
	publicChat.API.PromptEnhance.PromptResultField = cloned.Chat.API.PromptEnhance.PromptResultField
	publicChat.API.PromptEnhance.NegativePromptResultField = cloned.Chat.API.PromptEnhance.NegativePromptResultField
	publicChat.API.PromptEnhance.ErrorField = cloned.Chat.API.PromptEnhance.ErrorField
	publicChat.Context.ImageIndexBase = cloned.Chat.Context.ImageIndexBase
	publicChat.Context.MaximumSessionNotesCharacters = cloned.Chat.Context.MaximumSessionNotesCharacters
	publicChat.Context.CanvasInputSource = cloned.Chat.Context.CanvasInputSource
	publicChat.Context.ReferenceFilename = cloned.Chat.Context.ReferenceFilename
	publicChat.Skills.Fields = cloned.Chat.Skills.Fields
	publicChat.Skills.Creation = cloned.Chat.Skills.Creation
	publicChat.Skills.Categories = cloned.Chat.Skills.Categories
	publicCanvas := PublicCanvasSchema{
		API:          cloned.Canvas.API,
		Limits:       cloned.Canvas.Limits,
		Frontend:     cloned.Canvas.Frontend,
		Presentation: cloned.Canvas.Presentation,
	}
	publicCanvas.Defaults.BGStyle = cloned.Canvas.Defaults.BGStyle
	publicCanvas.Defaults.CoordinateVersion = cloned.Canvas.Defaults.CoordinateVersion
	publicAuth := PublicAuthenticationSchema{
		API: PublicAuthenticationAPI{
			IDField:       cloned.Authentication.API.IDField,
			UsernameField: cloned.Authentication.API.UsernameField,
			PasswordField: cloned.Authentication.API.PasswordField,
			NameField:     cloned.Authentication.API.NameField,
			EmailField:    cloned.Authentication.API.EmailField,
			ErrorField:    cloned.Authentication.API.ErrorField,
			UserField:     cloned.Authentication.API.UserField,
			IsAdminField:  cloned.Authentication.API.IsAdminField,
		},
		PublicPaths:  cloned.Authentication.PublicPaths,
		SessionRetry: cloned.Authentication.SessionRetry,
		Messages:     cloned.Authentication.Messages,
		Login:        cloned.Authentication.Login,
	}
	deliveryFormats := make([]PublicDeliveryFormat, 0, len(cloned.ModelDelivery.Formats))
	for _, format := range cloned.ModelDelivery.Formats {
		deliveryFormats = append(deliveryFormats, PublicDeliveryFormat{ID: format.ID, Label: format.Label})
	}
	var publicProject *PublicProjectSchema
	if cloned.Project != nil {
		fields := make([]string, 0, len(cloned.Project.Conventions.Fields))
		for _, field := range cloned.Project.Conventions.Fields {
			fields = append(fields, field.Name)
		}
		texts := make([]string, 0, len(cloned.Project.Conventions.Texts))
		for _, text := range cloned.Project.Conventions.Texts {
			texts = append(texts, text.Name)
		}
		publicProject = &PublicProjectSchema{
			Limits:      cloned.Project.Limits,
			Assets:      cloned.Project.Assets,
			StyleGuide:  cloned.Project.StyleGuide,
			Conventions: PublicProjectConventionsSchema{Fields: fields, Texts: texts},
		}
	}
	return PublicApplicationSchema{
		AgentConfiguration: cloned.AgentConfiguration.Public(),
		LLMSettings:        cloned.LLMSettings.Public(),
		Revision:           source.Revision,
		Runtime: PublicRuntimeSchema{
			HTTP: PublicHTTPRuntimeSchema{
				Routes:                cloned.Runtime.HTTP.Routes,
				LegacyRoutes:          cloned.Runtime.HTTP.LegacyRoutes,
				LegacyProviderBalance: cloned.Runtime.HTTP.LegacyProviderBalance,
			},
			InputAssets: PublicInputAssetRuntimeSchema{
				ModelProfile:     cloned.Runtime.InputAssets.ModelProfile,
				UploadFieldName:  cloned.Runtime.InputAssets.UploadFieldName,
				UploadMaxBytes:   cloned.Runtime.InputAssets.UploadMaxBytes,
				DefaultAssetType: cloned.Runtime.InputAssets.DefaultAssetType,
				API:              cloned.Runtime.InputAssets.API,
				TypeRules:        cloned.Runtime.InputAssets.TypeRules,
			},
			Cache: PublicCacheRuntimeSchema{
				BytesPerMegabyte: cloned.Runtime.Cache.BytesPerMegabyte,
				Fields:           cloned.Runtime.Cache.Fields,
			},
			AssetDelivery: publicAssetDelivery(cloned.Runtime.AssetDelivery, publicProxy),
			Frontend:      cloned.Runtime.Frontend,
			GenerationMessages: PublicGenerationMessagesSchema{
				ReferenceLimit:    cloned.Runtime.GenerationMessages.ReferenceLimit,
				ReferenceLimitOne: cloned.Runtime.GenerationMessages.ReferenceLimitOne,
				ReferenceCapacity: cloned.Runtime.GenerationMessages.ReferenceCapacity,

				ReferenceCapacityMultiview: cloned.Runtime.GenerationMessages.ReferenceCapacityMultiview,
			},
		},
		Content:     cloned.Content,
		Chat:        publicChat,
		Canvas:      publicCanvas,
		TaskHistory: cloned.TaskHistory,
		TaskStream:  PublicTaskStreamSchema{Aliases: cloned.TaskStream.Aliases},
		Administration: PublicAdministrationSchema{
			Navigation: cloned.Administration.Navigation,
			Users: PublicAdministrationUsersSchema{
				API: cloned.Administration.Users.API, Limits: cloned.Administration.Users.Limits,
				Messages: cloned.Administration.Users.Messages, Presentation: cloned.Administration.Users.Presentation,
			},
			AuditLogs: PublicAdministrationAuditLogsSchema{
				API: cloned.Administration.AuditLogs.API, Limits: cloned.Administration.AuditLogs.Limits,
				Messages: cloned.Administration.AuditLogs.Messages, Presentation: cloned.Administration.AuditLogs.Presentation,
			},
			Generations: PublicAdministrationGenerationsSchema{
				API: cloned.Administration.Generations.API, Limits: cloned.Administration.Generations.Limits,
				Assets: PublicAdministrationGenerationAssets{
					DownloadFilenameTemplate:   cloned.Administration.Generations.Assets.DownloadFilenameTemplate,
					RemoteThumbnailURLQuery:    cloned.Administration.Generations.Assets.RemoteThumbnailURLQuery,
					RemoteThumbnailTaskIDQuery: cloned.Administration.Generations.Assets.RemoteThumbnailTaskIDQuery,
					PreviewImageExtensions:     cloned.Administration.Generations.Assets.PreviewImageExtensions,
				},
				Messages: cloned.Administration.Generations.Messages, Presentation: cloned.Administration.Generations.Presentation,
			},
			Usage: PublicAdministrationUsageSchema{
				API: cloned.Administration.Usage.API, Defaults: cloned.Administration.Usage.Defaults,
				Messages: cloned.Administration.Usage.Messages, Presentation: cloned.Administration.Usage.Presentation,
			},
			UserAssets: PublicAdministrationUserAssetsSchema{
				API: cloned.Administration.UserAssets.API, Limits: cloned.Administration.UserAssets.Limits,
				Filters: cloned.Administration.UserAssets.Filters, Messages: cloned.Administration.UserAssets.Messages,
				Presentation: cloned.Administration.UserAssets.Presentation,
			},
		},
		Capabilities:    cloned.Capabilities,
		TaskStates:      cloned.TaskStates,
		AuditActions:    cloned.AuditActions,
		DeliveryFormats: deliveryFormats,
		Authentication:  publicAuth,
		Localization:    source.publicLocalization(),
		Project:         publicProject,
	}
}

// PublicEventWhitelist 构建 事件类型 → 允许公开字段集合 的查找索引，
// 供传输层消费（消费方不得另行建立第二真源）。
func (schema TaskEventsRuntimeSchema) PublicEventWhitelist() map[string]map[string]struct{} {
	whitelist := make(
		map[string]map[string]struct{},
		len(schema.PublicProjection),
	)
	for _, entry := range schema.PublicProjection {
		fields := make(map[string]struct{}, len(entry.Fields))
		for _, field := range entry.Fields {
			fields[field] = struct{}{}
		}
		whitelist[entry.Type] = fields
	}
	return whitelist
}

func firstOrEmpty(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func publicAssetDelivery(source AssetDeliveryRuntimeSchema, proxy PublicAssetProxyRuntimeSchema) PublicAssetDeliveryRuntimeSchema {
	delivery := PublicAssetDeliveryRuntimeSchema{Proxy: proxy}
	delivery.AssetPreview.Query = source.AssetPreview.Query
	delivery.AssetPreview.RequestValue = source.AssetPreview.RequestValue
	return delivery
}
