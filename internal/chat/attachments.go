package chat

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// Attachment kinds and roles are stable identifiers. Every visible label that
// names them comes from Product Schema (chat.context).
const (
	AttachmentKindImage = "image"
	AttachmentKindModel = "model"

	AttachmentRoleUpload    = "upload"
	AttachmentRoleGenerated = "generated"
	AttachmentRoleCapture   = "capture"
	AttachmentRoleSelection = "selection"
	// AttachmentRoleCanvas marks the artboard snapshot of a canvas turn: the
	// layout the person arranged, flattened into one picture (#272). The
	// service gives this role itself; a client cannot claim it.
	AttachmentRoleCanvas = "canvas"
)

// CanvasLayout describes an artboard snapshot. Width and Height are read from
// the uploaded picture by the service; Layers is the client's count of the
// visible layers it flattened, kept only when it is within the Schema bound
// (0 means unknown).
type CanvasLayout struct {
	Width  int `json:"width"`
	Height int `json:"height"`
	Layers int `json:"layers,omitempty"`
	// Sources are the Tasks of the picture layers the snapshot flattens, as
	// the client claimed them and the server verified for the owner and the
	// session (#189): a tool that takes a source Task can resolve the
	// snapshot back to the one Task it shows. Unverified claims are dropped.
	Sources []string `json:"sources,omitempty"`
}

// Attachment is one image or 3D model that a chat message shows or sends. It
// keeps the platform facts the Agent needs to act on the object: the asset
// and task IDs, the 3D capture it came from, and a short stable handle
// ("@img3", "@model2") that the Agent uses instead of a history index.
//
// An attachment never carries an upstream ID or URL. Every ID is a platform
// ID that was verified for the owner (and, for Tasks and captures, for the
// session) before it was stored.
type Attachment struct {
	Handle    string `json:"handle"`
	Kind      string `json:"kind"`
	Role      string `json:"role,omitempty"`
	AssetID   string `json:"asset_id,omitempty"`
	TaskID    string `json:"task_id,omitempty"`
	CaptureID string `json:"capture_id,omitempty"`
	// ImageSlot is the 1-based position of the attachment's picture in the
	// owning message's ImageURLs; 0 means the message has no picture for it.
	// The slot keeps large data URIs out of the attachment record.
	ImageSlot int `json:"image_slot,omitempty"`
	// URL is a verified platform URL for an attachment whose picture is not
	// one of the message's ImageURLs (a selected object that was never sent).
	URL string `json:"url,omitempty"`
	// Canvas is set only for an artboard snapshot (Role canvas).
	Canvas *CanvasLayout `json:"canvas,omitempty"`
}

// AttachmentRef is what a client claims about an object it shows or sends.
// It is untrusted until an AttachmentVerifier turns it into an Attachment.
type AttachmentRef struct {
	Kind      string
	URL       string
	AssetID   string
	TaskID    string
	CaptureID string
	// ImageSlot is the 1-based position of the message image the claim
	// describes; 0 for a claim about an object that is not one of the images.
	ImageSlot int
	// CanvasLayers is the client's count of the visible layers an artboard
	// snapshot flattens. It is read only for the snapshot of a canvas turn.
	CanvasLayers int
	// CanvasSources are the picture URLs of the layers an artboard snapshot
	// flattens (#189). Each is verified like any image claim, for the owner
	// and the session, and only the Tasks that verify are kept.
	CanvasSources []string
}

// empty reports a claim that names nothing at all.
func (ref AttachmentRef) empty() bool {
	return ref.Kind == "" && ref.URL == "" && ref.AssetID == "" && ref.TaskID == "" && ref.CaptureID == "" &&
		ref.ImageSlot == 0 && ref.CanvasLayers == 0 && len(ref.CanvasSources) == 0
}

// ImageDimensionReader reads the pixel size of an owned input asset. The
// attachment verifier implements it so the service measures an artboard
// snapshot instead of trusting a claimed size (#272).
type ImageDimensionReader interface {
	ImageDimensions(ctx context.Context, ownerID, assetID string) (width, height int, err error)
}

