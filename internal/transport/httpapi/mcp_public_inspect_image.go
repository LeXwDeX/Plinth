package httpapi

import (
	"errors"
	"strconv"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/imagereview"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/imageinspect"
	"github.com/LeXwDeX/Plinth/internal/mcppublic"
)

// The bounds of the external MCP inspect_image tool (#332 step 7b): at most
// mcpImageSlotsInFlight pictures are decoded at once, and a call that finds
// every slot taken is answered with image_busy and a short wait.
const (
	mcpImageSlotsInFlight   = 2
	mcpImageBusyWaitSeconds = 2
)

// mcpImageSourceView names the inspected picture and where it came from. Nils
// are written as null, as the schema declares.
type mcpImageSourceView struct {
	Source       string  `json:"source"`
	InputAssetID *string `json:"input_asset_id"`
	TaskID       *string `json:"task_id"`
	AssetID      *string `json:"asset_id"`
	MediaType    string  `json:"media_type"`
	Size         int64   `json:"size"`
}

// mcpImageUsageCheck is the checklist verdict block. The purpose fields are
// always present and say null when no purpose judged the picture (3d_input).
type mcpImageUsageCheck struct {
	Checklist     string               `json:"checklist"`
	Checks        []imageinspect.Check `json:"checks"`
	Purpose       *string              `json:"purpose"`
	PurposeLabel  *string              `json:"purpose_label"`
	PurposeSource *string              `json:"purpose_source"`
	Note          *string              `json:"note"`
}

// mcpVisualItemView is one item only a look at the picture can judge (#286).
// inspect_image lists them; it never takes a verdict.
type mcpVisualItemView struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Instruction string `json:"instruction"`
	FixHint     string `json:"fix_hint"`
	// Remedies is the structured form of FixHint (#332), the same shape the
	// model inspection reports: the item's id as the code to branch on, the
	// sentence, and what it costs. Judging such an item is done by eye and the
	// fix is a redraw, which the step policy does not count, so a remedy here
	// names no capability and costs 0 steps. FixHint stays for clients that
	// only read the sentence.
	Remedies []mcpRemedy `json:"remedies,omitempty"`
}

// mcpRemedy is one executable fix of a failed item (#332): the id an Agent can
// branch on (Code), the Schema's own sentence for it (Message), the
// capabilities that do it in declaration order, and how many 3D steps they cost
// (Steps). It is the outward MCP shape of the model inspection's remedies.
type mcpRemedy struct {
	Code         string   `json:"code"`
	Message      string   `json:"message"`
	Steps        int      `json:"steps"`
	Capabilities []string `json:"capabilities,omitempty"`
}

// mcpInspectedPicture is the resolved source of one inspection before the
// bytes are read: the request core, its reported identity, and the session
// whose brief it judged (empty for a direct upload).
type mcpInspectedPicture struct {
	request imagereview.InspectRequest
	view    mcpImageSourceView
	session string
}

