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
	"sync"
	"time"

	"github.com/LeXwDeX/Plinth/internal/access"
	"github.com/LeXwDeX/Plinth/internal/accesskeys"
	"github.com/LeXwDeX/Plinth/internal/accountsettings"
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
	"github.com/LeXwDeX/Plinth/internal/assetlinks"
	"github.com/LeXwDeX/Plinth/internal/assetlist"
	"github.com/LeXwDeX/Plinth/internal/assetlistrunner"
	"github.com/LeXwDeX/Plinth/internal/assetreview"
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
	"github.com/LeXwDeX/Plinth/internal/chat/imagemask"
	"github.com/LeXwDeX/Plinth/internal/chat/imagereview"
	"github.com/LeXwDeX/Plinth/internal/chat/references"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/harness/resolve"
	"github.com/LeXwDeX/Plinth/internal/imageinspect"
	"github.com/LeXwDeX/Plinth/internal/imaging"
	"github.com/LeXwDeX/Plinth/internal/legacythumbnail"
	"github.com/LeXwDeX/Plinth/internal/lineage"
	"github.com/LeXwDeX/Plinth/internal/listcrop"
	"github.com/LeXwDeX/Plinth/internal/llmsettings"
	"github.com/LeXwDeX/Plinth/internal/modeldelivery"
	"github.com/LeXwDeX/Plinth/internal/pendingapproval"
	"github.com/LeXwDeX/Plinth/internal/processing"
	"github.com/LeXwDeX/Plinth/internal/project"
	"github.com/LeXwDeX/Plinth/internal/promptenhance"
	"github.com/LeXwDeX/Plinth/internal/provideraccounts"
	provideraccounthttpjson "github.com/LeXwDeX/Plinth/internal/provideraccounts/httpjson"
	"github.com/LeXwDeX/Plinth/internal/providers"
	"github.com/LeXwDeX/Plinth/internal/rendermanifest"
	"github.com/LeXwDeX/Plinth/internal/scene"
	"github.com/LeXwDeX/Plinth/internal/skillrun"
	"github.com/LeXwDeX/Plinth/internal/taskreroll"
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
	// assetListRunner drives confirmed lists (#328). Nil unless the Catalog
	// declares application.asset_list.execution with enabled=true.
	assetListRunner *assetlistrunner.Runner
	// pendingApprovals finishes Agent approvals decided after their turn ended.
	pendingApprovals *pendingapproval.Service
	scene            *scene.Service
	manifests        *rendermanifest.Service
	// modelDelivery is the format delivery service, and with it the automatic
	// completion reconciler (#461). Nil when the Product Schema declares no
	// model_delivery block.
	modelDelivery *modeldelivery.Service
	// accessKeyAuthenticator resolves personal access keys for a separate
	// listener (#332); the main server's identity does not accept them.
	accessKeyAuthenticator *accesskeys.Authenticator
	server                 *http.Server
	// mcpServer is the external MCP listener (#332): a second http.Server that
	// serves the MCP routes only. Nil unless the Schema enables the service and
	// the runtime configuration gives it an address.
	mcpServer *http.Server
	mcpMutex  sync.Mutex
	mcpBound  string
}

// The chat references read a snapshot's recorded digest through this, never
// the bytes (#49).
var _ references.InputAssetRecords = (*inputassets.Service)(nil)

// threeViewAssetStore lets the three-view consistency check open both kinds
// of picture a view can have (#243): a generated Task output, or an input asset
// the person put in its place.
type threeViewAssetStore struct {
	inputs *inputassets.Service
	tasks  interface {
		OpenAsset(context.Context, generation.OwnerID, generation.TaskID,
			generation.AssetID) (generation.AssetContent, error)
	}
}

func (s threeViewAssetStore) OpenOwned(ctx context.Context, owner generation.OwnerID,
	id generation.AssetID,
) (generation.InputAsset, io.ReadCloser, error) {
	return s.inputs.OpenOwned(ctx, owner, id)
}

