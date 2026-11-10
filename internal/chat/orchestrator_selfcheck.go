package chat

// The self-check state block (#246): every finished 3D result of the session
// with its delivery state and counters. It is projected from the persisted
// records and the public Task list, not from the message window, so a context
// compaction never changes it. The block states the unchecked rule as a server
// fact: an unchecked result must never be reported as checked.

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

type selfCheckPromptEntry struct {
	task    SelfCheckTask
	record  SelfCheckRecord
	line    string
	updated int64
}

func (orchestrator *Orchestrator) selfCheckPrompt(ctx context.Context, ownerID, sessionID string,
	history []Message) string {
	schema := orchestrator.schema.SelfCheck
	policy := schema.Context
	if orchestrator.selfCheckTasks == nil || orchestrator.service == nil ||
		strings.TrimSpace(policy.ContextTemplate) == "" || ownerID == "" || sessionID == "" {
		return ""
	}
	tasks, err := orchestrator.selfCheckTasks(ctx, ownerID, sessionID)
	if err != nil || len(tasks) == 0 {
		return ""
	}
	records, err := orchestrator.service.ListSelfChecks(ctx, ownerID, sessionID)
	if err != nil {
		orchestrator.logger.Warn("self-check records not readable for the prompt",
			"owner_id", ownerID, "session_id", sessionID, "error", err)
		return ""
	}
	byTask := make(map[string]SelfCheckRecord, len(records))
	for _, record := range records {
		byTask[record.TaskID] = record
	}
	background := backgroundResultTasks(history)
	entries := make([]selfCheckPromptEntry, 0, len(tasks))
	for _, task := range tasks {
		record := byTask[task.TaskID]
		if record.TaskID == "" {
			record = SelfCheckRecord{TaskID: task.TaskID}
		}
		counters := SelfCheckCounts(schema, record)
		summary := ""
		if record.Conclusion != nil && strings.TrimSpace(record.Conclusion.Summary) != "" {
			summary = "｜" + truncateRunes(record.Conclusion.Summary, policy.MaxCharacters/4)
		}
		backgroundText := ""
		if task.Background || background[task.TaskID] {
			backgroundText = "｜" + policy.BackgroundText
		}
		line := strings.NewReplacer(
			"{task_id}", task.TaskID,
			"{status}", counters.StatusLabel,
			"{rounds_used}", fmt.Sprint(counters.RoundsUsed),
			"{rounds_limit}", fmt.Sprint(counters.RoundsLimit),
			"{renders_in_round}", fmt.Sprint(counters.RendersInRound),
			"{renders_limit}", fmt.Sprint(counters.RendersLimit),
			"{corrections_used}", fmt.Sprint(counters.CorrectionsUsed),
			"{corrections_limit}", fmt.Sprint(counters.CorrectionsLimit),
			"{background}", backgroundText,
			"{summary}", summary,
		).Replace(orchestrator.text(ctx, policy.TaskLineTemplate))
		entries = append(entries, selfCheckPromptEntry{
			task: task, record: record, line: line,
			updated: record.UpdatedAt.UnixNano(),
		})
	}
	// Unchecked results first (they are the ones the Agent must not claim as
	// checked), then the most recently updated.
	sort.SliceStable(entries, func(left, right int) bool {
		leftUnchecked := SelfCheckCounts(schema, entries[left].record).StatusRole == SelfCheckRoleUnchecked
		rightUnchecked := SelfCheckCounts(schema, entries[right].record).StatusRole == SelfCheckRoleUnchecked
		if leftUnchecked != rightUnchecked {
			return leftUnchecked
		}
		return entries[left].updated > entries[right].updated
	})
	if limit := policy.MaxResults; limit > 0 && len(entries) > limit {
		entries = entries[:limit]
	}
	var lines []string
	used := 0
	for _, entry := range entries {
		length := len([]rune(entry.line)) + 1
		if policy.MaxCharacters > 0 && used+length > policy.MaxCharacters && len(lines) > 0 {
			break
		}
		lines = append(lines, entry.line)
		used += length
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.NewReplacer(
		"{results}", strings.Join(lines, "\n"),
		"{rule}", policy.UncheckedRuleText,
	).Replace(orchestrator.text(ctx, policy.ContextTemplate))
}

// backgroundResultTasks names the Tasks the person stopped waiting for, read
// from the historical tool calls of the session.
func backgroundResultTasks(history []Message) map[string]bool {
	tasks := map[string]bool{}
	for _, message := range history {
		for _, call := range message.ToolCalls {
			if call.Background && strings.TrimSpace(call.TaskID) != "" {
				tasks[strings.TrimSpace(call.TaskID)] = true
			}
		}
	}
	return tasks
}