// mcpInspectImage is inspect_image of the external MCP service (#332 step 7b):
// the same read-only inspection the platform tool runs, over an uploaded
// picture or a saved Task picture. It never writes self-check evidence, counts
// a step or creates a Task.
func (s *Server) mcpInspectImage(call *mcpCall) (mcpOutcome, error) {
	imageID := mcpStringArgument(call, "image")
	taskID := mcpStringArgument(call, "task_id")
	assetID := mcpStringArgument(call, "asset_id")
	checklist := mcpStringArgument(call, "checklist")
	if checklist == "" {
		checklist = "delivery"
	}
	switch {
	case imageID != "" && taskID != "":
		return s.mcpInvalidArguments(call.locale, "$.image: 与 task_id 二选一"), nil
	case imageID == "" && taskID == "":
		return s.mcpInvalidArguments(call.locale, "$: image 或 task_id 必须给一个"), nil
	case assetID != "" && taskID == "":
		return s.mcpInvalidArguments(call.locale, "$.asset_id: 只能与 task_id 同用"), nil
	}
	purpose := mcpStringArgument(call, "purpose")
	if purpose != "" {
		// The 3D input checklist has its own gate and judges no 2D purpose.
		if checklist == "3d_input" {
			return s.mcpInvalidArguments(call.locale, "$.purpose: 不能与 checklist=3d_input 同用"), nil
		}
		if !s.mcpImagePurposeKnown(purpose) {
			return s.mcpInvalidArguments(call.locale, "$.purpose: 未知用途，可用 "+s.mcpImagePurposeIDs()), nil
		}
	}
	if s.imageInspector == nil || s.inputAssets == nil {
		return mcpOutcome{problem: &mcpProblem{
			Code:    "image_unavailable",
			Message: s.mcpText(call.locale, s.application.MCPPublic.Messages.ImageUnavailable),
		}}, nil
	}
	// A full pool is a refusal, not a queue: the listener never holds more
	// picture memory than the slots allow.
	if s.mcpImageSlots != nil {
		select {
		case s.mcpImageSlots <- struct{}{}:
			defer func() { <-s.mcpImageSlots }()
		default:
			seconds := mcpImageBusyWaitSeconds
			return mcpOutcome{problem: &mcpProblem{
				Code:              "image_busy",
				Message:           s.mcpFill(call.locale, s.application.MCPPublic.Messages.ImageBusy, "{seconds}", strconv.Itoa(seconds)),
				RetryAfterSeconds: &seconds,
			}}, nil
		}
	}
	picture, missing, err := s.mcpImagePicture(call, imageID, taskID, assetID)
	if err != nil {
		return mcpOutcome{}, err
	}
	if missing != nil {
		return *missing, nil
	}
	picture.request.Checklist = checklist
	picture.request.Purpose = purpose
	picture.request.MaxPixels = s.application.MCPPublic.InspectImage.MaxPixels
	inspection, err := s.imageInspector.InspectOwned(call.ctx, picture.request)
	if err != nil {
		outcome, failure := s.mcpInspectImageProblem(call, picture, err)
		if failure != nil {
			return mcpOutcome{}, failure
		}
		return outcome, nil
	}
	subject := mcppublic.Subject{}
	if picture.session != "" {
		marked, err := s.mcpAssetSessions(call, picture.session)
		if err != nil {
			return mcpOutcome{}, err
		}
		if marked[picture.session] {
			subject.AssetID, subject.Costs = picture.session, s.mcpStepCosts(call.ctx, call.owner, picture.session)
		}
	}
	picture.view.MediaType, picture.view.Size = inspection.MediaType, inspection.Size
	usage := mcpImageUsageCheck{Checklist: checklist, Checks: inspection.Checks}
	if usage.Checks == nil {
		usage.Checks = []imageinspect.Check{}
	}
	if inspection.Purpose != nil {
		usage.Purpose, usage.PurposeLabel, usage.PurposeSource = &inspection.Purpose.ID, &inspection.Purpose.Label, &inspection.Purpose.Source
		if inspection.Purpose.Note != "" {
			usage.Note = &inspection.Purpose.Note
		}
	}
	data := map[string]any{"image": picture.view, "report": inspection.Report, "usage_check": usage}
	if checklist == "3d_input" {
		data["visual_items"] = s.mcpImageVisualItems()
	}
	return mcpOutcome{data: data, subject: subject}, nil
}

// mcpImagePicture resolves the named picture: either a Task of the caller
// (task_id, with an optional asset_id) or a picture ID (image) that may be a
// direct upload or a saved Task output.
func (s *Server) mcpImagePicture(call *mcpCall, imageID, taskID, assetID string) (*mcpInspectedPicture, *mcpOutcome, error) {
	if taskID != "" {
		task, missing, err := s.mcpOwnedTask(call, taskID)
		if err != nil {
			return nil, nil, err
		}
		if missing != nil {
			return nil, missing, nil
		}
		asset, problem := s.mcpTaskImageAsset(call, task, assetID)
		if problem != nil {
			return nil, problem, nil
		}
		brief, err := s.chat.SessionBrief(call.ctx, string(call.owner), task.SessionID)
		if err != nil {
			brief = chat.ProductionBrief{}
		}
		source := string(task.ID)
		output := string(asset.ID)
		return &mcpInspectedPicture{
			request: imagereview.InspectRequest{
				Owner: call.owner, Source: imagereview.PictureSource{TaskID: task.ID, TaskAsset: asset.ID}, Brief: brief,
			},
			view:    mcpImageSourceView{Source: "task_asset", TaskID: &source, AssetID: &output},
			session: task.SessionID,
		}, nil, nil
	}
	asset, body, err := s.inputAssets.OpenAuthorized(call.ctx, call.owner, generation.AssetID(imageID), false)
	if err != nil {
		resolved, missing, lookupErr := s.mcpPictureAsset(call, imageID)
		if lookupErr != nil {
			return nil, nil, lookupErr
		}
		if missing != nil {
			return nil, missing, nil
		}
		asset = resolved
	} else if body != nil {
		// Only the metadata is needed; the core reads the bytes for its owner.
		body.Close()
	}
	if asset.TaskID != "" {
		if asset.Role != "image" {
			problem := mcpProblemOutcome("asset_not_found", s.mcpText(call.locale, s.application.MCPPublic.Messages.AssetNotImage), mcppublic.Subject{})
			return nil, &problem, nil
		}
		if asset.StorageKey == "" {
			problem := mcpProblemOutcome("image_not_ready", s.mcpText(call.locale, s.application.MCPPublic.Messages.ImageNotReady), mcppublic.Subject{})
			return nil, &problem, nil
		}
		brief, err := s.chat.SessionBrief(call.ctx, string(call.owner), asset.SessionID)
		if err != nil {
			brief = chat.ProductionBrief{}
		}
		source := string(asset.TaskID)
		output := string(asset.ID)
		return &mcpInspectedPicture{
			request: imagereview.InspectRequest{
				Owner: call.owner, Source: imagereview.PictureSource{TaskID: asset.TaskID, TaskAsset: asset.ID}, Brief: brief,
			},
			view:    mcpImageSourceView{Source: "task_asset", TaskID: &source, AssetID: &output},
			session: asset.SessionID,
		}, nil, nil
	}
	if !strings.HasPrefix(strings.ToLower(asset.MediaType), "image/") {
		missing := mcpProblemOutcome("image_not_found", s.mcpText(call.locale, s.application.MCPPublic.Messages.ImageNotFound), mcppublic.Subject{})
		return nil, &missing, nil
	}
	id := string(asset.ID)
	return &mcpInspectedPicture{
		request: imagereview.InspectRequest{
			Owner: call.owner, Source: imagereview.PictureSource{InputAsset: asset.ID}, Brief: chat.ProductionBrief{},
		},
		view: mcpImageSourceView{Source: "input_asset", InputAssetID: &id},
	}, nil, nil
}