// ErrAttachmentRejected reports a claim that is not owned by the owner or does
// not belong to the session. The claimed IDs are never echoed back.
var ErrAttachmentRejected = errors.New("chat: attachment rejected")

// AttachmentVerifier validates a client claim against owner and session scope
// and returns the canonical facts. Handle and ImageSlot are left empty.
type AttachmentVerifier interface {
	VerifyAttachment(ctx context.Context, ownerID, sessionID string, ref AttachmentRef) (Attachment, error)
}

// HandlePolicy names the handle syntax. It comes from Product Schema.
type HandlePolicy struct {
	// Prefixes maps an attachment kind to its handle prefix ("@img").
	Prefixes map[string]string
	// SelectionAlias refers to every object of the current selection.
	SelectionAlias string
	// MaximumAttachments and MaximumSelection bound the claims of one turn.
	MaximumAttachments int
	MaximumSelection   int
	// MaximumCanvasLayers bounds the layer count an artboard snapshot claims.
	MaximumCanvasLayers int
}

// NewHandlePolicy reads the handle syntax declared by chat.context.
func NewHandlePolicy(context catalog.ChatContextSchema) HandlePolicy {
	prefixes := make(map[string]string, len(context.HandlePrefixes))
	for kind, prefix := range context.HandlePrefixes {
		prefixes[kind] = prefix
	}
	return HandlePolicy{
		Prefixes:            prefixes,
		SelectionAlias:      context.SelectionAlias,
		MaximumAttachments:  context.MaximumAttachmentsPerMessage,
		MaximumSelection:    context.MaximumSelection,
		MaximumCanvasLayers: context.MaximumCanvasLayers,
	}
}

// Enabled reports whether the policy can name attachments at all.
func (policy HandlePolicy) Enabled() bool {
	return len(policy.Prefixes) > 0
}

// Parse splits a handle token into its kind and number. The leading marker is
// part of the Schema prefix; a bare "img3" is accepted for it as well.
func (policy HandlePolicy) Parse(token string) (kind string, number int, ok bool) {
	token = strings.ToLower(strings.TrimSpace(token))
	for candidateKind, prefix := range policy.Prefixes {
		prefix = strings.ToLower(prefix)
		for _, form := range []string{prefix, strings.TrimLeft(prefix, "@#")} {
			if form == "" || !strings.HasPrefix(token, form) {
				continue
			}
			digits := token[len(form):]
			value, err := strconv.Atoi(digits)
			if err != nil || value < 1 || strconv.Itoa(value) != digits {
				continue
			}
			return candidateKind, value, true
		}
	}
	return "", 0, false
}

// Format renders the handle of the number-th attachment of a kind.
func (policy HandlePolicy) Format(kind string, number int) string {
	return policy.Prefixes[kind] + strconv.Itoa(number)
}

// IsSelectionAlias reports whether the token names the whole selection.
func (policy HandlePolicy) IsSelectionAlias(token string) bool {
	return policy.SelectionAlias != "" &&
		strings.EqualFold(strings.TrimSpace(token), policy.SelectionAlias)
}

// Assign gives every attachment without a handle the next free number of its
// kind. Numbers already used by the session's messages, or by attachments
// earlier in the same slice, are never reused, so a handle stays stable for
// the life of the message that carries it.
func (policy HandlePolicy) Assign(history []Message, attachments []Attachment) []Attachment {
	if !policy.Enabled() || len(attachments) == 0 {
		return attachments
	}
	highest := map[string]int{}
	note := func(attachment Attachment) {
		if kind, number, ok := policy.Parse(attachment.Handle); ok && number > highest[kind] {
			highest[kind] = number
		}
	}
	for _, message := range history {
		for _, attachment := range message.Attachments {
			note(attachment)
		}
	}
	for _, attachment := range attachments {
		note(attachment)
	}
	result := make([]Attachment, len(attachments))
	copy(result, attachments)
	for index := range result {
		if result[index].Handle != "" {
			continue
		}
		if _, known := policy.Prefixes[result[index].Kind]; !known {
			continue
		}
		highest[result[index].Kind]++
		result[index].Handle = policy.Format(result[index].Kind, highest[result[index].Kind])
	}
	return result
}

