package generationtool

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// multiviewPictures are the pictures and views of one multiview step (#65,
// #433), bound by the server: from the person's structured request of the turn,
// from the three-view group the Agent named, or from the pictures the call
// itself names (handles, historical indices, this turn's or the selected
// pictures).
type multiviewPictures struct {
	URLs  []string
	Views []string
	// fromRequest: the turn's structured request named the pictures, so the
	// model it names is the one to run.
	fromRequest bool
	// fromHandles: the call named its own pictures, which the ordinary
	// reference path resolves; URLs is empty and the resolved references are
	// kept as they are (#433).
	fromHandles bool
	// group: the pictures are a finished three-view group's views. Such a
	// group may hold a view of the time before the views followed the
	// multiview keys (a "side"), which is left out instead of refusing. Every
	// other source names each picture itself, so an unsupported view refuses
	// the step rather than silently dropping a picture the person or the Agent
	// chose.
	group bool
	model string
}

// multiviewSelection reads the pictures of a multiview step. It returns nil
// when the call is not one: another tool, or a call that names neither a
// structured request nor a group, which keeps the ordinary handle path
// (callMultiviewViews binds that path's views). A refusal is the Schema's text
// and nothing is prepared.
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
	pictures := &multiviewPictures{group: true}
	for _, view := range views {
		address, named := executor.viewURL(view.TaskID, view.AssetID)
		if !named {
			return nil, "", errors.New("generation tool: input asset route missing")
		}
		pictures.URLs, pictures.Views = append(pictures.URLs, address), append(pictures.Views, view.View)
	}
	return pictures, "", nil
}

// callMultiviewViews binds the views of a multiview step whose pictures the
// call itself supplies (#433): the handles, historical indices or selected
// pictures the ordinary reference path resolves. The tool describes two ways to
// say which view each picture is: reference_views in the same order as the
// pictures, or the platform's view order by position (front, left, back,
// right). Labels that repeat a view, name one the platform does not declare,
// give another number of views than there are pictures, or leave out the
// required view refuse the step with the Schema's views_invalid, and nothing is
// prepared. A call that supplies no picture at all binds nothing: no picture
// has a view to claim.
func (executor *Executor) callMultiviewViews(command chat.ToolCommand, arguments map[string]json.RawMessage,
	pictures int) (*multiviewPictures, string) {
	multiview := executor.chat.ThreeView.Multiview
	if multiview.Tool == "" || command.Tool.Name != multiview.Tool || pictures == 0 {
		return nil, ""
	}
	messages := executor.chat.ThreeView.Messages
	if raw, named := arguments[multiview.ViewsArgument]; named && string(raw) != "null" {
		var declared []string
		if json.Unmarshal(raw, &declared) != nil {
			return nil, executor.localized(command, messages.ViewsInvalid)
		}
		views, valid := namedViews(declared, pictures, multiview)
		if !valid {
			return nil, executor.localized(command, messages.ViewsInvalid)
		}
		return &multiviewPictures{Views: views, fromHandles: true}, ""
	}
	order := multiviewViewsOrder(multiview)
	if pictures > len(order) {
		return nil, executor.localized(command, messages.ViewsInvalid)
	}
	return &multiviewPictures{Views: append([]string(nil), order[:pictures]...), fromHandles: true}, ""
}

// namedViews reads the views a call labelled its pictures with: one declared
// multiview key per picture, no repeats, and the required view among them.
func namedViews(declared []string, pictures int, multiview catalog.ChatThreeViewMultiviewSchema) ([]string, bool) {
	order := multiviewViewsOrder(multiview)
	if len(declared) != pictures {
		return nil, false
	}
	views := make([]string, 0, len(declared))
	seen := make(map[string]bool, len(declared))
	for _, view := range declared {
		view = strings.TrimSpace(view)
		if !slices.Contains(order, view) || seen[view] {
			return nil, false
		}
		seen[view] = true
		views = append(views, view)
	}
	if multiview.RequiredView != "" && !seen[multiview.RequiredView] {
		return nil, false
	}
	return views, true
}

