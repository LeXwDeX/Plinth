package generationtool

import (
	"context"
	"net/url"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/chat"
)

// taskOfAssetURL reads the Task a result-picture URL belongs to, using the
// Schema's asset URL template (task first, then asset). A URL that is not a
// generated picture of this deployment reports false.
func (executor *Executor) taskOfAssetURL(reference string) (string, bool) {
	taskID, _, ok := executor.taskAssetOfURL(reference)
	return taskID, ok
}

// taskAssetOfURL reads both parts a generated picture URL names. Only the
// Schema's template decides the split; a path that does not match reports
// false.
func (executor *Executor) taskAssetOfURL(reference string) (string, string, bool) {
	template := executor.chat.Context.AssetURLTemplate
	head, rest, ok := strings.Cut(template, "{task_id}")
	if !ok {
		return "", "", false
	}
	middle, tail, ok := strings.Cut(rest, "{asset_id}")
	if !ok {
		return "", "", false
	}
	path := reference
	if parsed, err := url.Parse(reference); err == nil {
		path = parsed.Path
	}
	if !strings.HasPrefix(path, head) || !strings.HasSuffix(path, tail) {
		return "", "", false
	}
	inner := strings.TrimSuffix(strings.TrimPrefix(path, head), tail)
	taskID, asset, found := strings.Cut(inner, middle)
	if !found || taskID == "" || asset == "" || strings.Contains(taskID, "/") || strings.Contains(asset, "/") {
		return "", "", false
	}
	return taskID, asset, true
}

func (executor *Executor) assetURL(taskID, assetID string) string {
	return strings.NewReplacer("{task_id}", taskID, "{asset_id}", assetID).Replace(executor.chat.Context.AssetURLTemplate)
}

// modelSourceReferences replaces every reference picture that is a view of a
// three-view group by the group's front view. A model is made from the front
// view alone, whichever view the person pointed at, and the views that do not
// take part are dropped rather than sent along. When the front view is not
// finished it returns the Schema's refusal instead of a model request.
//
// The returned selection names what each retained picture came from, so the
// source fact can be checked against the prepared input instead of being
// inferred again later from the turn or today's groups.
func (executor *Executor) modelSourceReferences(ctx context.Context, command chat.ToolCommand,
	references []string) (referenceSelection, string, error) {
	selection := referenceSelection{Given: len(references)}
	origins := make([]referenceOrigin, 0, len(references))
	for index, reference := range references {
		origin := referenceOrigin{Index: index, Digest: referenceDigest(reference)}
		if taskID, assetID, generated := executor.taskAssetOfURL(reference); generated {
			origin.TaskID, origin.AssetID, origin.Digest = taskID, assetID, ""
		}
		origins = append(origins, origin)
	}
	selection.Origins = origins
	if len(references) == 0 {
		return selection, "", nil
	}
	if executor.threeViews == nil {
		for index, reference := range references {
			selection.Candidates = append(selection.Candidates, referenceCandidate{URL: reference, Origin: origins[index]})
		}
		return selection, "", nil
	}
	result := make([]referenceCandidate, 0, len(references))
	seen := map[string]struct{}{}
	add := func(candidate referenceCandidate) {
		if _, duplicate := seen[candidate.URL]; duplicate {
			selection.Deduped = true
			return
		}
		seen[candidate.URL] = struct{}{}
		result = append(result, candidate)
	}
	for index, reference := range references {
		origin := origins[index]
		taskID, generated := executor.taskOfAssetURL(reference)
		if !generated {
			add(referenceCandidate{URL: reference, Origin: origin})
			continue
		}
		source, member, err := executor.threeViews.ModelSource(ctx, command.OwnerID, command.SessionID, taskID)
		if err != nil {
			return referenceSelection{}, "", err
		}
		switch {
		case !member:
			add(referenceCandidate{URL: reference, Origin: origin})
		case !source.Available:
			return referenceSelection{}, executor.localized(command, executor.chat.ThreeView.Messages.ModelSourceNotYet), nil
		default:
			selection.Replaced = true
			add(referenceCandidate{URL: executor.assetURL(source.TaskID, source.AssetID), Origin: origin,
				FrontTaskID: source.TaskID, FrontAssetID: source.AssetID})
		}
	}
	selection.Candidates = result
	return selection, "", nil
}