// mcpPictureAsset resolves a picture ID the input-asset open path does not
// hold: a saved output of one of the owner's succeeded Tasks. The lookup is
// the generation kernel's own, so an output of another owner or of an
// unfinished Task is not found.
func (s *Server) mcpPictureAsset(call *mcpCall, imageID string) (generation.InputAsset, *mcpOutcome, error) {
	missing := func() (generation.InputAsset, *mcpOutcome, error) {
		outcome := mcpProblemOutcome("image_not_found", s.mcpText(call.locale, s.application.MCPPublic.Messages.ImageNotFound), mcppublic.Subject{})
		return generation.InputAsset{}, &outcome, nil
	}
	if s.pictureAssets == nil {
		return missing()
	}
	id := generation.AssetID(imageID)
	loaded, err := s.pictureAssets.LoadInputAssets(call.ctx, call.owner, []generation.AssetID{id})
	if err != nil {
		return generation.InputAsset{}, nil, err
	}
	asset, found := loaded[id]
	if !found {
		return missing()
	}
	return asset, nil, nil
}

// mcpTaskImageAsset resolves the saved picture of a Task: the named one, or the
// primary one. A name that is not an output is asset_not_found; an output that
// is not a saved picture says so with asset_not_image; a Task without one is
// image_not_ready.
func (s *Server) mcpTaskImageAsset(call *mcpCall, task generation.Task, assetID string) (generation.Asset, *mcpOutcome) {
	if assetID == "" {
		if asset := mcpPrimaryImageAsset(task); asset != nil {
			return *asset, nil
		}
		outcome := mcpProblemOutcome("image_not_ready", s.mcpText(call.locale, s.application.MCPPublic.Messages.ImageNotReady), mcppublic.Subject{})
		return generation.Asset{}, &outcome
	}
	for _, asset := range task.Assets {
		if string(asset.ID) != assetID {
			continue
		}
		if asset.Role != "image" {
			outcome := mcpProblemOutcome("asset_not_found", s.mcpText(call.locale, s.application.MCPPublic.Messages.AssetNotImage), mcppublic.Subject{})
			return generation.Asset{}, &outcome
		}
		if asset.StorageKey == "" {
			outcome := mcpProblemOutcome("image_not_ready", s.mcpText(call.locale, s.application.MCPPublic.Messages.ImageNotReady), mcppublic.Subject{})
			return generation.Asset{}, &outcome
		}
		return asset, nil
	}
	missing := s.mcpAssetNotFound(call.locale)
	return generation.Asset{}, &missing
}

