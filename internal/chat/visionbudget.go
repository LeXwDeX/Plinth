package chat

import (
	"context"
	"sort"
	"strconv"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// Purpose-based vision budget (#247).
//
// A turn's pictures are classified by the role they play in the conversation:
// the artboard snapshot (canvas), the current user message's references (turn),
// the session style lock (style), the model-review tools' renders (render),
// earlier generated results (generated) and older user pictures (history). The
// Product Schema declares each role's place quota and detail in priority order.
// Picture places, estimated vision tokens and inline bytes are bounded for the
// whole request; once a bound is reached, the remaining pictures of lower
// priority roles are dropped and their handles are named in the overflow note.
//
// A Catalog without context.vision keeps the historical behavior: one combined
// user-picture pool at image_detail, and the newest generated pictures at the
// same detail. The role names below are internal to that fallback.

const (
	visionRoleCanvas      = "canvas"
	visionRoleTurn        = "turn"
	visionRoleStyle       = "style"
	visionRoleRender      = "render"
	visionRoleGenerated   = "generated"
	visionRoleHistory     = "history"
	visionRoleLegacyUsers = "legacy-users"
	visionRoleLegacyAI    = "legacy-ai"
)

type visionBudget struct {
	// Vision is nil for a Catalog that predates the purpose-based budget.
	Vision *catalog.ChatVisionSchema
	// Enabled says whether this turn may send any picture at all.
	Enabled bool
	// Legacy selects the historical image_detail / recent limits behavior.
	Legacy        bool
	MaximumImages int
	MaximumTokens int
	MaximumBytes  int64
	DefaultDetail string
	CanvasDetail  string
	Roles         []catalog.ChatVisionRoleSchema
	// StyleIntro, StyleLabel, Overflow and VisionOff are declared texts; they
	// are empty in the legacy fallback.
	StyleIntro string
	StyleLabel string
	Overflow   string
	VisionOff  string
	Contact    catalog.ChatVisionContactSheetSchema
	Crop       catalog.ChatVisionCropSchema
}

// effectiveVisionBudget resolves the Schema's vision block for a model. Role
// quotas are capped by the legacy keys, which stay hard limits: a generated
// role by recent_generated_image_limit and a history role by
// recent_image_limit. A model profile may narrow the image and token bounds or
// turn vision off.
func effectiveVisionBudget(schema catalog.ChatSchema, model string) visionBudget {
	context := schema.Context
	vision := context.Vision
	if vision == nil {
		maximum := minPositive(context.MaximumVisionImages, context.RecentImageLimit)
		budget := visionBudget{
			Legacy:        true,
			Enabled:       schema.Agent.Vision,
			MaximumImages: maximum,
			DefaultDetail: context.ImageDetail,
			CanvasDetail:  context.CanvasImageDetail,
		}
		budget.Roles = []catalog.ChatVisionRoleSchema{
			{ID: visionRoleCanvas, Sources: []string{catalog.ChatVisionSourceCanvas},
				MaximumImages: maximum, Detail: context.CanvasImageDetail},
			{ID: visionRoleLegacyUsers, Sources: []string{catalog.ChatVisionSourceTurn, catalog.ChatVisionSourceHistory},
				MaximumImages: maximum, Detail: context.ImageDetail},
			{ID: visionRoleLegacyAI, Sources: []string{catalog.ChatVisionSourceGenerated, catalog.ChatVisionSourceRender},
				MaximumImages: context.RecentGeneratedImageLimit, Detail: context.ImageDetail},
		}
		return budget
	}
	budget := visionBudget{
		Vision:        vision,
		Enabled:       schema.Agent.Vision,
		MaximumImages: minPositive(vision.MaximumImages, context.MaximumVisionImages),
		MaximumTokens: vision.MaximumTokens,
		MaximumBytes:  vision.MaximumBytes,
		DefaultDetail: context.ImageDetail,
		CanvasDetail:  context.CanvasImageDetail,
		StyleIntro:    vision.StyleAnchorIntro,
		StyleLabel:    vision.StyleAnchorLabelTemplate,
		Overflow:      vision.OverflowTemplate,
		VisionOff:     vision.VisionOffNote,
		Contact:       vision.ContactSheet,
		Crop:          vision.Crop,
	}
	if profile, matched := visionProfileFor(vision.ModelProfiles, model); matched {
		if profile.SupportsVision != nil {
			budget.Enabled = budget.Enabled && *profile.SupportsVision
		}
		if profile.MaximumImages > 0 {
			budget.MaximumImages = min(budget.MaximumImages, profile.MaximumImages)
		}
		if profile.MaximumTokens > 0 {
			budget.MaximumTokens = min(budget.MaximumTokens, profile.MaximumTokens)
		}
	}
	for _, role := range vision.Roles {
		for _, source := range role.Sources {
			switch source {
			case catalog.ChatVisionSourceGenerated:
				// The legacy key is a hard cap even at zero (no generated
				// picture at all), not an "unset" value.
				role.MaximumImages = legacyImageCap(role.MaximumImages, context.RecentGeneratedImageLimit)
			case catalog.ChatVisionSourceHistory:
				role.MaximumImages = legacyImageCap(role.MaximumImages, context.RecentImageLimit)
			}
		}
		if role.MaximumImages <= 0 {
			continue
		}
		budget.Roles = append(budget.Roles, role)
	}
	return budget
}

// visionProfileFor matches a model family by case-insensitive prefix; the
// first profile wins, like the thinking profiles.
func visionProfileFor(profiles []catalog.ChatVisionModelProfileSchema, model string) (catalog.ChatVisionModelProfileSchema, bool) {
	lower := strings.ToLower(model)
	for _, profile := range profiles {
		for _, prefix := range profile.ModelPrefixes {
			if prefix = strings.ToLower(strings.TrimSpace(prefix)); prefix != "" &&
				strings.HasPrefix(lower, prefix) {
				return profile, true
			}
		}
	}
	return catalog.ChatVisionModelProfileSchema{}, false
}

// legacyImageCap applies a legacy image key as a hard cap: zero means none.
func legacyImageCap(roleMaximum, legacy int) int {
	if legacy < 0 {
		legacy = 0
	}
	if roleMaximum <= 0 {
		return legacy
	}
	return min(roleMaximum, legacy)
}

func minPositive(first, second int) int {
	if first <= 0 {
		return second
	}
	if second <= 0 {
		return first
	}
	return min(first, second)
}

// roleFor returns the role declared for a source, if any.
func (budget visionBudget) roleFor(source string) (catalog.ChatVisionRoleSchema, bool) {
	for _, role := range budget.Roles {
		for _, declared := range role.Sources {
			if declared == source {
				return role, true
			}
		}
	}
	return catalog.ChatVisionRoleSchema{}, false
}

// roleQuota is how many pictures of a source the turn may show.
func (budget visionBudget) roleQuota(source string) int {
	role, found := budget.roleFor(source)
	if !found {
		return 0
	}
	return role.MaximumImages
}

func (budget visionBudget) detailFor(source string) string {
	if source == catalog.ChatVisionSourceCanvas {
		return budget.CanvasDetail
	}
	role, found := budget.roleFor(source)
	if !found || role.Detail == "" {
		return budget.DefaultDetail
	}
	return role.Detail
}

// tokenCostFor is the declared estimate of one picture of a source.
func (budget visionBudget) tokenCostFor(source string) int {
	role, found := budget.roleFor(source)
	if !found {
		return 0
	}
	return role.TokenCost
}

// visionSpend is what a request has already used of its budget.
type visionSpend struct {
	Images int
	Tokens int
	Bytes  int64
}

type visionPick struct {
	URL       string
	Detail    string
	Label     string
	RoleID    string
	TokenCost int
	// index is the history image number, used to show assistant pictures
	// oldest first whichever roles picked them.
	index int
}

type visionPlan struct {
	// Picks are the user pictures shown in place, by message index and the
	// 0-based position in message.ImageURLs.
	Picks map[int]map[int]visionPick
	// Generated are assistant pictures (renders and results) shown after the
	// newest user message, oldest first. Style are the style lock references.
	Generated []visionPick
	Style     []visionPick
	// Dropped names the pictures the budget refused, in priority order.
	Dropped []string
	Spend   visionSpend
	// Off says no picture at all is sent because vision is off.
	Off bool
}

func (plan visionPlan) pickFor(messageIndex, slotIndex int) (visionPick, bool) {
	picks, found := plan.Picks[messageIndex]
	if !found {
		return visionPick{}, false
	}
	pick, found := picks[slotIndex]
	return pick, found
}

// overflowText is the declared note naming the dropped pictures.
func (budget visionBudget) overflowText(dropped []string) string {
	if len(dropped) == 0 || strings.TrimSpace(budget.Overflow) == "" {
		return ""
	}
	return strings.ReplaceAll(budget.Overflow, "{handles}", strings.Join(dropped, " "))
}

// visionCandidate is one picture the budget may show.
type visionCandidate struct {
	source       string
	messageIndex int
	slotIndex    int
	index        int
	url          string
	label        string
	dropped      string
	style        bool
	// assistant marks a picture of an assistant message, whether the role is
	// the declared generated/render one or the legacy combined pool.
	assistant bool
}

// planVision selects the pictures of a request under the effective budget.
// initial is what earlier parts of the same request (the canvas snapshot)
// already used. Pictures are considered role by role in declaration order;
// within a role the newest comes first. A picture that cannot be inlined is
// skipped without using a place, like before.
// visionBudget resolves the Schema's vision block for the turn's model.
func (orchestrator *Orchestrator) visionBudget(options TurnOptions) visionBudget {
	return effectiveVisionBudget(orchestrator.schema, orchestrator.turnModel(options))
}

func (orchestrator *Orchestrator) planVision(
	ctx context.Context,
	ownerID string,
	history []Message,
	indexed [][]indexedChatImage,
	summarized, lastUser int,
	options TurnOptions,
	initial visionSpend,
) visionPlan {
	budget := orchestrator.visionBudget(options)
	plan := visionPlan{Picks: map[int]map[int]visionPick{}, Spend: initial}
	if !budget.Enabled {
		plan.Off = true
		return plan
	}
	candidates := orchestrator.visionCandidates(history, indexed, summarized, lastUser, options, budget)
	for _, role := range budget.Roles {
		picked := 0
		for _, candidate := range visionRoleCandidates(candidates, role) {
			if picked >= role.MaximumImages || plan.Spend.Images >= budget.MaximumImages {
				plan.Dropped = append(plan.Dropped, candidate.dropped)
				continue
			}
			tokenCost := role.TokenCost
			if budget.MaximumTokens > 0 && tokenCost > 0 &&
				plan.Spend.Tokens+tokenCost > budget.MaximumTokens {
				plan.Dropped = append(plan.Dropped, candidate.dropped)
				continue
			}
			url, ok := orchestrator.inlineVisionURL(ctx, ownerID, candidate)
			if !ok {
				continue
			}
			if budget.MaximumBytes > 0 && plan.Spend.Bytes+int64(len(url)) > budget.MaximumBytes {
				plan.Dropped = append(plan.Dropped, candidate.dropped)
				continue
			}
			pick := visionPick{
				URL: url, Detail: role.Detail, Label: candidate.label,
				RoleID: role.ID, TokenCost: tokenCost, index: candidate.index,
			}
			switch {
			case candidate.style:
				plan.Style = append(plan.Style, pick)
			case candidate.assistant:
				plan.Generated = append(plan.Generated, pick)
			default:
				if plan.Picks[candidate.messageIndex] == nil {
					plan.Picks[candidate.messageIndex] = map[int]visionPick{}
				}
				plan.Picks[candidate.messageIndex][candidate.slotIndex] = pick
			}
			plan.Spend.Images++
			plan.Spend.Tokens += tokenCost
			plan.Spend.Bytes += int64(len(url))
			picked++
		}
	}
	// Candidates were collected newest first; the model reads them oldest
	// first, like the messages themselves.
	sort.SliceStable(plan.Generated, func(left, right int) bool {
		return plan.Generated[left].index < plan.Generated[right].index
	})
	return plan
}

// plannedGeneratedParts renders the assistant pictures the plan kept after the
// newest user message, each followed by its label.
func (orchestrator *Orchestrator) plannedGeneratedParts(plan visionPlan) []agents.ContentPart {
	if len(plan.Generated) == 0 {
		return nil
	}
	parts := []agents.ContentPart{}
	if intro := orchestrator.schema.Context.GeneratedVisionIntro; intro != "" {
		parts = append(parts, agents.ContentPart{
			Kind: orchestrator.schema.Context.TextPartKind, Text: intro,
		})
	}
	return append(parts, orchestrator.pictureParts(plan.Generated)...)
}

// plannedStyleParts renders the session style lock's reference pictures.
func (orchestrator *Orchestrator) plannedStyleParts(plan visionPlan) []agents.ContentPart {
	if len(plan.Style) == 0 {
		return nil
	}
	parts := []agents.ContentPart{}
	if intro := orchestrator.schema.Context.Vision.StyleAnchorIntro; intro != "" {
		parts = append(parts, agents.ContentPart{
			Kind: orchestrator.schema.Context.TextPartKind, Text: intro,
		})
	}
	return append(parts, orchestrator.pictureParts(plan.Style)...)
}

func (orchestrator *Orchestrator) pictureParts(picks []visionPick) []agents.ContentPart {
	parts := make([]agents.ContentPart, 0, len(picks)*2)
	for _, pick := range picks {
		parts = append(parts, agents.ContentPart{
			Kind: orchestrator.schema.Context.ImagePartKind,
			URL:  pick.URL, Detail: pick.Detail,
		})
		if pick.Label != "" {
			parts = append(parts, agents.ContentPart{
				Kind: orchestrator.schema.Context.TextPartKind, Text: pick.Label,
			})
		}
	}
	return parts
}

// visionRoleCandidates returns the pictures declared for one role. The legacy
// fallback keys candidates by role ID; declared roles key them by source.
func visionRoleCandidates(candidates map[string][]visionCandidate, role catalog.ChatVisionRoleSchema) []visionCandidate {
	if list, found := candidates[role.ID]; found {
		return list
	}
	var list []visionCandidate
	for _, source := range role.Sources {
		list = append(list, candidates[source]...)
	}
	return list
}

// inlineVisionURL turns a candidate into a data URI the model can read.
func (orchestrator *Orchestrator) inlineVisionURL(ctx context.Context, ownerID string, candidate visionCandidate) (string, bool) {
	if candidate.style {
		if orchestrator.visionAssetInline == nil {
			return "", false
		}
		return orchestrator.visionAssetInline(ctx, generation.OwnerID(ownerID), candidate.url)
	}
	if visionSafeImage(candidate.url) {
		return candidate.url, true
	}
	if orchestrator.visionInline == nil {
		return "", false
	}
	return orchestrator.visionInline(ctx, generation.OwnerID(ownerID), candidate.url)
}

// visionCandidates classifies the request's pictures by role. The order inside
// each role is newest first.
func (orchestrator *Orchestrator) visionCandidates(
	history []Message,
	indexed [][]indexedChatImage,
	summarized, lastUser int,
	options TurnOptions,
	budget visionBudget,
) map[string][]visionCandidate {
	result := map[string][]visionCandidate{}
	add := func(roleID string, candidate visionCandidate) {
		result[roleID] = append(result[roleID], candidate)
	}
	if budget.Legacy {
		orchestrator.legacyVisionCandidates(history, indexed, summarized, lastUser, options, add)
	} else {
		orchestrator.roleVisionCandidates(history, indexed, summarized, lastUser, options, add)
	}
	return result
}

func (orchestrator *Orchestrator) legacyVisionCandidates(
	history []Message,
	indexed [][]indexedChatImage,
	summarized, lastUser int,
	options TurnOptions,
	add func(string, visionCandidate),
) {
	if len(options.ReferenceImageIndices) > 0 {
		selected := selectedVisionImages(history, indexed)
		for _, index := range options.ReferenceImageIndices {
			entry, found := selected[index]
			if !found || entry.image.role != orchestrator.schema.Roles.User ||
				!orchestrator.visionCandidate(entry.image.url) {
				continue
			}
			add(visionRoleLegacyUsers, orchestrator.userCandidate(
				history[entry.messageIndex], entry.image, entry.messageIndex, visionRoleLegacyUsers))
		}
		return
	}
	for messageIndex := len(history) - 1; messageIndex > summarized; messageIndex-- {
		message := history[messageIndex]
		if !orchestrator.contextMessage(message) ||
			message.Role != orchestrator.schema.Roles.User {
			continue
		}
		images := indexed[messageIndex]
		for imageIndex := len(images) - 1; imageIndex >= 0; imageIndex-- {
			image := images[imageIndex]
			if !orchestrator.visionCandidate(image.url) {
				continue
			}
			add(visionRoleLegacyUsers, orchestrator.userCandidate(
				history[messageIndex], image, messageIndex, visionRoleLegacyUsers))
		}
	}
	// Generated pictures keep the historical scan over the whole history.
	for messageIndex := len(history) - 1; messageIndex >= 0; messageIndex-- {
		message := history[messageIndex]
		if message.Role != orchestrator.schema.Roles.Assistant {
			continue
		}
		images := indexed[messageIndex]
		for imageIndex := len(images) - 1; imageIndex >= 0; imageIndex-- {
			image := images[imageIndex]
			if !orchestrator.visionCandidate(image.url) {
				continue
			}
			add(visionRoleLegacyAI, orchestrator.assistantCandidate(
				message, image, visionRoleLegacyAI))
		}
	}
}

func (orchestrator *Orchestrator) roleVisionCandidates(
	history []Message,
	indexed [][]indexedChatImage,
	summarized, lastUser int,
	options TurnOptions,
	add func(string, visionCandidate),
) {
	if len(options.ReferenceImageIndices) > 0 {
		selected := selectedVisionImages(history, indexed)
		for _, index := range options.ReferenceImageIndices {
			entry, found := selected[index]
			if !found || entry.image.role != orchestrator.schema.Roles.User ||
				!orchestrator.visionCandidate(entry.image.url) {
				continue
			}
			add(catalog.ChatVisionSourceTurn, orchestrator.userCandidate(
				history[entry.messageIndex], entry.image, entry.messageIndex, catalog.ChatVisionSourceTurn))
		}
	} else {
		for messageIndex := len(history) - 1; messageIndex > summarized; messageIndex-- {
			message := history[messageIndex]
			if !orchestrator.contextMessage(message) ||
				message.Role != orchestrator.schema.Roles.User {
				continue
			}
			source := catalog.ChatVisionSourceHistory
			if messageIndex == lastUser {
				source = catalog.ChatVisionSourceTurn
			}
			images := indexed[messageIndex]
			for imageIndex := len(images) - 1; imageIndex >= 0; imageIndex-- {
				image := images[imageIndex]
				if !orchestrator.visionCandidate(image.url) {
					continue
				}
				add(source, orchestrator.userCandidate(message, image, messageIndex, source))
			}
		}
	}
	for messageIndex := len(history) - 1; messageIndex >= 0; messageIndex-- {
		message := history[messageIndex]
		if message.Role != orchestrator.schema.Roles.Assistant {
			continue
		}
		images := indexed[messageIndex]
		for imageIndex := len(images) - 1; imageIndex >= 0; imageIndex-- {
			image := images[imageIndex]
			if !orchestrator.visionCandidate(image.url) {
				continue
			}
			source := catalog.ChatVisionSourceGenerated
			if attachment, found := AttachmentForSlot(message, image.slot); found &&
				attachment.Role == AttachmentRoleRender {
				source = catalog.ChatVisionSourceRender
			}
			add(source, orchestrator.assistantCandidate(message, image, source))
		}
	}
	lock := options.StyleLock
	if lock.Enabled {
		for index, assetID := range lock.References {
			if strings.TrimSpace(assetID) == "" {
				continue
			}
			add(catalog.ChatVisionSourceStyle, visionCandidate{
				source:  catalog.ChatVisionSourceStyle,
				url:     assetID,
				label:   strings.ReplaceAll(orchestrator.schema.Context.Vision.StyleAnchorLabelTemplate, "{index}", strconv.Itoa(index+1)),
				dropped: "风格锁参考图 #" + strconv.Itoa(index+1),
				style:   true,
			})
		}
	}
}

func (orchestrator *Orchestrator) userCandidate(
	message Message,
	image indexedChatImage,
	messageIndex int,
	source string,
) visionCandidate {
	return visionCandidate{
		source:       source,
		messageIndex: messageIndex,
		slotIndex:    image.slot - 1,
		index:        image.index,
		url:          image.url,
		label:        orchestrator.visionUserLabel(message, image),
		dropped:      orchestrator.visionDroppedLabel(message, image),
	}
}

// selectedVisionImages indexes the history pictures by their global index,
// remembering the message each belongs to (ReferenceImageIndices names them).
type selectedVisionImage struct {
	messageIndex int
	image        indexedChatImage
}

func selectedVisionImages(history []Message, indexed [][]indexedChatImage) map[int]selectedVisionImage {
	result := map[int]selectedVisionImage{}
	for messageIndex, images := range indexed {
		if messageIndex >= len(history) {
			continue
		}
		for _, image := range images {
			result[image.index] = selectedVisionImage{messageIndex: messageIndex, image: image}
		}
	}
	return result
}

func (orchestrator *Orchestrator) assistantCandidate(
	message Message,
	image indexedChatImage,
	source string,
) visionCandidate {
	return visionCandidate{
		source:    source,
		assistant: true,
		slotIndex: image.slot - 1,
		index:     image.index,
		url:       image.url,
		label:     orchestrator.visionAssistantLabel(message, image),
		dropped:   orchestrator.visionDroppedLabel(message, image),
	}
}

// visionUserLabel names a user picture by its handle, or by the legacy index
// template when it has no structured attachment.
func (orchestrator *Orchestrator) visionUserLabel(message Message, image indexedChatImage) string {
	if label, structured := orchestrator.attachmentLabel(message, image.slot, image.index); structured {
		return label
	}
	if orchestrator.schema.Context.Vision == nil {
		// The legacy request showed a user picture without a label beside it.
		return ""
	}
	return strings.ReplaceAll(orchestrator.schema.Context.UserImageLabelTemplate, "{index}", strconv.Itoa(image.index))
}

func (orchestrator *Orchestrator) visionAssistantLabel(message Message, image indexedChatImage) string {
	if label, structured := orchestrator.attachmentLabel(message, image.slot, image.index); structured {
		return label
	}
	return strings.ReplaceAll(orchestrator.schema.Context.AIImageLabelTemplate, "{index}", strconv.Itoa(image.index))
}

// visionDroppedLabel is what the overflow note names: the picture's handle
// when it has one, else its history index.
func (orchestrator *Orchestrator) visionDroppedLabel(message Message, image indexedChatImage) string {
	if attachment, found := AttachmentForSlot(message, image.slot); found && attachment.Handle != "" {
		return attachment.Handle
	}
	return "#" + strconv.Itoa(image.index)
}

// requestVisionTokens estimates the vision tokens of one request from the
// Schema's declared per-detail costs (#247). It counts every image part,
// including tool pictures and the contact sheet, and never the upstream's own
// reported prompt tokens.
func (orchestrator *Orchestrator) requestVisionTokens(request agents.Request, model string) int {
	return orchestrator.requestVisionSpendFor(request, model).Tokens
}

// requestVisionSpend counts the picture parts a request already carries.
func (orchestrator *Orchestrator) requestVisionSpend(request agents.Request) visionSpend {
	return orchestrator.requestVisionSpendFor(request, request.Model)
}

func (orchestrator *Orchestrator) requestVisionSpendFor(request agents.Request, model string) visionSpend {
	costs := visionDetailCosts(effectiveVisionBudget(orchestrator.schema, model))
	var spend visionSpend
	for _, message := range request.Messages {
		for _, part := range message.Parts {
			if part.Kind != orchestrator.schema.Context.ImagePartKind {
				continue
			}
			spend.Images++
			spend.Tokens += costs[part.Detail]
			spend.Bytes += int64(len(part.URL))
		}
	}
	return spend
}

// visionDetailCosts maps a declared detail to its per-picture token estimate.
func visionDetailCosts(budget visionBudget) map[string]int {
	costs := map[string]int{}
	for _, role := range budget.Roles {
		if role.Detail != "" && costs[role.Detail] == 0 {
			costs[role.Detail] = role.TokenCost
		}
	}
	if sheet := budget.Contact; sheet.TokenCost > 0 && costs[sheet.Detail] == 0 {
		costs[sheet.Detail] = sheet.TokenCost
	}
	if crop := budget.Crop; crop.TokenCost > 0 && costs[crop.Detail] == 0 {
		costs[crop.Detail] = crop.TokenCost
	}
	return costs
}

// EffectiveVisionImages is the effective per-request image budget of a model
// under the Schema's purpose-based vision block (#247); the context view
// shows it beside the estimated vision tokens.
func EffectiveVisionImages(schema catalog.ChatSchema, model string) int {
	return effectiveVisionBudget(schema, model).MaximumImages
}

// canSpend reports whether one more picture fits the request's budget. A zero
// or negative bound is unlimited (the legacy fallback and an undeclared cap).
func (budget visionBudget) canSpend(spend visionSpend, tokenCost int, bytes int64) bool {
	if !budget.Enabled {
		return false
	}
	if budget.MaximumImages > 0 && spend.Images >= budget.MaximumImages {
		return false
	}
	if budget.MaximumTokens > 0 && spend.Tokens+tokenCost > budget.MaximumTokens {
		return false
	}
	if budget.MaximumBytes > 0 && spend.Bytes+bytes > budget.MaximumBytes {
		return false
	}
	return true
}
