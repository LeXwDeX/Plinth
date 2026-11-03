package generationtool

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// multiviewPictures are the pictures and views of one multiview step (#65),
// bound by the server: from the person's structured request of the turn, or
// from the three-view group the Agent named. The Agent's handles and views
// take no part.
type multiviewPictures struct {
	URLs  []string
	Views []string
	// fromRequest: the turn's structured request named the pictures, so the
	// model it names is the one to run.
	fromRequest bool
	model       string
}

// multiviewSelection reads the pictures of a multiview step. It returns nil
// when the call is not one: another tool, or a call that names neither a
// structured request nor a group, which keeps the ordinary handle path. A
// refusal is the Schema's text and nothing is prepared.
func (executor *Executor) multiviewSelection(ctx context.Context, command chat.ToolCommand,
	arguments map[string]json.RawMessage) (*multiviewPictures, string, error) {
	multiview := executor.chat.ThreeView.Multiview
	if multiview.Tool == "" || command.Tool.Name != multiview.Tool {
		return nil, "", nil
	}
	messages := executor.chat.ThreeView.Messages
	if request := command.Options.Multiview; request != nil {
		return executor.requestedViews(ctx, command, *request)
	}
	var group string
	if raw, named := arguments[multiview.GroupArgument]; named {
		if err := json.Unmarshal(raw, &group); err != nil || strings.TrimSpace(group) == "" {
			return nil, executor.localized(command, messages.GroupNotFound), nil
		}
	}
	if group == "" {
		return nil, "", nil
	}
	if executor.threeViews == nil {
		return nil, executor.localized(command, messages.GroupNotFound), nil
	}
	views, err := executor.threeViews.GroupViews(ctx, command.OwnerID, command.SessionID, group)
	if errors.Is(err, ErrThreeViewGroupNotFound) {
		return nil, executor.localized(command, messages.GroupNotFound), nil
	}
	if err != nil {
		return nil, "", err
	}
	pictures := &multiviewPictures{}
	for _, view := range views {
		address, named := executor.viewURL(view.TaskID, view.AssetID)
		if !named {
			return nil, "", errors.New("generation tool: input asset route missing")
		}
		pictures.URLs, pictures.Views = append(pictures.URLs, address), append(pictures.Views, view.View)
	}
	return pictures, "", nil
}

// requestedViews binds the pictures of the person's structured request. A
// request that names a group must match the group as it stands: every view it
// names is a finished view of the group with exactly that picture. A view
// replaced or made again since the person pressed the button refuses the step
// rather than sending pictures they did not see. Uploads name no group; the
// reference resolver proves each one is the owner's.
func (executor *Executor) requestedViews(ctx context.Context, command chat.ToolCommand,
	request chat.MultiviewRequest) (*multiviewPictures, string, error) {
	messages := executor.chat.ThreeView.Messages
	if !request.Valid() {
		return nil, executor.localized(command, messages.ViewsInvalid), nil
	}
	pictures := &multiviewPictures{fromRequest: true, model: request.Model}
	if request.Group == "" {
		for _, view := range request.Views {
			address, named := executor.viewURL(view.TaskID, view.AssetID)
			if !named {
				return nil, "", errors.New("generation tool: input asset route missing")
			}
			pictures.URLs, pictures.Views = append(pictures.URLs, address), append(pictures.Views, view.View)
		}
		return pictures, "", nil
	}
	if executor.threeViews == nil {
		return nil, executor.localized(command, messages.GroupNotFound), nil
	}
	current, err := executor.threeViews.GroupViews(ctx, command.OwnerID, command.SessionID, request.Group)
	if errors.Is(err, ErrThreeViewGroupNotFound) {
		return nil, executor.localized(command, messages.GroupNotFound), nil
	}
	if err != nil {
		return nil, "", err
	}
	requested := make(map[string]chat.MultiviewView, len(request.Views))
	for _, view := range request.Views {
		requested[view.View] = view
	}
	for _, view := range current {
		asked, named := requested[view.View]
		if !named {
			continue
		}
		if asked.TaskID != view.TaskID || asked.AssetID != view.AssetID {
			return nil, executor.localized(command, messages.ViewsChanged), nil
		}
		delete(requested, view.View)
		address, addressed := executor.viewURL(view.TaskID, view.AssetID)
		if !addressed {
			return nil, "", errors.New("generation tool: input asset route missing")
		}
		pictures.URLs, pictures.Views = append(pictures.URLs, address), append(pictures.Views, view.View)
	}
	if len(requested) > 0 {
		// A view the person saw finished is no longer a finished view of the group.
		return nil, executor.localized(command, messages.ViewsChanged), nil
	}
	return pictures, "", nil
}

// fitMultiviewViews keeps the views the Offering takes, in the group's order,
// and checks the step can run: the Offering declares the reference-views
// parameter, the required view is there and enough views remain. A group's
// view the Offering does not name (a "side" view of a group made before the
// views followed the multiview keys) is left out. Labelled uploads keep every
// view: a view the Offering does not take is the person's mistake and refuses
// the step.
func (executor *Executor) fitMultiviewViews(command chat.ToolCommand, offering generation.Offering,
	pictures *multiviewPictures, group bool) string {
	multiview := executor.chat.ThreeView.Multiview
	messages := executor.chat.ThreeView.Messages
	var accepted []string
	declared := false
	for _, parameter := range offering.Parameters {
		if parameter.Semantic != generation.SemanticReferenceViews {
			continue
		}
		declared = true
		for _, option := range parameter.Options {
			accepted = append(accepted, option.Value)
		}
	}
	if !declared {
		return executor.localized(command, messages.ViewsInvalid)
	}
	urls, views := pictures.URLs[:0:0], pictures.Views[:0:0]
	for index, view := range pictures.Views {
		if !slices.Contains(accepted, view) {
			if group {
				continue
			}
			return executor.localized(command, messages.ViewsInvalid)
		}
		urls, views = append(urls, pictures.URLs[index]), append(views, view)
	}
	pictures.URLs, pictures.Views = urls, views
	if !slices.Contains(views, multiview.RequiredView) || len(views) < multiview.MinimumViews {
		if group {
			return executor.localized(command, messages.GroupNotReady)
		}
		return executor.localized(command, messages.ViewsInvalid)
	}
	return ""
}
