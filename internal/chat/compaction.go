package chat

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/LeXwDeX/Plinth/internal/agents"
)

// Context compaction (#322). See context.go for the round and the summary.
// The chat history is only ever read here; handles, image indices, choice
// cards, notes, tasks and approvals keep using the whole history and their
// own stores, so compaction changes nothing but the messages an Agent
// request carries.

// turnModel is the model and connection every request of a turn uses.
type turnModel struct {
	connection string
	name       string
}

// turnModel is the model every request of the turn uses: the one frozen
// with it, or the deployment's (turns stored before #322).
func (orchestrator *Orchestrator) turnModel(options TurnOptions) string {
	if options.Model != "" {
		return options.Model
	}
	return orchestrator.model
}

// turnChatModel is turnModel with the connection that serves it.
func (orchestrator *Orchestrator) turnChatModel(options TurnOptions) turnModel {
	if options.Model == "" {
		return turnModel{name: orchestrator.model}
	}
	return turnModel{connection: options.ModelConnection, name: options.Model}
}

// contextMessage reports whether a stored message enters the Agent context:
// pending placeholders and tool records never do.
func (orchestrator *Orchestrator) contextMessage(message Message) bool {
	return message.Content != orchestrator.schema.State.PendingContent &&
		message.Role != orchestrator.schema.Roles.Tool
}

// summarizedThrough is the index in history of the last message the
// compaction covers, -1 for none. A covered message that was deleted since is
// found by time: every message created up to it is covered.
func summarizedThrough(history []Message, latest *Compaction) int {
	if latest == nil {
		return -1
	}
	for index := range history {
		if history[index].ID == latest.ThroughMessageID {
			return index
		}
	}
	through := -1
	if latest.ThroughCreatedAt.IsZero() {
		return through
	}
	for index, message := range history {
		if !message.CreatedAt.After(latest.ThroughCreatedAt) {
			through = index
		}
	}
	return through
}

// compactionLock is one session's compaction mutex; holders counts the
// callers that hold or wait for it, so the last one can drop it from the map.
type compactionLock struct {
	sync.Mutex
	holders int
}

// lockCompaction serializes the compactions of one session in this process;
// the store's compare-and-set keeps them consistent across processes. The
// lock is removed once no caller holds or waits for it, so a long-running
// process keeps only the locks of compactions in flight (#334).
func (orchestrator *Orchestrator) lockCompaction(ownerID, sessionID string) func() {
	key := orchestrator.turnKey(ownerID, sessionID)
	orchestrator.compactMu.Lock()
	lock, found := orchestrator.compacting[key]
	if !found {
		lock = &compactionLock{}
		orchestrator.compacting[key] = lock
	}
	lock.holders++
	orchestrator.compactMu.Unlock()
	lock.Lock()
	return func() {
		lock.Unlock()
		orchestrator.compactMu.Lock()
		lock.holders--
		if lock.holders == 0 {
			delete(orchestrator.compacting, key)
		}
		orchestrator.compactMu.Unlock()
	}
}

// CompactContext compacts the session's context now, at the person's request.
// It is refused while a turn of the session has not finished; a failed
// summary call leaves the session as it was.
func (orchestrator *Orchestrator) CompactContext(ctx context.Context, ownerID, sessionID string) (ContextView, error) {
	if !orchestrator.schema.ContextCompaction.Enabled {
		return ContextView{}, ErrCompactionUnavailable
	}
	if err := orchestrator.service.requireSession(ctx, ownerID, sessionID); err != nil {
		return ContextView{}, err
	}
	unlock := orchestrator.lockCompaction(ownerID, sessionID)
	defer unlock()
	active, err := orchestrator.service.HasActiveTurn(ctx, ownerID, sessionID)
	if err != nil {
		return ContextView{}, err
	}
	if active {
		return ContextView{}, ErrCompactionTurnActive
	}
	history, err := orchestrator.service.ListMessages(ctx, ownerID, sessionID)
	if err != nil {
		return ContextView{}, err
	}
	state, err := orchestrator.service.ContextState(ctx, ownerID, sessionID)
	if err != nil {
		return ContextView{}, err
	}
	// The summary runs on the model the session's next turn would use.
	model, effort, err := orchestrator.service.TurnModel(ctx, ownerID, sessionID)
	if err != nil {
		return ContextView{}, err
	}
	if model.Model == "" {
		model.Model = orchestrator.model
	}
	if _, err := orchestrator.compactHistory(ctx, ownerID, sessionID, history, state,
		CompactionManual, turnModel{connection: model.ID, name: model.Model}, effort); err != nil {
		return ContextView{}, err
	}
	return orchestrator.service.SessionContextView(ctx, ownerID, sessionID)
}

