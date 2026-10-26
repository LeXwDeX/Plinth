package generationtool

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// taskOfAssetURL reads the Task a result-picture URL belongs to, using the
// Schema's asset URL template (task first, then asset). A URL that is not a
// generated picture of this deployment reports false.
func (executor *Executor) taskOfAssetURL(reference string) (string, bool) {
	template := executor.chat.Context.AssetURLTemplate
	head, rest, ok := strings.Cut(template, "{task_id}")
	if !ok {
		return "", false
	}
	middle, tail, ok := strings.Cut(rest, "{asset_id}")
	if !ok {
		return "", false
	}
	path := reference
	if parsed, err := url.Parse(reference); err == nil {
		path = parsed.Path
	}
	if !strings.HasPrefix(path, head) || !strings.HasSuffix(path, tail) {
		return "", false
	}
	inner := strings.TrimSuffix(strings.TrimPrefix(path, head), tail)
	taskID, asset, found := strings.Cut(inner, middle)
	if !found || taskID == "" || asset == "" || strings.Contains(taskID, "/") {
		return "", false
	}
	return taskID, true
}

func (executor *Executor) assetURL(taskID, assetID string) string {
	return strings.NewReplacer("{task_id}", taskID, "{asset_id}", assetID).Replace(executor.chat.Context.AssetURLTemplate)
}

// modelSourceReferences replaces every reference picture that is a view of a
// three-view group by the group's front view. A model is made from the front
// view alone, whichever view the person pointed at, and the views that do not
// take part are dropped rather than sent along. When the front view is not
// finished it returns the Schema's refusal instead of a model request.
func (executor *Executor) modelSourceReferences(ctx context.Context, command chat.ToolCommand,
	references []string) ([]string, string, error) {
	if executor.threeViews == nil || len(references) == 0 {
		return references, "", nil
	}
	result := make([]string, 0, len(references))
	seen := map[string]struct{}{}
	add := func(reference string) {
		if _, duplicate := seen[reference]; !duplicate {
			seen[reference] = struct{}{}
			result = append(result, reference)
		}
	}
	for _, reference := range references {
		taskID, generated := executor.taskOfAssetURL(reference)
		if !generated {
			add(reference)
			continue
		}
		source, member, err := executor.threeViews.ModelSource(ctx, command.OwnerID, command.SessionID, taskID)
		if err != nil {
			return nil, "", err
		}
		switch {
		case !member:
			add(reference)
		case !source.Available:
			return nil, executor.localized(command, executor.chat.ThreeView.Messages.ModelSourceNotYet), nil
		default:
			add(executor.assetURL(source.TaskID, source.AssetID))
		}
	}
	return result, "", nil
}

// referenceNote is the Schema's statement that a model came from the front
// view of a three-view group, for the tools whose Schema names a key for it.
// It is derived again from the call's references, so a recovered call reports
// it as well.
func (executor *Executor) referenceNote(ctx context.Context, command chat.ToolCommand, offering generation.Offering) string {
	if offering.ReferenceSelectionPolicy == "preserve" {
		return ""
	}
	if command.Tool.ResultFields.ReferenceNote == "" || command.Tool.Capability != generation.ImageTo3D {
		return ""
	}
	var arguments map[string]json.RawMessage
	if json.Unmarshal(command.Call.Arguments, &arguments) != nil {
		arguments = map[string]json.RawMessage{}
	}
	references, err := executor.selectReferences(command, arguments)
	if err != nil {
		return ""
	}
	if executor.threeViews == nil {
		return referenceLimitNote(command.Tool, offering, len(references), executor.localizeText(command))
	}
	for _, reference := range references {
		taskID, generated := executor.taskOfAssetURL(reference)
		if !generated {
			continue
		}
		if _, member, err := executor.threeViews.ModelSource(ctx, command.OwnerID, command.SessionID, taskID); err == nil && member {
			return executor.localized(command, executor.chat.ThreeView.ModelSourceNote)
		}
	}
	return referenceLimitNote(command.Tool, offering, len(references), executor.localizeText(command))
}
