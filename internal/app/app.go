package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/access"
	"github.com/LeXwDeX/Plinth/internal/adapters/catalogrecipes"
	"github.com/LeXwDeX/Plinth/internal/adapters/legacyassetproxy"
	"github.com/LeXwDeX/Plinth/internal/adapters/legacygeneration"
	"github.com/LeXwDeX/Plinth/internal/adapters/mcpagent"
	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/administration"
	"github.com/LeXwDeX/Plinth/internal/agentconfiguration"
	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/agentscope"
	"github.com/LeXwDeX/Plinth/internal/approvalpolicy"
	"github.com/LeXwDeX/Plinth/internal/assetdelivery"
	inputassets "github.com/LeXwDeX/Plinth/internal/assets/input"
	"github.com/LeXwDeX/Plinth/internal/assets/legacygenerated"
	"github.com/LeXwDeX/Plinth/internal/assets/legacyinput"
	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/assets/modelsource"
	"github.com/LeXwDeX/Plinth/internal/assets/visionimage"
	"github.com/LeXwDeX/Plinth/internal/authentication"
	"github.com/LeXwDeX/Plinth/internal/cachemanagement"
	"github.com/LeXwDeX/Plinth/internal/canvas"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/agentparams"
	"github.com/LeXwDeX/Plinth/internal/chat/agenttools"
	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/chat/references"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/harness/resolve"
	"github.com/LeXwDeX/Plinth/internal/legacythumbnail"
	"github.com/LeXwDeX/Plinth/internal/lineage"
	"github.com/LeXwDeX/Plinth/internal/llmsettings"
	"github.com/LeXwDeX/Plinth/internal/modeldelivery"
	"github.com/LeXwDeX/Plinth/internal/pendingapproval"
	"github.com/LeXwDeX/Plinth/internal/processing"
	"github.com/LeXwDeX/Plinth/internal/promptenhance"
	"github.com/LeXwDeX/Plinth/internal/provideraccounts"
	provideraccounthttpjson "github.com/LeXwDeX/Plinth/internal/provideraccounts/httpjson"
	"github.com/LeXwDeX/Plinth/internal/providers"
	"github.com/LeXwDeX/Plinth/internal/rendermanifest"
	"github.com/LeXwDeX/Plinth/internal/scene"
	"github.com/LeXwDeX/Plinth/internal/skillrun"
	"github.com/LeXwDeX/Plinth/internal/taskretry"
	"github.com/LeXwDeX/Plinth/internal/threeview"
	"github.com/LeXwDeX/Plinth/internal/transport/httpapi"
	"github.com/LeXwDeX/Plinth/internal/transport/webui"
)

type Application struct {
	readiness  *readinessState
	config     Config
	logger     *slog.Logger
	schema     catalog.ApplicationSchema
	generation *generation.Module
	thumbnails *legacythumbnail.Service
	chatAgent  *chat.Orchestrator
	processing *processing.Service
	// pendingApprovals finishes Agent approvals decided after their turn ended.
	pendingApprovals *pendingapproval.Service
	scene            *scene.Service
	manifests        *rendermanifest.Service
	server           *http.Server
}

type renderAssetStore struct {
	inputs   *inputassets.Service
	maxBytes int64
}

func (s renderAssetStore) OpenOwned(ctx context.Context, owner, id string) (io.ReadCloser, error) {
	_, body, err := s.inputs.OpenOwned(ctx, generation.OwnerID(owner), generation.AssetID(id))
	return body, err
}
func (s renderAssetStore) UploadPNG(ctx context.Context, owner string, pixels []byte) (string, error) {
	asset, err := s.inputs.UploadAsset(ctx, generation.OwnerID(owner), generation.InputAssetUpload{
		Body: bytes.NewReader(pixels), MediaType: "image/png", OriginalFilename: "render-frame.png",
		AssetType: "render-frame", MaxBytes: s.maxBytes})
	return string(asset.ID), err
}

type preparedSceneContext struct {
	*rendermanifest.Service
	scene *scene.Service
}

func (p preparedSceneContext) VerifyPreparedContext(ctx context.Context, owner generation.OwnerID,
	sessionID, sceneID, manifestID, digest string) error {
	return p.Service.VerifyPreparedContext(ctx, string(owner), sessionID, sceneID, manifestID, digest)
}

func (p preparedSceneContext) VerifiedPreparedOutput(ctx context.Context, owner generation.OwnerID,
	sessionID, sceneID, manifestID, digest string) (generation.PreparedContextOutput, error) {
	output, err := p.Service.VerifiedPreparedOutput(ctx, string(owner), sessionID, sceneID, manifestID, digest)
	if err != nil {
		return generation.PreparedContextOutput{}, err
	}
	return generation.PreparedContextOutput{AssetID: generation.AssetID(output.AssetID),
		SHA256: output.SHA256, Size: output.SizeBytes}, nil
}

func (p preparedSceneContext) ResolvePreparedSource(ctx context.Context, owner generation.OwnerID,
	sessionID, objectID string, revision int64, input json.RawMessage) (string, error) {
	return p.scene.ResolvePreparedSource(ctx, string(owner), sessionID, objectID, revision, input)
}

// componentCount is the number of background components Run starts: the
// coordinator, the chat agent, processing plans, the pending-approval sweep,
// scene reconciliation and the HTTP server. Run waits for each to return.
const componentCount = 6

type componentResult struct {
	name string
	err  error
}

// processingCatalog 把 catalog 的声明与 generation Module 的运行时可用性
// 组合成 processing.Catalog：Resume 由此检查供给是否可用。
type processingCatalog struct {
	*catalog.Catalog
	module *generation.Module
}

func (p processingCatalog) OfferingAvailable(id generation.OfferingID) bool {
	return p.module.OfferingAvailable(id)
}

// assemblyOverrides is deliberately private: test binaries may decorate ports,
// while every ordinary application uses the same unmodified assembly.
type assemblyOverrides struct {
	providers         func(generation.ProviderRegistry) generation.ProviderRegistry
	processingGateway func(processing.Gateway) processing.Gateway
	taskAdmissions    func(generation.TaskAdmissionStore)
	// httpTransport decorates each declared connection's HTTP transport. The
	// connection ID names the Schema connection; Tripo's anonymous source uses
	// its own "<connectionID>.native-source" call.
	httpTransport func(connectionID string, base http.RoundTripper) http.RoundTripper
}

func New(config Config, logger *slog.Logger) (*Application, error) {
	return newWithDependencies(config, logger, assemblyOverrides{})
}

