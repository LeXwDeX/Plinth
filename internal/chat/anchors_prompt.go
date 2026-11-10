package chat

import (
	"strconv"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// visualAnchorsPrompt renders the session's visual anchors for the system
// prompt (#250). The style lock's text is carried by the work anchors block
// (#248): repeating it here would show one user text twice and put two
// instructions about it in one prompt. This block states the lock's enabled
// state and reference count and tells the Agent not to copy the lock's wording
// into a generation prompt. Anchor names and descriptions are user- or
// Agent-written data, so every declared marker is stripped and the whole block
// is framed as data, not instructions, exactly like the session notes and the
// work ledger. The names the Agent may pass in a generation tool's anchor
// argument are the stored ones; the server never injects an anchor silently.
func (orchestrator *Orchestrator) visualAnchorsPrompt(options TurnOptions) string {
	schema := orchestrator.schema.VisualAnchors
	if strings.TrimSpace(schema.ContextTemplate) == "" {
		return ""
	}
	styleLock := options.StyleLock
	// A disabled lock is not injected: its text is a stored draft, not a
	// setting in force. The block still renders when the session has anchors,
	// so the Agent sees the lock state without the disabled text.
	hasLock := styleLock.Enabled && (strings.TrimSpace(styleLock.Text) != "" || len(styleLock.References) > 0)
	if len(options.VisualAnchors) == 0 && !hasLock {
		return ""
	}
	items := make([]string, 0, len(options.VisualAnchors))
	for _, anchor := range options.VisualAnchors {
		kind, declared := anchor.DeclaredKind(schema.Kinds)
		kindLabel := kind.Label
		if !declared {
			kindLabel = anchor.Kind
		}
		references := strings.ReplaceAll(schema.ReferenceCountTemplate, "{count}", strconv.Itoa(len(anchor.References)))
		items = append(items, strings.NewReplacer(
			"{kind}", kindLabel,
			"{name}", orchestrator.stripAnchorMarkers(anchor.Name),
			"{description}", orchestrator.stripAnchorMarkers(anchor.Description),
			"{references}", references,
		).Replace(schema.ItemTemplate))
	}
	anchorsText := schema.EmptyText
	if len(items) > 0 {
		anchorsText = "\n" + strings.Join(items, "\n")
	}
	styleLockText := ""
	if template := orchestrator.schema.StyleLock.AgentContextTemplate; strings.TrimSpace(template) != "" {
		enabled := orchestrator.schema.StyleLock.DisabledText
		lockText := ""
		references := 0
		if styleLock.Enabled {
			enabled = orchestrator.schema.StyleLock.EnabledText
			references = len(styleLock.References)
			// A Catalog without the #248 work anchors block has nowhere else to
			// carry the lock text, so this block carries it instead of losing a
			// user setting. With the block declared, the text is rendered there
			// exactly once.
			if strings.TrimSpace(orchestrator.schema.WorkLedger.Anchors.ContextTemplate) == "" {
				lockText = orchestrator.stripAnchorMarkers(styleLock.Text)
			}
		}
		styleLockText = strings.NewReplacer(
			"{enabled}", enabled,
			"{text}", lockText,
			"{references}", strconv.Itoa(references),
		).Replace(template) + "\n"
	}
	return strings.NewReplacer(
		"{open}", schema.OpenMarker,
		"{style_lock}", styleLockText,
		"{anchors}", anchorsText,
		"{precedence}", schema.PrecedenceNote,
		"{argument}", anchorArgumentForSchema(orchestrator.schema.Tools),
		"{close}", schema.CloseMarker,
	).Replace(schema.ContextTemplate)
}

// stripAnchorMarkers removes every declared anchor marker from user- or
// Agent-written text. Both blocks strip both pairs, so a text that carries the
// other block's close marker cannot break out of either frame (#250 review).
func (orchestrator *Orchestrator) stripAnchorMarkers(text string) string {
	return stripDeclaredAnchorMarkers(text, orchestrator.schema.VisualAnchors, orchestrator.schema.WorkLedger.Anchors)
}

// stripDeclaredAnchorMarkers removes the visual-anchor and work-anchor marker
// pairs from text. An empty pair (a Schema that declares no block) is skipped
// by withoutMarkers itself.
func stripDeclaredAnchorMarkers(text string, visual catalog.ChatVisualAnchorsSchema, work catalog.ChatWorkLedgerAnchorsSchema) string {
	text = withoutMarkers(text, visual.OpenMarker, visual.CloseMarker)
	return withoutMarkers(text, work.OpenMarker, work.CloseMarker)
}

// stripAnchorMarkers is the Service-side form used by the work anchors block.
func (service *Service) stripAnchorMarkers(text string) string {
	return stripDeclaredAnchorMarkers(text, service.visualAnchors, service.workLedger.Anchors)
}

// anchorToolNote is added to a generation tool's description when the session
// has identity anchors (#250). The Agent reads the stored names here, so it
// can name one in the anchor argument instead of guessing; the server still
// never injects an anchor the call did not name.
func (orchestrator *Orchestrator) anchorToolNote(tool catalog.ChatToolSchema, options TurnOptions) string {
	if tool.AnchorArgument == "" {
		return ""
	}
	names := VisualAnchorIdentityNames(options.VisualAnchors)
	template := orchestrator.schema.VisualAnchors.PromptHint
	if len(names) == 0 || strings.TrimSpace(template) == "" {
		return ""
	}
	text := strings.NewReplacer(
		"{names}", strings.Join(names, "、"),
		"{argument}", tool.AnchorArgument,
	).Replace(template)
	if orchestrator.texts != nil {
		text = orchestrator.texts.Text(options.Locale, text)
	}
	return text
}

// anchorArgumentForSchema names the argument a generation tool of this turn
// reads anchors from. The block is one prompt for every tool, so it names the
// first declared anchor argument; a tool that declares none cannot use an
// anchor and its tool note stays silent.
func anchorArgumentForSchema(tools []catalog.ChatToolSchema) string {
	for _, tool := range tools {
		if tool.AnchorArgument != "" {
			return tool.AnchorArgument
		}
	}
	return "anchors"
}
