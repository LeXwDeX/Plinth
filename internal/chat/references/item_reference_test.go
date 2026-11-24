package references_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/references"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/project"
)

// itemReferenceResolver builds a resolver over one proposal-session Task with
// two pictures and one other owner's Task, so an item turn can be checked
// against the exact pairs it stored.
func itemReferenceResolver(t *testing.T) (*references.Resolver, *storedInputs) {
	t.Helper()
	picture := []byte("\x89PNG\r\n\x1a\nitem-reference")
	inputs := &storedInputs{
		assets: map[generation.AssetID]generation.InputAsset{
			"owned-a": {ID: "owned-a", OwnerID: "owner-a"},
		},
		bodies: map[generation.AssetID][]byte{"owned-a": picture},
	}
	tasks := &fakeTasks{
		tasks: map[generation.TaskID]generation.Task{
			"proposal-task": {
				ID: "proposal-task", OwnerID: "owner-a", SessionID: "session-proposal",
				Assets: []generation.Asset{{ID: "source-a"}, {ID: "source-b"}},
			},
			"other-proposal-task": {
				ID: "other-proposal-task", OwnerID: "owner-a", SessionID: "session-proposal",
				Assets: []generation.Asset{{ID: "source-a"}},
			},
			"foreign-task": {
				ID: "foreign-task", OwnerID: "owner-b", SessionID: "session-proposal",
				Assets: []generation.Asset{{ID: "source-a"}},
			},
		},
		contents: map[string]generation.AssetContent{
			"proposal-task/source-a": {
				Body: io.NopCloser(bytes.NewReader(picture)), MediaType: "image/png", Size: int64(len(picture)),
			},
		},
	}
	return limitedResolver(t, inputs, resolverLimits{upload: 1024, vision: 1024, tasks: tasks}), inputs
}

// An item turn reads exactly the Task pictures its item stored: the proposal
// session's picture is accepted in the item sub-session, while another asset
// of the same Task, another Task, another owner and the same reference without
// the item context are all refused.
func TestItemContextAcceptsOnlyTheExactStoredTaskPicture(t *testing.T) {
	ctx := context.Background()
	resolver, inputs := itemReferenceResolver(t)
	itemScope := references.ReferenceScope{
		Owner: "owner-a", SessionID: "session-item", ProjectID: "prj_1",
		AssetListItemID:     "aitem_1",
		AssetListReferences: []project.Reference{{TaskID: "proposal-task", AssetID: "source-a"}},
	}
	reference := "/configured/tasks/proposal-task/assets/source-a"
	ids, err := resolver.Resolve(ctx, itemScope, []string{reference})
	if err != nil || len(ids) != 1 || inputs.uploads != 1 {
		t.Fatalf("exact item reference ids=%v err=%v uploads=%d", ids, err, inputs.uploads)
	}

	for name, item := range map[string]struct {
		scope     references.ReferenceScope
		reference string
	}{
		"same task another asset": {
			scope:     itemScope,
			reference: "/configured/tasks/proposal-task/assets/source-b",
		},
		"another task": {
			scope:     itemScope,
			reference: "/configured/tasks/other-proposal-task/assets/source-a",
		},
		"another owner": {
			scope:     itemScope,
			reference: "/configured/tasks/foreign-task/assets/source-a",
		},
		"the same reference without the item": {
			scope: references.ReferenceScope{
				Owner: "owner-a", SessionID: "session-item", ProjectID: "prj_1",
			},
			reference: reference,
		},
		"the item names another picture": {
			scope: references.ReferenceScope{
				Owner: "owner-a", SessionID: "session-item", ProjectID: "prj_1",
				AssetListItemID:     "aitem_1",
				AssetListReferences: []project.Reference{{TaskID: "proposal-task", AssetID: "source-b"}},
			},
			reference: reference,
		},
		"the item id without references": {
			scope: references.ReferenceScope{
				Owner: "owner-a", SessionID: "session-item", ProjectID: "prj_1",
				AssetListItemID: "aitem_1",
			},
			reference: reference,
		},
	} {
		if _, err := resolver.Resolve(ctx, item.scope, []string{item.reference}); !errors.Is(err, references.ErrUnsupportedReference) {
			t.Errorf("%s err = %v; want ErrUnsupportedReference", name, err)
		}
	}

	// An input asset is owner-level on every path, so the item turn reads the
	// one its references name like the proposal session did.
	ids, err = resolver.Resolve(ctx, itemScope, []string{"/configured/assets/owned-a"})
	if err != nil || len(ids) != 1 || ids[0] != "owned-a" {
		t.Fatalf("input asset ids=%v err=%v", ids, err)
	}
}

