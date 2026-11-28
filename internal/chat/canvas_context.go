package chat

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// CanvasBoard is what the canvas Port reads for one session's workspace board
// (#709). Every layer field except the geometry and flags comes from the
// node data the browser saved: it is untrusted client data, and an image URL
// or Task named there is never used before VerifyAttachment accepts it.
type CanvasBoard struct {
	ProjectID string
	Revision  int64
	Width     int
	Height    int
	// FrameX and FrameY are the top-left corner of the canvas frame in world
	// coordinates (the Schema's artboard centre minus half the size). They
	// are read only when Width and Height are set.
	FrameX float64
	FrameY float64
	Layers []CanvasBoardLayer
}

// CanvasBoardLayer is one saved board node, as stored.
type CanvasBoardLayer struct {
	ID        string
	Type      string
	X         float64
	Y         float64
	Width     float64
	Height    float64
	ZIndex    int
	Visible   bool
	Locked    bool
	Name      string
	Text      string
	ShapeType string
	ImageURL  string
	TaskID    string
	CaptureID string
	// CreatedAt is when the server first stored the node (kept across saves).
	CreatedAt time.Time
	// Transformed says the node data carries a crop, a rotation or an
	// opacity other than 1; nil when the data says nothing about it.
	Transformed *bool
}

// CanvasSource is the canvas Port of #709, assembled by internal/app over the
// canvas service. Board reads the owner's board of one session without
// creating it; found is false when the session has no board yet.
type CanvasSource interface {
	Board(ctx context.Context, ownerID, sessionID string) (board CanvasBoard, found bool, err error)
}

// CanvasContext is the board snapshot frozen when a turn that is not a canvas
// turn starts (#709). It lets the Agent look at the board as a last resort and
// name a board picture by its @canvasN handle; it is never a canvas turn: no
// board size, layout, mask or input source comes with it. Total counts every
// layer of the board; Layers holds at most the Schema's layer limit, top layer
// first.
type CanvasContext struct {
	ProjectID string `json:"project_id,omitempty"`
	Revision  int64  `json:"revision,omitempty"`
	Width     int    `json:"width,omitempty"`
	Height    int    `json:"height,omitempty"`
	Total     int    `json:"total"`
	// OffConversation counts the layers that are not already in the
	// conversation: a picture that is also an attachment or selection of the
	// session is left out, every annotation (text, shape, pencil, frame) and
	// every picture that is not counts. The board line uses only this count.
	OffConversation int                  `json:"off_conversation"`
	Layers          []CanvasLayerContext `json:"layers,omitempty"`
}

// CanvasLayerContext is one frozen board layer. Picture is the verified
// attachment of a picture layer (owner, and session for a Task); a picture
// layer whose claim did not verify keeps Picture nil and has no handle.
type CanvasLayerContext struct {
	ID        string      `json:"id"`
	Type      string      `json:"type"`
	X         float64     `json:"x"`
	Y         float64     `json:"y"`
	Width     float64     `json:"width"`
	Height    float64     `json:"height"`
	ZIndex    int         `json:"z_index"`
	Visible   bool        `json:"visible"`
	Locked    bool        `json:"locked"`
	Name      string      `json:"name,omitempty"`
	Text      string      `json:"text,omitempty"`
	ShapeType string      `json:"shape_type,omitempty"`
	Handle    string      `json:"handle,omitempty"`
	Picture   *Attachment `json:"picture,omitempty"`
	// Unreadable marks a picture layer whose picture did not verify.
	Unreadable bool `json:"unreadable,omitempty"`
	// InConversation marks a picture that is already an attachment or a
	// selection of this session.
	InConversation bool `json:"in_conversation,omitempty"`
	// AddedAfterLastTurn marks a layer stored after the session's previous
	// user message (every layer of a session without one).
	AddedAfterLastTurn bool `json:"added_after_last_turn,omitempty"`
	// InFrame says whether the layer meets the canvas frame; nil when the
	// board has no frame size yet. Transformed is the node's own claim.
	InFrame     *bool `json:"in_frame,omitempty"`
	Transformed *bool `json:"transformed,omitempty"`
}

// canvasImageLayer is the saved node type of a picture layer.
const canvasImageLayer = "image"

// LayerByHandle returns the frozen picture layer an @canvasN handle names, or
// nil. The comparison ignores case, as for the attachment handles.
func (context *CanvasContext) LayerByHandle(handle string) *CanvasLayerContext {
	if context == nil {
		return nil
	}
	handle = strings.TrimSpace(handle)
	for index := range context.Layers {
		layer := &context.Layers[index]
		if layer.Handle != "" && layer.Picture != nil && strings.EqualFold(layer.Handle, handle) {
			return layer
		}
	}
	return nil
}