// autoCompact compacts before a new turn's first request when the session's
// newest usage reached the Schema share of the model's window, or, while the
// tokens are not measured (no declared window or no reported usage), when the
// messages after the summary exceed auto_message_threshold. A failure is
// shown as a status and the turn goes on with the full context.
func (orchestrator *Orchestrator) autoCompact(ctx context.Context, command ProcessTurn, history []Message) {
	compaction := orchestrator.schema.ContextCompaction
	if !orchestrator.compactionActive() {
		return
	}
	state, err := orchestrator.service.ContextState(ctx, command.OwnerID, command.SessionID)
	if err != nil {
		return
	}
	model := orchestrator.turnChatModel(command.Options)
	window := orchestrator.turnWindow(ctx, command.Options)
	if window > 0 && state.Usage != nil {
		if float64(state.Usage.Tokens()) < float64(window)*compaction.AutoThresholdRatio {
			return
		}
	} else if !orchestrator.pastMessageThreshold(history, state.Latest) {
		return
	}
	// The turn's own round (its user message onwards) is never summarized.
	current := len(history)
	for index := len(history) - 1; index >= 0; index-- {
		if history[index].Role == orchestrator.schema.Roles.User && orchestrator.contextMessage(history[index]) {
			current = index
			break
		}
	}
	unlock := orchestrator.lockCompaction(command.OwnerID, command.SessionID)
	defer unlock()
	status := orchestrator.schema.Presentation.Status
	orchestrator.statusEvent(ctx, command.SessionID, status.Compacting)
	_, err = orchestrator.compactHistory(ctx, command.OwnerID, command.SessionID, history[:current], state,
		CompactionAuto, model, command.Options.ReasoningEffort)
	switch {
	case err == nil:
		orchestrator.statusEvent(ctx, command.SessionID, status.Compacted)
	case errors.Is(err, ErrNothingToCompact), errors.Is(err, ErrCompactionConflict):
		orchestrator.logger.Debug("自动压缩上下文跳过", "session_id", command.SessionID, "reason", err)
	default:
		orchestrator.logger.Warn("自动压缩上下文失败，本轮按完整上下文继续",
			"session_id", command.SessionID, "assistant_message_id", command.AssistantMessageID, "error", err)
		orchestrator.statusEvent(ctx, command.SessionID, status.CompactionFailed)
	}
}

// compactionActive reports whether summaries can bound the history: the
// Schema turns compaction on and the service keeps a store for them.
func (orchestrator *Orchestrator) compactionActive() bool {
	return orchestrator.schema.ContextCompaction.Enabled && orchestrator.service != nil && orchestrator.service.contextStore != nil
}

// pastMessageThreshold reports whether the context messages after the
// session's summary exceed auto_message_threshold.
func (orchestrator *Orchestrator) pastMessageThreshold(history []Message, latest *Compaction) bool {
	threshold := orchestrator.schema.ContextCompaction.AutoMessageThreshold
	if threshold <= 0 {
		return false
	}
	count := 0
	for _, message := range history[summarizedThrough(history, latest)+1:] {
		if orchestrator.contextMessage(message) {
			count++
		}
	}
	return count > threshold
}

// statusEvent appends a Schema status text; best effort.
func (orchestrator *Orchestrator) statusEvent(ctx context.Context, sessionID, text string) {
	if text == "" {
		return
	}
	if err := orchestrator.appendEvent(ctx, sessionID, map[string]any{
		orchestrator.schema.Stream.EventFields.Type:    orchestrator.schema.Stream.EventTypes.Status,
		orchestrator.schema.Stream.EventFields.Content: orchestrator.text(ctx, text),
	}); err != nil && ctx.Err() == nil {
		orchestrator.logger.Warn("对话状态事件写入失败", "session_id", sessionID, "error", err)
	}
}