// The server path that starts an item turn turns the stored references into
// attachments with platform URLs, and the ordinary handle lookup resolves
// them back to a reference Resolve accepts.
func TestItemReferencesBecomeHandledAttachmentsThatResolveBack(t *testing.T) {
	ctx := context.Background()
	resolver, inputs := itemReferenceResolver(t)
	stored := []project.Reference{
		{TaskID: "proposal-task", AssetID: "source-a"},
		{InputAssetID: "owned-a"},
	}
	attachments, err := resolver.ItemReferenceAttachments(ctx, "owner-a", stored)
	if err != nil || len(attachments) != 2 {
		t.Fatalf("attachments=%+v err=%v", attachments, err)
	}
	taskPicture, assetPicture := attachments[0], attachments[1]
	if taskPicture.Kind != chat.AttachmentKindImage || taskPicture.Role != chat.AttachmentRoleGenerated ||
		taskPicture.TaskID != "proposal-task" || taskPicture.AssetID != "source-a" ||
		taskPicture.URL != "/configured/tasks/proposal-task/assets/source-a" {
		t.Fatalf("task picture = %+v", taskPicture)
	}
	if assetPicture.Kind != chat.AttachmentKindImage || assetPicture.Role != chat.AttachmentRoleUpload ||
		assetPicture.TaskID != "" || assetPicture.AssetID != "owned-a" ||
		assetPicture.URL != "/configured/assets/owned-a" {
		t.Fatalf("input asset = %+v", assetPicture)
	}

	policy := chat.NewHandlePolicy(catalog.ChatContextSchema{
		HandlePrefixes: map[string]string{chat.AttachmentKindImage: "@img"},
	})
	assigned := policy.Assign(nil, attachments)
	if assigned[0].Handle != "@img1" || assigned[1].Handle != "@img2" {
		t.Fatalf("handles = %q %q", assigned[0].Handle, assigned[1].Handle)
	}
	resolved, err := policy.ResolveHandles(nil, assigned, []string{"@img1"}, chat.AttachmentKindImage)
	if err != nil || len(resolved) != 1 {
		t.Fatalf("resolved=%+v err=%v", resolved, err)
	}
	uploads := inputs.uploads
	ids, err := resolver.Resolve(ctx, references.ReferenceScope{
		Owner: "owner-a", SessionID: "session-item", ProjectID: "prj_1",
		AssetListItemID: "aitem_1", AssetListReferences: stored,
	}, []string{resolved[0].URL})
	if err != nil || len(ids) != 1 || inputs.uploads != uploads+1 {
		t.Fatalf("handle URL ids=%v err=%v uploads=%d", ids, err, inputs.uploads-uploads)
	}
	resolved, err = policy.ResolveHandles(nil, assigned, []string{"@img2"}, chat.AttachmentKindImage)
	if err != nil || len(resolved) != 1 {
		t.Fatalf("input handle resolved=%+v err=%v", resolved, err)
	}
	ids, err = resolver.Resolve(ctx, references.ReferenceScope{
		Owner: "owner-a", SessionID: "session-item", ProjectID: "prj_1",
		AssetListItemID: "aitem_1", AssetListReferences: stored,
	}, []string{resolved[0].URL})
	if err != nil || len(ids) != 1 || ids[0] != "owned-a" {
		t.Fatalf("input handle URL ids=%v err=%v", ids, err)
	}
}

