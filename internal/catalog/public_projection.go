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
	publicChat.ApprovalPolicy = PublicChatApprovalPolicy{
		DefaultMode:                cloned.Chat.ApprovalPolicy.DefaultMode,
		Modes:                      cloned.Chat.ApprovalPolicy.Modes,
		ManualApprovalCapabilities: cloned.Chat.ApprovalPolicy.ManualApprovalCapabilities,
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
