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
