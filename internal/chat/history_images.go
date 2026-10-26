package chat

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// HistoryLookupPolicy is the history image lookup of #279: when the user's
// words refer to several pictures but the turn does not carry all of them, the
// Agent looks through this session's pictures and settles on the ones meant.
//
// The lookup only ever reads the history and selection the server loaded for
// the turn's owner and session, so it cannot reach another session or owner.
// Limit bounds the distinct pictures one Agent reply may surface (listed or
// shown); the count is rebuilt from the reply's earlier lookup calls, so it
// holds across tool rounds and restarts without separate state.
type HistoryLookupPolicy struct {
	Handles        HandlePolicy
	UserRole       string
	PromptArgument string
	// TextCharacters bounds the user words and prompt quoted per picture.
	TextCharacters int
	Limit          int
	// BudgetTemplate ({limit}) is the note once the bound is reached.
	BudgetTemplate string
}

// NewHistoryLookupPolicy reads the lookup bound and texts from the chat Schema.
func NewHistoryLookupPolicy(schema catalog.ChatSchema) HistoryLookupPolicy {
	return HistoryLookupPolicy{
		Handles:        NewHandlePolicy(schema.Context),
		UserRole:       schema.Roles.User,
		PromptArgument: schema.Context.ToolPromptArgument,
		TextCharacters: schema.Context.ToolPromptPreviewChars,
		Limit:          schema.Context.HistoryImageLookupLimit,
		BudgetTemplate: schema.Context.HistoryImageBudgetTemplate,
	}
}

// Enabled reports whether the Schema declares a lookup bound.
func (policy HistoryLookupPolicy) Enabled() bool {
	return policy.Limit > 0 && policy.Handles.Enabled()
}

// HistoryImageFact is what the Agent reads about one picture of the session.
// It carries the handle and text facts only, never a URL or an ID.
type HistoryImageFact struct {
	Handle string `json:"handle"`
	// Source is the attachment role: upload, generated, capture or canvas.
	Source string `json:"source"`
	// Recency is 1 for the newest picture of the session.
	Recency int `json:"recency"`
	// UserText is what the user wrote with the picture, or for a generated
	// picture the request that led to it.
	UserText string `json:"user_text,omitempty"`
	// Tool and Prompt name the generation call that made the picture.
	Tool        string `json:"tool,omitempty"`
	Prompt      string `json:"prompt,omitempty"`
	CurrentTurn bool   `json:"current_turn,omitempty"`
	Selected    bool   `json:"selected,omitempty"`
	// Shown marks a picture the call asked to show; the orchestrator attaches
	// it, or says why it could not.
	Shown bool `json:"picture_requested,omitempty"`
}

// HistoryLookupResult is one lookup call's answer.
type HistoryLookupResult struct {
	Images    []HistoryImageFact `json:"images"`
	Limit     int                `json:"limit"`
	Used      int                `json:"used"`
	Remaining int                `json:"remaining"`
	// OlderNotListed says older pictures exist that the budget kept out.
	OlderNotListed bool `json:"older_not_listed,omitempty"`
	// OverBudget lists asked-for handles that were not shown: the budget was
	// spent on other pictures.
	OverBudget []string `json:"over_budget,omitempty"`
	Note       string   `json:"note,omitempty"`
	// Pictures are the images to show the Agent, in the order asked.
	Pictures []ToolPicture `json:"-"`
}

// ErrHistoryLookupArguments reports arguments the lookup cannot read.
var ErrHistoryLookupArguments = errors.New("chat: invalid history lookup arguments")

type historyCandidate struct {
	fact       HistoryImageFact
	attachment Attachment
	url        string
}

// Lookup answers one find_history_images call. earlier holds the arguments of
// the same reply's previous lookup calls, oldest first: they are replayed to
// know which pictures this reply has already surfaced, so asking again costs
// nothing and the bound counts each picture once.
func (policy HistoryLookupPolicy) Lookup(
	history []Message,
	selection []Attachment,
	earlier [][]byte,
	arguments []byte,
) (HistoryLookupResult, error) {
	candidates := policy.candidates(history, selection)
	surfaced := map[int]bool{}
	for _, previous := range earlier {
		// A call that was refused surfaced nothing then and surfaces nothing now.
		_, _ = policy.apply(history, selection, candidates, previous, surfaced)
	}
	return policy.apply(history, selection, candidates, arguments, surfaced)
}

func (policy HistoryLookupPolicy) apply(
	history []Message,
	selection []Attachment,
	candidates []historyCandidate,
	arguments []byte,
	surfaced map[int]bool,
) (HistoryLookupResult, error) {
	result := HistoryLookupResult{Images: []HistoryImageFact{}, Limit: policy.Limit}
	tokens, err := lookupHandleTokens(arguments)
	if err != nil {
		return result, err
	}
	admit := func(index int) bool {
		if surfaced[index] {
			return true
		}
		if len(surfaced) >= policy.Limit {
			return false
		}
		surfaced[index] = true
		return true
	}
	if len(tokens) == 0 {
		for index := range candidates {
			if !admit(index) {
				result.OlderNotListed = true
				continue
			}
			result.Images = append(result.Images, candidates[index].fact)
		}
	} else {
		resolved, err := policy.Handles.ResolveHandles(history, selection, tokens, AttachmentKindImage)
		if err != nil {
			return result, err
		}
		for _, item := range resolved {
			index := matchCandidate(candidates, item.Attachment, item.URL)
			if index < 0 {
				// A handle of an image without a picture: nothing can be shown.
				result.Pictures = append(result.Pictures, ToolPicture{Handle: item.Attachment.Handle})
				continue
			}
			if !admit(index) {
				result.OverBudget = append(result.OverBudget, item.Attachment.Handle)
				continue
			}
			fact := candidates[index].fact
			fact.Handle, fact.Shown = item.Attachment.Handle, true
			result.Images = append(result.Images, fact)
			result.Pictures = append(result.Pictures, ToolPicture{Handle: item.Attachment.Handle, URL: candidates[index].url})
		}
	}
	result.Used = len(surfaced)
	result.Remaining = max(0, policy.Limit-len(surfaced))
	if result.Remaining == 0 {
		result.Note = strings.ReplaceAll(policy.BudgetTemplate, "{limit}", itoa(policy.Limit))
	}
	return result, nil
}

