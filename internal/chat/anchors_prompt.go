package chat

import (
	"strconv"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// visualAnchorsPrompt renders the session's visual anchors and the person's
// style lock for the system prompt (#250). The style text and the anchor
// descriptions are user- or Agent-written data, so every marker is stripped
// and the whole block is framed as data, not instructions, exactly like the
// session notes and the work ledger. The names the Agent may pass in a
// generation tool's anchor argument are the stored ones; the server never
// injects an anchor silently.
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
			"{name}", withoutMarkers(anchor.Name, schema.OpenMarker, schema.CloseMarker),
			"{description}", withoutMarkers(anchor.Description, schema.OpenMarker, schema.CloseMarker),
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
			lockText = withoutMarkers(styleLock.Text, schema.OpenMarker, schema.CloseMarker)
			references = len(styleLock.References)
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