// IsCanvasHandle reports whether a token carries the board handle prefix.
func IsCanvasHandle(schema catalog.ChatCanvasLayersSchema, token string) bool {
	prefix := strings.TrimSpace(schema.HandlePrefix)
	return schema.Enabled() && prefix != "" && len(token) > len(prefix) &&
		strings.EqualFold(token[:len(prefix)], prefix)
}

// ResolveCanvasHandle resolves one @canvasN token against the board snapshot
// frozen with the turn. matched is false for a token without the prefix; a
// matched token that names no readable layer is refused with the Schema's
// copy, never resolved as a session handle of the same spelling.
func ResolveCanvasHandle(schema catalog.ChatCanvasLayersSchema, options TurnOptions, token string) (ResolvedAttachment, bool, error) {
	if !IsCanvasHandle(schema, token) {
		return ResolvedAttachment{}, false, nil
	}
	layer := options.CanvasContext.LayerByHandle(token)
	if layer == nil || layer.Picture == nil || layer.Picture.URL == "" {
		return ResolvedAttachment{}, true, &CanvasHandleError{Handle: token, Message: strings.NewReplacer(
			"{handle}", token, "{tool}", schema.ToolName).Replace(schema.UnknownHandleTemplate)}
	}
	picture := *layer.Picture
	picture.Handle = layer.Handle
	return ResolvedAttachment{Attachment: picture, URL: picture.URL}, true, nil
}

// ResolveImageTokens resolves picture tokens for a read-only tool: an
// @canvasN board handle from the frozen snapshot (#709), any other token with
// the session handle policy, in the order named.
func ResolveImageTokens(policy HandlePolicy, board catalog.ChatCanvasLayersSchema, command ToolCommand,
	tokens []string) ([]ResolvedAttachment, error) {
	var result []ResolvedAttachment
	for _, token := range tokens {
		picture, matched, err := ResolveCanvasHandle(board, command.Options, token)
		if matched {
			if err != nil {
				return nil, err
			}
			result = append(result, picture)
			continue
		}
		resolved, err := policy.ResolveHandles(command.ReferenceHistory(), command.Options.Selection,
			[]string{token}, AttachmentKindImage)
		if err != nil {
			return nil, err
		}
		result = append(result, resolved...)
	}
	return result, nil
}

// CanvasHandleError refuses an @canvasN handle the turn's snapshot does not
// hold; Message is the Schema's wording.
type CanvasHandleError struct {
	Handle  string
	Message string
}

func (err *CanvasHandleError) Error() string { return err.Message }

// freezeCanvasContext freezes the board snapshot of a turn that is not a
// canvas turn (#709). A canvas turn already carries the flattened board, so it
// freezes nothing. Every picture layer is verified like any image claim; one
// that fails is kept as an unreadable layer without a handle. A read failure
// leaves the turn without a snapshot: the board is a fallback, never a reason
// to refuse the turn.
func (service *Service) freezeCanvasContext(ctx context.Context, command StartTurn, options *TurnOptions) {
	schema := service.canvasLayers
	if !schema.Enabled() || service.canvas == nil || service.attachments == nil || options.InputSource != "" {
		return
	}
	board, found, err := service.canvas.Board(ctx, command.OwnerID, command.SessionID)
	if err != nil {
		// The board is a fallback: the turn runs without a snapshot, and the
		// tool then answers that the board is unavailable.
		return
	}
	frozen := &CanvasContext{}
	if !found {
		options.CanvasContext = frozen
		return
	}
	frozen.ProjectID, frozen.Revision = board.ProjectID, board.Revision
	frozen.Width, frozen.Height = board.Width, board.Height
	frozen.Total = len(board.Layers)
	history, since := service.canvasHistory(ctx, command)
	layers := append([]CanvasBoardLayer(nil), board.Layers...)
	// Top layer first: the frontend draws a higher z-index above.
	sort.SliceStable(layers, func(left, right int) bool { return layers[left].ZIndex > layers[right].ZIndex })
	if len(layers) > schema.LayerLimit {
		// Layers past the bound are not verified; they count as not in the
		// conversation.
		frozen.OffConversation = len(layers) - schema.LayerLimit
		layers = layers[:schema.LayerLimit]
	}
	pictures := 0
	for _, layer := range layers {
		item := CanvasLayerContext{
			ID: layer.ID, Type: layer.Type, X: layer.X, Y: layer.Y, Width: layer.Width, Height: layer.Height,
			ZIndex: layer.ZIndex, Visible: layer.Visible, Locked: layer.Locked,
			Name:               boundedRunes(layer.Name, schema.TextCharacters),
			Text:               boundedRunes(layer.Text, schema.TextCharacters),
			ShapeType:          boundedRunes(layer.ShapeType, 32),
			AddedAfterLastTurn: since.IsZero() || layer.CreatedAt.After(since),
			Transformed:        layer.Transformed,
		}
		if board.Width > 0 && board.Height > 0 {
			inFrame := layer.X < board.FrameX+float64(board.Width) && layer.X+layer.Width > board.FrameX &&
				layer.Y < board.FrameY+float64(board.Height) && layer.Y+layer.Height > board.FrameY
			item.InFrame = &inFrame
		}
		if layer.Type == canvasImageLayer {
			picture, verifyErr := service.attachments.VerifyAttachment(ctx, command.OwnerID, command.SessionID,
				AttachmentRef{Kind: AttachmentKindImage, URL: layer.ImageURL})
			if verifyErr != nil || picture.URL == "" || picture.Kind != AttachmentKindImage {
				item.Unreadable = true
			} else {
				pictures++
				picture.ImageSlot, picture.Handle = 0, ""
				item.Picture = &picture
				item.Handle = schema.HandlePrefix + strconv.Itoa(pictures)
				item.InConversation = inConversation(history, command.Options.Selection, picture)
			}
		}
		if !item.InConversation {
			frozen.OffConversation++
		}
		frozen.Layers = append(frozen.Layers, item)
	}
	options.CanvasContext = frozen
}