func newWithDependencies(config Config, logger *slog.Logger, overrides assemblyOverrides) (*Application, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.Default()
	}
	taskCatalog, err := loadCatalog(config.CatalogFile)
	if err != nil {
		return nil, err
	}
	skillCatalog, err := loadSkillCatalog(config.SkillCatalogFile)
	if err != nil {
		return nil, err
	}
	applicationSchema := taskCatalog.Application()
	if err := catalog.ValidateSkillCatalogAgainstProductSchema(
		skillCatalog,
		applicationSchema,
		taskCatalog.List(""),
	); err != nil {
		return nil, fmt.Errorf(
			"Skill Catalog 与 Product Schema 不一致: %w",
			err,
		)
	}
	legacyGeneratedImages, err := legacygenerated.New(
		config.LegacyGeneratedImagesRoot,
	)
	if err != nil {
		return nil, err
	}
	accessPolicy, err := buildAccessPolicy(applicationSchema.Authentication)
	if err != nil {
		return nil, err
	}
	repository, err := sqlite.Open(config.DatabasePath)
	if err != nil {
		return nil, err
	}
	closeRepository := true
	defer func() {
		if closeRepository {
			_ = repository.Close()
		}
	}()
	assetStore, err := local.New(config.StorageRoot)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeRepository {
			_ = assetStore.Close()
		}
	}()
	correctStoredAssetLabels(repository, assetStore, logger)
	modelValidator, modelPolicy, err := modelInputValidation(applicationSchema.Runtime.InputAssets.ModelProfile)
	if err != nil {
		return nil, err
	}
	inputAssetService, err := inputassets.NewWithConfig(
		repository,
		assetStore,
		inputassets.Config{
			UploadValidator: modelValidator,
			PublicBaseURL:   config.PublicBaseURL,
			SigningKey:      config.AssetSigningKey,
			ProviderAssetRoute: applicationSchema.Runtime.HTTP.Routes.
				ProviderAsset,
			Messages: inputImageMessages(applicationSchema.Runtime.InputAssets),
			Fitting:  inputImageFitting(applicationSchema.Runtime.InputAssets),
		},
	)
	if err != nil {
		return nil, err
	}
	var generationInputs generation.InputAssetResolver = inputAssetService
	var uploadedModels resolve.UploadedModels
	var modelPreviews httpapi.ModelPreviewReader
	if applicationSchema.Runtime.InputAssets.ModelProfile != nil {
		models, err := modelsource.New(inputAssetService, modelPolicy)
		if err != nil {
			return nil, err
		}
		generationInputs = models
		uploadedModels = models
		modelPreviews = models
	}
	legacyInputAssetService, err := legacyinput.New(
		legacyinput.Config{Root: configuredRuntimeSetting(
			config,
			applicationSchema,
			applicationSchema.Runtime.InputAssets.LegacyStorageRootSetting,
		)},
		repository,
	)
	if err != nil {
		return nil, err
	}
	compatibleInputAssetService, err := inputassets.NewCompatible(
		inputAssetService,
		legacyInputAssetService,
	)
	if err != nil {
		return nil, err
	}
	cacheService, err := cachemanagement.New(cachemanagement.Config{
		Stats: repository,
	})
	if err != nil {
		return nil, err
	}
	approvalPolicy := approvalPolicyFromSchema(applicationSchema)
	chatService, err := chat.NewService(chat.ServiceConfig{
		ApprovalPolicy:      approvalPolicy,
		AgentScope:          agentscope.FromSchema(applicationSchema.Chat.AgentScope),
		Handles:             chat.NewHandlePolicy(applicationSchema.Chat.Context),
		MaximumSessionNotes: applicationSchema.Chat.Context.MaximumSessionNotesCharacters,
		CanvasInputSource:   applicationSchema.Chat.Context.CanvasInputSource,
		// A turn's pick of a retired generation model runs on its successor
		// (#329 follow-up).
		RetiredModels: taskCatalog.RetiredModelSuccessors(),
		// The deployment's chat model, its thinking profile and context
		// window (#322).
		Models: chat.ModelPolicy{
			Model:          configuredRuntimeSetting(config, applicationSchema, applicationSchema.Chat.Agent.ModelSetting),
			Thinking:       applicationSchema.Chat.Agent.Thinking,
			WindowOverride: config.ContextWindowTokens,
		},
		Repository:      repository,
		DefaultTitle:    applicationSchema.Chat.Defaults.Title,
		UserRole:        applicationSchema.Chat.Roles.User,
		AssistantRole:   applicationSchema.Chat.Roles.Assistant,
		PendingContent:  applicationSchema.Chat.State.PendingContent,
		StreamActive:    applicationSchema.Chat.State.Active,
		StreamDone:      applicationSchema.Chat.State.Done,
		StreamError:     applicationSchema.Chat.State.Error,
		TurnQueued:      applicationSchema.Chat.State.TurnQueued,
		TurnRunning:     applicationSchema.Chat.State.TurnRunning,
		TurnWaitingTask: applicationSchema.Chat.State.TurnWaitingTask,
		TurnDone:        applicationSchema.Chat.State.TurnDone,
		TurnError:       applicationSchema.Chat.State.TurnError,
		PlaceholderHealing: chat.PlaceholderHealingPolicy{
			StaleAfter: time.Duration(
				applicationSchema.Chat.State.PlaceholderStaleMilliseconds,
			) * time.Millisecond,
			PromptArgument: applicationSchema.Chat.Context.
				ToolPromptArgument,
			PromptPreviewCharacters: applicationSchema.Chat.State.
				FailedPromptPreviewChars,
			InterruptedMessage: applicationSchema.Chat.Presentation.Messages.
				Interrupted,
			InterruptedPromptTemplate: applicationSchema.Chat.Presentation.
				Messages.InterruptedPrompt,
		},
		StreamCleanupTTL: time.Duration(
			applicationSchema.Chat.Stream.CleanupTTLMilliseconds,
		) * time.Millisecond,
		SubscriberBufferSize: applicationSchema.Chat.Stream.
			SubscriberBufferSize,
		EventFields: chat.EventFieldPolicy{
			Type:      applicationSchema.Chat.Stream.EventFields.Type,
			MessageID: applicationSchema.Chat.Stream.EventFields.MessageID,
		},
		EventTypes: chat.EventTypePolicy{
			MessageCreated: applicationSchema.Chat.Stream.EventTypes.
				MessageCreated,
		},
	})
	if err != nil {
		return nil, err
	}
	canvasService, err := canvas.New(canvas.Config{
		Repository:         repository,
		Sessions:           chatService,
		DefaultName:        applicationSchema.Canvas.Defaults.ProjectName,
		DefaultViewport:    applicationSchema.Canvas.Defaults.Viewport,
		DefaultBGStyle:     applicationSchema.Canvas.Defaults.BGStyle,
		ProjectIDPrefix:    applicationSchema.Canvas.Defaults.ProjectIDPrefix,
		MaximumBitmapNodes: applicationSchema.Canvas.Limits.MaximumBitmapNodes,
		MaximumPlacedResults: applicationSchema.Canvas.Limits.
			MaximumPlacedResults,
		MaximumPlacedResultKeyCharacters: applicationSchema.Canvas.Limits.
			MaximumPlacedResultKeyCharacters,
		MinimumFrameDimension: applicationSchema.Canvas.Limits.MinimumDimension,
		MaximumFrameDimension: applicationSchema.Canvas.Limits.MaximumDimension,
		CoordinateVersion:     applicationSchema.Canvas.Defaults.CoordinateVersion,
		NodeDefaults: canvas.Node{
			X:       applicationSchema.Canvas.Defaults.NodeX,
			Y:       applicationSchema.Canvas.Defaults.NodeY,
			Width:   applicationSchema.Canvas.Defaults.NodeWidth,
			Height:  applicationSchema.Canvas.Defaults.NodeHeight,
			ZIndex:  applicationSchema.Canvas.Defaults.NodeZIndex,
			Data:    applicationSchema.Canvas.Defaults.NodeData,
			Visible: applicationSchema.Canvas.Defaults.NodeVisible,
			Locked:  applicationSchema.Canvas.Defaults.NodeLocked,
		},
	})
	if err != nil {
		return nil, err
	}
	sceneRepository, err := repository.SceneRepository(sqlite.SQLiteSceneEvidence{})
	if err != nil {
		return nil, err
	}
	sceneService, err := scene.New(sceneRepository)
	if err != nil {
		return nil, err
	}
	renderAssets := renderAssetStore{inputs: inputAssetService,
		maxBytes: applicationSchema.Runtime.HTTP.MultipartBodyMaxBytes}
	maxRenderPixels := int64(applicationSchema.Canvas.Limits.MaximumDimension) *
		int64(applicationSchema.Canvas.Limits.MaximumDimension)
	proof, err := rendermanifest.NewVerifier(sceneService, renderAssets, maxRenderPixels, renderAssets.maxBytes)
	if err != nil {
		return nil, err
	}
	manifestRepository, err := repository.RenderManifestRepository(sqlite.RenderManifestRepositoryConfig{
		Bytes: renderAssets, Verifier: proof, Evidence: sqlite.SQLiteRenderManifestEvidence{},
		MaxOutputBytes: renderAssets.maxBytes})
	if err != nil {
		return nil, err
	}
	manifestService, err := rendermanifest.New(sceneService, renderAssets, manifestRepository,
		maxRenderPixels, renderAssets.maxBytes)
	if err != nil {
		return nil, err
	}
	registry := providers.NewRegistry()
	if err := registerProvidersWithTransport(config, taskCatalog, registry, inputAssetService, assetStore, logger, overrides.httpTransport, repository); err != nil {
		return nil, err
	}
	var providerRegistry generation.ProviderRegistry = registry
	if overrides.providers != nil {
		providerRegistry = overrides.providers(providerRegistry)
	}
	if overrides.taskAdmissions != nil {
		overrides.taskAdmissions(repository)
	}
	module, err := generation.New(generation.Dependencies{
		Repository:                 repository,
		Catalog:                    taskCatalog,
		Providers:                  providerRegistry,
		Assets:                     assetStore,
		Inputs:                     generationInputs,
		InputFitter:                inputAssetService,
		PreparedContext:            preparedSceneContext{Service: manifestService, scene: sceneService},
		SchemaVersion:              applicationSchema.Revision,
		Cache:                      cacheService,
		Logger:                     logger,
		Conversions:                modeldelivery.NewSchemaConversionRule(applicationSchema.ModelDelivery),
		DispatchCheckpointMaxBytes: applicationSchema.Runtime.Coordinator.DispatchCheckpointMaxBytes,
		DefaultRequestTimeout: time.Duration(
			applicationSchema.Runtime.Coordinator.DefaultRequestTimeoutSeconds,
		) * time.Second,
		PerOwnerMaxInflightTasks: applicationSchema.Runtime.HTTP.TaskAdmission.PerOwnerMaxInflightTasks,
	})
	if err != nil {
		return nil, err
	}
	captureStore, err := repository.CaptureRepository(sqlite.CaptureRepositoryConfig{
		Bytes:     captureInputBytes{inputs: inputAssetService},
		MaxBytes:  applicationSchema.Runtime.InputAssets.UploadMaxBytes,
		MaxPixels: applicationSchema.Runtime.Frontend.ImageUpload.MaximumPixels,
	})
	if err != nil {
		return nil, err
	}
	// #292: a picture over vision_inline_max_bytes reaches the model as a
	// downscaled copy; generation references never use it.
	visionDerivative, err := visionimage.New(applicationSchema.Chat.Context.VisionDerivative)
	if err != nil {
		return nil, err
	}
	referenceResolver, err := references.New(references.Config{
		Inputs: inputAssetService,
		Tasks:  module,
		Legacy: legacyGeneratedImages,
		InputAssetRoutes: []string{
			applicationSchema.Runtime.HTTP.Routes.AssetItem,
			applicationSchema.Runtime.HTTP.LegacyRoutes.AssetItem,
		},
		TaskAssetRoute: applicationSchema.Runtime.HTTP.Routes.TaskAsset,
		GeneratedImageRoute: applicationSchema.Runtime.HTTP.
			LegacyRoutes.GeneratedImage,
		PublicBaseURL:  config.PublicBaseURL,
		UploadMaxBytes: applicationSchema.Runtime.InputAssets.UploadMaxBytes,
		GeneratedReferenceMaxBytes: applicationSchema.Runtime.InputAssets.
			GeneratedReferenceMaxBytes,
		GeneratedReferenceTooLargeTemplate: applicationSchema.Runtime.InputAssets.
			Messages.GeneratedReferenceTooLargeTemplate,
		DefaultMediaType: applicationSchema.Runtime.InputAssets.DefaultMediaType,
		DefaultAssetType: applicationSchema.Runtime.InputAssets.DefaultAssetType,
		ReferenceFilename: applicationSchema.Chat.Context.
			ReferenceFilename,
		LegacyGeneratedImage: applicationSchema.Runtime.AssetDelivery.
			LegacyGeneratedImage,
		VisionInlineMaxBytes: applicationSchema.Chat.Context.
			VisionInlineMaxBytes,
		VisionFallbackMediaType: applicationSchema.Runtime.InputAssets.
			DefaultMediaType,
		VisionDerivative: visionDerivative,
		Captures:         captureStore,
		ModelAssetRole:   generationtool.ModelAssetRole,
	})
	if err != nil {
		return nil, err
	}
	chatService.SetAttachmentVerifier(referenceResolver)
	// In-chat choice cards (#321): processing tools ask the person which service to use.
	chatService.SetChoices(repository, applicationSchema.Chat.Choice.ListLimit)
	// Session thinking level, context usage and compactions (#322).
	chatService.SetContextStore(repository, applicationSchema.Chat.ContextCompaction.ListLimit)
	harnessResolver := &resolve.Service{UploadedModels: uploadedModels, Store: repository, Schema: applicationSchema.Chat.Harness, Tools: applicationSchema.Chat.Tools, Catalog: taskCatalog, Gateway: module}
	chatService.SetHarnessSnapshotResolver(harnessResolver.Snapshot)
	canvasImageService, err := canvas.NewImageService(
		canvas.ImageServiceConfig{
			References:        referenceResolver,
			Assets:            inputAssetService,
			MaximumNodes:      applicationSchema.Canvas.Limits.MaximumMergeNodes,
			MaximumDimension:  applicationSchema.Canvas.Limits.MaximumDimension,
			MaximumAssetBytes: applicationSchema.Runtime.HTTP.MultipartBodyMaxBytes,
			FilenamePrefix: applicationSchema.Canvas.Defaults.
				MergeFilenamePrefix,
			Extension: applicationSchema.Canvas.Defaults.MergeExtension,
			MediaType: applicationSchema.Canvas.Defaults.MergeMediaType,
			AssetType: applicationSchema.Canvas.Defaults.MergeAssetType,
		},
	)
	if err != nil {
		return nil, err
	}
	agentConfigurationOptions := []agentconfiguration.Option{agentconfiguration.WithPersonalSkills(repository)}
	if config.SkillCatalogFile == "" {
		agentConfigurationOptions = append(agentConfigurationOptions, agentconfiguration.WithRetiredBuiltInSkills())
	}
	agentConfigurationService, err := agentconfiguration.New(repository, applicationSchema, skillCatalog, taskCatalog.List(""), agentConfigurationOptions...)
	if err != nil {
		return nil, err
	}
	if err := agentConfigurationService.ReconcileRetiredBuiltInSkills(context.Background()); err != nil {
		return nil, fmt.Errorf("退役内建 Skill 失败: %w", err)
	}
	// One grant service for every paid-step entry point. The session approval
	// mode and the Schema policy decide whether a step waits for a person.
	executionGrants := &executiongrant.Service{
		Grants: repository, Prepared: repository,
		Policy: approvalPolicy, Modes: chatService,
		// An Agent approval stays approvable for the Schema's duration; after
		// that it can only be requested again in a new turn.
		PendingTTL: time.Duration(applicationSchema.Chat.PendingApprovals.TTLSeconds) * time.Second,
	}
	paramsStore := agentparams.NewPersistentParameterStore(repository, logger)
	// One localizer serves the Agent turn and its tool results.
	texts, err := catalog.NewTextLocalizer(applicationSchema)
	if err != nil {
		return nil, err
	}
	toolExecutor, err := generationtool.New(generationtool.Config{
		Logger:          logger,
		Choices:         repository,
		Harness:         repository,
		HarnessResolver: harnessResolver,
		Texts:           texts,
		OfferingParameterReasons: func(ownerID, sessionID, offeringID string) map[string]generation.ParameterReason {
			reasons := map[string]generation.ParameterReason{}
			for name, state := range paramsStore.OfferingStates(agentparams.ScopeKey(ownerID, sessionID), offeringID) {
				if state.Source == "agent" && state.Reason != "" {
					reasons[name] = generation.ParameterReason{Value: state.Value, Reason: state.Reason}
				}
			}
			return reasons
		},
		OfferingParameters: func(ownerID, sessionID, offeringID string) map[string]json.RawMessage {
			scope := agentparams.ScopeKey(ownerID, sessionID)
			values := paramsStore.OfferingOverrides(scope, offeringID)
			states := paramsStore.OfferingStates(scope, offeringID)
			offering, ok := taskCatalog.Get(generation.OfferingID(offeringID))
			if !ok {
				return nil
			}
			for _, p := range offering.Parameters {
				if states[p.Name].Source == "user" && !offering.UserParameterAllowed(p) {
					delete(values, p.Name)
				}
			}
			return values
		},
		Gateway:       module,
		Tasks:         module,
		Operations:    repository,
		Grants:        executionGrants,
		StopRequested: chatService.TurnStopRequested,
		References:    referenceResolver,
		Chat:          applicationSchema.Chat,
		ModelDelivery: applicationSchema.ModelDelivery,
		Skills:        skillCatalog,
		TaskStates:    applicationSchema.TaskStates,
	})
	if err != nil {
		return nil, err
	}
	// B10：agent 状态（参数/绑定/同意）写穿透 SQLite——重启后同意
	// 门禁仍然生效；会话删除时级联清理（chat_repository.DeleteSession）。
	agentStateStore := agentparams.NewPersistentStore(repository, logger)
	// 付费同意硬门禁按真实计费侧（ProviderMode）启用（AGT-03）：
	// mock Agent 也会对每条消息自动提交付费生成，门禁依据不能是
	// AgentMode，否则 live Provider + mock Agent 会免同意扣费。
	consentEnforced := config.ProviderMode != "mock"
	if consentEnforced && config.AgentMode == "mock" {
		logger.Warn(
			"live Provider 搭配 mock Agent：每条消息都会自动提交付费生成，" +
				"同意硬门禁已按计费侧强制启用",
		)
	}
	agentToolsConfig := agenttools.Config{
		Offerings:          func() []generation.Offering { return module.Offerings("") },
		Context:            applicationSchema.Chat.Context,
		HistoryLookup:      chat.NewHistoryLookupPolicy(applicationSchema.Chat),
		Params:             paramsStore,
		Generate:           toolExecutor,
		ReadTask:           newAgentTaskReader(module, applicationSchema.Chat, applicationSchema.Content.Messages, texts),
		RecoverTask:        newAgentTaskRecoverer(module, applicationSchema.Chat, applicationSchema.Content.Messages, texts),
		ListModels:         toolExecutor.ListSessionModels,
		ReadProcessingPlan: toolExecutor.ReadProcessingPlan,
		UpdateNotes:        chatService.SetSessionNotes,
		Consent: func(command chat.ToolCommand, request json.RawMessage, snapshot json.RawMessage) error {
			scope := agentparams.ScopeKey(command.OwnerID, command.SessionID)
			return agentStateStore.BeginConsent(scope, request, snapshot)
		},
		Preflight:                 toolExecutor.SelectionError,
		MaxGenerationCallsPerTurn: applicationSchema.Chat.Agent.MaxGenerationCallsPerTurn,
		ConsentApprovedContent:    applicationSchema.Chat.Agent.ConsentApprovedContent,
		ConsentDeniedContent:      applicationSchema.Chat.Agent.ConsentDeniedContent,
		ConsentWaitingContent:     applicationSchema.Chat.Agent.ConsentWaitingContent,
		ConsentBlockedContent:     applicationSchema.Chat.Agent.ConsentBlockedContent,
		ConsentReconcileContent:   applicationSchema.Chat.Agent.ConsentReconcileContent,
		ConsentRequiredContent:    applicationSchema.Chat.Agent.ConsentRequiredContent,
		ConsentLimitContent:       applicationSchema.Chat.Agent.ConsentLimitContent,
		ConsentWaitTimeout: time.Duration(
			applicationSchema.Chat.Agent.ConsentWaitTimeoutSeconds,
		) * time.Second,
		TaskObservationTimeout: time.Duration(applicationSchema.Chat.Agent.RequestTimeoutSeconds) * time.Second,
		Projection: func() json.RawMessage {
			return buildAgentOfferingsProjection(
				applicationSchema.Capabilities,
				taskCatalog,
				func(id generation.OfferingID) bool {
					return module.OfferingAvailable(id)
				},
			)
		},
	}
	if consentEnforced {
		agentToolsConfig.ConsentState = agentStateStore.Get
		agentToolsConfig.ConsentClaim = agentStateStore.ClaimConsent
		agentToolsConfig.ConsentAccepted = agentStateStore.AcceptConsent
		agentToolsConfig.ConsentRelease = agentStateStore.ReleaseConsent
		agentToolsConfig.ConsentClear = func(scope string) {
			agentStateStore.Delete(
				scope,
				agenttools.ConsentPendingKey,
				agenttools.ConsentDecisionKey,
				agenttools.ConsentApprovedKey,
				agenttools.ConsentSnapshotKey,
			)
		}
	}
	agentToolExecutor, err := agenttools.New(agentToolsConfig)
	if err != nil {
		return nil, err
	}
	llmModel := configuredRuntimeSetting(
		config,
		applicationSchema,
		applicationSchema.Chat.Agent.ModelSetting,
	)
	agentAdapter, err := buildChatAgentWithTransport(
		config,
		applicationSchema,
		overrides.httpTransport,
	)
	if err != nil {
		return nil, err
	}
	if standIn, unconfigured := agentAdapter.(agents.NotConfigured); unconfigured {
		// The public Schema tells the browser the Agent is not configured;
		// the log names the missing settings, never their values (#253).
		applicationSchema.Chat.AgentNotConfigured = true
		logger.Warn(
			"Agent 对话模型未配置，对话与提示词增强不可用",
			"missing_settings", strings.Join(standIn.Missing, ","),
		)
	}
	// The administrator's LLM settings (#322 Scope 2) decide which chat
	// models sessions may use and the levels each offers (Scope 5).
	var llmSettingsService *llmsettings.Service
	if applicationSchema.LLMSettings != nil {
		llmSettingsService, err = llmsettings.New(llmsettings.Config{
			Store:        repository,
			Connections:  llmSettingsConnections(chatConnections(config, applicationSchema), config.AgentMode == "live"),
			Thinking:     applicationSchema.Chat.Agent.Thinking,
			Prober:       connectionProber(agentAdapter, applicationSchema, logger),
			HistoryLimit: applicationSchema.LLMSettings.HistoryLimit,
		})
		if err != nil {
			return nil, err
		}
		chatService.SetModelSource(chatModelSource(llmSettingsService))
	}
	promptEnhancer, err := promptenhance.New(promptenhance.Config{
		Offerings: taskCatalog,
		Schema:    applicationSchema.Chat,
		Model:     llmModel,
		Agent:     agentAdapter,
	})
	if err != nil {
		return nil, err
	}
	visionInline := referenceResolver.OpenVisionImage
	mcpService, err := buildWorkspaceMCP(taskCatalog, module, chatService)
	if err != nil {
		return nil, err
	}
	orchestratorSchema := withToolCallBudget(
		orchestratorChatSchema(applicationSchema.Chat, llmModel),
		config.AgentMaxToolCallsPerReply,
	)
	chatAgent, err := chat.NewOrchestrator(chat.OrchestratorConfig{
		Texts: texts,
		Configuration: func(ctx context.Context, owner string) (chat.AgentConfigurationSnapshot, error) {
			snapshot, err := agentConfigurationService.SnapshotForOwner(ctx, owner)
			if err != nil {
				return chat.AgentConfigurationSnapshot{}, err
			}
			frozen := chat.AgentConfigurationSnapshot{Revision: snapshot.Revision, SystemPrompt: snapshot.Configuration.SystemPrompt, DomainContext: snapshot.Configuration.DomainContext, Skills: snapshot.Configuration.Skills}
			enabled := map[string]bool{}
			for _, name := range snapshot.Configuration.EnabledToolNames {
				enabled[name] = true
			}
			for _, tool := range applicationSchema.Chat.Tools {
				if enabled[tool.Name] {
					frozen.Tools = append(frozen.Tools, tool)
				}
			}
			for _, tool := range applicationSchema.Chat.AgentTools {
				if enabled[tool.Name] {
					frozen.AgentTools = append(frozen.AgentTools, tool)
				}
			}
			mcpTools, err := mcpService.Declarations(snapshot.Configuration.EnabledMCPPresetIDs)
			if err != nil {
				return chat.AgentConfigurationSnapshot{}, err
			}
			frozen.AgentTools = append(frozen.AgentTools, mcpTools...)
			return frozen, nil
		},
		Service: chatService,
		Schema:  orchestratorSchema,
		Model:   llmModel,
		// DSML tool calls are read per turn, for a model whose profile
		// writes them (#253, #322 Scope 5).
		DSMLFor: func(model string) bool {
			profile, _ := chatModelProfile(applicationSchema.Chat.Agent.Thinking, model)
			return applicationSchema.Chat.DSML.Enabled || profile.DSMLToolCalls
		},
		Agent:        agentAdapter,
		Tools:        mcpagent.Wrap(agentToolExecutor, mcpService),
		Logger:       logger,
		VisionInline: visionInline,
		ToolAllowed: func(capability, _ string) bool {
			// ToolExecution.domains is the creative workspace (image/model).
			// CapabilityPresentation.domain is a product category, where
			// processing contains both background removal and 3D work.
			// The registry applies the workspace rule from Schema; this gate
			// only requires an actually available Offering for the capability.
			for _, offering := range module.Offerings(generation.Capability(capability)) {
				if module.OfferingAvailable(offering.ID) {
					return true
				}
			}
			return false
		},
		OfferingAllowed: func(capability, modelID string) bool {
			for _, offering := range module.Offerings(generation.Capability(capability)) {
				if offering.ModelID == modelID && offering.Enabled && module.OfferingAvailable(offering.ID) {
					return true
				}
			}
			return false
		},
		// #329 E': a generation tool's description says how many reference
		// pictures the turn's picked model uses.
		ToolNote: toolExecutor.ToolDescriptionNote,
		TaskCanceler: func(ctx context.Context, ownerID, taskID string) error {
			_, err := module.Cancel(
				ctx,
				generation.OwnerID(ownerID),
				generation.TaskID(taskID),
			)
			return err
		},
		TaskLookup: func(ctx context.Context, ownerID, key string) (generation.Task, error) {
			return module.LookupByIdempotency(ctx, generation.OwnerID(ownerID), key)
		},
		TaskGet: func(ctx context.Context, ownerID, taskID string) (generation.Task, error) {
			return module.Get(ctx, generation.OwnerID(ownerID), generation.TaskID(taskID))
		},
		StopConsent: func(ownerID, sessionID, callID, idempotencyKey, taskID string) error {
			scope := agentparams.ScopeKey(ownerID, sessionID)
			raw := agentStateStore.Get(scope)[agentparams.ConsentClaimKey]
			if len(raw) == 0 {
				return nil
			}
			var claim struct {
				CallID string `json:"call_id"`
			}
			if err := json.Unmarshal(raw, &claim); err != nil {
				return fmt.Errorf("decode stopped turn consent claim: %w", err)
			}
			if claim.CallID != callID {
				return nil
			}
			if taskID != "" {
				return agentStateStore.AcceptConsent(scope, callID, taskID)
			}
			return agentStateStore.ReleaseConsent(scope, callID, idempotencyKey)
		},
	})
	if err != nil {
		return nil, err
	}
	fallbackIdentity, err := buildIdentity(config, accessPolicy)
	if err != nil {
		return nil, err
	}
	authenticationSchema := applicationSchema.Authentication
	if cookieName := configuredRuntimeSetting(config, applicationSchema, "session_cookie_name"); cookieName != "" {
		authenticationSchema.Cookie.Name = cookieName
	}
	authenticationService, err := authentication.New(authentication.Config{
		Schema:         authenticationSchema,
		SessionSecret:  config.SessionSecret,
		PublicAppURL:   config.FrontendPublicAppURL,
		Environment:    config.Environment,
		MockEnabled:    config.MockAuth,
		MockUserJSON:   config.MockUserJSON,
		AdminAllowlist: config.AdminAllowlist,
		Policy:         accessPolicy,
		Directory:      repository,
		Fallback:       fallbackIdentity,
	})
	if err != nil {
		return nil, err
	}
	adminBootstrap, err := authenticationService.BootstrapAdminOutcome(
		context.Background(),
		authentication.BootstrapAdmin{
			Username: config.AdminUsername,
			Password: config.AdminPassword,
			Name:     config.AdminName,
			Email:    config.AdminEmail,
			Role:     applicationSchema.Authentication.AdminRole,
			Reset:    config.AdminPasswordReset,
		},
	)
	if err != nil {
		if errors.Is(err, authentication.ErrWeakPassword) {
			// Self-deployers meet this on the very first start: name the
			// variable and the rule instead of a bare "weak password" (#251).
			return nil, fmt.Errorf(
				"ADMIN_PASSWORD 至少需要 %d 个字符（创建或重置管理员 %s 时检查）: %w",
				authenticationSchema.API.PasswordMinimumCharacters,
				config.AdminUsername,
				err,
			)
		}
		return nil, err
	}
	administrationUsers := applicationSchema.Administration.Users
	administrationAuditLogs := applicationSchema.Administration.AuditLogs
	administrationAuditService, err := administration.NewAuditService(
		administration.AuditServiceConfig{
			Repository: repository,
			DefaultPage: administrationAuditLogs.Limits.
				DefaultPage,
			DefaultPageSize: administrationAuditLogs.Limits.
				DefaultPageSize,
			MaximumPageSize: administrationAuditLogs.Limits.
				MaximumPageSize,
			IDPrefix: administrationAuditLogs.Defaults.IDPrefix,
			WriteMaximumAttempts: administrationAuditLogs.WritePolicy.
				MaximumAttempts,
			WriteRetryDelay: time.Duration(
				administrationAuditLogs.WritePolicy.RetryDelayMilliseconds,
			) * time.Millisecond,
		},
	)
	if err != nil {
		return nil, err
	}
	// #319: the bootstrap ran before the audit trail existed; report it now.
	reportAdminBootstrap(context.Background(), logger, administrationAuditService,
		authenticationSchema.API, config.AdminUsername, adminBootstrap)
	administrationGenerations :=
		applicationSchema.Administration.Generations
	legacyGenerationProjector, err := legacygeneration.NewProjector(
		legacygeneration.ProjectorConfig{
			Capabilities: applicationSchema.Capabilities,
			Aliases:      applicationSchema.TaskStream.Aliases,
			ExcludedRoles: administrationGenerations.Assets.
				DownloadExcludedRoles,
		},
	)
	if err != nil {
		return nil, err
	}
	administrationGenerationService, err :=
		administration.NewGenerationService(
			administration.GenerationServiceConfig{
				Repository: repository,
				DefaultPage: administrationGenerations.Limits.
					DefaultPage,
				DefaultPageSize: administrationGenerations.Limits.
					DefaultPageSize,
				MaximumPageSize: administrationGenerations.Limits.
					MaximumPageSize,
				NativeAssetRoute: applicationSchema.Runtime.HTTP.Routes.
					TaskAsset,
				ThumbnailAssetRole: administrationGenerations.Assets.
					ThumbnailRole,
				LegacyAssetProjector: legacyGenerationProjector,
			},
		)
	if err != nil {
		return nil, err
	}
	administrationUsage := applicationSchema.Administration.Usage
	succeededUsageStates, failedUsageStates :=
		administrationUsageStateGroups(applicationSchema.TaskStates)
	administrationUsageService, err := administration.NewUsageService(
		administration.UsageServiceConfig{
			Repository:      repository,
			DefaultDays:     administrationUsage.Defaults.Days,
			DatePattern:     administrationUsage.Validation.DatePattern,
			DateLayout:      administrationUsage.Validation.DateLayout,
			Timezone:        administrationUsage.Validation.Timezone,
			SucceededStates: succeededUsageStates,
			FailedStates:    failedUsageStates,
		},
	)
	if err != nil {
		return nil, err
	}
	nativeAdministrationGenerationAssetReader :=
		administration.GenerationAssetReaderFunc(
			func(
				ctx context.Context,
				request administration.GenerationAssetRequest,
			) (administration.GenerationAssetContent, error) {
				content, err := module.OpenAsset(
					ctx,
					generation.OwnerID(request.OwnerID),
					generation.TaskID(request.TaskID),
					generation.AssetID(request.Asset.ID),
				)
				if err != nil {
					return administration.GenerationAssetContent{}, err
				}
				return administration.GenerationAssetContent{
					Body:      content.Body,
					MediaType: content.MediaType,
					Size:      content.Size,
				}, nil
			},
		)
	administrationGenerationAssetReader, err :=
		legacygeneration.NewReader(legacygeneration.ReaderConfig{
			Native:              nativeAdministrationGenerationAssetReader,
			GeneratedImages:     legacyGeneratedImages,
			GeneratedImageRoute: applicationSchema.Runtime.HTTP.LegacyRoutes.GeneratedImage,
			CacheRoot: configuredRuntimeSetting(
				config,
				applicationSchema,
				administrationGenerations.Assets.LegacyCacheRootSetting,
			),
			CachePrefixCharacters: administrationGenerations.Assets.
				LegacyCachePrefixCharacters,
			CacheMetadataSuffix: administrationGenerations.Assets.
				LegacyCacheMetadataSuffix,
			CacheContentTypeField: administrationGenerations.Assets.
				LegacyCacheContentTypeField,
			AllowedHosts: legacygeneration.CollectAssetHosts(
				taskCatalog.List(""),
			),
			DefaultMediaType: administrationGenerations.Assets.
				DefaultMediaType,
			UnknownExtension: administrationGenerations.Assets.
				UnknownExtension,
			MaximumBytes: administrationGenerations.Assets.
				MaximumPackageBytes,
			RemoteTimeoutMilliseconds: administrationGenerations.Assets.
				RemoteDownloadTimeoutMilliseconds,
			MaximumRedirects: administrationGenerations.Assets.
				MaximumRemoteRedirects,
			MediaTypeExtensions: administrationGenerations.Assets.
				MediaTypeExtensions,
			AssetDownloads: legacygeneration.CollectAssetDownloads(
				applicationSchema.Capabilities,
			),
		})
	if err != nil {
		return nil, err
	}
	administrationGenerationDownloadService, err :=
		administration.NewGenerationDownloadService(
			administration.GenerationDownloadServiceConfig{
				Details: administrationGenerationService,
				Assets:  administrationGenerationAssetReader,
				DownloadFilenameTemplate: administrationGenerations.Assets.
					DownloadFilenameTemplate,
				AssetFilenameTemplate: administrationGenerations.Assets.
					AssetFilenameTemplate,
				VariantSeparator: administrationGenerations.Assets.
					VariantSeparator,
				DuplicateSeparator: administrationGenerations.Assets.
					DuplicateSeparator,
				OrdinalBase: administrationGenerations.Assets.OrdinalBase,
				ArchiveMediaType: administrationGenerations.Assets.
					ArchiveMediaType,
				DefaultMediaType: administrationGenerations.Assets.
					DefaultMediaType,
				UnknownExtension: administrationGenerations.Assets.
					UnknownExtension,
				MaximumPackageBytes: administrationGenerations.Assets.
					MaximumPackageBytes,
				MediaTypeExtensions: administrationGenerations.Assets.
					MediaTypeExtensions,
			},
		)
	if err != nil {
		return nil, err
	}
	administrationService, err := administration.NewUserService(
		administration.UserServiceConfig{
			Repository: repository,
			Guards: administration.UserGuards{
				MinimumActiveAdministrators: administrationUsers.Guards.
					MinimumActiveAdministrators,
			},
			Identity: administration.UserIdentityRules{
				EmailRequired: administrationUsers.API.Identity.
					EmailRequired,
				EmailMustContainAt: administrationUsers.API.Identity.
					EmailMustContainAt,
			},
			Audit:            administrationAuditService,
			PasswordHashCost: applicationSchema.Authentication.API.PasswordHashCost,
			UsernameMinimumCharacters: administrationUsers.Limits.
				UsernameMinimumCharacters,
			PasswordMinimumCharacters: administrationUsers.Limits.
				PasswordMinimumCharacters,
			IDPrefix:      administrationUsers.Defaults.IDPrefix,
			DefaultRole:   applicationSchema.Authentication.DefaultRole,
			AdminRole:     applicationSchema.Authentication.AdminRole,
			DefaultActive: administrationUsers.Defaults.Active,
			AuditActions: administration.UserAuditActions{
				Create:     administrationUsers.Audit.CreateAction,
				Update:     administrationUsers.Audit.UpdateAction,
				Deactivate: administrationUsers.Audit.DeactivateAction,
				Delete:     administrationUsers.Audit.DeleteAction,
				Resource:   administrationUsers.Audit.ResourceType,
			},
			AuditFields: administration.UserAuditFields{
				Username: administrationUsers.Audit.DetailFields.Username,
				Admin:    administrationUsers.Audit.DetailFields.IsAdmin,
				Active:   administrationUsers.Audit.DetailFields.IsActive,
			},
		},
	)
	if err != nil {
		return nil, err
	}
	administrationUserAssets :=
		applicationSchema.Administration.UserAssets
	userAssetDownloadActions := make(
		[]string,
		0,
		len(administrationUserAssets.Filters.DownloadActions),
	)
	for _, action := range administrationUserAssets.Filters.DownloadActions {
		userAssetDownloadActions = append(
			userAssetDownloadActions,
			action.Action,
		)
	}
	administrationUserAssetsModule, err :=
		administration.NewUserAssetsModule(
			administration.UserAssetsModuleConfig{
				Repository: repository,
				DefaultPage: administrationUserAssets.Limits.
					DefaultPage,
				DefaultPageSize: administrationUserAssets.Limits.
					DefaultPageSize,
				MaximumPageSize: administrationUserAssets.Limits.
					MaximumPageSize,
				DownloadActions: userAssetDownloadActions,
				NativeAssetRoute: applicationSchema.Runtime.HTTP.Routes.
					TaskAsset,
				ThumbnailRole: administrationGenerations.Assets.
					ThumbnailRole,
			},
		)
	if err != nil {
		return nil, err
	}
	administrationBackfillStatus :=
		applicationSchema.Administration.BackfillStatus
	administrationBackfillStatusModule, err :=
		administration.NewBackfillStatusModule(
			administration.BackfillStatusModuleConfig{
				Repository: repository,
				PendingStatus: administrationBackfillStatus.
					Statuses.Pending,
				SucceededStatus: administrationBackfillStatus.
					Statuses.Succeeded,
				SyncProviderIDs: administrationBackfillStatus.
					SyncProviderIDs,
				ResultFields: administration.BackfillResultFields{
					ModelURLs: administrationBackfillStatus.
						CacheResultFields.ModelURLs,
					ThumbnailURL: administrationBackfillStatus.
						CacheResultFields.ThumbnailURL,
					ImageURLs: administrationBackfillStatus.
						CacheResultFields.ImageURLs,
					TextureURLs: administrationBackfillStatus.
						CacheResultFields.TextureURLs,
					BaseColor: administrationBackfillStatus.
						CacheResultFields.BaseColor,
					VideoURL: administrationBackfillStatus.
						CacheResultFields.VideoURL,
				},
			},
		)
	if err != nil {
		return nil, err
	}
	providerAccountService, err := buildProviderAccounts(
		config,
		applicationSchema.Runtime,
	)
	if err != nil {
		return nil, err
	}
	administrationBalanceSummaryService, err :=
		administration.NewBalanceSummaryService(
			administration.BalanceSummaryServiceConfig{
				Accounts:  providerAccountService,
				Snapshots: repository,
				AccountID: administrationUsage.Balance.
					AccountID,
				HistoryDays: administrationUsage.Balance.
					HistoryDays,
				SnapshotIDPrefix: administrationUsage.Balance.
					SnapshotIDPrefix,
				SnapshotWritePolicy: administrationUsage.Balance.
					SnapshotWritePolicy,
			},
		)
	if err != nil {
		return nil, err
	}
	assetProxySchema := applicationSchema.Runtime.AssetDelivery.Proxy
	assetHosts := assetProxySchema.Security.AllowedHosts
	thumbnailBackfillSchema :=
		applicationSchema.Runtime.LegacyMigration.ThumbnailBackfill
	thumbnailOfferingMappings := make(
		[]legacyassetproxy.OfferingMapping,
		len(thumbnailBackfillSchema.OfferingMappings),
	)
	for index, mapping := range thumbnailBackfillSchema.OfferingMappings {
		thumbnailOfferingMappings[index] = legacyassetproxy.OfferingMapping{
			LegacyProviderID: mapping.LegacyProviderID,
			Capability:       mapping.Capability,
			OfferingID:       mapping.OfferingID,
		}
	}
	thumbnailSourceLosses := make(
		map[generation.TaskID]string,
		len(thumbnailBackfillSchema.AcknowledgedSourceLosses),
	)
	for _, sourceLoss := range thumbnailBackfillSchema.
		AcknowledgedSourceLosses {
		thumbnailSourceLosses[generation.TaskID(sourceLoss.TaskID)] =
			sourceLoss.Reason
	}
	assetURLRefresher, err :=
		legacyassetproxy.NewProviderRefresher(
			legacyassetproxy.ProviderRefresherConfig{
				Repository:       repository,
				Catalog:          taskCatalog,
				Providers:        registry,
				OfferingMappings: thumbnailOfferingMappings,
			},
		)
	if err != nil {
		return nil, err
	}
	legacyAssetCacheRoot := configuredRuntimeSetting(
		config,
		applicationSchema,
		assetProxySchema.Cache.RootSetting,
	)
	assetSource, err := legacyassetproxy.NewSource(
		legacyassetproxy.SourceConfig{
			Root:         legacyAssetCacheRoot,
			Schema:       assetProxySchema,
			AllowedHosts: assetHosts,
			Refresher:    assetURLRefresher,
		},
	)
	if err != nil {
		return nil, err
	}
	thumbnailSourceResolver, err := legacygeneration.NewSourceResolver(
		applicationSchema.Runtime.HTTP.LegacyRoutes.MeshyProxyAsset,
		assetProxySchema.API.Query.URL,
	)
	if err != nil {
		return nil, err
	}
	thumbnailSourceFields, err := legacyThumbnailSourceFields(
		applicationSchema.Capabilities,
		applicationSchema.Administration.Generations.Assets.ThumbnailRole,
	)
	if err != nil {
		return nil, err
	}
	thumbnailBackfillSource, err := legacyassetproxy.NewSource(
		legacyassetproxy.SourceConfig{
			Root: filepath.Join(
				config.StorageRoot,
				thumbnailBackfillSchema.SourceCacheSubdirectory,
			),
			ReadOnlyRoots: []string{legacyAssetCacheRoot},
			Schema:        assetProxySchema,
			AllowedHosts:  assetHosts,
			Refresher:     assetURLRefresher,
		},
	)
	if err != nil {
		return nil, err
	}
	thumbnailBackfill, err := legacythumbnail.New(
		legacythumbnail.Config{
			Repository:    repository,
			Assets:        assetStore,
			Source:        thumbnailBackfillSource,
			ResolveSource: thumbnailSourceResolver.Resolve,
			ThumbnailRole: applicationSchema.Administration.Generations.Assets.
				ThumbnailRole,
			SourceFields:             thumbnailSourceFields,
			AcknowledgedSourceLosses: thumbnailSourceLosses,
			BatchSize:                thumbnailBackfillSchema.BatchSize,
			MaximumAssetSize:         thumbnailBackfillSchema.MaximumAssetBytes,
		},
	)
	if err != nil {
		return nil, err
	}
	assetThumbnails, err :=
		legacyassetproxy.NewThumbnailStore(
			legacyassetproxy.ThumbnailStoreConfig{
				Root: configuredRuntimeSetting(
					config,
					applicationSchema,
					assetProxySchema.Thumbnail.RootSetting,
				),
				Schema: assetProxySchema,
			},
		)
	if err != nil {
		return nil, err
	}
	assetProxy, err := assetdelivery.NewProxy(
		assetdelivery.ProxyConfig{
			Schema:       assetProxySchema,
			AllowedHosts: assetHosts,
			Source:       assetSource,
			Thumbnailer:  assetThumbnails,
			Stamper:      assetdelivery.NewExifStamper(nil),
		},
	)
	if err != nil {
		return nil, err
	}
	providerWebhookRuntime := make(
		map[string]httpapi.ProviderWebhookRuntime,
		len(applicationSchema.ProviderWebhooks),
	)
	for _, webhook := range applicationSchema.ProviderWebhooks {
		providerWebhookRuntime[webhook.ID] = httpapi.ProviderWebhookRuntime{
			PathSecret: configuredRuntimeSetting(
				config,
				applicationSchema,
				webhook.PathSecretSetting,
			),
			Key: configuredRuntimeSetting(
				config,
				applicationSchema,
				webhook.KeySetting,
			),
		}
	}
	readiness := &readinessState{ping: repository.CheckReadiness, storageRoot: config.StorageRoot}
	var processingGateway processing.Gateway = module
	if overrides.processingGateway != nil {
		processingGateway = overrides.processingGateway(processingGateway)
	}
	processingService, err := processing.New(repository, processingCatalog{Catalog: taskCatalog, module: module}, processingGateway, logger)
	if err != nil {
		return nil, err
	}
	processingService.WithGrants(executionGrants)
	processingService.WithSourcePreference(catalog.ProcessingSourcePreference(applicationSchema.Chat.Tools))
	toolExecutor.SetProcessing(processingService, processingCatalog{Catalog: taskCatalog, module: module})
	var modelDeliveryService *modeldelivery.Service
	if !applicationSchema.ModelDelivery.Disabled() {
		modelDeliveryService, err = modeldelivery.New(applicationSchema.ModelDelivery, module, processingService, logger)
		if err != nil {
			return nil, err
		}
		modelDeliveryService.Localize(texts)
		// list_session_models reports the same format coverage as the 格式 tab (#321).
		toolExecutor.SetModelFormats(func(ctx context.Context, owner generation.OwnerID, task generation.TaskID) (map[string]string, error) {
			coverage, err := modelDeliveryService.Coverage(ctx, generation.OwnTasks(owner), task)
			if err != nil || !coverage.Applicable {
				return nil, err
			}
			formats := make(map[string]string, len(coverage.Formats))
			for _, format := range coverage.Formats {
				formats[format.ID] = string(format.Status)
			}
			return formats, nil
		})
	}
	lineageConfig := lineage.Config{MaxTasks: applicationSchema.Runtime.HTTP.MaximumLineageTasks}
	if modelDeliveryService != nil {
		// A format conversion is part of the version it converted (#330).
		// Conversions made before the upgrade keep their version numbers:
		// the chat history already names those versions.
		lineageConfig.Conversions = modelDeliveryService
		if lineageConfig.FoldConversionsSince, err = repository.ConversionFoldSince(context.Background()); err != nil {
			return nil, err
		}
	}
	lineageService, err := lineage.New(module, captureStore, repository, lineageConfig)
	if err != nil {
		return nil, err
	}
	// Tool results name models by the version strip's numbers (#326 L).
	toolExecutor.SetModelVersions(func(ctx context.Context, owner generation.OwnerID, session string) (map[generation.TaskID]int, error) {
		return lineageService.SessionVersions(ctx, string(owner), session)
	})
	taskRetryService, err := taskretry.New(taskretry.Config{
		Kernel: module, Grants: executionGrants, Origins: repository,
		OperationID: httpapi.StandaloneOperationID,
	})
	if err != nil {
		return nil, err
	}
	pendingApprovalService, err := pendingapproval.New(pendingapproval.Config{
		Texts: texts,
		VerifyPreparedSource: func(ctx context.Context, op generationtool.Operation, prepared generation.PreparedInput) error {
			turn, err := chatService.GetTurn(ctx, op.OwnerID, op.SessionID, op.AssistantMessageID)
			if errors.Is(err, chat.ErrMessageNotFound) {
				return generationtool.ErrOperationConflict
			}
			if err != nil {
				return err
			}
			return toolExecutor.VerifyDetachedHarnessSource(ctx, turn, op, prepared)
		},
		Operations: repository, Grants: executionGrants, Gateway: module,
		Chat: chatService, Schema: applicationSchema.Chat,
		DefaultLocale: applicationSchema.Runtime.Frontend.DisplayLocale,
	})
	if err != nil {
		return nil, err
	}
	skillRunService, err := skillrun.NewService(repository, catalogrecipes.New(taskCatalog))
	if err != nil {
		return nil, err
	}
	skillRuntime, err := skillrun.NewRuntime(skillRunService, module, executionGrants)
	if err != nil {
		return nil, err
	}
	if err := agentToolExecutor.SetSkillRuntime(skillRuntime, referenceResolver, module,
		applicationSchema.Chat.Skills.Recipes, chatService.TurnStopRequested); err != nil {
		return nil, err
	}
	agentToolExecutor.SetThreeView(applicationSchema.Chat.ThreeView)
	var threeViewService *threeview.Service
	if applicationSchema.Chat.ThreeView.RecipeID != "" {
		threeViewService, err = threeview.New(threeview.Config{
			Runtime: skillRuntime, Sessions: chatService, Schema: applicationSchema.Chat.ThreeView,
		})
		if err != nil {
			return nil, err
		}
		lineageService.SetGroups(threeViewService)
		toolExecutor.SetThreeViews(threeview.ForGeneration{Service: threeViewService})
	}
	// #290: downscaled task pictures for chat cards and the version strip, cached under the storage root.
	assetPreviewSchema := applicationSchema.Runtime.AssetDelivery.AssetPreview
	assetPreviews, err := assetdelivery.NewPreviewStore(
		filepath.Join(config.StorageRoot, assetPreviewSchema.CacheSubdirectory),
		assetPreviewSchema,
	)
	if err != nil {
		return nil, err
	}
	transportApplicationSchema := applicationSchema
	transportApplicationSchema.Authentication = authenticationSchema
	api, err := httpapi.New(httpapi.Config{
		Harness:                           harnessResolver,
		AgentConfiguration:                agentConfigurationService,
		LLMSettings:                       llmSettingsService,
		MCP:                               mcpService,
		AccountInputAssets:                repository,
		Readiness:                         readiness.check,
		Generation:                        module,
		ExecutionGrants:                   executionGrants,
		AgentOperations:                   repository,
		PendingApprovals:                  pendingApprovalService,
		ThreeViews:                        threeViewServiceOrNil(threeViewService),
		Processing:                        processingService,
		ModelDelivery:                     modelDeliveryService,
		Lineage:                           lineageService,
		Retry:                             taskRetryService,
		ProviderWebhookRuntime:            providerWebhookRuntime,
		Identity:                          authenticationService,
		Authentication:                    authenticationService,
		Administration:                    administrationService,
		AdministrationAudit:               administrationAuditService,
		AdministrationGenerations:         administrationGenerationService,
		AdministrationGenerationDownloads: administrationGenerationDownloadService,
		AdministrationUsage:               administrationUsageService,
		AdministrationBalanceSummary:      administrationBalanceSummaryService,
		AdministrationUserAssets:          administrationUserAssetsModule,
		AdministrationBackfillStatus:      administrationBackfillStatusModule,
		Cache:                             cacheService,
		Canvas:                            canvasService,
		CanvasImages:                      canvasImageService,
		Chat:                              chatService,
		AgentParams:                       paramsStore,
		AgentState:                        agentStateStore,
		ChatScheduler:                     chatAgent,
		ChatContext:                       chatAgent,
		OwnsAgentSession: func(ctx context.Context, ownerID, sessionID string) bool {
			owned, err := chatService.OwnsSession(ctx, ownerID, sessionID)
			return err == nil && owned
		},
		PromptEnhancer:        promptEnhancer,
		ProviderAccounts:      providerAccountService,
		AssetProxy:            assetProxy,
		LegacyGeneratedImages: legacyGeneratedImages,
		Application:           transportApplicationSchema,
		SkillCatalog:          skillCatalog,
		InputAssets:           compatibleInputAssetService,
		ModelPreviews:         modelPreviews,
		AssetPreviews:         assetPreviews,
		Captures:              captureStore,
		SkillRuns:             skillRuntime,
		Scene:                 sceneService,
		RenderManifests:       manifestService,
		AssetGateway:          inputAssetService,
		// Range 归档要物化整个 ZIP；放到运行卷而不是容器 /tmp，
		// 后者的默认容量可能远小于一个真实 3D 归档。
		ArchiveTempDir:         filepath.Join(config.StorageRoot, "tmp"),
		AssetVerificationStats: assetStore.VerificationStats,
		RuntimeVersion:         runtimeRevision,
		TrustedProxies:         config.TrustedProxies,
		Logger:                 logger,
	})
	if err != nil {
		return nil, err
	}
	handler := api.Handler()
	if config.WebAssetsRoot != "" {
		handler, err = webui.New(webui.Config{
			Assets:      os.DirFS(config.WebAssetsRoot),
			Application: applicationSchema,
			API:         handler,
		})
		if err != nil {
			return nil, err
		}
	}
	closeRepository = false
	return &Application{
		readiness: readiness, config: config,
		logger:           logger,
		schema:           applicationSchema,
		generation:       module,
		thumbnails:       thumbnailBackfill,
		chatAgent:        chatAgent,
		processing:       processingService,
		scene:            sceneService,
		pendingApprovals: pendingApprovalService,
		manifests:        manifestService,
		server: &http.Server{
			Addr:    config.Address,
			Handler: handler,
			ReadHeaderTimeout: time.Duration(
				applicationSchema.Runtime.HTTP.ReadHeaderTimeoutSeconds,
			) * time.Second,
			ReadTimeout: time.Duration(
				applicationSchema.Runtime.HTTP.ReadTimeoutSeconds,
			) * time.Second,
			WriteTimeout: time.Duration(
				applicationSchema.Runtime.HTTP.WriteTimeoutSeconds,
			) * time.Second,
			IdleTimeout: time.Duration(
				applicationSchema.Runtime.HTTP.IdleTimeoutSeconds,
			) * time.Second,
		},
	}, nil
}