// itemCropProvenance answers the stored source of the test crops.
type itemCropProvenance map[generation.AssetID]generation.AssetID

func (provenance itemCropProvenance) CropSource(_ context.Context,
	_ generation.OwnerID, cropAssetID generation.AssetID,
) (generation.AssetID, bool, error) {
	source, found := provenance[cropAssetID]
	return source, found, nil
}

// #331: an item's stored crop is attached with the crop role, and its source
// scene follows once per item set with the source_scene role, so the
// sub-session does not read either picture as an ordinary upload. A source
// that cannot be read any more is skipped and the crop still resolves.
func TestItemCropReferencesBecomeCropAndSourceSceneAttachments(t *testing.T) {
	ctx := context.Background()
	picture := []byte("\x89PNG\r\n\x1a\nitem-crop")
	inputs := &storedInputs{
		assets: map[generation.AssetID]generation.InputAsset{
			"crop-a":    {ID: "crop-a", OwnerID: "owner-a", AssetType: "list_crop"},
			"crop-b":    {ID: "crop-b", OwnerID: "owner-a", AssetType: "list_crop"},
			"scene-a":   {ID: "scene-a", OwnerID: "owner-a"},
			"scene-old": {ID: "scene-old", OwnerID: "owner-a"},
		},
		bodies: map[generation.AssetID][]byte{
			"crop-a": picture, "crop-b": picture, "scene-a": picture, "scene-old": picture,
		},
	}
	resolver := limitedResolver(t, inputs, resolverLimits{
		upload: 1024, vision: 1024,
		crops: itemCropProvenance{
			"crop-a": "scene-a", "crop-b": "scene-a", "crop-old": "scene-gone",
		},
	})
	attachments, err := resolver.ItemReferenceAttachments(ctx, "owner-a", []project.Reference{
		{InputAssetID: "crop-a"},
		{InputAssetID: "crop-b"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(attachments) != 3 {
		t.Fatalf("attachments = %+v; want two crops and one source scene", attachments)
	}
	for index, want := range []struct{ role, assetID string }{
		{chat.AttachmentRoleCrop, "crop-a"},
		{chat.AttachmentRoleCrop, "crop-b"},
		{chat.AttachmentRoleSourceScene, "scene-a"},
	} {
		attachment := attachments[index]
		if attachment.Kind != chat.AttachmentKindImage || attachment.Role != want.role ||
			attachment.AssetID != want.assetID || attachment.TaskID != "" ||
			attachment.URL != "/configured/assets/"+want.assetID {
			t.Fatalf("attachment %d = %+v; want role %s of %s", index, attachment, want.role, want.assetID)
		}
	}

	// A crop whose source has been deleted still attaches as a crop; the
	// missing source picture is left out instead of failing the turn.
	orphaned, err := resolver.ItemReferenceAttachments(ctx, "owner-a", []project.Reference{
		{InputAssetID: "crop-old"},
	})
	if err == nil {
		t.Fatal("a crop asset without a stored row must be refused")
	}
	inputs.assets["crop-old"] = generation.InputAsset{ID: "crop-old", OwnerID: "owner-a", AssetType: "list_crop"}
	inputs.bodies["crop-old"] = picture
	orphaned, err = resolver.ItemReferenceAttachments(ctx, "owner-a", []project.Reference{
		{InputAssetID: "crop-old"},
	})
	if err != nil || len(orphaned) != 1 || orphaned[0].Role != chat.AttachmentRoleCrop {
		t.Fatalf("orphaned source attachments = %+v err=%v", orphaned, err)
	}
}

// The item path never becomes a client claim: VerifyAttachment still applies
// the session rule of an ordinary turn, including for captures.
func TestItemContextDoesNotRelaxClientAttachmentsOrCaptures(t *testing.T) {
	ctx := context.Background()
	resolver := attachmentFixture(t)
	if _, err := resolver.VerifyAttachment(ctx, "owner-a", "session-item",
		chat.AttachmentRef{Kind: chat.AttachmentKindImage, TaskID: "other-session", AssetID: "other-asset"}); !errors.Is(err, chat.ErrAttachmentRejected) {
		t.Fatalf("cross-session task attachment err = %v; want ErrAttachmentRejected", err)
	}
	if _, err := resolver.VerifyAttachment(ctx, "owner-a", "session-item",
		chat.AttachmentRef{Kind: chat.AttachmentKindImage, CaptureID: "capture-s2"}); !errors.Is(err, chat.ErrAttachmentRejected) {
		t.Fatalf("cross-session capture attachment err = %v; want ErrAttachmentRejected", err)
	}
}

// The server path accepts exactly the frozen references: an invalid shape or
// a reference of another owner is refused, never silently attached.
func TestItemReferenceAttachmentsRefuseInvalidOrForeignReferences(t *testing.T) {
	ctx := context.Background()
	resolver, _ := itemReferenceResolver(t)
	if _, err := resolver.ItemReferenceAttachments(ctx, "owner-a",
		[]project.Reference{{AssetID: "source-a"}}); !errors.Is(err, chat.ErrAttachmentRejected) {
		t.Fatalf("invalid reference err = %v; want ErrAttachmentRejected", err)
	}
	if _, err := resolver.ItemReferenceAttachments(ctx, "owner-b",
		[]project.Reference{{TaskID: "proposal-task", AssetID: "source-a"}}); !errors.Is(err, chat.ErrAttachmentRejected) {
		t.Fatalf("foreign task err = %v; want ErrAttachmentRejected", err)
	}
	if _, err := resolver.ItemReferenceAttachments(ctx, "owner-b",
		[]project.Reference{{InputAssetID: "owned-a"}}); err == nil {
		t.Fatal("another owner's input asset must be refused")
	}
}

// #552: a crop_image_regions crop shown as a picture of a chat reply keeps the
// crop role its stored provenance gives it, so the session never labels it as
// the person's own upload; a plain input asset is still an upload.
func TestVerifyAttachmentNamesAStoredCropByItsProvenance(t *testing.T) {
	ctx := context.Background()
	picture := []byte("\x89PNG\r\n\x1a\nchat-crop")
	inputs := &storedInputs{
		assets: map[generation.AssetID]generation.InputAsset{
			"crop-a":   {ID: "crop-a", OwnerID: "owner-a", AssetType: "list_crop"},
			"upload-a": {ID: "upload-a", OwnerID: "owner-a"},
		},
		bodies: map[generation.AssetID][]byte{"crop-a": picture, "upload-a": picture},
	}
	resolver := limitedResolver(t, inputs, resolverLimits{
		upload: 1024, vision: 1024,
		crops: itemCropProvenance{"crop-a": "sheet-a"},
	})
	for assetID, role := range map[string]string{
		"crop-a":   chat.AttachmentRoleCrop,
		"upload-a": chat.AttachmentRoleUpload,
	} {
		attachment, err := resolver.VerifyAttachment(ctx, "owner-a", "session-a", chat.AttachmentRef{
			Kind: chat.AttachmentKindImage, URL: "/configured/assets/" + assetID, ImageSlot: 2,
		})
		if err != nil || attachment.Role != role || attachment.AssetID != assetID ||
			attachment.URL != "/configured/assets/"+assetID || attachment.ImageSlot != 2 {
			t.Fatalf("%s attachment = %+v err=%v; want role %s", assetID, attachment, err, role)
		}
	}
	// Another owner's crop is refused like any foreign input asset.
	if _, err := resolver.VerifyAttachment(ctx, "owner-b", "session-a", chat.AttachmentRef{
		Kind: chat.AttachmentKindImage, URL: "/configured/assets/crop-a",
	}); err == nil {
		t.Fatal("another owner's crop was verified")
	}
}