// compactHistory summarizes every round of history after the current summary
// and before the newest kept ones, merges the earlier summary into it and
// stores it as the session's summary.
func (orchestrator *Orchestrator) compactHistory(
	ctx context.Context,
	ownerID, sessionID string,
	history []Message,
	state SessionContext,
	trigger string, model turnModel, effort string,
) (Compaction, error) {
	compaction := orchestrator.schema.ContextCompaction
	start := summarizedThrough(history, state.Latest) + 1
	var starts []int
	for index := start; index < len(history); index++ {
		if history[index].Role == orchestrator.schema.Roles.User && orchestrator.contextMessage(history[index]) {
			starts = append(starts, index)
		}
	}
	if len(starts) <= compaction.KeepRecentRounds {
		return Compaction{}, ErrNothingToCompact
	}
	cut := starts[len(starts)-compaction.KeepRecentRounds]
	through := -1
	for index := cut - 1; index >= start; index-- {
		if orchestrator.contextMessage(history[index]) {
			through = index
			break
		}
	}
	if through < 0 {
		return Compaction{}, ErrNothingToCompact
	}
	rounds := 0
	for _, message := range history[:through+1] {
		if message.Role == orchestrator.schema.Roles.User && orchestrator.contextMessage(message) {
			rounds++
		}
	}
	previous, previousID := "", ""
	if state.Latest != nil {
		previousID = state.Latest.ID
		previous = strings.ReplaceAll(compaction.PreviousSummaryTemplate, "{summary}", state.Latest.Summary)
	}
	source := strings.NewReplacer(
		"{previous_summary}", previous,
		"{transcript}", orchestrator.transcript(history, start, through),
	).Replace(compaction.SourceTemplate)
	request := agents.Request{
		Model:           model.name,
		Connection:      model.connection,
		Temperature:     orchestrator.schema.Agent.Temperature,
		ReasoningEffort: effort,
		Purpose:         agents.PurposeSummary,
		Messages: []agents.Message{
			{Role: orchestrator.schema.Roles.System, Content: compaction.SummaryPrompt},
			{Role: orchestrator.schema.Roles.User, Content: source},
		},
	}
	callCtx, cancel := context.WithTimeout(ctx, time.Duration(compaction.RequestTimeoutSeconds)*time.Second)
	defer cancel()
	completion, err := orchestrator.agent.Complete(callCtx, request)
	if err != nil {
		return Compaction{}, fmt.Errorf("%w: %w", ErrCompactionFailed, err)
	}
	summary := truncateRunes(strings.TrimSpace(completion.Content), compaction.MaximumSummaryCharacters)
	if summary == "" {
		return Compaction{}, fmt.Errorf("%w: the model returned an empty summary", ErrCompactionFailed)
	}
	return orchestrator.service.SaveCompaction(ctx, Compaction{
		OwnerID:          ownerID,
		SessionID:        sessionID,
		Trigger:          trigger,
		Summary:          summary,
		ThroughMessageID: history[through].ID,
		ThroughCreatedAt: history[through].CreatedAt,
		Rounds:           rounds,
	}, previousID)
}

// transcript renders history[from..through] for the summary call: each
// context message with its role label, its handle labels and its tool calls,
// the way the Agent itself reads them.
func (orchestrator *Orchestrator) transcript(history []Message, from, through int) string {
	compaction := orchestrator.schema.ContextCompaction
	indexed, _ := orchestrator.indexHistoryImages(history)
	roles := map[string]string{
		orchestrator.schema.Roles.User:      compaction.RoleLabels["user"],
		orchestrator.schema.Roles.Assistant: compaction.RoleLabels["assistant"],
	}
	var lines []string
	for index := from; index <= through; index++ {
		message := history[index]
		if !orchestrator.contextMessage(message) {
			continue
		}
		content := orchestrator.contextText(message) + orchestrator.imageLabels(message, indexed[index]) +
			orchestrator.unpicturedLabels(message)
		if len(message.ToolCalls) > 0 && message.Role == orchestrator.schema.Roles.Assistant {
			names := make([]string, 0, len(message.ToolCalls))
			for _, call := range message.ToolCalls {
				names = append(names, call.Name)
			}
			content += "\n" + strings.ReplaceAll(orchestrator.schema.Context.ToolCallNote, "{tools}", strings.Join(names, ", "))
		}
		role := roles[message.Role]
		if role == "" {
			role = message.Role
		}
		lines = append(lines, strings.NewReplacer("{role}", role, "{content}", content).
			Replace(compaction.TranscriptLineTemplate))
	}
	return strings.Join(lines, "\n")
}

// imageLabels names a message's pictures by handle (or legacy index label).
func (orchestrator *Orchestrator) imageLabels(message Message, images []indexedChatImage) string {
	var labels strings.Builder
	for _, image := range images {
		if label, structured := orchestrator.attachmentLabel(message, image.slot, image.index); structured {
			labels.WriteString(label)
			continue
		}
		template := orchestrator.schema.Context.AIImageLabelTemplate
		if message.Role == orchestrator.schema.Roles.User {
			template = orchestrator.schema.Context.UserImageLabelTemplate
		}
		labels.WriteString(strings.ReplaceAll(template, "{index}", fmt.Sprint(image.index)))
	}
	return labels.String()
}