// mcpPrimaryImageAsset picks the Task output the tool inspects when the caller
// names none: a saved picture, a required output first, then the lowest
// ordinal, then the ID (the same ordering as generation.PrimaryModelAsset).
func mcpPrimaryImageAsset(task generation.Task) *generation.Asset {
	var primary *generation.Asset
	for index := range task.Assets {
		asset := &task.Assets[index]
		if asset.Role != "image" || asset.StorageKey == "" {
			continue
		}
		if primary == nil || mcpImageAssetBefore(*asset, *primary) {
			primary = asset
		}
	}
	return primary
}

func mcpImageAssetBefore(asset, than generation.Asset) bool {
	if asset.Required != than.Required {
		return asset.Required
	}
	if asset.Ordinal != than.Ordinal {
		return asset.Ordinal < than.Ordinal
	}
	return asset.ID < than.ID
}

// mcpInspectImageProblem maps a refusal of the core to the tool's business
// code. Every other error is the caller's to see as a server failure.
func (s *Server) mcpInspectImageProblem(call *mcpCall, picture *mcpInspectedPicture,
	err error,
) (mcpOutcome, error) {
	var refusal *imagereview.InspectRefusal
	if !errors.As(err, &refusal) {
		return mcpOutcome{}, err
	}
	messages := s.application.MCPPublic.Messages
	switch refusal.Kind {
	case imagereview.InspectNotFound:
		if picture.view.Source == "task_asset" {
			return s.mcpAssetNotFound(call.locale), nil
		}
		problem := mcpProblemOutcome("image_not_found", s.mcpText(call.locale, messages.ImageNotFound), mcppublic.Subject{})
		return problem, nil
	case imagereview.InspectNotReady:
		return mcpProblemOutcome("image_not_ready", s.mcpText(call.locale, messages.ImageNotReady), mcppublic.Subject{}), nil
	case imagereview.InspectTooLarge:
		limits := s.application.Chat.ImageReview.Limits
		message := s.mcpFill(call.locale, messages.ImageTooLarge,
			"{bytes}", strconv.FormatInt(limits.MaxBytes, 10),
			"{pixels}", strconv.FormatInt(s.application.MCPPublic.InspectImage.MaxPixels, 10))
		return mcpProblemOutcome("image_too_large", message, mcppublic.Subject{}), nil
	case imagereview.InspectUnreadable:
		return mcpProblemOutcome("image_unreadable", s.mcpText(call.locale, messages.ImageUnreadable), mcppublic.Subject{}), nil
	case imagereview.InspectUnavailable:
		return mcpProblemOutcome("image_unavailable", s.mcpText(call.locale, messages.ImageUnavailable), mcppublic.Subject{}), nil
	case imagereview.InspectUnknownChecklist, imagereview.InspectUnknownPurpose:
		return s.mcpInvalidArguments(call.locale, refusal.Message), nil
	default:
		return mcpOutcome{}, err
	}
}

// mcpImagePurposeKnown reports whether the Schema declares the purpose.
func (s *Server) mcpImagePurposeKnown(id string) bool {
	for _, profile := range s.application.Chat.Image2DUsage.Profiles {
		if profile.ID == id {
			return true
		}
	}
	return false
}

// mcpImagePurposeIDs lists the purposes a caller may pass, for the refusal that
// names them.
func (s *Server) mcpImagePurposeIDs() string {
	ids := make([]string, 0, len(s.application.Chat.Image2DUsage.Profiles))
	for _, profile := range s.application.Chat.Image2DUsage.Profiles {
		ids = append(ids, profile.ID)
	}
	return strings.Join(ids, "、")
}

// mcpImageVisualItems lists the 3d_input items only a look at the picture can
// judge. They are listed with the checklist and never judged here.
func (s *Server) mcpImageVisualItems() []mcpVisualItemView {
	items := s.application.Chat.ImageReview.Input3DVisualItems
	views := make([]mcpVisualItemView, 0, len(items))
	for _, item := range items {
		views = append(views, mcpVisualItemView{
			ID: item.ID, Label: item.Label, Instruction: item.Instruction, FixHint: item.FixHint,
			Remedies: mcpVisualItemRemedies(item),
		})
	}
	return views
}

// mcpVisualItemRemedies is the structured form of a visual item's fix hint
// (#332): what to fix (the item's own id), the Schema's sentence, and what it
// costs. A look at the picture is judged by eye and fixed by redrawing, which
// no counted capability performs, so the remedy names none and costs 0 steps.
func mcpVisualItemRemedies(item catalog.ChatImageVisualItem) []mcpRemedy {
	if strings.TrimSpace(item.ID) == "" || strings.TrimSpace(item.FixHint) == "" {
		return nil
	}
	return []mcpRemedy{{Code: item.ID, Message: item.FixHint}}
}