// canvasHistory reads the session's stored messages and the time of its
// previous user message (zero when there is none). The current message is
// not stored yet when the snapshot is frozen.
func (service *Service) canvasHistory(ctx context.Context, command StartTurn) ([]Message, time.Time) {
	if service.repository == nil {
		return nil, time.Time{}
	}
	history, err := service.repository.ListMessages(ctx, command.OwnerID, command.SessionID)
	if err != nil {
		return nil, time.Time{}
	}
	var since time.Time
	for _, message := range history {
		if message.Role == service.userRole && message.CreatedAt.After(since) {
			since = message.CreatedAt
		}
	}
	return history, since
}

// inConversation reports whether a verified board picture is already an
// attachment of the session or a selection of this turn.
func inConversation(history []Message, selection []Attachment, picture Attachment) bool {
	for _, attachment := range selection {
		if SameObject(attachment, picture) {
			return true
		}
	}
	for _, message := range history {
		for _, attachment := range message.Attachments {
			if SameObject(attachment, picture) {
				return true
			}
		}
	}
	return false
}

// boundedRunes keeps at most limit runes of a client text (0 keeps none).
func boundedRunes(text string, limit int) string {
	text = strings.TrimSpace(text)
	if limit <= 0 {
		return ""
	}
	if utf8.RuneCountInString(text) <= limit {
		return text
	}
	return string([]rune(text)[:limit]) + "…"
}

// cloneCanvasContext copies a frozen board snapshot so a running turn never
// sees another turn's decoded options change.
func cloneCanvasContext(context *CanvasContext) *CanvasContext {
	if context == nil {
		return nil
	}
	cloned := *context
	cloned.Layers = make([]CanvasLayerContext, len(context.Layers))
	for index, layer := range context.Layers {
		cloned.Layers[index] = layer
		if layer.Picture != nil {
			picture := *layer.Picture
			cloned.Layers[index].Picture = &picture
		}
		if layer.InFrame != nil {
			inFrame := *layer.InFrame
			cloned.Layers[index].InFrame = &inFrame
		}
		if layer.Transformed != nil {
			transformed := *layer.Transformed
			cloned.Layers[index].Transformed = &transformed
		}
	}
	return &cloned
}

// copyFrozenCanvas puts the board snapshot stored with the turn into the
// process command; a caller can never supply one.
func copyFrozenCanvas(options *TurnOptions, stored TurnOptions) {
	options.CanvasContext = cloneCanvasContext(stored.CanvasContext)
}

// CanvasTurnHasMaterial reports whether the turn itself carries material: a
// picture or model attached to the person's message or a selected object.
// The board fallback stays closed for such a turn (#709).
func CanvasTurnHasMaterial(command ToolCommand, userRole string) bool {
	if len(command.Options.Selection) > 0 {
		return true
	}
	for index := len(command.History) - 1; index >= 0; index-- {
		message := command.History[index]
		if message.Role == userRole {
			return len(message.ImageURLs) > 0 || len(message.Attachments) > 0
		}
	}
	return false
}

// canvasPrompt is the one board line of a turn that is not a canvas turn
// while the frozen board holds layers (#709). Only the count is filled in.
func (orchestrator *Orchestrator) canvasPrompt(ctx context.Context, options TurnOptions) string {
	schema := orchestrator.schema.CanvasLayers
	board := options.CanvasContext
	if !schema.Enabled() || board == nil || board.OffConversation == 0 || options.InputSource != "" {
		return ""
	}
	return strings.NewReplacer("{count}", strconv.Itoa(board.OffConversation), "{tool}", schema.ToolName).
		Replace(orchestrator.text(ctx, schema.ContextTemplate))
}