// multiviewViewsOrder is the platform's multiview key order, which is also the
// order an order-only Offering reads its pictures in.
func multiviewViewsOrder(multiview catalog.ChatThreeViewMultiviewSchema) []string {
	if len(multiview.ViewOrder) > 0 {
		return multiview.ViewOrder
	}
	return defaultViewOrder
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
	pictures := &multiviewPictures{fromRequest: true, model: request.Model, group: request.Group != ""}
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
// parameter or reads the pictures in view order, the required view is there and
// enough views remain. A group's view the Offering does not name (a "side" view
// of a group made before the views followed the multiview keys) is left out.
// Pictures the person or the Agent named keep every view: a view the Offering
// does not take is a mistake and refuses the step, and an Offering that takes
// no several views at all refuses it too (#433) instead of running one picture
// as a single-image step.
func (executor *Executor) fitMultiviewViews(command chat.ToolCommand, offering generation.Offering,
	pictures *multiviewPictures, group bool) string {
	multiview := executor.chat.ThreeView.Multiview
	messages := executor.chat.ThreeView.Messages
	accepted, ordered, declared := multiviewViewsOf(offering, multiview)
	if !declared {
		if group || executor.familyTakesSeveralViews(offering) {
			// The model's family does take several views, so the views given are
			// what does not fit; the view check words that.
			return executor.localized(command, messages.ViewsInvalid)
		}
		return executor.viewsUnsupportedText(command, offering)
	}
	if ordered {
		// An order-only Offering reads positions, so the pictures must be a
		// contiguous front, left, back, right prefix starting at the required
		// view (#243). Nothing is dropped, because dropping would shift every
		// later picture into the wrong angle.
		if !orderedViewPrefix(pictures.Views, accepted, multiview.RequiredView) ||
			len(pictures.Views) < multiview.MinimumViews {
			if group {
				return executor.localized(command, messages.GroupNotReady)
			}
			return executor.localized(command, messages.ViewsInvalid)
		}
		return ""
	}
	urls, views := pictures.URLs[:0:0], pictures.Views[:0:0]
	for index, view := range pictures.Views {
		if !slices.Contains(accepted, view) {
			if group {
				continue
			}
			return executor.localized(command, messages.ViewsInvalid)
		}
		views = append(views, view)
		// A call's own pictures keep the list the reference path resolved; only
		// a group's or a request's pictures carry their address here (#433).
		if index < len(pictures.URLs) {
			urls = append(urls, pictures.URLs[index])
		}
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

// defaultViewOrder is the platform's multiview key order used when the Schema
// declares no view order (a historical Catalog).
var defaultViewOrder = []string{"front", "left", "back", "right"}

// multiviewViewsOf describes how an Offering takes several views: labelled
// (its reference-views parameter says which view each picture is), or
// order-only (the provider's multi-image endpoint reads the pictures in the
// declared view order). A plain multi-reference Offering declares neither.
func multiviewViewsOf(offering generation.Offering, multiview catalog.ChatThreeViewMultiviewSchema) (
	views []string, ordered, declared bool,
) {
	for _, parameter := range offering.Parameters {
		if parameter.Semantic != generation.SemanticReferenceViews {
			continue
		}
		for _, option := range parameter.Options {
			views = append(views, option.Value)
		}
		return views, false, true
	}
	var options struct {
		Endpoint string `json:"endpoint"`
	}
	if len(offering.ProviderOptions) > 0 && json.Unmarshal(offering.ProviderOptions, &options) == nil &&
		options.Endpoint == "multi-image-to-3d" {
		order := multiview.ViewOrder
		if len(order) == 0 {
			order = defaultViewOrder
		}
		return append([]string(nil), order...), true, true
	}
	return nil, false, false
}

// orderedViewPrefix reports whether views is the contiguous prefix of order
// that starts with the required view.
func orderedViewPrefix(views, order []string, required string) bool {
	if len(views) == 0 || len(views) > len(order) || (required != "" && views[0] != required) {
		return false
	}
	for index, view := range views {
		if view != order[index] {
			return false
		}
	}
	return true
}

// declaresReferenceViews reports whether an Offering labels each picture's
// view. An order-only Offering takes no reference-views argument.
func declaresReferenceViews(offering generation.Offering) bool {
	for _, parameter := range offering.Parameters {
		if parameter.Semantic == generation.SemanticReferenceViews {
			return true
		}
	}
	return false
}

// viewsUnsupportedText refuses a step whose chosen Offering takes no several
// views at all, such as the single-image shape of a model the person locked
// (#433). It names the model and says nothing ran, so the Agent neither reports
// a single-image 3D model as the multiview result nor asks the person to
// relabel pictures that are not the problem. A Catalog without that copy keeps
// the view check's older wording.
func (executor *Executor) viewsUnsupportedText(command chat.ToolCommand, offering generation.Offering) string {
	messages := executor.chat.ThreeView.Messages
	if strings.TrimSpace(messages.ViewsUnsupported) == "" {
		return executor.localized(command, messages.ViewsInvalid)
	}
	return strings.ReplaceAll(
		executor.localized(command, messages.ViewsUnsupported),
		"{model}", executor.localized(command, OfferingModelName(offering)),
	)
}

// familyTakesSeveralViews reports whether another shape of the Offering's model
// family takes several views (#417). When one does, a view set it does not
// accept means the views are wrong, not that the model cannot take several
// views at all.
func (executor *Executor) familyTakesSeveralViews(offering generation.Offering) bool {
	if offering.ModelFamily == "" || executor.gateway == nil {
		return false
	}
	for _, candidate := range executor.gateway.Offerings(offering.Capability) {
		if candidate.ID == offering.ID || !chat.SameModelFamily(offering, candidate) {
			continue
		}
		if _, _, declared := multiviewViewsOf(candidate, executor.chat.ThreeView.Multiview); declared {
			return true
		}
	}
	return false
}

// TakesSeveralViews reports whether an Offering takes several views of one
// object (#65): labelled by a reference-views parameter, or in order through a
// multi-image endpoint. The external MCP service (#332) picks such a service
// for a call that gives several pictures of one model and names none.
func TakesSeveralViews(offering generation.Offering) bool {
	_, _, declared := multiviewViewsOf(offering, catalog.ChatThreeViewMultiviewSchema{})
	return declared
}

// MultiviewModelAvailable reports whether model names an enabled Offering of
// one of the capabilities that takes several views of one object (TakesSeveralViews)
// and, when available is given, whose runtime configuration is present (#373).
// model is any form the generation tools match: the model ID, the Offering ID
// or the provider/model pair. The chat service uses it to refuse a structured
// multiview request for a model that cannot run before the turn starts.
func MultiviewModelAvailable(offerings []generation.Offering, capabilities []generation.Capability,
	available func(generation.OfferingID) bool, model string) bool {
	for _, offering := range offerings {
		if !offering.Enabled || !slices.Contains(capabilities, offering.Capability) ||
			!modelMatches(offering, model) || !TakesSeveralViews(offering) {
			continue
		}
		if available == nil || available(offering.ID) {
			return true
		}
	}
	return false
}
