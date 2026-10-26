package generationtool

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/chat"
)

// handlePolicy is the attachment handle syntax from chat.context.
func (executor *Executor) handlePolicy() chat.HandlePolicy {
	return chat.NewHandlePolicy(executor.chat.Context)
}

// handleTokens reads the handles a tool call named. The same argument may
// carry one string or a list.
func (executor *Executor) handleTokens(command chat.ToolCommand, arguments map[string]json.RawMessage, argument string) ([]string, error) {
	raw, present := arguments[argument]
	if !present || argument == "" {
		return nil, nil
	}
	var tokens []string
	if err := json.Unmarshal(raw, &tokens); err != nil {
		var single string
		if json.Unmarshal(raw, &single) != nil {
			return nil, errors.New(executor.localized(command, executor.chat.Presentation.Messages.InvalidReferenceIndices))
		}
		tokens = []string{single}
	}
	cleaned := tokens[:0]
	for _, token := range tokens {
		if strings.TrimSpace(token) != "" {
			cleaned = append(cleaned, strings.TrimSpace(token))
		}
	}
	return cleaned, nil
}

// handleFailure turns a handle refusal into the Schema's wording; any other
// error is returned unchanged.
func (executor *Executor) handleFailure(command chat.ToolCommand, err error) error {
	var unknown *chat.UnknownHandleError
	if !errors.As(err, &unknown) {
		return err
	}
	template := executor.localized(command, executor.chat.Context.UnknownHandleTemplate)
	if unknown.Reason == "kind" {
		template = executor.localized(command, executor.chat.Context.WrongHandleKindTemplate)
	}
	return errors.New(strings.ReplaceAll(template, "{handle}", unknown.Handle))
}

// resolveImageHandles maps image handles (and the selection alias) to the
// picture URLs of the session's attachments. The history and selection come
// from the server, so a handle cannot name another owner's or session's
// object; the URLs are resolved to owned assets later like any reference.
func (executor *Executor) resolveImageHandles(command chat.ToolCommand, tokens []string) ([]string, error) {
	resolved, err := executor.handlePolicy().ResolveHandles(
		command.History, command.Options.Selection, tokens, chat.AttachmentKindImage)
	if err != nil {
		return nil, executor.handleFailure(command, err)
	}
	urls := make([]string, 0, len(resolved))
	for _, item := range resolved {
		if item.URL != "" {
			urls = append(urls, item.URL)
		}
	}
	if len(urls) == 0 && len(tokens) > 0 {
		return nil, executor.handleFailure(command, &chat.UnknownHandleError{Handle: tokens[0], Reason: "unknown"})
	}
	return urls, nil
}

// selectedImageURLs are the pictures of the images selected on the workspace.
func selectedImageURLs(command chat.ToolCommand) []string {
	var urls []string
	for _, attachment := range command.Options.Selection {
		if attachment.Kind == chat.AttachmentKindImage && attachment.URL != "" {
			urls = append(urls, attachment.URL)
		}
	}
	return urls
}

// selectedModelTask is the Task of the 3D model selected on the workspace.
func selectedModelTask(command chat.ToolCommand) (taskID, assetID string) {
	for _, attachment := range command.Options.Selection {
		if attachment.Kind == chat.AttachmentKindModel && attachment.TaskID != "" {
			return attachment.TaskID, attachment.AssetID
		}
	}
	return "", ""
}