func legacyThumbnailSourceFields(
	capabilities []catalog.CapabilityPresentation,
	thumbnailRole string,
) (map[generation.Capability][]string, error) {
	thumbnailRole = strings.TrimSpace(thumbnailRole)
	result := make(map[generation.Capability][]string)
	for _, capability := range capabilities {
		if capability.LegacyAPI == nil {
			continue
		}
		for _, projection := range capability.LegacyAPI.OutputProjections {
			if strings.TrimSpace(projection.Role) != thumbnailRole {
				continue
			}
			if projection.Mode != "scalar" {
				return nil, fmt.Errorf(
					"Capability %q 的预览来源 %q 必须使用 scalar 模式",
					capability.ID,
					projection.Name,
				)
			}
			result[capability.ID] = append(
				result[capability.ID],
				projection.Name,
			)
		}
	}
	if len(result) == 0 {
		return nil, fmt.Errorf(
			"Product Schema 未声明 role=%q 的历史预览来源",
			thumbnailRole,
		)
	}
	return result, nil
}

func (a *Application) BackfillLegacyThumbnails(
	ctx context.Context,
) (legacythumbnail.Report, error) {
	if a == nil || a.thumbnails == nil {
		return legacythumbnail.Report{}, errors.New(
			"应用未初始化历史截图补迁移模块",
		)
	}
	return a.thumbnails.Run(ctx)
}