// indexHistoryImages numbers every picture of the history's context
// messages, oldest first from the Schema base, and finds the newest user
// message. The numbering always spans the whole history, compacted or not,
// so an index or handle label never changes after a compaction.
func (orchestrator *Orchestrator) indexHistoryImages(history []Message) ([][]indexedChatImage, int) {
	indexed := make([][]indexedChatImage, len(history))
	next := orchestrator.schema.Context.ImageIndexBase
	lastUser := -1
	for messageIndex, message := range history {
		if !orchestrator.contextMessage(message) {
			continue
		}
		for slotIndex, imageURL := range message.ImageURLs {
			if imageURL == "" {
				continue
			}
			indexed[messageIndex] = append(indexed[messageIndex], indexedChatImage{
				index: next, slot: slotIndex + 1, url: imageURL, role: message.Role,
			})
			next++
		}
		if message.Role == orchestrator.schema.Roles.User {
			lastUser = messageIndex
		}
	}
	return indexed, lastUser
}

// summaryPrompt frames the session summary for the system prompt; markers in
// the summary are removed first, like the session notes.
func (orchestrator *Orchestrator) summaryPrompt(latest *Compaction) string {
	compaction := orchestrator.schema.ContextCompaction
	if latest == nil || compaction.ContextTemplate == "" {
		return ""
	}
	summary := withoutMarkers(latest.Summary, compaction.OpenMarker, compaction.CloseMarker)
	if strings.TrimSpace(summary) == "" {
		return ""
	}
	return strings.NewReplacer(
		"{open}", compaction.OpenMarker,
		"{summary}", summary,
		"{close}", compaction.CloseMarker,
	).Replace(compaction.ContextTemplate)
}

// compactedHandlePrompt names, for the model, the handles the newest user
// message mentions whose messages are in the summarized part: the summary may
// have shortened their labels, and the handle itself keeps working because
// tools resolve it against the whole history.
func (orchestrator *Orchestrator) compactedHandlePrompt(
	history []Message,
	indexed [][]indexedChatImage,
	through, lastUser int,
) string {
	template := orchestrator.schema.ContextCompaction.CompactedHandleTemplate
	if through < 0 || lastUser < 0 || template == "" || !orchestrator.handlePolicy().Enabled() {
		return ""
	}
	text := history[lastUser].Content
	var labels strings.Builder
	seen := map[string]bool{}
	for index := 0; index <= through && index < len(history); index++ {
		message := history[index]
		if !orchestrator.contextMessage(message) {
			continue
		}
		for _, attachment := range message.Attachments {
			handle := attachment.Handle
			if handle == "" || seen[strings.ToLower(handle)] || !mentionsHandle(text, handle) {
				continue
			}
			seen[strings.ToLower(handle)] = true
			if attachment.ImageSlot > 0 {
				for _, image := range indexed[index] {
					if image.slot == attachment.ImageSlot {
						if label, structured := orchestrator.attachmentLabel(message, image.slot, image.index); structured {
							labels.WriteString(label)
						}
					}
				}
				continue
			}
			single := message
			single.Attachments = []Attachment{attachment}
			labels.WriteString(orchestrator.unpicturedLabels(single))
		}
	}
	if labels.Len() == 0 {
		return ""
	}
	return strings.ReplaceAll(template, "{labels}", labels.String())
}

// mentionsHandle reports whether text names the handle as a whole token.
func mentionsHandle(text, handle string) bool {
	pattern, err := regexp.Compile(`(?i)` + regexp.QuoteMeta(handle) + `(?:[^0-9A-Za-z_]|$)`)
	return err == nil && pattern.MatchString(text)
}

// recordUsage stores the usage the upstream reported for a turn's request;
// the newest request of the turn wins. Best effort.
func (orchestrator *Orchestrator) recordUsage(ctx context.Context, command ProcessTurn, model string, usage *agents.Usage) {
	var stored *ContextUsage
	if usage != nil {
		stored = &ContextUsage{PromptTokens: usage.PromptTokens, CompletionTokens: usage.CompletionTokens, Model: model}
	}
	if err := orchestrator.service.RecordContextUsage(context.WithoutCancel(ctx), command.OwnerID, command.SessionID, stored); err != nil {
		orchestrator.logger.Warn("记录对话上下文用量失败", "session_id", command.SessionID, "error", err)
	}
}

// turnWindow is the context window of the turn's frozen model: the window
// its model option declares now, or the deployment profile's for a turn
// stored before #322 Scope 5.
func (orchestrator *Orchestrator) turnWindow(ctx context.Context, options TurnOptions) int {
	policy := orchestrator.service.ModelPolicy()
	if options.ModelConnection != "" {
		if set, err := policy.Models(ctx); err == nil {
			for _, option := range set.Options {
				if option.ID == options.ModelConnection && option.Model == options.Model {
					return option.Window
				}
			}
		}
	}
	return policy.Window(orchestrator.turnModel(options))
}