// candidates lists the session's pictures newest first, one entry per
// platform object: a generated picture the user later attached again is one
// candidate, under its newest handle. Selected images that no message shows
// (the artboard snapshot) come first, being part of the turn itself.
func (policy HistoryLookupPolicy) candidates(history []Message, selection []Attachment) []historyCandidate {
	var result []historyCandidate
	currentTurn := -1
	if last := len(history) - 1; last >= 0 && history[last].Role == policy.UserRole {
		currentTurn = last
	}
	for messageIndex := len(history) - 1; messageIndex >= 0; messageIndex-- {
		message := history[messageIndex]
		for attachmentIndex := len(message.Attachments) - 1; attachmentIndex >= 0; attachmentIndex-- {
			attachment := message.Attachments[attachmentIndex]
			url := AttachmentURL(message, attachment)
			if attachment.Kind != AttachmentKindImage || attachment.Handle == "" || url == "" {
				continue
			}
			if index := matchCandidate(result, attachment, url); index >= 0 {
				result[index].fact.CurrentTurn = result[index].fact.CurrentTurn || messageIndex == currentTurn
				continue
			}
			fact := HistoryImageFact{
				Handle:      attachment.Handle,
				Source:      attachment.Role,
				UserText:    policy.userText(history, messageIndex),
				CurrentTurn: messageIndex == currentTurn,
			}
			fact.Tool, fact.Prompt = policy.generatingCall(history, attachment.TaskID)
			result = append(result, historyCandidate{fact: fact, attachment: attachment, url: url})
		}
	}
	var leading []historyCandidate
	for _, attachment := range selection {
		if attachment.Kind != AttachmentKindImage || attachment.Handle == "" {
			continue
		}
		if index := matchCandidate(result, attachment, attachment.URL); index >= 0 {
			result[index].fact.Selected = true
			continue
		}
		if attachment.URL == "" || matchCandidate(leading, attachment, attachment.URL) >= 0 {
			continue
		}
		fact := HistoryImageFact{Handle: attachment.Handle, Source: attachment.Role, Selected: true, CurrentTurn: true}
		if currentTurn >= 0 {
			fact.UserText = policy.userText(history, currentTurn)
		}
		fact.Tool, fact.Prompt = policy.generatingCall(history, attachment.TaskID)
		leading = append(leading, historyCandidate{fact: fact, attachment: attachment, url: attachment.URL})
	}
	result = append(leading, result...)
	for index := range result {
		result[index].fact.Recency = index + 1
	}
	return result
}

// matchCandidate finds the candidate showing the same picture: the same
// handle, the same platform object, or the same picture URL.
func matchCandidate(candidates []historyCandidate, attachment Attachment, url string) int {
	for index, candidate := range candidates {
		if strings.EqualFold(candidate.attachment.Handle, attachment.Handle) ||
			SameObject(candidate.attachment, attachment) ||
			(url != "" && candidate.url == url) {
			return index
		}
	}
	return -1
}

// userText is the user's own words for the picture of a message: the message
// itself when the user sent it, else the user message that led to it.
func (policy HistoryLookupPolicy) userText(history []Message, messageIndex int) string {
	for index := messageIndex; index >= 0; index-- {
		if history[index].Role == policy.UserRole {
			return truncateRunes(strings.TrimSpace(history[index].Content), policy.TextCharacters)
		}
	}
	return ""
}

// generatingCall names the newest tool call of the history that produced the
// Task, with its prompt.
func (policy HistoryLookupPolicy) generatingCall(history []Message, taskID string) (string, string) {
	if taskID == "" {
		return "", ""
	}
	for messageIndex := len(history) - 1; messageIndex >= 0; messageIndex-- {
		calls := history[messageIndex].ToolCalls
		for callIndex := len(calls) - 1; callIndex >= 0; callIndex-- {
			if calls[callIndex].TaskID != taskID {
				continue
			}
			var arguments map[string]json.RawMessage
			prompt := ""
			if json.Unmarshal(calls[callIndex].Arguments, &arguments) == nil {
				_ = json.Unmarshal(arguments[policy.PromptArgument], &prompt)
			}
			return calls[callIndex].Name, truncateRunes(strings.TrimSpace(prompt), policy.TextCharacters)
		}
	}
	return "", ""
}

// lookupHandleTokens reads the optional "handles" argument: a list of handles
// or a single one.
func lookupHandleTokens(arguments []byte) ([]string, error) {
	if len(strings.TrimSpace(string(arguments))) == 0 {
		return nil, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(arguments, &fields); err != nil {
		return nil, ErrHistoryLookupArguments
	}
	raw, present := fields["handles"]
	if !present || string(raw) == "null" {
		return nil, nil
	}
	var tokens []string
	if err := json.Unmarshal(raw, &tokens); err != nil {
		var single string
		if json.Unmarshal(raw, &single) != nil {
			return nil, ErrHistoryLookupArguments
		}
		tokens = []string{single}
	}
	cleaned := tokens[:0]
	for _, token := range tokens {
		if token = strings.TrimSpace(token); token != "" {
			cleaned = append(cleaned, token)
		}
	}
	return cleaned, nil
}