func (a *Application) Close() error {
	if a == nil || a.generation == nil {
		return nil
	}
	return a.generation.Close()
}

func administrationUsageStateGroups(
	taskStates []catalog.TaskStatePresentation,
) ([]string, []string) {
	succeeded := make([]string, 0)
	failed := make([]string, 0)
	for _, state := range taskStates {
		switch state.Outcome {
		case "success":
			succeeded = append(succeeded, state.InternalState)
		case "failure":
			failed = append(failed, state.InternalState)
		}
	}
	return succeeded, failed
}

func configuredRuntimeSetting(
	config Config,
	schema catalog.ApplicationSchema,
	name string,
) string {
	if value := strings.TrimSpace(config.RuntimeSettings[name]); value != "" {
		return value
	}
	setting, exists := schema.Runtime.Configuration.Settings[name]
	if !exists {
		return ""
	}
	if value := strings.TrimSpace(
		setting.DefaultByEnvironment[config.Environment],
	); value != "" {
		return value
	}
	return strings.TrimSpace(setting.Default)
}

func buildProviderAccounts(
	config Config,
	runtime catalog.RuntimeSchema,
) (*provideraccounts.Service, error) {
	accounts := runtime.ProviderAccounts
	settingValue := func(name string) string {
		if value, exists := config.RuntimeSettings[name]; exists {
			return value
		}
		setting := runtime.Configuration.Settings[name]
		if configured, exists := setting.DefaultByEnvironment[config.Environment]; exists {
			return configured
		}
		return setting.Default
	}
	readers := make(
		map[string]provideraccounts.BalanceReader,
		len(accounts),
	)
	for accountID, account := range accounts {
		balance := account.Balance
		if config.ProviderMode == "mock" {
			if balance.NativeUnit != "" {
				readers[accountID] = provideraccounts.FixedNativeBalanceReader{FixedBalanceReader: provideraccounts.FixedBalanceReader(balance.MockValue), Amount: generation.NativeAmount{Unit: balance.NativeUnit, UnitLabel: balance.NativeUnitLabel, Amount: balance.MockNativeAmount}}
				continue
			}
			readers[accountID] = provideraccounts.FixedBalanceReader(
				balance.MockValue,
			)
			continue
		}
		switch balance.AdapterKey {
		case "http-json":
			reader, err := provideraccounthttpjson.New(
				provideraccounthttpjson.Config{
					BaseURL:              settingValue(account.BaseURLSetting),
					APIKey:               settingValue(account.APIKeySetting),
					EndpointPath:         balance.EndpointPath,
					AuthorizationHeader:  balance.AuthorizationHeader,
					AuthorizationType:    balance.AuthorizationType,
					ResponseBalanceField: balance.UpstreamBalanceField,
					NativeUnit:           balance.NativeUnit,
					NativeUnitLabel:      balance.NativeUnitLabel,
					RequestTimeout: time.Duration(
						balance.RequestTimeoutSeconds,
					) * time.Second,
					ResponseMaxBytes:  balance.ResponseMaxBytes,
					ErrorBodyMaxBytes: balance.ErrorBodyMaxBytes,
				},
			)
			if err != nil {
				return nil, fmt.Errorf(
					"初始化 Provider 账户 %q: %w",
					accountID,
					err,
				)
			}
			readers[accountID] = reader
		default:
			return nil, fmt.Errorf(
				"Provider 账户 %q 使用未知余额适配器 %q",
				accountID,
				balance.AdapterKey,
			)
		}
	}
	service, err := provideraccounts.New(provideraccounts.Config{
		Readers: readers,
	})
	if err != nil {
		return nil, fmt.Errorf("初始化 Provider 账户模块: %w", err)
	}
	return service, nil
}

