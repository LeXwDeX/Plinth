package chat_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

func lookupPolicy(t *testing.T, limit int) (chat.HistoryLookupPolicy, catalog.ChatSchema) {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	schema := product.Application().Chat
	if schema.Context.HistoryImageLookupLimit != 10 {
		t.Fatalf("the shipped lookup bound is %d; #279 asks for 10", schema.Context.HistoryImageLookupLimit)
	}
	schema.Context.HistoryImageLookupLimit = limit
	return chat.NewHistoryLookupPolicy(schema), schema
}

// longHistory has count pictures, one per message, alternating a user upload
// and a generated picture of a refine call; the turn's own message comes last.
func longHistory(schema catalog.ChatSchema, count int) []chat.Message {
	var history []chat.Message
	for number := 1; number <= count; number++ {
		handle := fmt.Sprintf("@img%d", number)
		if number%2 == 1 {
			history = append(history, chat.Message{Role: schema.Roles.User, Content: fmt.Sprintf("upload %d", number),
				ImageURLs: []string{fmt.Sprintf("/api/v1/input-assets/u%d", number)},
				Attachments: []chat.Attachment{{Handle: handle, Kind: chat.AttachmentKindImage, Role: chat.AttachmentRoleUpload,
					AssetID: fmt.Sprintf("u%d", number), ImageSlot: 1}}})
			continue
		}
		task := fmt.Sprintf("task-%d", number)
		history = append(history, chat.Message{Role: schema.Roles.Assistant, Content: "done",
			ImageURLs: []string{fmt.Sprintf("/api/v1/tasks/%s/assets/g", task)},
			ToolCalls: []chat.ToolCall{{Name: "refine_image", TaskID: task, Arguments: []byte(fmt.Sprintf(`{"prompt":"picture %d"}`, number))}},
			Attachments: []chat.Attachment{{Handle: handle, Kind: chat.AttachmentKindImage, Role: chat.AttachmentRoleGenerated,
				TaskID: task, AssetID: "g", ImageSlot: 1}}})
	}
	return append(history, chat.Message{Role: schema.Roles.User, Content: "use the pose of one and the outfit of another"})
}

func handlesOf(facts []chat.HistoryImageFact) []string {
	var handles []string
	for _, fact := range facts {
		handles = append(handles, fact.Handle)
	}
	return handles
}

// #279: the lookup lists this session's pictures newest first with their text
// clues, never more than the Schema bound in one reply, and says so.
func TestHistoryLookupListsTheNewestPicturesWithinTheBound(t *testing.T) {
	policy, schema := lookupPolicy(t, 10)
	result, err := policy.Lookup(longHistory(schema, 12), nil, nil, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"@img12", "@img11", "@img10", "@img9", "@img8", "@img7", "@img6", "@img5", "@img4", "@img3"}
	if got := handlesOf(result.Images); !slices.Equal(got, want) {
		t.Fatalf("listed = %v; want %v", got, want)
	}
	if !result.OlderNotListed || result.Used != 10 || result.Remaining != 0 ||
		result.Note != strings.ReplaceAll(schema.Context.HistoryImageBudgetTemplate, "{limit}", "10") {
		t.Fatalf("budget facts = %+v", result)
	}
	generated, upload := result.Images[0], result.Images[1]
	if generated.Source != chat.AttachmentRoleGenerated || generated.Tool != "refine_image" ||
		generated.Prompt != "picture 12" || generated.UserText != "upload 11" || generated.Recency != 1 {
		t.Fatalf("generated picture facts = %+v", generated)
	}
	if upload.Source != chat.AttachmentRoleUpload || upload.UserText != "upload 11" || upload.Prompt != "" || upload.Recency != 2 {
		t.Fatalf("upload facts = %+v", upload)
	}
	if len(result.Pictures) != 0 {
		t.Fatalf("a plain listing must not show pictures: %+v", result.Pictures)
	}
	// The Agent reads handles and text, never a URL or a platform ID.
	encoded, _ := json.Marshal(result)
	for _, leak := range []string{"/api/", "task-12", "input-assets"} {
		if strings.Contains(string(encoded), leak) {
			t.Fatalf("lookup result leaks %q: %s", leak, encoded)
		}
	}
}