// AttachmentURL returns the picture URL of an attachment of a message, or an
// empty string when the attachment has no picture.
func AttachmentURL(message Message, attachment Attachment) string {
	if attachment.ImageSlot >= 1 && attachment.ImageSlot <= len(message.ImageURLs) {
		return message.ImageURLs[attachment.ImageSlot-1]
	}
	return attachment.URL
}

// SameObject reports whether two attachments name the same platform object.
func SameObject(left, right Attachment) bool {
	if left.Kind != right.Kind {
		return false
	}
	switch {
	case left.CaptureID != "" || right.CaptureID != "":
		return left.CaptureID == right.CaptureID
	case left.Kind == AttachmentKindModel:
		return left.TaskID != "" && left.TaskID == right.TaskID
	case left.AssetID != "" && right.AssetID != "":
		return left.AssetID == right.AssetID && left.TaskID == right.TaskID
	}
	return false
}

// ResolvedAttachment is an attachment found by handle together with the URL
// that shows it.
type ResolvedAttachment struct {
	Attachment Attachment
	URL        string
}

// UnknownHandleError is a refusal the Agent can act on: nothing was submitted.
type UnknownHandleError struct {
	Handle string
	// Reason is "unknown" for a handle that names nothing in this session and
	// "kind" for one that names an object of the wrong kind.
	Reason string
}

func (err *UnknownHandleError) Error() string {
	return fmt.Sprintf("chat: handle %q cannot be used (%s)", err.Handle, err.Reason)
}

// ResolveHandles maps handle tokens to the session's attachments. The history
// and selection are the ones the server loaded for this owner and session, so
// a token can never reach an object of another session or owner. The
// selection alias expands to every selected object of the wanted kind.
func (policy HandlePolicy) ResolveHandles(
	history []Message,
	selection []Attachment,
	tokens []string,
	wantKind string,
) ([]ResolvedAttachment, error) {
	var result []ResolvedAttachment
	seen := map[string]bool{}
	add := func(item ResolvedAttachment) {
		if seen[item.Attachment.Handle] {
			return
		}
		seen[item.Attachment.Handle] = true
		result = append(result, item)
	}
	for _, token := range tokens {
		if policy.IsSelectionAlias(token) {
			for _, attachment := range selection {
				if attachment.Kind != wantKind {
					continue
				}
				if item, found := policy.find(history, selection, attachment.Handle); found {
					add(item)
				}
			}
			continue
		}
		item, found := policy.find(history, selection, token)
		if !found {
			return nil, &UnknownHandleError{Handle: token, Reason: "unknown"}
		}
		if item.Attachment.Kind != wantKind {
			return nil, &UnknownHandleError{Handle: token, Reason: "kind"}
		}
		add(item)
	}
	return result, nil
}

func (policy HandlePolicy) find(history []Message, selection []Attachment, token string) (ResolvedAttachment, bool) {
	kind, number, ok := policy.Parse(token)
	if !ok {
		return ResolvedAttachment{}, false
	}
	canonical := policy.Format(kind, number)
	for index := len(history) - 1; index >= 0; index-- {
		for _, attachment := range history[index].Attachments {
			if strings.EqualFold(attachment.Handle, canonical) {
				return ResolvedAttachment{Attachment: attachment, URL: AttachmentURL(history[index], attachment)}, true
			}
		}
	}
	for _, attachment := range selection {
		if strings.EqualFold(attachment.Handle, canonical) {
			return ResolvedAttachment{Attachment: attachment, URL: attachment.URL}, true
		}
	}
	return ResolvedAttachment{}, false
}

// AttachmentForSlot returns the attachment whose picture is the slot-th image
// (1-based) of the message.
func AttachmentForSlot(message Message, slot int) (Attachment, bool) {
	for _, attachment := range message.Attachments {
		if attachment.ImageSlot == slot {
			return attachment, true
		}
	}
	return Attachment{}, false
}

func itoa(value int) string { return strconv.Itoa(value) }