func (a *Application) Run(ctx context.Context) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	listener, err := net.Listen("tcp", a.server.Addr)
	if err != nil {
		return errors.Join(
			fmt.Errorf("监听 %s: %w", a.server.Addr, err),
			a.generation.Close(),
		)
	}
	results := make(chan componentResult, componentCount)
	go func() {
		a.readiness.coordinator.Store(true)
		defer a.readiness.coordinator.Store(false)
		runtime := a.schema.Runtime.Coordinator
		coordinatorErr := a.generation.Run(
			runCtx,
			generation.CoordinatorConfig{
				PollEvery: time.Duration(
					runtime.PollIntervalMilliseconds,
				) * time.Millisecond,
				LeaseFor:        time.Duration(runtime.LeaseSeconds) * time.Second,
				BatchSize:       runtime.BatchSize,
				Concurrency:     runtime.Concurrency,
				LeaseRenewEvery: time.Duration(runtime.LeaseRenewMilliseconds) * time.Millisecond,
				MaxBackoff:      time.Duration(runtime.MaxBackoffMilliseconds) * time.Millisecond,
				ShutdownGrace:   time.Duration(runtime.ShutdownGraceSeconds) * time.Second,
			},
		)
		a.readiness.coordinator.Store(false)
		results <- componentResult{name: "coordinator", err: coordinatorErr}
	}()
	go func() {
		a.readiness.chat.Store(true)
		err := a.chatAgent.Run(runCtx)
		a.readiness.chat.Store(false)
		results <- componentResult{name: "chat-agent", err: err}
	}()
	go func() {
		results <- componentResult{name: "processing-plans", err: a.processing.Run(runCtx)}
	}()
	go func() {
		results <- componentResult{name: "pending-approvals", err: a.pendingApprovals.Run(runCtx, func(err error) {
			a.logger.Error("待批准请求巡检失败", "error", err)
		})}
	}()
	go func() {
		interval := time.Duration(a.schema.Runtime.Coordinator.PollIntervalMilliseconds) * time.Millisecond
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			processed, reconcileErr := a.scene.ReconcileTaskCandidates(runCtx, 100)
			if reconcileErr != nil && runCtx.Err() == nil {
				a.logger.Error("场景候选对账失败", "error", reconcileErr)
			} else if processed > 0 {
				a.logger.Info("场景候选对账完成", "count", processed)
			}
			select {
			case <-runCtx.Done():
				results <- componentResult{name: "scene-reconcile", err: runCtx.Err()}
				return
			case <-ticker.C:
			}
		}
	}()
	go func() {
		a.logger.Info(
			"art-ai-go 已启动",
			"address", listener.Addr().String(),
			"provider_mode", a.config.ProviderMode,
			"agent_mode", a.config.AgentMode,
			"auth_mode", a.config.AuthMode,
		)
		results <- componentResult{name: "http", err: a.server.Serve(listener)}
	}()

	var componentErrors []error
	received := 0
	select {
	case <-ctx.Done():
	case result := <-results:
		received++
		if err := actionableComponentError(result); err != nil {
			componentErrors = append(componentErrors, err)
		}
	}
	a.readiness.coordinator.Store(false)
	a.readiness.chat.Store(false)
	cancel()
	shutdownTimeout := time.Duration(
		a.schema.Runtime.HTTP.ShutdownTimeoutSeconds,
	) * time.Second
	shutdownErr := httpapi.Shutdown(a.server, shutdownTimeout)
	waitTimer := time.NewTimer(shutdownTimeout)
	defer waitTimer.Stop()
	for received < componentCount {
		select {
		case result := <-results:
			received++
			if err := actionableComponentError(result); err != nil {
				componentErrors = append(componentErrors, err)
			}
		case <-waitTimer.C:
			componentErrors = append(
				componentErrors,
				errors.New("等待 HTTP/协调器/Chat Agent/场景对账/待批准巡检退出超时"),
			)
			received = componentCount
		}
	}
	// Background chat-title summaries are best effort: give them the shutdown window to finish, then go on.
	if a.chatAgent != nil {
		titlesDone := make(chan struct{})
		go func() {
			a.chatAgent.WaitForTitles()
			close(titlesDone)
		}()
		titleTimer := time.NewTimer(shutdownTimeout)
		defer titleTimer.Stop()
		select {
		case <-titlesDone:
		case <-titleTimer.C:
			componentErrors = append(componentErrors, errors.New("等待对话标题后台摘要退出超时"))
		}
	}
	closeErr := a.generation.Close()
	return errors.Join(
		errors.Join(componentErrors...),
		shutdownErr,
		closeErr,
	)
}