func (s threeViewAssetStore) OpenAsset(ctx context.Context, owner generation.OwnerID,
	task generation.TaskID, id generation.AssetID,
) (generation.AssetContent, error) {
	return s.tasks.OpenAsset(ctx, owner, task, id)
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

// cropInputAssets is the crop service's input port (#331): the input asset
// service owns uploads and opens real uploads, while the repository's
// LoadInputAssets also projects a finished Task's outputs, which is how a Task
// source is resolved before it is copied into an input asset.
type cropInputAssets struct {
	inputs *inputassets.Service
	loader interface {
		LoadInputAssets(context.Context, generation.OwnerID,
			[]generation.AssetID) (map[generation.AssetID]generation.InputAsset, error)
	}
}

func (store cropInputAssets) UploadAsset(ctx context.Context, owner generation.OwnerID,
	upload generation.InputAssetUpload,
) (generation.InputAsset, error) {
	return store.inputs.UploadAsset(ctx, owner, upload)
}

func (store cropInputAssets) OpenOwned(ctx context.Context, owner generation.OwnerID,
	id generation.AssetID,
) (generation.InputAsset, io.ReadCloser, error) {
	return store.inputs.OpenOwned(ctx, owner, id)
}

func (store cropInputAssets) LoadInputAssets(ctx context.Context, owner generation.OwnerID,
	ids []generation.AssetID,
) (map[generation.AssetID]generation.InputAsset, error) {
	return store.loader.LoadInputAssets(ctx, owner, ids)
}

// assetListReferenceAssets is the production-list reference port (#331): the
// input asset service resolves an owner's stored uploads without opening their
// bytes, and the crop repository supplies the provenance that marks an asset as
// a crop and names its source. The asset-list package validates the crop rules
// over this; it never reads the asset tables itself.
type assetListReferenceAssets struct {
	inputs *inputassets.Service
	crops  *sqlite.InputAssetCropRepository
}

// cropProvenance is the attachment resolver's crop port (#331): it names the
// source picture of one stored list crop, so a sub-session's first turn reads
// the crop as the identity/composition reference and the scene as the style
// reference.
type cropProvenance struct {
	crops *sqlite.InputAssetCropRepository
}

func (port cropProvenance) CropSource(ctx context.Context,
	owner generation.OwnerID, cropAssetID generation.AssetID,
) (generation.AssetID, bool, error) {
	crop, found, err := port.crops.Get(ctx, owner, cropAssetID)
	if err != nil || !found {
		return "", false, err
	}
	// A Task source is copied into an input asset before it is cropped, and the
	// copy is the picture the crop actually came from. Naming the copy keeps
	// the source attachment openable; for an input source both ids are equal.
	if crop.SourceInputAssetID != "" {
		return crop.SourceInputAssetID, true, nil
	}
	return crop.SourceAssetID, true, nil
}

func (port assetListReferenceAssets) ResolveInputAsset(ctx context.Context,
	owner generation.OwnerID, inputAssetID generation.AssetID,
) (assetlist.InputAssetReference, bool, error) {
	asset, err := port.inputs.OwnedInputAsset(ctx, owner, inputAssetID)
	if err != nil {
		var domain *generation.Error
		if errors.As(err, &domain) &&
			(domain.Code == generation.ErrNotFound || domain.Code == generation.ErrForbidden) {
			return assetlist.InputAssetReference{}, false, nil
		}
		return assetlist.InputAssetReference{}, false, err
	}
	reference := assetlist.InputAssetReference{MediaType: asset.MediaType}
	crop, found, err := port.crops.Get(ctx, owner, inputAssetID)
	if err != nil {
		return assetlist.InputAssetReference{}, false, err
	}
	switch {
	case found:
		reference.IsCrop = true
		reference.CropSourceAssetID = crop.SourceAssetID
	case asset.AssetType == listcrop.CropAssetType:
		// A list_crop row without provenance is inconsistent stored data; the
		// service refuses it instead of accepting it as a plain picture.
		reference.IsCrop = true
	}
	return reference, true, nil
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
// scene reconciliation and the HTTP server. Run waits for each to return. The
// external MCP listener (#332), when the application has one, is a seventh;
// the asset-list runner (#328), when execution is enabled, is another.
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

// openRepository opens the application database. Only this package's test
// binary replaces it, with a copy of a migrated template for new files (#73).
var openRepository = sqlite.Open

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
	shape, err := resolveUpstreamShape(config)
	if err != nil {
		return nil, err
	}
	taskCatalog, err := loadCatalogForShape(config.CatalogFile, shape)
	if err != nil {
		return nil, err
	}
	skillCatalog, err := loadSkillCatalog(config.SkillCatalogFile)
	if err != nil {
		return nil, err
	}
	applicationSchema := taskCatalog.Application()
	if err := validateUpstreamWiring(shape, config, applicationSchema); err != nil {
		return nil, err
	}
	if shape.fake() {
		if overrides.httpTransport == nil {
			overrides.httpTransport = fakeShapeHTTPTransport(shape.origin)
		}
		logger.Warn("上游形态为 fake：全部 Provider 与对话模型连接指向 fake 上游，不发起真实上游请求",
			"upstream_shape", upstreamShapeFake, "fake_upstream_origin", shape.origin.String())
	}
	if shape.mixed {
		logger.Warn("上游形态为混合：对话模型连接指向真实网关（消耗真实对话 token），生成 Provider 仍指向 fake 上游，不发起真实生成请求",
			"upstream_shape", upstreamShapeMixed, "fake_upstream_origin", shape.origin.String())
	}
	// Opt-in recording of upstream calls (#233): off unless the operator set a
	// directory. Capture wraps every connection's transport, outside any
	// routing decorator, so it sees each request as the Adapter made it.
	upstreamCapture, err := newUpstreamCapture(config, applicationSchema, logger)
	if err != nil {
		return nil, err
	}
	overrides.httpTransport = withUpstreamCapture(upstreamCapture, overrides.httpTransport)
	logUpstreamCapture(logger, upstreamCapture)
	// The per-reply tool call budget in force (#275) is one value for the
	// orchestrator and the public Schema, so the composer's counter shows the
	// limit the Agent is held to rather than the Schema default.
	applicationSchema.Chat = withToolCallBudget(applicationSchema.Chat, config.AgentMaxToolCallsPerReply)
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
	repository, err := openRepository(config.DatabasePath)
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
	// The audit service is created before the chat service so the Agent's
	// production-brief tool can record the same product audit as the HTTP PUT
	// (#103); the bootstrap report below still uses it.
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
	approvalPolicy := approvalPolicyFromSchema(applicationSchema)
	// Projects are assembled before chat so a session can be attributed to
	// an owner project at creation (#240). A Catalog without the section
	// leaves the routes unregistered and every project assignment refused.
	var projectService *project.Service
	var projectSource chat.ProjectSource
	if applicationSchema.Project != nil {
		profiles, imageProfiles := project.PurposeProfiles(applicationSchema.Chat)
		projectService, err = project.New(repository.ProjectRepository(), project.Config{
			Schema:        *applicationSchema.Project,
			Profiles:      profiles,
			ImageProfiles: imageProfiles,
			Sources:       repository.ProjectAssetSource(),
		})
		if err != nil {
			return nil, err
		}
		projectSource = newProjectChatSource(projectService, *applicationSchema.Project,
			applicationSchema.Runtime.HTTP.Routes)
	}
	chatService, err := chat.NewService(chat.ServiceConfig{
		ApprovalPolicy:             approvalPolicy,
		AgentScope:                 agentscope.FromSchema(applicationSchema.Chat.AgentScope),
		Handles:                    chat.NewHandlePolicy(applicationSchema.Chat.Context),
		MaximumSessionNotes:        applicationSchema.Chat.Context.MaximumSessionNotesCharacters,
		MaximumStyleLockText:       applicationSchema.Chat.StyleLock.MaximumTextCharacters,
		MaximumStyleLockReferences: applicationSchema.Chat.StyleLock.MaximumReferenceImages,
		StyleLockSources:           applicationSchema.Chat.StyleLock.Sources,
		Projects:                   projectSource,
		VisualAnchors:              applicationSchema.Chat.VisualAnchors,
		Preferences:                applicationSchema.Chat.Preferences,
		CanvasInputSource:          applicationSchema.Chat.Context.CanvasInputSource,
		// A turn's pick of a retired generation model runs on its successor
		// (#329 follow-up).
		RetiredModels: taskCatalog.RetiredModelSuccessors(),
		// The turn plan's confirmation rule (#380 step 2): the Schema's text
		// decides whether a purpose recommendation asks before replacing the
		// model the person sees, the same declaration the generation tools read.
		TurnPlanConfirmsWorkspace: applicationSchema.Chat.Presentation.Messages.ModelChoiceWorkspaceConflict != "",
		// The production purposes and the brief fields (#103).
		Model3DUsage: applicationSchema.Chat.Model3DUsage,
		// The 2D production purposes and their requirements (#239).
		Image2DUsage: applicationSchema.Chat.Image2DUsage,
		// An explicit 3D model lock must name an enabled 3D Offering (#103
		// review C4); the picker sends a model_id, and the tools also match the
		// Offering id and provider/model pair.
		ExplicitModel3DValid: func(model string) bool {
			return explicitModel3DValid(taskCatalog.List(""), model)
		},
		// An explicit 2D model lock must name an enabled image Offering (#239).
		ExplicitModelImageValid: func(model string) bool {
			return explicitModelImageValid(taskCatalog.List(""), model)
		},
		// The declared generic cards; the brief write settles each purpose
		// kind by its Schema option source (#103, #239).
		ChoiceKinds:      applicationSchema.Chat.Choice.Kinds,
		ChoiceMaxOptions: applicationSchema.Chat.Choice.MaxOptions,
		// The diverge-then-converge flow (#242): concept direction cards carry
		// exact refine_image arguments, checked against the declared tool.
		ConceptDirections:     applicationSchema.Chat.ConceptDirections,
		ChoiceTargetArguments: chatToolArgumentNames(applicationSchema.Chat.Tools),
		// A direction card option may switch the session to one of these
		// functions (#374).
		HarnessOperations: applicationSchema.Chat.Harness.Operations,
		// The session production ledger and its summary bounds (#248).
		WorkLedger: applicationSchema.Chat.WorkLedger,
		// The deployment's chat model, its thinking profile and context
		// window (#322).
		Models: chat.ModelPolicy{
			Model:          configuredRuntimeSetting(config, applicationSchema, applicationSchema.Chat.Agent.ModelSetting),
			Thinking:       applicationSchema.Chat.Agent.Thinking,
			WindowOverride: config.ContextWindowTokens,
		},
		VisionBudget: func(model string) int {
			return chat.EffectiveVisionImages(applicationSchema.Chat, model)
		},
		VisionTokenBudget: func(model string) int {
			return chat.EffectiveVisionTokens(applicationSchema.Chat, model)
		},
		Repository:            repository,
		DefaultTitle:          applicationSchema.Chat.Defaults.Title,
		BranchTitleTemplate:   applicationSchema.Chat.Defaults.BranchTitleTemplate,
		CanvasProjectIDPrefix: applicationSchema.Canvas.Defaults.ProjectIDPrefix,
		UserRole:              applicationSchema.Chat.Roles.User,
		AssistantRole:         applicationSchema.Chat.Roles.Assistant,
		PendingContent:        applicationSchema.Chat.State.PendingContent,
		StreamActive:          applicationSchema.Chat.State.Active,
		StreamDone:            applicationSchema.Chat.State.Done,
		StreamError:           applicationSchema.Chat.State.Error,
		TurnQueued:            applicationSchema.Chat.State.TurnQueued,
		TurnRunning:           applicationSchema.Chat.State.TurnRunning,
		TurnWaitingTask:       applicationSchema.Chat.State.TurnWaitingTask,
		TurnDone:              applicationSchema.Chat.State.TurnDone,
		TurnError:             applicationSchema.Chat.State.TurnError,
		TurnEndReasons:        applicationSchema.Chat.State.TurnEndReasons,
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
	// #330: an external MCP asset session (#332) derives the internal steps
	// mode and the mcp step entry when the deployment declares both the step
	// policy and the external MCP service. A deployment without either keeps
	// every session on its stored approval mode.
	if applicationSchema.StepPolicy != nil && applicationSchema.MCPPublic != nil {
		chatService.SetMCPStepSessions(repository)
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
	var projectReferenceChecker references.ProjectReferenceChecker
	if applicationSchema.Project != nil {
		projectReferenceChecker = repository.ProjectRepository()
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
		RenderAssetType:  applicationSchema.Chat.ModelReview.Render.AssetType,
		TaskInSession: func(ctx context.Context, owner generation.OwnerID, sessionID string, taskID generation.TaskID) (bool, error) {
			return module.TaskInSession(ctx, owner, sessionID, taskID)
		},
		// #240: a finished Task picture registered in the owner's project may
		// be read from any session of that project. The exact (task, asset)
		// pair is checked here; no request can name its own scope.
		ProjectReferences: projectReferenceChecker,
		// #331: a production-list item's stored crop is attached with its own
		// role, and its source scene follows as the style reference.
		Crops: cropProvenance{crops: repository.InputAssetCropRepository()},
		// #244: the frozen selection mask and its server-composed preview.
		SelectionMaskLimits: imageinspect.Limits{
			MaxBytes:  applicationSchema.Chat.ImageMask.Limits.MaxBytes,
			MaxPixels: applicationSchema.Chat.ImageMask.Limits.MaxPixels,
		},
		SelectionPreview: imaging.SelectionPreviewStyle{
			MaxPixels:   applicationSchema.Chat.Context.SelectionMaskPreview.MaxPixels,
			DimOpacity:  applicationSchema.Chat.Context.SelectionMaskPreview.DimOpacity,
			TintColor:   applicationSchema.Chat.Context.SelectionMaskPreview.TintColor,
			TintOpacity: applicationSchema.Chat.Context.SelectionMaskPreview.TintOpacity,
		},
	})
	if err != nil {
		return nil, err
	}
	chatService.SetAttachmentVerifier(referenceResolver)
	// The session production ledger (#248) reads its steps from the public
	// Tasks of the session; a branch additionally reads its ancestors' Tasks
	// through the refs table. The summary reads one bounded page, while
	// get_production_step pages the complete session set; the read is not an
	// execution grant.
	chatService.SetSessionTaskSource(func(ctx context.Context, ownerID, sessionID string, offset, limit int) (generation.TaskPage, error) {
		return module.List(ctx, generation.OwnTasks(generation.OwnerID(ownerID)), generation.ListQuery{
			SessionID:         sessionID,
			IncludeBranchRefs: true,
			Offset:            offset,
			Limit:             limit,
		})
	})
	// get_production_step reads the one requested step in full; the list
	// projection carries only its prompt.
	chatService.SetSessionTaskDetail(func(ctx context.Context, ownerID, sessionID, taskID string) (generation.Task, error) {
		return module.Get(ctx, generation.OwnerID(ownerID), generation.TaskID(taskID))
	})
	// Rejected directions (#248). Explicit rejection writes are #241's scope;
	// until they exist the reader projects the selected results a later
	// selection or pick superseded from the persisted turn options. It never
	// invents a direction, and a read failure becomes a ledger omission, not an
	// empty list. No migration is added.
	chatService.SetDecisionReader(chat.NewSessionDecisionReader(repository,
		applicationSchema.Chat.WorkLedger.Rejected.SupersededReason))
	// In-chat choice cards (#321): processing tools ask the person which service to use.
	chatService.SetChoices(repository, applicationSchema.Chat.Choice.ListLimit)
	// Session thinking level, context usage and compactions (#322).
	chatService.SetContextStore(repository, applicationSchema.Chat.ContextCompaction.ListLimit)
	harnessResolver := &resolve.Service{UploadedModels: uploadedModels, Store: repository, Schema: applicationSchema.Chat.Harness, Tools: applicationSchema.Chat.Tools, Catalog: taskCatalog, Gateway: module}
	chatService.SetHarnessSnapshotResolver(harnessResolver.Snapshot)
	// A structured multiview request names the model of its turn (#373): the
	// turn starts only when that model is an enabled, runtime-available
	// Offering that takes several views, so a model that cannot run is refused
	// before the Agent spends a turn on it.
	if capabilities := multiviewToolCapabilities(applicationSchema.Chat); len(capabilities) > 0 {
		chatService.SetMultiviewModelValid(func(model string) bool {
			return generationtool.MultiviewModelAvailable(module.Offerings(""), capabilities, module.OfferingAvailable, model)
		})
	}
	// The turn plan's Catalog view (#380 step 2): the Offerings a turn resolves
	// its capabilities against, and which of them can run now. Wired here
	// because availability is runtime state.
	chatService.SetTurnPlanCatalog(func() []generation.Offering {
		return module.Offerings("")
	}, module.OfferingAvailable)
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
		// A plan stored before #426 pinned the Agent's model name; the read
		// view resolves it to the Offering ID of the current Catalog (#434).
		Budgets: budgetPlanStore(repository, module.Offerings),
		Policy:  approvalPolicy, Modes: chatService,
		// An Agent approval stays approvable for the Schema's duration; after
		// that it can only be requested again in a new turn.
		PendingTTL: time.Duration(applicationSchema.Chat.PendingApprovals.TTLSeconds) * time.Second,
	}
	// #410: collect the plans that finished while nothing settled — a
	// deployment that was stopped when the last step succeeded, or a plan whose
	// steps were paid without a reservation. It runs once here; the sweep only
	// reconsiders the plans whose reservations it settled, so an abandoned plan
	// is not re-read every two seconds.
	if completed, err := executionGrants.CompleteFinishedBudgets(context.Background()); err != nil {
		logger.Warn("启动时收尾已完成的制作计划失败", "error", err)
	} else if len(completed) > 0 {
		logger.Info("启动时收尾已完成的制作计划", "count", len(completed))
	}
	// Budget-mode plans (#103) need the grant service, which needs the chat
	// service for the session mode; install the writer once both exist.
	chatService.SetProductionPlans(chat.ProductionPlanPolicy{
		Budget: applicationSchema.Chat.ApprovalPolicy.Budget,
		Offerings: func() []generation.Offering {
			return module.Offerings("")
		},
		OfferingAvailable: module.OfferingAvailable,
		Store:             executionGrants,
		Checkpoints:       selfCheckCheckpointIDs(applicationSchema.Chat.SelfCheck),
		// #242: a plan step may declare parallel candidates.
		Candidates: applicationSchema.Chat.Candidates,
		// #251: the server derives acceptance and checkpoint thresholds from
		// the purpose budget and the production review settings.
		Usage:      applicationSchema.Chat.Model3DUsage,
		Production: applicationSchema.Chat.ModelReview.Inspect.Production,
		// #380 step 2: the plan quotes a step with the same confirmation rule
		// the generation tools apply, so an approved plan covers the step that
		// runs.
		ConfirmsWorkspace: applicationSchema.Chat.Presentation.Messages.ModelChoiceWorkspaceConflict != "",
	})
	// #331: the read-only crop_image_regions tool cuts normalized regions out
	// of one owned picture at original resolution and stores every crop as a
	// list_crop input asset. The decompose section declares the product item
	// cap and the crop size bounds; a Catalog without it keeps the package
	// defaults. The source decode bounds are the mask tool's, and a Task source
	// is copied and decoded under that same byte cap. The storage guardrail is
	// a multiple of the product cap because re-framing accumulates crop assets
	// and old crops are never collected; the asset-list rules keep enforcing
	// maximum_items_per_image itself. The same service backs the edit crop
	// instruction, through the asset-list Config port.
	cropConfig := listcrop.Config{
		Inputs:          cropInputAssets{inputs: inputAssetService, loader: repository},
		Tasks:           module,
		Crops:           repository.InputAssetCropRepository(),
		SourceMaxBytes:  applicationSchema.Chat.ImageMask.Limits.MaxBytes,
		SourceMaxPixels: applicationSchema.Chat.ImageMask.Limits.MaxPixels,
		SourceAssetType: applicationSchema.Runtime.InputAssets.DefaultAssetType,
	}
	// One localizer serves the Agent turn and its tool results. It is built
	// before the list service so the list's own Schema copy (the per-item
	// review answers, the batch archive's README) reads in the request's
	// language too.
	texts, err := catalog.NewTextLocalizer(applicationSchema)
	if err != nil {
		return nil, err
	}
	if applicationSchema.AssetList != nil && applicationSchema.AssetList.Decompose != nil {
		decompose := applicationSchema.AssetList.Decompose
		cropConfig.MaximumCropsPerSource = decompose.MaximumItemsPerImage * listcrop.StorageCropLimitMultiplier
		cropConfig.MinimumSide = decompose.Crop.MinimumShortEdge
		cropConfig.MaximumRegionPixels = decompose.Crop.MaximumRegionPixels
		cropConfig.UploadMaxBytes = decompose.Crop.MaximumPNGBytes
	}
	cropService, err := listcrop.New(cropConfig)
	if err != nil {
		return nil, fmt.Errorf("裁剪服务: %w", err)
	}
	// Production lists (#327): the service reuses the plan's offering
	// selection through the chat adapters, so an explicit model lock and an
	// unavailable provider are reflected in the availability snapshot. The
	// feature is absent when the Catalog declares no asset_list section.
	var assetListService *assetlist.Service
	var assetListPresence assetlist.PresenceStore
	// assetListCounted is the step policy's 3D capability set; the list
	// service and the review snapshot share it.
	var assetListCounted map[generation.Capability]bool
	// assetListTasks reads the terminal Tasks of an item's own sub-session. The
	// completion tool and the runner (#328) share it.
	var assetListTasks chat.AssetListTaskSource
	if applicationSchema.AssetList != nil {
		if applicationSchema.Project == nil || applicationSchema.StepPolicy == nil {
			return nil, fmt.Errorf("资产清单: application.asset_list 需要 application.project 与 application.step_policy")
		}
		counted := make(map[generation.Capability]bool, len(applicationSchema.StepPolicy.CountedCapabilities))
		for _, capability := range applicationSchema.StepPolicy.CountedCapabilities {
			counted[generation.Capability(capability)] = true
		}
		assetListCounted = counted
		// The batch archive (#329c) reuses the model delivery's OBJ rules
		// (application.model_delivery.archive.obj_package) so a packed OBJ keeps
		// its library and texture names; a Catalog without that section keeps
		// the plain layout.
		var assetListObjPackage *catalog.ModelObjPackageSchema
		if !applicationSchema.ModelDelivery.Disabled() {
			if spec := applicationSchema.ModelDelivery.Archive.ObjPackage; spec.FormatID != "" &&
				strings.TrimSpace(spec.LibraryStatement) != "" {
				assetListObjPackage = &spec
			}
		}
		assetListService, err = assetlist.New(assetlist.Config{
			Repository: repository.AssetListRepository(),
			Runs:       repository.AssetListRunRepository(),
			Presence:   repository.AssetListPresenceRepository(),
			Reviews:    repository.AssetListReviewRepository(),
			References: assetListReferenceAssets{
				inputs: inputAssetService,
				crops:  repository.InputAssetCropRepository(),
			},
			Crops:              cropService,
			Archive:            module,
			ObjPackage:         assetListObjPackage,
			Schema:             *applicationSchema.AssetList,
			Texts:              texts,
			ProjectMaxItems:    applicationSchema.Project.Limits.MaximumListItems,
			MaxListsPerProject: applicationSchema.AssetList.Limits.MaxListsPerProject,
			Counted:            counted,
			Steps:              assetListStepsConfig(executionGrants, applicationSchema.StepPolicy),
			ProjectAssets:      projectService,
			ProjectAssetLimits: assetlist.ProjectAssetLimits{
				MaximumAssets: applicationSchema.Project.Limits.MaximumAssetsPerProject,
				MaximumImages: applicationSchema.Project.Limits.MaximumAssetImages,
				MaximumModels: applicationSchema.Project.Limits.MaximumAssetModels,
			},
			ResolveStepFor: func(ctx context.Context, ownerID, sessionID, capability string) (assetlist.RouteStep, error) {
				return chatService.ResolveAssetListStep(ctx, ownerID, sessionID, generation.Capability(capability))
			},
			ValidateBrief: chatService.ValidateAssetListBrief,
		})
		if err != nil {
			return nil, fmt.Errorf("资产清单: %w", err)
		}
		// The completion tool reports only the Tasks of the item's own
		// sub-session; branch ancestors are another session's work. The runner
		// reads the same source, which reads the reported Task by id first
		// (#473).
		assetListTasks = assetListTaskSource(module)
		// The proposal tool validates and stores through the service; its copy
		// comes from the same Schema section (never hardcoded).
		chatService.SetAssetLists(chat.AssetListPolicy{
			Service: assetListService,
			Schema:  *applicationSchema.AssetList,
			// #459: the same Schema rule the coverage projection uses decides
			// which post-success slots a deferred report waits for.
			Delivery: applicationSchema.ModelDelivery,
			// Item sub-sessions derive the internal steps mode only when the
			// deployment declares application.step_policy (#330).
			StepsEnabled: applicationSchema.StepPolicy != nil,
			Tasks:        assetListTasks,
		})
		// The browser heartbeat (#328) is served only when the Catalog
		// declares execution; a Catalog without it keeps the route
		// unregistered and the list behavior of #327 unchanged.
		if applicationSchema.AssetList.Execution != nil {
			assetListPresence = repository.AssetListPresenceRepository()
		}
	}
	// The kernel paid-gate fallback (#328 §2.5.5, third layer) reuses the same
	// session→item→run→presence read as the tool Preflight, so a SubmitPrepared
	// that bypassed the earlier layers cannot create a paid Task while the list
	// is paused. The helper returns nil outside a declared execution section.
	module.SetSubmissionGate(newAssetListKernelSubmissionGate(assetListService, applicationSchema.AssetList))
	paramsStore := agentparams.NewPersistentParameterStore(repository, logger)
	// The ledger's parameter source is the existing parameter store's
	// last-writer state (#248): when the person's pin still equals the frozen
	// value, the step reports "user"; otherwise the source stays unknown. A
	// read failure becomes a ledger omission. No new store or migration.
	chatService.SetParameterSource(func(_ context.Context, ownerID, sessionID, offeringID string) (map[string]chat.WorkParameterState, error) {
		states := paramsStore.OfferingStates(agentparams.ScopeKey(ownerID, sessionID), offeringID)
		result := make(map[string]chat.WorkParameterState, len(states))
		for name, state := range states {
			result[name] = chat.WorkParameterState{Value: state.Value, Source: state.Source}
		}
		return result, nil
	})
	// Provider balances and the person's 3D step settings serve both the
	// external MCP status block (#332) and the platform Agent's step segment
	// (#330). They are built before either, so both read one balance cache and
	// one settings service.
	providerAccountService, err := buildProviderAccounts(config, applicationSchema.Runtime)
	if err != nil {
		return nil, err
	}
	accountSettingsService, err := accountSettingsFromSchema(repository, applicationSchema.StepPolicy)
	if err != nil {
		return nil, fmt.Errorf("账户设置（3D 步数模式）: %w", err)
	}
	var stepBalanceCache *provideraccounts.Cache
	if policy := applicationSchema.StepPolicy; policy != nil &&
		policy.BalanceCacheSeconds > 0 && policy.BalanceReadTimeoutSeconds > 0 {
		stepBalanceCache, err = provideraccounts.NewCache(providerAccountService, provideraccounts.CacheConfig{
			TTL:     time.Duration(policy.BalanceCacheSeconds) * time.Second,
			Timeout: time.Duration(policy.BalanceReadTimeoutSeconds) * time.Second,
			Logger:  logger,
		})
		if err != nil {
			return nil, fmt.Errorf("余额读取缓存: %w", err)
		}
	}
	// #251: the acceptance wording is frozen in the proposal turn's language.
	// Text takes the turn locale; an empty locale would never translate.
	chatService.SetProductionPlanLocalizer(texts.Text)
	imageReview, err := imagereview.New(imagereview.Config{
		Context: applicationSchema.Chat.Context,
		Review:  applicationSchema.Chat.ImageReview,
		Inputs:  inputAssetService,
		Tasks:   module,
		// #282/#284/#287: the delivery checklist judges a picture against the
		// session's 2D purpose, and names the Offerings that can reach its
		// minimum resolution from the Catalog.
		Usage: applicationSchema.Chat.Image2DUsage,
		Brief: applicationSchema.Chat.Model3DUsage.Brief,
		// Only Offerings the runtime can run are named as a fix: a declared
		// but unconfigured model would be refused again (AGENTS.md).
		Offerings: func(id string) (generation.Offering, bool) {
			offering, found := taskCatalog.Get(generation.OfferingID(id))
			if !found || !module.OfferingAvailable(offering.ID) {
				return generation.Offering{}, false
			}
			return offering, true
		},
	})
	if err != nil {
		return nil, err
	}
	// The persisted 3D self-check record (#246) serves both the tools and the
	// session read route. Its store is the chat repository; the record is
	// session data and never rewrites a Task.
	selfCheckService := chat.NewSelfCheckService(repository, applicationSchema.Chat.SelfCheck)
	chatService.SetSelfChecks(repository, applicationSchema.Chat.SelfCheck)
	// #244: the read-only create_selection_mask tool builds its mask locally and
	// stores it as the owner's input asset; no upstream call, Task or credits.
	imageMask, err := imagemask.New(imagemask.Config{
		Context:          applicationSchema.Chat.Context,
		Mask:             applicationSchema.Chat.ImageMask,
		Inputs:           inputAssetService,
		Tasks:            module,
		AssetItemRoute:   applicationSchema.Runtime.HTTP.Routes.AssetItem,
		DefaultAssetType: applicationSchema.Runtime.InputAssets.DefaultAssetType,
	})
	if err != nil {
		return nil, err
	}
	// #331: the read-only crop_image_regions tool cuts normalized regions out
	// of one owned picture. The service is built above, beside the asset list,
	// because an edit crop instruction reuses it through the asset-list port.
	// A custom Catalog without application.chat.image_crop leaves the tool
	// unassembled instead of refusing startup: the executor answers its calls
	// with the unavailable result.
	var cropToolExecute func(context.Context, chat.ToolCommand) (chat.ToolResult, error)
	if strings.TrimSpace(applicationSchema.Chat.ImageCrop.Tool) != "" {
		cropTool, err := agenttools.NewCropTool(agenttools.CropToolConfig{
			Context:  applicationSchema.Chat.Context,
			Schema:   applicationSchema.Chat.ImageCrop,
			Crops:    cropService,
			Localize: texts.Text,
		})
		if err != nil {
			return nil, fmt.Errorf("裁剪工具: %w", err)
		}
		cropToolExecute = cropTool.Execute
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
		// #296: the parameters list of a tool result names who wrote a remembered
		// value, the person's pin or the Agent's setting, from the same store.
		OfferingParameterStates: func(ownerID, sessionID, offeringID string) map[string]generationtool.RememberedParameter {
			states := map[string]generationtool.RememberedParameter{}
			for name, state := range paramsStore.OfferingStates(agentparams.ScopeKey(ownerID, sessionID), offeringID) {
				if (state.Source == "user" || state.Source == "agent") && len(state.Value) > 0 {
					states[name] = generationtool.RememberedParameter{Value: state.Value, Source: state.Source}
				}
			}
			return states
		},
		Gateway:         module,
		Tasks:           module,
		Operations:      repository,
		Grants:          executionGrants,
		StopRequested:   chatService.TurnStopRequested,
		References:      referenceResolver,
		ModelAssets:     module,
		InputAssets:     inputAssetService,
		ImageReview:     imageReview,
		PictureReader:   imageReview,
		SelfChecks:      selfCheckService,
		InputAssetRoute: applicationSchema.Runtime.HTTP.Routes.AssetItem,
		Chat:            applicationSchema.Chat,
		Project:         applicationSchema.Project,
		StepPolicy:      applicationSchema.StepPolicy,
		CanvasArtboard:  applicationSchema.Canvas.Frontend.Artboard,
		ModelDelivery:   applicationSchema.ModelDelivery,
		Skills:          skillCatalog,
		TaskStates:      applicationSchema.TaskStates,
	})
	if err != nil {
		return nil, err
	}
	// A planned step is checked against the session purpose's budget by the
	// generation tools that own it (#129).
	chatService.SetProductionPlanPurposeBudget(toolExecutor.PlanStepPurposeBudgetNote)
	// #329b: the acceptance snapshot reuses the tool executor's model reading,
	// the image review service, the persisted self-check records and the chat
	// service's purpose thresholds. The write ports are optional: without them
	// the board still reads and every item keeps waiting.
	if assetListService != nil {
		assetListService.SetReview(assetlist.ReviewConfig{
			Snapshots: assetreview.New(assetreview.Config{
				Checks:          chatService.AssetListReviewChecks,
				Models:          toolExecutor,
				Images:          imageReview,
				SelfChecks:      chatService,
				SelfCheckSchema: applicationSchema.Chat.SelfCheck,
				Review:          applicationSchema.AssetList.Review,
				Texts:           texts,
				Steps:           executionGrants.StepStatus,
				Counted:         assetListCounted,
			}),
		})
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
		Offerings:             func() []generation.Offering { return module.Offerings("") },
		Context:               applicationSchema.Chat.Context,
		HistoryLookup:         chat.NewHistoryLookupPolicy(applicationSchema.Chat),
		Params:                paramsStore,
		Generate:              toolExecutor,
		ReadTask:              newAgentTaskReader(module, applicationSchema.Chat, applicationSchema.Content.Messages, texts),
		RecoverTask:           newAgentTaskRecoverer(module, applicationSchema.Chat, applicationSchema.Content.Messages, texts),
		ListModels:            toolExecutor.ListSessionModels,
		ReadProcessingPlan:    toolExecutor.ReadProcessingPlan,
		ReviseProcessingPlan:  toolExecutor.ReviseProcessingPlan,
		InspectImage:          imageReview.Inspect,
		CreateSelectionMask:   imageMask.Execute,
		CropImageRegions:      cropToolExecute,
		InspectModel:          toolExecutor.InspectModel,
		RenderModelViews:      toolExecutor.RenderModelViews,
		CompareModels:         toolExecutor.CompareModels,
		CompareModelReference: toolExecutor.CompareModelReference,
		RecordSelfCheck:       toolExecutor.RecordSelfCheck,
		ReadProductionStep:    chatService.ReadProductionStep,
		UpdateNotes:           chatService.SetSessionNotes,
		UpdateBrief: func(ctx context.Context, ownerID, sessionID string, brief map[string]json.RawMessage) error {
			return updateProductionBriefAudited(
				ctx,
				chatService,
				administrationAuditService,
				logger,
				applicationSchema.Chat.Audit.ProductionBriefAction,
				applicationSchema.Chat.Audit.SessionResourceType,
				ownerID,
				sessionID,
				brief,
			)
		},
		AskChoice:            chatService.AskChoice,
		ProposePlan:          chatService.ProposeProductionPlan,
		ProposeAssetList:     chatService.ProposeAssetList,
		SubmitAssetListItem:  chatService.SubmitAssetListItem,
		ReadAssetList:        chatService.ReadAssetList,
		WriteVisualAnchor:    chatService.WriteVisualAnchor,
		Project:              applicationSchema.Project,
		ChoiceKinds:          applicationSchema.Chat.Choice.Kinds,
		ChoiceOptionInvalid:  applicationSchema.Chat.Presentation.Messages.ChoiceOptionInvalid,
		BriefSavedMessage:    applicationSchema.Chat.Model3DUsage.Brief.SavedMessage,
		BriefInvalidTemplate: applicationSchema.Chat.Model3DUsage.Brief.InvalidTemplate,
		BriefMessages:        applicationSchema.Chat.Model3DUsage.Brief.Messages,
		Consent: func(command chat.ToolCommand, request json.RawMessage, snapshot json.RawMessage) error {
			scope := agentparams.ScopeKey(command.OwnerID, command.SessionID)
			return agentStateStore.BeginConsent(scope, request, snapshot)
		},
		Preflight:                 toolExecutor.SelectionError,
		SubmissionGate:            newAssetListSubmissionGate(assetListService, applicationSchema.AssetList),
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
		Projection: func(capability, model string) json.RawMessage {
			return buildAgentOfferingsProjection(
				applicationSchema.Capabilities,
				taskCatalog,
				func(id generation.OfferingID) bool {
					return module.OfferingAvailable(id)
				},
				capability,
				model,
				// Ids only while the model asks for no capability and no model:
				// the detail of every Offering is what made this call cost
				// 45-98 KB per round (#382).
				capability != "" || model != "",
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
		logger,
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
		// Prompt text for a capability without an enabled Offering is not sent
		// to the model (#115).
		orchestratorChatSchema(
			applicationSchema.Chat.WithoutInactivePromptText(catalog.ActiveCapabilities(taskCatalog.List(""))),
			llmModel,
		),
		config.AgentMaxToolCallsPerReply,
	)
	chatAgent, err := chat.NewOrchestrator(chat.OrchestratorConfig{
		Project: applicationSchema.Project,
		Texts:   texts,
		// #330: the platform Agent's 3D step segment reads the same facts as
		// the external MCP status block: the shared balance cache, the step
		// standing under the owner's setting and the per-step costs.
		Capabilities: applicationSchema.Capabilities,
		StepPolicy:   applicationSchema.StepPolicy,
		StepUsage: func(ctx context.Context, ownerID, sessionID string) (executiongrant.StepSummary, error) {
			return executionGrants.StepStatus(ctx, ownerID, sessionID)
		},
		StepBalances: stepBalancesFromSchema(applicationSchema, stepBalanceCache),
		StepCosts: func(ctx context.Context, ownerID, sessionID string) []chat.StepCost {
			if applicationSchema.StepPolicy == nil {
				return nil
			}
			costs, err := module.StepCosts(ctx, generation.OwnerID(ownerID), sessionID,
				applicationSchema.StepPolicy.CountedCapabilities, stepCostTaskWindow(applicationSchema))
			if err != nil {
				logger.Warn("3D 步数段：资产任务读取失败，每步费用按未知处理",
					"owner_id", ownerID, "session_id", sessionID, "error", err)
				return nil
			}
			result := make([]chat.StepCost, 0, len(costs))
			for _, cost := range costs {
				result = append(result, chat.StepCost{
					OfferingID: cost.OfferingID, Capability: cost.Capability,
					Amount: cost.Amount, UnitLabel: cost.UnitLabel,
				})
			}
			return result
		},
		Configuration: func(ctx context.Context, owner string) (chat.AgentConfigurationSnapshot, error) {
			return agentTurnConfiguration(ctx, owner, agentConfigurationService, applicationSchema, mcpService)
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
		VisionAssetInline: func(ctx context.Context, owner generation.OwnerID, assetID string) (string, bool) {
			return referenceResolver.OpenVisionAsset(ctx, owner, generation.AssetID(assetID))
		},
		// #250: an anchor reference may be an input asset ID or the canonical
		// Task asset URL of a generated result; the resolver proves the owner
		// and the session (including a branch's ancestor Tasks).
		VisionAnchorInline: referenceResolver.OpenVisionReference,
		SelectionPreview:   referenceResolver.OpenSelectionPreview,
		ToolAllowed: func(capability, _ string) bool {
			// ToolExecution.domains is the creative workspace (image/model).
			// CapabilityPresentation.domain is a product category, where
			// processing contains both background removal and 3D work.
			// The registry applies the workspace rule from Schema; this gate
			// only requires an actually available Offering for the capability.
			// #413: these three sources are read on every model_choices and
			// tool-list evaluation, so they borrow the Catalog's values instead
			// of deep-copying the Offering directory.
			for _, offering := range module.OfferingsShared(generation.Capability(capability)) {
				if module.OfferingRunnable(offering) {
					return true
				}
			}
			return false
		},
		OfferingAllowed: func(capability, modelID string) bool {
			for _, offering := range module.OfferingsShared(generation.Capability(capability)) {
				if offering.ModelID == modelID && module.OfferingRunnable(offering) {
					return true
				}
			}
			return false
		},
		// #249: the model enum and the craft brief follow the Offerings the
		// deployment can run now.
		Offerings: func(capability string) []generation.Offering {
			return module.OfferingsShared(generation.Capability(capability))
		},
		// #329 E': a generation tool's description says how many reference
		// pictures the turn's picked model uses.
		ToolNote: toolExecutor.ToolDescriptionNote,
		// #246: the self-check state block lists the session's finished 3D
		// results and their persisted records every turn.
		SelfCheckTasks: toolExecutor.SessionModelTasks,
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
	// The asset-list runner (#328 §2.1) is assembled only for a declared
	// execution section with enabled=true. It reuses the completion tool's Task
	// source, the chat service's server-side item turns, and the orchestrator's
	// queue; Run starts it beside the other background components.
	var assetListRunner *assetlistrunner.Runner
	if assetListService != nil && applicationSchema.AssetList.Execution != nil &&
		applicationSchema.AssetList.Execution.Enabled {
		assetListRunner = assetlistrunner.New(assetlistrunner.Config{
			Runs:      repository.AssetListRunRepository(),
			Lists:     assetListService,
			Chat:      chatService,
			Tasks:     assetListTasks,
			Scheduler: chatAgent,
			Presence:  repository.AssetListPresenceRepository(),
			StepCaps:  repository,
			// #329f: the list-end summary words each item's actual consumption
			// with the same per-asset step standing the board reads, and takes
			// its copy from the Catalog.
			Steps: func(ctx context.Context, ownerID, sessionID string) (int, error) {
				summary, err := executionGrants.StepStatus(ctx, ownerID, sessionID)
				return summary.Used, err
			},
			Texts:  texts,
			Schema: *applicationSchema.AssetList,
			Clock:  time.Now,
			Logger: logger,
		})
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
	// #251: checkpoints read the saved model through the review tool's bounded,
	// owner-scoped cache; a read failure is recorded as unknown, not a pass.
	processingService.WithModelReport(toolExecutor.ModelReport)
	// #292: texture_size is judged on the deliverables the purpose sized (the
	// exports), as inspect_model judges it, not on the native file.
	processingService.WithSizedTextureEdge(toolExecutor.SizedTextureEdge)
	toolExecutor.SetProcessing(processingService, processingCatalog{Catalog: taskCatalog, module: module})
	var modelDeliveryService *modeldelivery.Service
	if !applicationSchema.ModelDelivery.Disabled() {
		modelDeliveryService, err = modeldelivery.New(applicationSchema.ModelDelivery, module, processingService, logger)
		if err != nil {
			return nil, err
		}
		modelDeliveryService.Localize(texts)
		// The platform keeps its own delivery promise (#457): after a
		// generation succeeded, the formats it still owes are completed on our
		// own, and the durable record survives a restart. The kernel derives no
		// completion task; the reconciler only builds Processing Plans.
		modelDeliveryService.WithAutoCompletions(repository).WithClock(time.Now)
		// The administration download leaves out a repeated file as the one-click ZIP
		// does and names it in the same words (#300).
		administrationGenerationDownloadService.WithRepeatText(modelDeliveryService.RepeatText)
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
		// A format conversion delivers files of the model it converted, so the
		// session model listing and the self-check queue leave it out, using the
		// same rule the version chain folds conversions with (#458).
		toolExecutor.SetConversionRule(modelDeliveryService.IsConversion)
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
	// #242: "one more" replays a succeeded task's frozen input with fresh
	// seeds; without the Schema block the route stays unregistered.
	var taskRerollService *taskreroll.Service
	if applicationSchema.Chat.Reroll.Enabled {
		taskRerollService, err = taskreroll.New(taskreroll.Config{
			Kernel: module, Grants: executionGrants, Origins: taskRerollOrigins{repository: repository},
			OperationID: httpapi.StandaloneOperationID,
			Schema:      applicationSchema.Chat.Reroll,
		})
		if err != nil {
			return nil, err
		}
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
		// #246: an approval submitted in the background is still a paid
		// correction of the result the operation froze. The callback is keyed
		// by operation, so a retried sweep counts it once.
		ProcessingCorrection: func(ctx context.Context, op generationtool.Operation) error {
			_, _, err := selfCheckService.IncrementCorrectionsOnce(ctx,
				op.OwnerID, op.SessionID, op.CorrectionTaskID, generationtool.CorrectionKey(op))
			return err
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
			Assets: threeViewAssetStore{inputs: inputAssetService, tasks: module},
			Review: applicationSchema.Chat.ImageReview,
		})
		if err != nil {
			return nil, err
		}
		agentToolExecutor.SetThreeViewService(threeViewService)
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
	accessKeysService, accessKeyAuthenticator, err := accessKeysFromSchema(
		repository, repository, accessPolicy, applicationSchema.AccessKeys, logger)
	if err != nil {
		return nil, fmt.Errorf("个人访问 Key: %w", err)
	}
	accessKeyAdminService, err := accessKeyAdminFromSchema(repository, administrationAuditService, applicationSchema)
	if err != nil {
		return nil, fmt.Errorf("管理端访问 Key: %w", err)
	}
	mcpStatus, err := mcpStatusFromSchema(applicationSchema, accountSettingsService, repository,
		stepBalanceCache, chatService, texts, logger)
	if err != nil {
		return nil, fmt.Errorf("对外 MCP 状态块: %w", err)
	}
	mcpAssets, err := mcpAssetsFromSchema(applicationSchema, chatService, repository, texts)
	if err != nil {
		return nil, fmt.Errorf("对外 MCP 资产映射: %w", err)
	}
	var mcpIdentity httpapi.IdentityResolver
	if accessKeyAuthenticator != nil {
		mcpIdentity = accessKeyAuthenticator
	}
	// 未配置 asset_signing_key 时不装配签名器：对外 MCP 的 signed 下载链接不可用。
	var taskAssetLinks httpapi.TaskAssetLinkSigner
	if config.AssetSigningKey != "" {
		taskAssetLinks = newTaskAssetLinkSigner(assetlinks.NewSigner([]byte(config.AssetSigningKey)))
	}
	api, err := httpapi.New(httpapi.Config{
		MCPIdentity:                       mcpIdentity,
		MCPStatus:                         mcpStatus,
		MCPAssets:                         mcpAssets,
		ModelInspector:                    toolExecutor,
		ImageInspector:                    imageReview,
		PictureAssets:                     repository,
		Harness:                           harnessResolver,
		AgentConfiguration:                agentConfigurationService,
		LLMSettings:                       llmSettingsService,
		UpstreamCapture:                   upstreamCapture,
		MCP:                               mcpService,
		AccountInputAssets:                repository,
		AccountSettings:                   accountSettingsService,
		AccessKeys:                        accessKeysService,
		AdministrationAccessKeys:          accessKeyAdminService,
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
		Reroll:                            taskRerollService,
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
		Projects:                          projectService,
		AssetLists:                        assetListService,
		AssetListPresence:                 assetListPresence,
		AssetListCrops:                    repository.InputAssetCropRepository(),
		DirectStyleReferences:             referenceResolver,
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
		ProviderBalances:      providerBalanceReader(stepBalanceCache),
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
		TaskAssetLinks:        taskAssetLinks,
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
	mcpHandler, err := api.MCPPublicHandler()
	if err != nil {
		return nil, err
	}
	var mcpServer *http.Server
	switch {
	case mcpHandler != nil && config.MCPPublicAddress != "":
		mcpServer = newMCPServer(config.MCPPublicAddress, *applicationSchema.MCPPublic, mcpHandler)
	case mcpHandler != nil:
		logger.Info("对外 MCP 已在 Schema 启用，但运行设置 mcp_public_listen_addr 为空：不启动对外 MCP 监听")
	case config.MCPPublicAddress != "":
		logger.Warn("已配置 mcp_public_listen_addr，但 Schema 没有启用 application.mcp_public：不启动对外 MCP 监听")
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
		logger:                 logger,
		schema:                 applicationSchema,
		generation:             module,
		thumbnails:             thumbnailBackfill,
		chatAgent:              chatAgent,
		processing:             processingService,
		assetListRunner:        assetListRunner,
		scene:                  sceneService,
		pendingApprovals:       pendingApprovalService,
		manifests:              manifestService,
		modelDelivery:          modelDeliveryService,
		accessKeyAuthenticator: accessKeyAuthenticator,
		mcpServer:              mcpServer,
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

// deliveryAutoCompleteInterval is how often the automatic format completion
// looks at its durable record. The Product Schema declares it; the coordinator's
// poll interval is the fallback of a Schema that declares none.
func deliveryAutoCompleteInterval(application catalog.ApplicationSchema) time.Duration {
	if milliseconds := application.ModelDelivery.AutoCompletion.PollIntervalMilliseconds; milliseconds > 0 {
		return time.Duration(milliseconds) * time.Millisecond
	}
	return time.Duration(application.Runtime.Coordinator.PollIntervalMilliseconds) * time.Millisecond
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

// providerBalanceReader hands the shared balance cache to the web balance route
// (#385), or nothing when the Schema does not declare one. The check keeps a nil
// *Cache from becoming a non-nil interface.
func providerBalanceReader(cache *provideraccounts.Cache) httpapi.ProviderBalanceReader {
	if cache == nil {
		return nil
	}
	return cache
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
	mcpListener, err := a.bindMCP()
	if err != nil {
		return errors.Join(err, listener.Close(), a.generation.Close())
	}
	components := componentCount
	if mcpListener != nil {
		components++
	}
	if a.assetListRunner != nil {
		components++
	}
	results := make(chan componentResult, components)
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
		if a.modelDelivery == nil {
			results <- componentResult{name: "delivery-auto-complete", err: runCtx.Err()}
			return
		}
		interval := deliveryAutoCompleteInterval(a.schema)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			processed, reconcileErr := a.modelDelivery.ReconcileAutoCompletions(runCtx, 100)
			if reconcileErr != nil && runCtx.Err() == nil {
				a.logger.Error("格式自动补齐对账失败", "error", reconcileErr)
			} else if processed > 0 {
				a.logger.Info("格式自动补齐对账完成", "count", processed)
			}
			select {
			case <-runCtx.Done():
				results <- componentResult{name: "delivery-auto-complete", err: runCtx.Err()}
				return
			case <-ticker.C:
			}
		}
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
	if mcpListener != nil {
		go func() { results <- componentResult{name: "mcp", err: a.serveMCP(mcpListener)} }()
	}
	if a.assetListRunner != nil {
		// The loop returns as soon as runCtx ends; it never delays shutdown.
		go func() {
			a.assetListRunner.Run(runCtx)
			results <- componentResult{name: "asset-list-runner"}
		}()
	}

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
	if a.mcpServer != nil {
		shutdownErr = errors.Join(shutdownErr, httpapi.Shutdown(a.mcpServer, shutdownTimeout))
	}
	waitTimer := time.NewTimer(shutdownTimeout)
	defer waitTimer.Stop()
	for received < components {
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
			received = components
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
	policy := approvalpolicy.New(declared.DefaultMode, declared.Modes, manual, known)
	if application.StepPolicy != nil {
		// The internal steps mode exists only when the Catalog declares the 3D
		// step policy (#330). It is not an offered mode: nothing assigns it to a
		// session yet, and a person can never pick it.
		policy = policy.WithSteps(stepsPolicyFromSchema(*application.StepPolicy))
	}
	return policy
}

// stepsPolicyFromSchema turns application.step_policy into the pure decision
// input: the capabilities that count as 3D steps and the setting of a person who
// never saved one, per entry.
func stepsPolicyFromSchema(declared catalog.StepPolicySchema) approvalpolicy.StepsPolicy {
	counted := make([]string, 0, len(declared.CountedCapabilities))
	for _, capability := range declared.CountedCapabilities {
		counted = append(counted, string(capability))
	}
	return approvalpolicy.StepsPolicy{
		Counted:           counted,
		DefaultList:       approvalpolicy.StepSetting{Mode: declared.DefaultMode.List, Cap: declared.Cap.Default},
		DefaultMCP:        approvalpolicy.StepSetting{Mode: declared.DefaultMode.MCP, Cap: declared.Cap.Default},
		ExtraMaximum:      declared.ExtraSteps.Maximum,
		ExtraTotalMaximum: declared.ExtraSteps.TotalMaximum,
	}
}

// accountSettingsFromSchema builds the person's settings service from the 3D
// step policy (#330); nil when the Catalog does not declare one.
func accountSettingsFromSchema(store accountsettings.Store, declared *catalog.StepPolicySchema) (*accountsettings.Service, error) {
	if declared == nil {
		return nil, nil
	}
	modes := make([]string, 0, len(declared.Modes))
	for _, mode := range declared.Modes {
		modes = append(modes, mode.Value)
	}
	return accountsettings.New(store, accountsettings.Config{
		Modes:      modes,
		Steps:      stepsPolicyFromSchema(*declared),
		CapMinimum: declared.Cap.Minimum,
		CapMaximum: declared.Cap.Maximum,
	})
}

// assetListStepsConfig adapts the grant service's per-asset extra-step write
// to the list service (#330 step 5C). The list layer stays independent of the
// grant package: the errors are mapped here, and the bounds come from the same
// Schema section.
func assetListStepsConfig(grants *executiongrant.Service, declared *catalog.StepPolicySchema) *assetlist.StepsConfig {
	if grants == nil || declared == nil {
		return nil
	}
	return &assetlist.StepsConfig{
		Maximum:      declared.ExtraSteps.Maximum,
		TotalMaximum: declared.ExtraSteps.TotalMaximum,
		Set: func(ctx context.Context, ownerID, sessionID string, target int) (assetlist.StepChange, error) {
			change, err := grants.SetExtraSteps(ctx, ownerID, sessionID, target)
			if err != nil {
				switch {
				case errors.Is(err, executiongrant.ErrExtraStepsLimit):
					return assetlist.StepChange{}, &assetlist.StepExtraLimitError{
						Maximum: declared.ExtraSteps.TotalMaximum, Requested: target,
					}
				case errors.Is(err, executiongrant.ErrExtraStepsSingleLimit):
					return assetlist.StepChange{}, &assetlist.StepExtraSingleLimitError{
						Maximum: declared.ExtraSteps.Maximum,
					}
				case errors.Is(err, executiongrant.ErrExtraStepsUnavailable):
					return assetlist.StepChange{}, assetlist.ErrStepUnavailable
				case errors.Is(err, executiongrant.ErrConflict):
					return assetlist.StepChange{}, assetlist.ErrConflict
				case errors.Is(err, executiongrant.ErrInvalid):
					return assetlist.StepChange{}, assetlist.ErrInvalid
				}
				return assetlist.StepChange{}, err
			}
			return assetlist.StepChange{
				Previous: assetListStepState(change.Previous),
				Current:  assetListStepState(change.Current),
			}, nil
		},
	}
}

// assetListStepState keeps the wire shape one place: the cap and the extra
// grant travel together, and only in the cap mode.
func assetListStepState(summary executiongrant.StepSummary) assetlist.StepState {
	state := assetlist.StepState{Mode: summary.Mode, Used: summary.Used}
	if summary.Mode == approvalpolicy.StepModeCap {
		state.Cap, state.Extra = summary.Cap, summary.Extra
	}
	return state
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

// selfCheckCheckpointIDs lists the plan checkpoint ids the Product Schema
// declares (#246). The interface is fixed here; #251 owns revising plans.
func selfCheckCheckpointIDs(schema catalog.ChatSelfCheckSchema) []string {
	ids := make([]string, 0, len(schema.Checkpoints))
	for _, checkpoint := range schema.Checkpoints {
		ids = append(ids, checkpoint.ID)
	}
	return ids
}

// taskRerollOrigins adapts the repository's admission lookup to the reroll
// package's own Origin type (#242).
type taskRerollOrigins struct{ repository *sqlite.Repository }

func (origins taskRerollOrigins) PreparedInputOf(ctx context.Context, owner, task string) (taskreroll.Origin, error) {
	origin, err := origins.repository.PreparedInputOf(ctx, owner, task)
	return taskreroll.Origin{PreparedInputID: origin.PreparedInputID, PlanStepID: origin.PlanStepID}, err
}

func (origins taskRerollOrigins) GrantByKey(ctx context.Context, owner, key string) (executiongrant.Grant, bool, error) {
	return origins.repository.GrantByKey(ctx, owner, key)
}

// chatToolArgumentNames lists each declared generation tool's parameter names,
// so an Agent-supplied choice option can only carry arguments the target tool
// really accepts (#242).
func chatToolArgumentNames(tools []catalog.ChatToolSchema) map[string][]string {
	names := make(map[string][]string, len(tools))
	for _, tool := range tools {
		var parameters struct {
			Properties map[string]json.RawMessage `json:"properties"`
		}
		if json.Unmarshal(tool.Parameters, &parameters) != nil || len(parameters.Properties) == 0 {
			continue
		}
		arguments := make([]string, 0, len(parameters.Properties))
		for name := range parameters.Properties {
			arguments = append(arguments, name)
		}
		names[tool.Name] = arguments
	}
	return names
}
