package references

import (
	"context"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

var _ chat.AttachmentVerifier = (*Resolver)(nil)

// VerifyAttachment turns a client claim about an image or a 3D model into the
// canonical platform facts. Every ID is checked against the owner, and every
// Task or capture against the chat session, before it is returned. A claim
// that fails any check is rejected without saying which ID was wrong, and a
// claim never carries an upstream URL or ID.
func (resolver *Resolver) VerifyAttachment(
	ctx context.Context,
	ownerID, sessionID string,
	ref chat.AttachmentRef,
) (chat.Attachment, error) {
	if ownerID == "" || sessionID == "" {
		return chat.Attachment{}, chat.ErrAttachmentRejected
	}
	owner := generation.OwnerID(ownerID)
	var (
		attachment chat.Attachment
		err        error
	)
	switch ref.Kind {
	case chat.AttachmentKindImage:
		attachment, err = resolver.verifyImage(ctx, owner, sessionID, ref)
	case chat.AttachmentKindModel:
		attachment, err = resolver.verifyModel(ctx, owner, sessionID, ref)
	default:
		err = chat.ErrAttachmentRejected
	}
	if err != nil {
		return chat.Attachment{}, chat.ErrAttachmentRejected
	}
	attachment.ImageSlot = ref.ImageSlot
	return attachment, nil
}

func (resolver *Resolver) verifyImage(
	ctx context.Context,
	owner generation.OwnerID,
	sessionID string,
	ref chat.AttachmentRef,
) (chat.Attachment, error) {
	if ref.CaptureID != "" {
		return resolver.verifyCapture(ctx, owner, sessionID, ref)
	}
	value := strings.TrimSpace(ref.URL)
	if strings.HasPrefix(value, "data:") {
		// A pasted or uploaded picture has no platform identity yet; the
		// reference resolver uploads it as an owned input asset when used.
		if ref.AssetID != "" || ref.TaskID != "" {
			return chat.Attachment{}, chat.ErrAttachmentRejected
		}
		return chat.Attachment{Kind: chat.AttachmentKindImage, Role: chat.AttachmentRoleUpload}, nil
	}
	if value != "" {
		referencePath, err := resolver.localPath(value)
		if err != nil {
			return chat.Attachment{}, err
		}
		for _, route := range resolver.inputAssetRoutes {
			if values, matched := matchRoute(route, referencePath); matched && len(values) == 1 {
				return resolver.verifyInputAsset(ctx, owner, ref, generation.AssetID(values[0]))
			}
		}
		if values, matched := matchRoute(resolver.taskAssetRoute, referencePath); matched && len(values) == 2 {
			return resolver.verifyTaskAsset(ctx, owner, sessionID, ref,
				generation.TaskID(values[0]), generation.AssetID(values[1]))
		}
		if values, matched := matchRoute(resolver.generatedImageRoute, referencePath); matched && len(values) == 1 {
			return resolver.verifyLegacyGenerated(ctx, owner, sessionID, ref, values[0])
		}
		return chat.Attachment{}, chat.ErrAttachmentRejected
	}
	switch {
	case ref.TaskID != "" && ref.AssetID != "":
		return resolver.verifyTaskAsset(ctx, owner, sessionID, ref,
			generation.TaskID(ref.TaskID), generation.AssetID(ref.AssetID))
	case ref.AssetID != "" && ref.TaskID == "":
		return resolver.verifyInputAsset(ctx, owner, ref, generation.AssetID(ref.AssetID))
	}
	return chat.Attachment{}, chat.ErrAttachmentRejected
}

func (resolver *Resolver) verifyInputAsset(
	ctx context.Context,
	owner generation.OwnerID,
	ref chat.AttachmentRef,
	id generation.AssetID,
) (chat.Attachment, error) {
	if ref.TaskID != "" || ref.CaptureID != "" || (ref.AssetID != "" && ref.AssetID != string(id)) {
		return chat.Attachment{}, chat.ErrAttachmentRejected
	}
	_, body, err := resolver.inputs.OpenOwned(ctx, owner, id)
	if err != nil {
		return chat.Attachment{}, err
	}
	if err := body.Close(); err != nil {
		return chat.Attachment{}, err
	}
	return chat.Attachment{
		Kind:    chat.AttachmentKindImage,
		Role:    chat.AttachmentRoleUpload,
		AssetID: string(id),
		URL:     resolver.inputAssetURL(id),
	}, nil
}

// ownedSessionTask loads a Task of the owner that belongs to this session. A
// picture may also come from a Task made outside any chat (allowUnscoped); a
// model is always a Task of this very session, like the processing tools
// require. A Task of another session is never accepted.
func (resolver *Resolver) ownedSessionTask(
	ctx context.Context,
	owner generation.OwnerID,
	sessionID string,
	id generation.TaskID,
	allowUnscoped bool,
) (generation.Task, error) {
	task, err := resolver.tasks.Get(ctx, owner, id)
	if err != nil {
		return generation.Task{}, err
	}
	if task.ID != id || task.OwnerID != owner ||
		(task.SessionID != sessionID && !(allowUnscoped && task.SessionID == "")) {
		return generation.Task{}, chat.ErrAttachmentRejected
	}
	return task, nil
}

func (resolver *Resolver) verifyTaskAsset(
	ctx context.Context,
	owner generation.OwnerID,
	sessionID string,
	ref chat.AttachmentRef,
	taskID generation.TaskID,
	assetID generation.AssetID,
) (chat.Attachment, error) {
	if ref.CaptureID != "" ||
		(ref.TaskID != "" && ref.TaskID != string(taskID)) ||
		(ref.AssetID != "" && ref.AssetID != string(assetID)) {
		return chat.Attachment{}, chat.ErrAttachmentRejected
	}
	task, err := resolver.ownedSessionTask(ctx, owner, sessionID, taskID, true)
	if err != nil {
		return chat.Attachment{}, err
	}
	for _, asset := range task.Assets {
		if asset.ID == assetID {
			return chat.Attachment{
				Kind:    chat.AttachmentKindImage,
				Role:    chat.AttachmentRoleGenerated,
				AssetID: string(assetID),
				TaskID:  string(taskID),
				URL:     resolver.taskAssetURL(taskID, assetID),
			}, nil
		}
	}
	return chat.Attachment{}, chat.ErrAttachmentRejected
}

func (resolver *Resolver) verifyLegacyGenerated(
	ctx context.Context,
	owner generation.OwnerID,
	sessionID string,
	ref chat.AttachmentRef,
	filename string,
) (chat.Attachment, error) {
	taskID, ordinal, ok := parseGeneratedFilename(resolver.legacyGeneratedImage, filename)
	if !ok || ref.CaptureID != "" || (ref.TaskID != "" && ref.TaskID != string(taskID)) {
		return chat.Attachment{}, chat.ErrAttachmentRejected
	}
	task, err := resolver.ownedSessionTask(ctx, owner, sessionID, taskID, true)
	if err != nil {
		return chat.Attachment{}, err
	}
	asset, found := generatedAsset(resolver.legacyGeneratedImage, task.Assets, ordinal)
	if ref.AssetID != "" && (!found || ref.AssetID != string(asset.ID)) {
		return chat.Attachment{}, chat.ErrAttachmentRejected
	}
	attachment := chat.Attachment{
		Kind:   chat.AttachmentKindImage,
		Role:   chat.AttachmentRoleGenerated,
		TaskID: string(taskID),
	}
	if found {
		attachment.AssetID = string(asset.ID)
		attachment.URL = resolver.taskAssetURL(taskID, asset.ID)
	}
	return attachment, nil
}

func (resolver *Resolver) verifyCapture(
	ctx context.Context,
	owner generation.OwnerID,
	sessionID string,
	ref chat.AttachmentRef,
) (chat.Attachment, error) {
	if resolver.captures == nil {
		return chat.Attachment{}, chat.ErrAttachmentRejected
	}
	record, err := resolver.captures.Get(ctx, string(owner), ref.CaptureID)
	if err != nil {
		return chat.Attachment{}, err
	}
	if record.ID != ref.CaptureID || record.OwnerID != string(owner) || record.SessionID != sessionID ||
		(ref.AssetID != "" && ref.AssetID != record.ScreenshotAssetID) ||
		(ref.TaskID != "" && ref.TaskID != record.SourceTaskID) {
		return chat.Attachment{}, chat.ErrAttachmentRejected
	}
	assetID := generation.AssetID(record.ScreenshotAssetID)
	if value := strings.TrimSpace(ref.URL); value != "" {
		// The picture the claim describes must be the capture's own screenshot,
		// like a Task picture must be a file of the claimed Task; otherwise the
		// capture lineage would be recorded on an unrelated picture.
		picture, err := resolver.verifyImage(ctx, owner, sessionID, chat.AttachmentRef{Kind: chat.AttachmentKindImage, URL: value})
		if err != nil || picture.AssetID != record.ScreenshotAssetID {
			return chat.Attachment{}, chat.ErrAttachmentRejected
		}
	}
	_, body, err := resolver.inputs.OpenOwned(ctx, owner, assetID)
	if err != nil {
		return chat.Attachment{}, err
	}
	if err := body.Close(); err != nil {
		return chat.Attachment{}, err
	}
	return chat.Attachment{
		Kind:      chat.AttachmentKindImage,
		Role:      chat.AttachmentRoleCapture,
		AssetID:   record.ScreenshotAssetID,
		TaskID:    record.SourceTaskID,
		CaptureID: record.ID,
		URL:       resolver.inputAssetURL(assetID),
	}, nil
}

func (resolver *Resolver) verifyModel(
	ctx context.Context,
	owner generation.OwnerID,
	sessionID string,
	ref chat.AttachmentRef,
) (chat.Attachment, error) {
	if resolver.modelAssetRole == "" || ref.CaptureID != "" {
		return chat.Attachment{}, chat.ErrAttachmentRejected
	}
	taskID := generation.TaskID(ref.TaskID)
	thumbnailURL := ""
	if value := strings.TrimSpace(ref.URL); value != "" {
		// The picture of a model must be a file of that same Task.
		picture, err := resolver.verifyImage(ctx, owner, sessionID, chat.AttachmentRef{Kind: chat.AttachmentKindImage, URL: value})
		if err != nil || picture.TaskID == "" || (taskID != "" && picture.TaskID != string(taskID)) {
			return chat.Attachment{}, chat.ErrAttachmentRejected
		}
		taskID = generation.TaskID(picture.TaskID)
		thumbnailURL = picture.URL
	}
	if taskID == "" {
		return chat.Attachment{}, chat.ErrAttachmentRejected
	}
	task, err := resolver.ownedSessionTask(ctx, owner, sessionID, taskID, false)
	if err != nil {
		return chat.Attachment{}, err
	}
	if task.State != generation.TaskSucceeded {
		return chat.Attachment{}, chat.ErrAttachmentRejected
	}
	modelAsset := ""
	for _, asset := range task.Assets {
		if asset.Role != resolver.modelAssetRole || asset.StorageKey == "" {
			continue
		}
		if ref.AssetID != "" && ref.AssetID != string(asset.ID) {
			continue
		}
		modelAsset = string(asset.ID)
		break
	}
	if modelAsset == "" {
		return chat.Attachment{}, chat.ErrAttachmentRejected
	}
	return chat.Attachment{
		Kind:    chat.AttachmentKindModel,
		Role:    chat.AttachmentRoleGenerated,
		AssetID: modelAsset,
		TaskID:  string(task.ID),
		URL:     thumbnailURL,
	}, nil
}

func (resolver *Resolver) inputAssetURL(id generation.AssetID) string {
	return renderRoute(resolver.inputAssetRoutes[0], string(id))
}

func (resolver *Resolver) taskAssetURL(task generation.TaskID, asset generation.AssetID) string {
	return renderRoute(resolver.taskAssetRoute, string(task), string(asset))
}

// renderRoute fills the {placeholders} of a route template in order.
func renderRoute(template string, values ...string) string {
	parts := strings.Split(template, "/")
	next := 0
	for index, part := range parts {
		if strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}") && next < len(values) {
			parts[index] = values[next]
			next++
		}
	}
	return strings.Join(parts, "/")
}