func actionableComponentError(result componentResult) error {
	if result.err == nil ||
		errors.Is(result.err, context.Canceled) ||
		errors.Is(result.err, http.ErrServerClosed) {
		return nil
	}
	return fmt.Errorf("%s: %w", result.name, result.err)
}

func loadCatalog(fileName string) (*catalog.Catalog, error) {
	if fileName == "" {
		return catalog.LoadDefault()
	}
	file, err := os.Open(fileName)
	if err != nil {
		return nil, fmt.Errorf("打开 CATALOG_FILE: %w", err)
	}
	defer file.Close()
	return catalog.Load(file)
}

func loadSkillCatalog(fileName string) (catalog.SkillCatalogSchema, error) {
	if fileName == "" {
		return catalog.LoadDefaultSkillCatalog()
	}
	file, err := os.Open(fileName)
	if err != nil {
		return catalog.SkillCatalogSchema{}, fmt.Errorf(
			"打开 SKILL_CATALOG_FILE: %w",
			err,
		)
	}
	defer file.Close()
	return catalog.LoadSkillCatalog(file)
}

func buildAccessPolicy(schema catalog.AuthenticationSchema) (*access.Policy, error) {
	roles := make([]access.RoleDefinition, 0, len(schema.Roles))
	for _, role := range schema.Roles {
		permissions := make([]access.Permission, 0, len(role.Permissions))
		for _, permission := range role.Permissions {
			permissions = append(permissions, access.Permission(permission))
		}
		roles = append(roles, access.RoleDefinition{
			Key:         role.Key,
			Permissions: permissions,
		})
	}
	policy, err := access.NewPolicy(access.PolicyConfig{
		DefaultRole: schema.DefaultRole,
		AdminRole:   schema.AdminRole,
		Roles:       roles,
	})
	if err != nil {
		return nil, fmt.Errorf("构建 Product Schema 权限策略: %w", err)
	}
	return policy, nil
}