// One platform object is one candidate: a generated picture the user attached
// again keeps its newest handle and is marked as part of this turn; the
// current selection is marked; a 3D model is not an image candidate; an
// artboard snapshot that only the selection carries leads the list.
func TestHistoryLookupDeduplicatesAndMarksTheTurnsOwnPictures(t *testing.T) {
	policy, schema := lookupPolicy(t, 10)
	history := []chat.Message{
		{Role: schema.Roles.User, Content: "draw a gunman"},
		{Role: schema.Roles.Assistant, ImageURLs: []string{"/api/v1/tasks/t1/assets/a", "/api/v1/tasks/t3d/assets/thumb"},
			ToolCalls: []chat.ToolCall{{Name: "refine_image", TaskID: "t1", Arguments: []byte(`{"prompt":"white-clad gunman"}`)}},
			Attachments: []chat.Attachment{
				{Handle: "@img1", Kind: chat.AttachmentKindImage, Role: chat.AttachmentRoleGenerated, TaskID: "t1", AssetID: "a", ImageSlot: 1},
				{Handle: "@model1", Kind: chat.AttachmentKindModel, Role: chat.AttachmentRoleGenerated, TaskID: "t3d", ImageSlot: 2}}},
		{Role: schema.Roles.User, Content: "an outfit", ImageURLs: []string{"/api/v1/input-assets/o"},
			Attachments: []chat.Attachment{{Handle: "@img2", Kind: chat.AttachmentKindImage, Role: chat.AttachmentRoleUpload, AssetID: "o", ImageSlot: 1}}},
		{Role: schema.Roles.User, Content: "keep this pose", ImageURLs: []string{"/api/v1/tasks/t1/assets/a"},
			Attachments: []chat.Attachment{{Handle: "@img3", Kind: chat.AttachmentKindImage, Role: chat.AttachmentRoleGenerated, TaskID: "t1", AssetID: "a", ImageSlot: 1}}},
	}
	selection := []chat.Attachment{
		{Handle: "@img4", Kind: chat.AttachmentKindImage, Role: chat.AttachmentRoleCanvas, AssetID: "board", URL: "/api/v1/input-assets/board"},
		{Handle: "@img5", Kind: chat.AttachmentKindImage, Role: chat.AttachmentRoleUpload, AssetID: "o", URL: "/api/v1/input-assets/o"},
	}
	result, err := policy.Lookup(history, selection, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := handlesOf(result.Images); !slices.Equal(got, []string{"@img4", "@img3", "@img2"}) {
		t.Fatalf("candidates = %v", got)
	}
	board, again, outfit := result.Images[0], result.Images[1], result.Images[2]
	if board.Source != chat.AttachmentRoleCanvas || !board.Selected || !board.CurrentTurn {
		t.Fatalf("artboard = %+v", board)
	}
	if !again.CurrentTurn || again.Selected || again.UserText != "keep this pose" || again.Prompt != "white-clad gunman" {
		t.Fatalf("re-attached generated picture = %+v", again)
	}
	if outfit.CurrentTurn || !outfit.Selected {
		t.Fatalf("selected upload = %+v", outfit)
	}
	if result.Used != 3 || result.Remaining != 7 || result.OlderNotListed || result.Note != "" {
		t.Fatalf("budget = %+v", result)
	}
}

// The bound counts distinct pictures across the reply's lookup calls: the
// earlier calls are replayed, a picture already surfaced is free to list or
// look at again, and a new one beyond the bound is reported, not shown.
func TestHistoryLookupBudgetCountsEachPictureOncePerReply(t *testing.T) {
	policy, schema := lookupPolicy(t, 3)
	history := longHistory(schema, 6)
	earlier := [][]byte{[]byte(`{"handles":["@img1","@img2"]}`), []byte(`{"handles":["@img404"]}`)}

	again, err := policy.Lookup(history, nil, earlier, []byte(`{"handles":["@img2","@img1"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if again.Used != 2 || again.Remaining != 1 || !slices.Equal(handlesOf(again.Images), []string{"@img2", "@img1"}) {
		t.Fatalf("looking again = %+v", again)
	}
	if len(again.Pictures) != 2 || again.Pictures[0].Handle != "@img2" || again.Pictures[0].URL != "/api/v1/tasks/task-2/assets/g" ||
		!again.Images[0].Shown {
		t.Fatalf("pictures = %+v", again.Pictures)
	}

	listing, err := policy.Lookup(history, nil, earlier, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	// The newest picture takes the last place; the two already surfaced are
	// listed for free; the rest is held back.
	if got := handlesOf(listing.Images); !slices.Equal(got, []string{"@img6", "@img2", "@img1"}) ||
		!listing.OlderNotListed || listing.Remaining != 0 || listing.Note == "" {
		t.Fatalf("listing = %v %+v", got, listing)
	}

	full := append(slices.Clone(earlier), []byte(`{}`))
	over, err := policy.Lookup(history, nil, full, []byte(`{"handles":["@img5","@img6"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(over.OverBudget, []string{"@img5"}) || !slices.Equal(handlesOf(over.Images), []string{"@img6"}) ||
		len(over.Pictures) != 1 || over.Used != 3 {
		t.Fatalf("over budget = %+v", over)
	}
}

// Only the session's own pictures can be named: the history and selection are
// the ones the server loaded for this owner and session.
func TestHistoryLookupRefusesHandlesItCannotReach(t *testing.T) {
	policy, schema := lookupPolicy(t, 10)
	history := longHistory(schema, 2)
	history[1].Attachments = append(history[1].Attachments, chat.Attachment{Handle: "@model1", Kind: chat.AttachmentKindModel, TaskID: "t3d"})
	var unknown *chat.UnknownHandleError
	if _, err := policy.Lookup(history, nil, nil, []byte(`{"handles":["@img9"]}`)); !errors.As(err, &unknown) || unknown.Reason != "unknown" {
		t.Fatalf("another session's handle: %v", err)
	}
	if _, err := policy.Lookup(history, nil, nil, []byte(`{"handles":["@model1"]}`)); !errors.As(err, &unknown) || unknown.Reason != "kind" {
		t.Fatalf("a model handle: %v", err)
	}
	if _, err := policy.Lookup(history, nil, nil, []byte(`{"handles":{"x":1}}`)); !errors.Is(err, chat.ErrHistoryLookupArguments) {
		t.Fatalf("malformed arguments: %v", err)
	}
	if !policy.Enabled() {
		t.Fatal("a policy with a bound must be enabled")
	}
	schema.Context.HistoryImageLookupLimit = 0
	if chat.NewHistoryLookupPolicy(schema).Enabled() {
		t.Fatal("a Schema without a bound must leave the lookup disabled")
	}
}
