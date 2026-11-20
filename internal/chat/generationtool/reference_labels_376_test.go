package generationtool_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #376: the Agent says which side each reference picture shows (view_labels, a
// map from an image handle to a mark), and the executor binds the marks to the
// final picture order. The rig runs the shipped create_model_3d_from_image tool
// against an Offering whose mark parameter is the shipped Rodin declaration
// (hidden, repeatable, the page's 11 marks), or against one with no such
// parameter.

const (
	labelModel      = "rodin-gen-2.5"
	plainModel      = "plain-model"
	labelsParameter = "reference_image_labels"
)

type labelRig struct {
	app      catalog.ApplicationSchema
	tool     catalog.ChatToolSchema
	gateway  *fakeGateway
	resolver *fakeReferences
	executor *generationtool.Executor
}

// newLabelRig builds the executor over one 3D Offering that takes at most limit
// pictures; withMarks gives it the shipped Rodin mark parameter.
func newLabelRig(t *testing.T, limit int, withMarks bool, views generationtool.ThreeViews) *labelRig {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	app := product.Application()
	rig := &labelRig{app: app, resolver: &fakeReferences{}}
	for _, candidate := range app.Chat.Tools {
		if candidate.Name == "create_model_3d_from_image" {
			rig.tool = candidate
		}
	}
	if rig.tool.ReferenceImageLabelsArgument != "view_labels" || rig.tool.ReferenceImageLabelUnknown != "?" {
		t.Fatalf("the shipped image-to-3d tool must declare reference marks: %+v", rig.tool.ReferenceImageLabelsArgument)
	}
	offering := generation.Offering{
		ID: "configured/plain", ModelID: plainModel, Capability: generation.ImageTo3D,
		Enabled: true, PollInterval: time.Second, Timeout: 5 * time.Second,
		Parameters: []generation.Parameter{
			{Name: "prompt", Type: "string", Semantic: generation.SemanticPrompt, Required: true},
			{Name: "reference_assets", Type: "asset-array", Semantic: generation.SemanticReferenceImage, MaxItems: limit},
		},
	}
	if withMarks {
		shipped, ok := product.Get("hyper3d/rodin-gen-2.5/image-to-3d")
		if !ok {
			t.Fatal("the Rodin image Offering is not declared")
		}
		var marks generation.Parameter
		for _, parameter := range shipped.Parameters {
			if parameter.Semantic == generation.SemanticReferenceImageLabels {
				marks = parameter
			}
		}
		if marks.Name != labelsParameter || len(marks.Options) != 11 {
			t.Fatalf("shipped mark parameter = %+v", marks)
		}
		marks.MaxItems = limit
		// Declaration order is wire order: the marks come before the pictures.
		offering.ID, offering.ModelID = "configured/rodin", labelModel
		offering.Parameters = []generation.Parameter{offering.Parameters[0], marks, offering.Parameters[1]}
	}
	offering.ExecutionMessages = app.Runtime.GenerationMessages
	rig.gateway = &fakeGateway{
		offerings: []generation.Offering{offering},
		tasks:     []generation.Task{{ID: "configured-task", State: generation.TaskSucceeded, Offering: offering, Assets: []generation.Asset{{ID: "model-thumb", Role: "thumbnail"}}}},
	}
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	rig.executor, err = generationtool.New(generationtool.Config{
		Gateway: rig.gateway, References: rig.resolver, Chat: app.Chat, TaskStates: app.TaskStates,
		InputAssetRoute: "/api/v1/input-assets/{assetID}",
		Clock:           func() time.Time { return now },
		Sleep:           func(_ context.Context, duration time.Duration) error { now = now.Add(duration); return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if views != nil {
		rig.executor.SetThreeViews(views)
	}
	return rig
}

// viewHistory is one user message with count uploaded pictures, handles @img1 to @imgN.
func viewHistory(schema catalog.ChatSchema, count int) []chat.Message {
	urls := make([]string, 0, count)
	attachments := make([]chat.Attachment, 0, count)
	for index := 1; index <= count; index++ {
		urls = append(urls, fmt.Sprintf("/api/v1/input-assets/view-%d", index))
		attachments = append(attachments, chat.Attachment{Handle: fmt.Sprintf("@img%d", index), Kind: chat.AttachmentKindImage,
			Role: chat.AttachmentRoleUpload, AssetID: fmt.Sprintf("view-%d", index), ImageSlot: index})
	}
	return []chat.Message{{Role: schema.Roles.User, Content: "make it 3D", ImageURLs: urls, Attachments: attachments}}
}

func viewURL(index int) string { return fmt.Sprintf("/api/v1/input-assets/view-%d", index) }

func (rig *labelRig) run(t *testing.T, options chat.TurnOptions, history []chat.Message, arguments string) chat.ToolResult {
	t.Helper()
	if history == nil {
		history = viewHistory(rig.app.Chat, 6)
	}
	if options.AIModel == "" {
		options.AIModel = rig.gateway.offerings[0].ModelID
	}
	result, err := rig.executor.Execute(context.Background(), chat.ToolCommand{
		OwnerID: "owner-a", SessionID: "session-a", Tool: rig.tool, History: history, Options: options,
		Call: agents.ToolCall{ID: "call-3d", Name: rig.tool.Name, Arguments: []byte(arguments)},
	}, func(chat.ToolObservation) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	return result
}

// frozen is the input the call submitted, as text and as a map.
func (rig *labelRig) frozen(t *testing.T) ([]byte, map[string]any) {
	t.Helper()
	if rig.gateway.submitted.OfferingID == "" {
		t.Fatal("nothing was submitted")
	}
	var input map[string]any
	if err := json.Unmarshal(rig.gateway.submitted.Input, &input); err != nil {
		t.Fatal(err)
	}
	return rig.gateway.submitted.Input, input
}

// marks are the frozen marks, nil when the input carries none.
func (rig *labelRig) marks(t *testing.T) []string {
	t.Helper()
	_, input := rig.frozen(t)
	raw, present := input[labelsParameter].([]any)
	if !present {
		return nil
	}
	marks := make([]string, 0, len(raw))
	for _, mark := range raw {
		marks = append(marks, mark.(string))
	}
	return marks
}

// sameFrozenInputAs fails unless both calls froze the same bytes: the marks of
// this one left nothing in its input, so its hash is the one without them.
func (rig *labelRig) sameFrozenInputAs(t *testing.T, plain *labelRig) {
	t.Helper()
	got, _ := rig.frozen(t)
	want, _ := plain.frozen(t)
	if !bytes.Equal(got, want) {
		t.Fatalf("frozen input differs from the call without marks:\n got %s\nwant %s", got, want)
	}
}

func (rig *labelRig) refused(t *testing.T, result chat.ToolResult, want string) {
	t.Helper()
	if !result.NoSubmission || result.Error != want {
		t.Fatalf("result = %+v; want a refusal %q", result, want)
	}
	if rig.gateway.submitted.OfferingID != "" || len(rig.resolver.urls) != 0 {
		t.Fatalf("a refused call reached the gateway: %+v %v", rig.gateway.submitted, rig.resolver.urls)
	}
}

func (rig *labelRig) pictureRefusal(handle string) string {
	return strings.ReplaceAll(rig.app.Chat.Context.ReferenceLabelPictureTemplate, "{handle}", handle)
}

func (rig *labelRig) valueRefusal(handle string) string {
	return strings.NewReplacer("{handle}", handle, "{marks}", "F, FL, FR, B, BL, BR, L, R, U, D, ?").
		Replace(rig.app.Chat.Context.ReferenceLabelValueTemplate)
}

// Each mark follows its picture: the Agent writes the handles and the marks in
// any order, and the frozen list is in the order the pictures are really sent.
func TestViewLabelsFollowTheFinalPictureOrder(t *testing.T) {
	rig := newLabelRig(t, 5, true, nil)
	result := rig.run(t, chat.TurnOptions{}, nil, `{"prompt":"a robot",
		"reference_handles":["@img3","@img1","@img2"],
		"view_labels":{"@img2":"L","@img3":"B","@img1":"F"}}`)
	if result.NoSubmission {
		t.Fatalf("result = %+v", result)
	}
	if got := rig.resolver.urls; !slices.Equal(got, []string{viewURL(3), viewURL(1), viewURL(2)}) {
		t.Fatalf("pictures = %v", got)
	}
	if got := rig.marks(t); !slices.Equal(got, []string{"B", "F", "L"}) {
		t.Fatalf("marks = %v, want [B F L] (the order of the pictures, not of the handles)", got)
	}

	// The same marks with the pictures named the other way round follow the new order.
	rig = newLabelRig(t, 5, true, nil)
	rig.run(t, chat.TurnOptions{}, nil, `{"prompt":"a robot","reference_handles":["@img1","@img2","@img3"],
		"view_labels":{"@img2":"L","@img3":"B","@img1":"F"}}`)
	if got := rig.marks(t); !slices.Equal(got, []string{"F", "L", "B"}) {
		t.Fatalf("marks = %v, want [F L B]", got)
	}
}

// With no reference_handles the pictures of the turn are the references, in the
// order the user attached them; their handles still carry the marks.
func TestViewLabelsMarkThePicturesOfTheTurnWhenNoHandlesAreGiven(t *testing.T) {
	rig := newLabelRig(t, 5, true, nil)
	result := rig.run(t, chat.TurnOptions{}, viewHistory(rig.app.Chat, 3), `{"prompt":"a robot","view_labels":{"@img3":"B","@img2":"L"}}`)
	if result.NoSubmission {
		t.Fatalf("result = %+v", result)
	}
	if got := rig.resolver.urls; !slices.Equal(got, []string{viewURL(1), viewURL(2), viewURL(3)}) {
		t.Fatalf("pictures = %v", got)
	}
	if got := rig.marks(t); !slices.Equal(got, []string{"?", "L", "B"}) {
		t.Fatalf("marks = %v", got)
	}
}

// A picture the Agent did not mark is sent as unknown, so the list always has
// one mark per picture.
func TestViewLabelsFillUnmarkedPicturesWithTheUnknownMark(t *testing.T) {
	rig := newLabelRig(t, 5, true, nil)
	rig.run(t, chat.TurnOptions{}, nil, `{"prompt":"a robot","reference_handles":["@img1","@img2","@img3","@img4"],
		"view_labels":{"@img1":"F","@img3":"B"}}`)
	if got := rig.marks(t); !slices.Equal(got, []string{"F", "?", "B", "?"}) {
		t.Fatalf("marks = %v", got)
	}
	// An explicit unknown mark is the same as none, beside a real one.
	rig = newLabelRig(t, 5, true, nil)
	rig.run(t, chat.TurnOptions{}, nil, `{"prompt":"a robot","reference_handles":["@img1","@img2"],
		"view_labels":{"@img1":"?","@img2":"R"}}`)
	if got := rig.marks(t); !slices.Equal(got, []string{"?", "R"}) {
		t.Fatalf("marks = %v", got)
	}
	// Marks repeat: two pictures can show the same side.
	rig = newLabelRig(t, 5, true, nil)
	rig.run(t, chat.TurnOptions{}, nil, `{"prompt":"a robot","reference_handles":["@img1","@img2","@img3"],
		"view_labels":{"@img1":"F","@img2":"F","@img3":"?"}}`)
	if got := rig.marks(t); !slices.Equal(got, []string{"F", "F", "?"}) {
		t.Fatalf("marks = %v", got)
	}
}

// An identity anchor's pictures lead the list (#250): the marks are bound after
// that order is fixed, so each still follows its own picture and the anchor
// picture, which the Agent cannot name by handle, is unknown.
func TestViewLabelsFollowTheirPicturesBehindAnAnchor(t *testing.T) {
	rig := newLabelRig(t, 5, true, nil)
	options := chat.TurnOptions{}
	options.VisualAnchors = []chat.VisualAnchor{anchor("hero", chat.VisualAnchorKindIdentity, "hero-a")}
	result := rig.run(t, options, nil, `{"prompt":"a robot","anchors":["hero"],
		"reference_handles":["@img2","@img1"],"view_labels":{"@img1":"F","@img2":"BL"}}`)
	if result.NoSubmission {
		t.Fatalf("result = %+v", result)
	}
	if got := rig.resolver.urls; !slices.Equal(got, []string{"/api/v1/input-assets/hero-a", viewURL(2), viewURL(1)}) {
		t.Fatalf("pictures = %v", got)
	}
	if got := rig.marks(t); !slices.Equal(got, []string{"?", "BL", "F"}) {
		t.Fatalf("marks = %v, want the anchor picture unknown and each mark on its own picture", got)
	}
}

// The model's limit cuts the tail before the marks are bound: the marks are cut
// with their pictures. The Agent writes the marks before it can know where the
// cut falls, so the mark of a picture that was cut is dropped with it (refusing
// would only cost a retry) and the pictures that are sent keep exactly their
// own marks, an unmarked one being unknown.
func TestViewLabelsOfPicturesCutByTheModelsLimitAreDropped(t *testing.T) {
	rig := newLabelRig(t, 3, true, nil)
	result := rig.run(t, chat.TurnOptions{}, nil, `{"prompt":"a robot","reference_handles":["@img1","@img2","@img3","@img4","@img5"],
		"view_labels":{"@img1":"F","@img2":"L","@img3":"B"}}`)
	if result.NoSubmission {
		t.Fatalf("result = %+v", result)
	}
	if got := rig.resolver.urls; !slices.Equal(got, []string{viewURL(1), viewURL(2), viewURL(3)}) {
		t.Fatalf("pictures = %v", got)
	}
	if got := rig.marks(t); !slices.Equal(got, []string{"F", "L", "B"}) {
		t.Fatalf("marks = %v", got)
	}

	// The fourth picture is cut and its mark goes with it; the sent pictures the
	// Agent did not mark are unknown.
	rig = newLabelRig(t, 3, true, nil)
	result = rig.run(t, chat.TurnOptions{}, nil, `{"prompt":"a robot","reference_handles":["@img1","@img2","@img3","@img4"],
		"view_labels":{"@img1":"F","@img4":"B"}}`)
	if result.NoSubmission {
		t.Fatalf("result = %+v", result)
	}
	if got := rig.resolver.urls; !slices.Equal(got, []string{viewURL(1), viewURL(2), viewURL(3)}) {
		t.Fatalf("pictures = %v", got)
	}
	if got := rig.marks(t); !slices.Equal(got, []string{"F", "?", "?"}) {
		t.Fatalf("marks = %v, want [F ? ?] (the cut picture's mark is not sent)", got)
	}

	// Five pictures fit the Rodin limit; the sixth is cut. When its mark was the
	// only one nothing is left to say, so no marks are frozen at all and the
	// input is what the same call freezes without view_labels.
	const references = `"prompt":"a robot","reference_handles":["@img1","@img2","@img3","@img4","@img5","@img6"]`
	rig = newLabelRig(t, 5, true, nil)
	result = rig.run(t, chat.TurnOptions{}, nil, `{`+references+`,"view_labels":{"@img6":"U"}}`)
	if result.NoSubmission {
		t.Fatalf("result = %+v", result)
	}
	if got := rig.resolver.urls; !slices.Equal(got, []string{viewURL(1), viewURL(2), viewURL(3), viewURL(4), viewURL(5)}) {
		t.Fatalf("pictures = %v", got)
	}
	if got := rig.marks(t); got != nil {
		t.Fatalf("marks = %v, want none", got)
	}
	plain := newLabelRig(t, 5, true, nil)
	plain.run(t, chat.TurnOptions{}, nil, `{`+references+`}`)
	rig.sameFrozenInputAs(t, plain)
}

// A view of a three-view group is replaced by the group's front view, and the
// views that do not take part are dropped (#329): the front picture keeps its
// own mark. The replaced view has no picture of its own to carry a mark, so its
// mark is dropped (not refused) and never moves onto the front picture.
func TestViewLabelsOfAThreeViewGroupDropTheReplacedViewsMark(t *testing.T) {
	front := generationtool.ModelSource{Available: true, TaskID: "task-front", AssetID: "asset-front"}
	views := &fakeThreeViews{source: front, members: map[string]bool{"task-front": true, "task-side": true}}
	frontURL, sideURL := "/api/v1/tasks/task-front/assets/asset-front", "/api/v1/tasks/task-side/assets/asset-side"
	history := func(schema catalog.ChatSchema) []chat.Message {
		return []chat.Message{{Role: schema.Roles.User, Content: "make it 3D",
			ImageURLs: []string{frontURL, sideURL, viewURL(3)},
			Attachments: []chat.Attachment{
				{Handle: "@img1", Kind: chat.AttachmentKindImage, Role: chat.AttachmentRoleGenerated, TaskID: "task-front", AssetID: "asset-front", ImageSlot: 1},
				{Handle: "@img2", Kind: chat.AttachmentKindImage, Role: chat.AttachmentRoleGenerated, TaskID: "task-side", AssetID: "asset-side", ImageSlot: 2},
				{Handle: "@img3", Kind: chat.AttachmentKindImage, Role: chat.AttachmentRoleUpload, AssetID: "view-3", ImageSlot: 3},
			}}}
	}
	rig := newLabelRig(t, 5, true, views)
	result := rig.run(t, chat.TurnOptions{}, history(rig.app.Chat), `{"prompt":"a robot","reference_handles":["@img2","@img1","@img3"],
		"view_labels":{"@img1":"F","@img3":"L"}}`)
	if result.NoSubmission {
		t.Fatalf("result = %+v", result)
	}
	if got := rig.resolver.urls; !slices.Equal(got, []string{frontURL, viewURL(3)}) {
		t.Fatalf("pictures = %v, want the front view once and the upload", got)
	}
	if got := rig.marks(t); !slices.Equal(got, []string{"F", "L"}) {
		t.Fatalf("marks = %v", got)
	}

	// The replaced view's mark is dropped, and it is not handed to the front
	// picture: that one stays unknown unless the Agent marked it itself.
	rig = newLabelRig(t, 5, true, views)
	result = rig.run(t, chat.TurnOptions{}, history(rig.app.Chat), `{"prompt":"a robot","reference_handles":["@img1","@img2","@img3"],
		"view_labels":{"@img2":"L","@img3":"R"}}`)
	if result.NoSubmission {
		t.Fatalf("result = %+v", result)
	}
	if got := rig.resolver.urls; !slices.Equal(got, []string{frontURL, viewURL(3)}) {
		t.Fatalf("pictures = %v", got)
	}
	if got := rig.marks(t); !slices.Equal(got, []string{"?", "R"}) {
		t.Fatalf("marks = %v, want [? R] (the replaced view's mark is not the front picture's)", got)
	}

	// A different mark on the replaced view is no conflict with the front
	// picture's own mark: it was never a mark of a picture that is sent.
	rig = newLabelRig(t, 5, true, views)
	result = rig.run(t, chat.TurnOptions{}, history(rig.app.Chat), `{"prompt":"a robot","reference_handles":["@img1","@img2","@img3"],
		"view_labels":{"@img1":"F","@img2":"L"}}`)
	if result.NoSubmission {
		t.Fatalf("result = %+v", result)
	}
	if got := rig.marks(t); !slices.Equal(got, []string{"F", "?"}) {
		t.Fatalf("marks = %v, want [F ?]", got)
	}

	// Only the replaced view was marked: nothing is left to say, so no marks are
	// frozen and the input is what the same call freezes without view_labels.
	rig = newLabelRig(t, 5, true, views)
	result = rig.run(t, chat.TurnOptions{}, history(rig.app.Chat), `{"prompt":"a robot","reference_handles":["@img1","@img2","@img3"],
		"view_labels":{"@img2":"L"}}`)
	if result.NoSubmission {
		t.Fatalf("result = %+v", result)
	}
	if got := rig.resolver.urls; !slices.Equal(got, []string{frontURL, viewURL(3)}) {
		t.Fatalf("pictures = %v", got)
	}
	if got := rig.marks(t); got != nil {
		t.Fatalf("marks = %v, want none", got)
	}
	plain := newLabelRig(t, 5, true, views)
	plain.run(t, chat.TurnOptions{}, history(plain.app.Chat), `{"prompt":"a robot","reference_handles":["@img1","@img2","@img3"]}`)
	rig.sameFrozenInputAs(t, plain)
}

// A key that names a picture of this session which this call does not send (it
// is not among the references, or the turn has no picture at all) is dropped:
// the picture is not sent, so its mark has nothing to describe, and the Agent
// could not tell which pictures would be sent when it wrote the marks. The
// pictures that are sent keep exactly their own marks, an unmarked one being
// unknown; when no sent picture is left with a definite mark nothing is frozen.
func TestViewLabelsOfSessionPicturesThatAreNotSentAreDropped(t *testing.T) {
	const references = `"prompt":"p","reference_handles":["@img1","@img3"]`
	for name, test := range map[string]struct {
		fresh    bool // the turn's own message has no picture: @img1 and @img2 are of an earlier one
		call     string
		labels   string
		pictures []string
		marks    []string // nil: none are frozen
	}{
		"beside a sent picture's mark": {false, references, `{"@img1":"F","@img2":"L"}`, []string{viewURL(1), viewURL(3)}, []string{"F", "?"}},
		"between two marked pictures":  {false, references, `{"@img2":"L","@img3":"B"}`, []string{viewURL(1), viewURL(3)}, []string{"?", "B"}},
		"several that are not sent":    {false, references, `{"@img2":"L","@img4":"R","@img5":"U","@img3":"B"}`, []string{viewURL(1), viewURL(3)}, []string{"?", "B"}},
		"only unsent pictures":         {false, references, `{"@img2":"L","@img4":"R"}`, []string{viewURL(1), viewURL(3)}, nil},
		"no picture at all":            {true, `"prompt":"p"`, `{"@img1":"L"}`, nil, nil},
	} {
		t.Run(name, func(t *testing.T) {
			history := func(rig *labelRig) []chat.Message {
				if test.fresh {
					return []chat.Message{viewHistory(rig.app.Chat, 2)[0], {Role: rig.app.Chat.Roles.User, Content: "a fresh request"}}
				}
				return nil
			}
			rig := newLabelRig(t, 5, true, nil)
			result := rig.run(t, chat.TurnOptions{}, history(rig), `{`+test.call+`,"view_labels":`+test.labels+`}`)
			if result.NoSubmission {
				t.Fatalf("result = %+v", result)
			}
			if got := rig.resolver.urls; !slices.Equal(got, test.pictures) {
				t.Fatalf("pictures = %v, want %v", got, test.pictures)
			}
			if got := rig.marks(t); !slices.Equal(got, test.marks) || (test.marks == nil) != (got == nil) {
				t.Fatalf("marks = %v, want %v", got, test.marks)
			}
			if test.marks == nil {
				plain := newLabelRig(t, 5, true, nil)
				plain.run(t, chat.TurnOptions{}, history(plain), `{`+test.call+`}`)
				rig.sameFrozenInputAs(t, plain)
			}
		})
	}
}

// A key that is no image of this session, the alias for the whole selection, or
// a picture that appears twice in what is sent cannot be placed, and is refused
// before submission with the Schema's wording, the way a reference purpose for
// an unsent picture is, instead of guessing where the mark belongs. A refusal is
// not masked by a dropped mark beside it. Two handles that give one picture
// different marks are refused too (TestViewLabelsOfTwoHandlesForOnePicture), and
// so is a mark the model does not take (the next test).
func TestViewLabelsThatCannotBePlacedAreRefusedBeforeSubmission(t *testing.T) {
	for name, test := range map[string]struct {
		arguments string
		handle    string
	}{
		"not in this session":    {`{"prompt":"p","reference_handles":["@img1"],"view_labels":{"@img42":"L"}}`, "@img42"},
		"not a handle":           {`{"prompt":"p","reference_handles":["@img1"],"view_labels":{"the left one":"L"}}`, "the left one"},
		"the whole selection":    {`{"prompt":"p","reference_handles":["@img1"],"view_labels":{"@selected":"L"}}`, "@selected"},
		"an empty key":           {`{"prompt":"p","reference_handles":["@img1"],"view_labels":{" ":"L"}}`, " "},
		"beside a dropped mark":  {`{"prompt":"p","reference_handles":["@img1"],"view_labels":{"@img2":"L","@img42":"B"}}`, "@img42"},
		"before a dropped mark":  {`{"prompt":"p","reference_handles":["@img1"],"view_labels":{"@img2":"L","@img1":"F","@selected":"B"}}`, "@selected"},
		"no picture to place it": {`{"prompt":"p","view_labels":{"@img42":"L"}}`, "@img42"},
	} {
		t.Run(name, func(t *testing.T) {
			rig := newLabelRig(t, 5, true, nil)
			history := []chat.Message{viewHistory(rig.app.Chat, 2)[0], {Role: rig.app.Chat.Roles.User, Content: "a fresh request"}}
			result := rig.run(t, chat.TurnOptions{}, history, test.arguments)
			rig.refused(t, result, rig.pictureRefusal(test.handle))
		})
	}
	t.Run("a model handle is not a picture", func(t *testing.T) {
		rig := newLabelRig(t, 5, true, nil)
		history := viewHistory(rig.app.Chat, 2)
		history[0].Attachments = append(history[0].Attachments, chat.Attachment{Handle: "@model1", Kind: chat.AttachmentKindModel,
			Role: chat.AttachmentRoleGenerated, TaskID: "task-3d", AssetID: "asset-3d"})
		result := rig.run(t, chat.TurnOptions{}, history, `{"prompt":"p","reference_handles":["@img1"],"view_labels":{"@model1":"F"}}`)
		rig.refused(t, result, rig.pictureRefusal("@model1"))
	})
	// The picture is sent twice (no anchor de-duplicates it): its place is ambiguous.
	t.Run("a picture that appears twice", func(t *testing.T) {
		rig := newLabelRig(t, 5, true, nil)
		history := []chat.Message{{Role: rig.app.Chat.Roles.User, Content: "make it 3D", ImageURLs: []string{"/same", "/same"},
			Attachments: []chat.Attachment{
				{Handle: "@img1", Kind: chat.AttachmentKindImage, ImageSlot: 1}, {Handle: "@img2", Kind: chat.AttachmentKindImage, ImageSlot: 2}}}}
		result := rig.run(t, chat.TurnOptions{}, history, `{"prompt":"p","view_labels":{"@img2":"F"}}`)
		rig.refused(t, result, rig.pictureRefusal("@img2"))
	})
}

// A mark the chosen model does not take, and an argument that is not a map of
// strings, are refused with the marks the model declares.
func TestViewLabelsWithMarksTheModelDoesNotTakeAreRefused(t *testing.T) {
	for name, test := range map[string]struct {
		arguments string
		handle    string
	}{
		"lowercase":        {`{"prompt":"p","reference_handles":["@img1"],"view_labels":{"@img1":"f"}}`, "@img1"},
		"a word":           {`{"prompt":"p","reference_handles":["@img1"],"view_labels":{"@img1":"front"}}`, "@img1"},
		"two marks in one": {`{"prompt":"p","reference_handles":["@img1"],"view_labels":{"@img1":"F,L"}}`, "@img1"},
		"a number":         {`{"prompt":"p","reference_handles":["@img1"],"view_labels":{"@img1":1}}`, "view_labels"},
		"a list":           {`{"prompt":"p","reference_handles":["@img1"],"view_labels":["F"]}`, "view_labels"},
		"a string":         {`{"prompt":"p","reference_handles":["@img1"],"view_labels":"F"}`, "view_labels"},
		// The mark is checked before the handle, so the Agent fixes what it can.
		"a bad mark on an unsent picture": {`{"prompt":"p","reference_handles":["@img1"],"view_labels":{"@img9":"X"}}`, "@img9"},
	} {
		t.Run(name, func(t *testing.T) {
			rig := newLabelRig(t, 5, true, nil)
			result := rig.run(t, chat.TurnOptions{}, nil, test.arguments)
			rig.refused(t, result, rig.valueRefusal(test.handle))
		})
	}
}

// Two handles can name one picture (the same asset reached twice): after
// de-duplication it is one picture with one mark. Equal marks are that mark;
// different ones are refused, since no mark is more right than the other.
func TestViewLabelsOfTwoHandlesForOnePicture(t *testing.T) {
	history := func(schema catalog.ChatSchema) []chat.Message {
		return []chat.Message{{Role: schema.Roles.User, Content: "make it 3D", ImageURLs: []string{"/same", viewURL(2)},
			Attachments: []chat.Attachment{
				{Handle: "@img1", Kind: chat.AttachmentKindImage, ImageSlot: 1},
				{Handle: "@img2", Kind: chat.AttachmentKindImage, ImageSlot: 1},
				{Handle: "@img3", Kind: chat.AttachmentKindImage, ImageSlot: 2}}}}
	}
	rig := newLabelRig(t, 5, true, nil)
	result := rig.run(t, chat.TurnOptions{}, history(rig.app.Chat), `{"prompt":"p","reference_handles":["@img1","@img2","@img3"],
		"view_labels":{"@img1":"F","@img2":"F","@img3":"B"}}`)
	if result.NoSubmission {
		t.Fatalf("result = %+v", result)
	}
	if got := rig.resolver.urls; !slices.Equal(got, []string{"/same", viewURL(2)}) {
		t.Fatalf("pictures = %v", got)
	}
	if got := rig.marks(t); !slices.Equal(got, []string{"F", "B"}) {
		t.Fatalf("marks = %v", got)
	}

	rig = newLabelRig(t, 5, true, nil)
	result = rig.run(t, chat.TurnOptions{}, history(rig.app.Chat), `{"prompt":"p","reference_handles":["@img1","@img2","@img3"],
		"view_labels":{"@img1":"F","@img2":"L"}}`)
	rig.refused(t, result, rig.pictureRefusal("@img2"))
}

// With no mark to send the call is exactly what it was without the argument:
// the frozen input, and so its hash, are byte for byte the same. Absent, null,
// empty, blank, unknown-only and for-a-model-without-marks all leave nothing in it.
func TestViewLabelsThatSayNothingLeaveTheFrozenInputUntouched(t *testing.T) {
	const base = `"prompt":"a robot","reference_handles":["@img1","@img2","@img3"]`
	baseline := newLabelRig(t, 5, true, nil)
	baseline.run(t, chat.TurnOptions{}, nil, `{`+base+`}`)
	want, input := baseline.frozen(t)
	if _, present := input[labelsParameter]; present {
		t.Fatalf("the baseline carries marks: %s", want)
	}
	for name, argument := range map[string]string{
		"empty object":           `"view_labels":{}`,
		"null":                   `"view_labels":null`,
		"only unknown marks":     `"view_labels":{"@img1":"?","@img3":"?"}`,
		"blank marks":            `"view_labels":{"@img1":"","@img2":"  "}`,
		"only blank and unknown": `"view_labels":{"@img1":"","@img2":"?"}`,
	} {
		t.Run(name, func(t *testing.T) {
			rig := newLabelRig(t, 5, true, nil)
			result := rig.run(t, chat.TurnOptions{}, nil, `{`+base+`,`+argument+`}`)
			if result.NoSubmission {
				t.Fatalf("result = %+v", result)
			}
			if got, _ := rig.frozen(t); !bytes.Equal(got, want) {
				t.Fatalf("frozen input changed:\n got %s\nwant %s", got, want)
			}
		})
	}
	// A model that declares no marks takes the argument for nothing: even marks
	// it could not have used, or ones that would be refused for a model with marks.
	plain := newLabelRig(t, 5, false, nil)
	plain.run(t, chat.TurnOptions{}, nil, `{`+base+`}`)
	plainWant, _ := plain.frozen(t)
	for name, argument := range map[string]string{
		"marks":               `"view_labels":{"@img1":"F","@img2":"L","@img3":"B"}`,
		"a mark it never has": `"view_labels":{"@img1":"front"}`,
		"an unsent picture":   `"view_labels":{"@img9":"F"}`,
		"not a map":           `"view_labels":["F"]`,
	} {
		t.Run("a model without marks: "+name, func(t *testing.T) {
			rig := newLabelRig(t, 5, false, nil)
			result := rig.run(t, chat.TurnOptions{}, nil, `{`+base+`,`+argument+`}`)
			if result.NoSubmission {
				t.Fatalf("result = %+v", result)
			}
			if got, _ := rig.frozen(t); !bytes.Equal(got, plainWant) {
				t.Fatalf("frozen input changed:\n got %s\nwant %s", got, plainWant)
			}
			if bytes.Contains(plainWant, []byte("image_label")) || bytes.Contains(plainWant, []byte(labelsParameter)) {
				t.Fatalf("a model without marks froze marks: %s", plainWant)
			}
		})
	}
}

// The marks the executor writes are a value the shipped Offering normalizes:
// the same list, in the same order, repeats and all.
func TestViewLabelsPassTheShippedOfferingsNormalization(t *testing.T) {
	rig := newLabelRig(t, 5, true, nil)
	rig.run(t, chat.TurnOptions{}, nil, `{"prompt":"a robot","reference_handles":["@img1","@img2","@img3"],
		"view_labels":{"@img1":"F","@img2":"F"}}`)
	_, input := rig.frozen(t)
	input["reference_assets"] = []string{"a", "b", "c"}
	input["authorize_extra_credits"] = true
	encoded, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	_, normalized, err := catalogOf(t).Normalize(generation.ImageTo3D, "hyper3d/rodin-gen-2.5/image-to-3d", encoded)
	if err != nil {
		t.Fatalf("the shipped Offering refused the executor's marks: %v", err)
	}
	var values map[string]any
	if err := json.Unmarshal(normalized, &values); err != nil {
		t.Fatal(err)
	}
	if got, _ := json.Marshal(values[labelsParameter]); string(got) != `["F","F","?"]` {
		t.Fatalf("normalized marks = %s", got)
	}
}

func catalogOf(t *testing.T) *catalog.Catalog {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	return product
}