func buildIdentity(
	config Config,
	policy *access.Policy,
) (httpapi.IdentityResolver, error) {
	switch config.AuthMode {
	case "development":
		return httpapi.DevelopmentIdentity{
			DefaultClaims: access.Claims{
				ID:    string(config.DevelopmentUser),
				Name:  config.DevelopmentName,
				Email: config.DevelopmentEmail,
				Roles: append([]string(nil), config.DevelopmentRoles...),
			},
			Policy: policy,
		}, nil
	case "session":
		return nil, nil
	case "bearer":
		return httpapi.NewBearerIdentity(config.AuthTokens, policy)
	default:
		return nil, fmt.Errorf(
			"运行设置 auth_mode 的值无效: %q",
			config.AuthMode,
		)
	}
}

// approvalPolicyFromSchema turns application.chat.approval_policy into the pure
// decision input. Known capabilities come from the same Schema, so a capability
// the Schema does not declare always needs a person in manual mode.
func approvalPolicyFromSchema(application catalog.ApplicationSchema) approvalpolicy.Policy {
	declared := application.Chat.ApprovalPolicy
	manual := make([]string, 0, len(declared.ManualApprovalCapabilities))
	for _, capability := range declared.ManualApprovalCapabilities {
		manual = append(manual, string(capability))
	}
	known := make([]string, 0, len(application.Capabilities))
	for _, capability := range application.Capabilities {
		known = append(known, string(capability.ID))
	}
	return approvalpolicy.New(declared.DefaultMode, declared.Modes, manual, known)
}

// threeViewServiceOrNil keeps a deployment without the three-view recipe from
// handing the HTTP layer a typed nil that looks like a service.
func threeViewServiceOrNil(service *threeview.Service) httpapi.ThreeViews {
	if service == nil {
		return nil
	}
	return service
}

// withToolCallBudget applies the deployment's per-reply tool call budget
// (AGENT_MAX_TOOL_CALLS_PER_REPLY, #275); 0 keeps the Schema value.
func withToolCallBudget(schema catalog.ChatSchema, override int) catalog.ChatSchema {
	if override > 0 {
		schema.Agent.MaxToolCallsPerReply = override
	}
	return schema
}

// orchestratorChatSchema turns DSML tool-call reading on only for a model
// whose vendor profile declares it (#253); chat.dsml stays as declared
// otherwise, so a standard OpenAI-compatible model gets no vendor parsing.
func orchestratorChatSchema(schema catalog.ChatSchema, model string) catalog.ChatSchema {
	if profile, matched := chatModelProfile(schema.Agent.Thinking, model); matched && profile.DSMLToolCalls {
		schema.DSML.Enabled = true
	}
	return schema
}
