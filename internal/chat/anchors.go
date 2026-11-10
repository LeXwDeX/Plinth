package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// Visual anchors are the session's reference-backed identity and style
// settings (#250). An anchor names an input asset of the owner's session, so a
// generation call can use the same character or look without the person
// re-selecting pictures. The session's own style lock stays a separate
// setting: an Agent-written anchor never silently replaces it.
const (
	VisualAnchorKindIdentity = "identity"
	VisualAnchorKindStyle    = "style"

	VisualAnchorOriginUser  = "user"
	VisualAnchorOriginAgent = "agent"
)

var (
	ErrVisualAnchorInvalid          = errors.New("chat: invalid visual anchor")
	ErrVisualAnchorLimit            = errors.New("chat: too many visual anchors")
	ErrVisualAnchorNameTooLong      = errors.New("chat: visual anchor name too long")
	ErrVisualAnchorDescriptionLong  = errors.New("chat: visual anchor description too long")
	ErrVisualAnchorReferenceLimit   = errors.New("chat: too many visual anchor references")
	ErrVisualAnchorReferenceInvalid = errors.New("chat: invalid visual anchor reference")
	ErrVisualAnchorNotFound         = errors.New("chat: visual anchor not found")
	ErrVisualAnchorReadOnly         = errors.New("chat: visual anchor belongs to the person")
)

// VisualAnchor is one session-level reference-backed anchor. References are
// input-asset IDs the owner and session verified when the anchor was written.
type VisualAnchor struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Kind        string   `json:"kind"`
	Description string   `json:"description,omitempty"`
	References  []string `json:"references,omitempty"`
	Origin      string   `json:"origin,omitempty"`
}

// VisualAnchorKind returns the declared kind, so a caller never invents a kind
// the Schema does not name.
func (anchor VisualAnchor) DeclaredKind(kinds []catalog.ChatVisualAnchorKindSchema) (catalog.ChatVisualAnchorKindSchema, bool) {
	for _, kind := range kinds {
		if kind.ID == anchor.Kind {
			return kind, true
		}
	}
	return catalog.ChatVisualAnchorKindSchema{}, false
}

// EncodeVisualAnchors stores the anchor list as the session column's JSON. An
// absent list is the empty array, exactly like the style lock references.
func EncodeVisualAnchors(anchors []VisualAnchor) (string, error) {
	if anchors == nil {
		anchors = []VisualAnchor{}
	}
	encoded, err := json.Marshal(anchors)
	if err != nil {
		return "", fmt.Errorf("encode visual anchors: %w", err)
	}
	return string(encoded), nil
}

// DecodeVisualAnchors reads a stored anchor list. A malformed or absent value
// reads as no anchors; the id, name and kind of an anchor are validated again
// when a turn uses it.
func DecodeVisualAnchors(raw string) []VisualAnchor {
	var anchors []VisualAnchor
	if json.Unmarshal([]byte(raw), &anchors) != nil || anchors == nil {
		return []VisualAnchor{}
	}
	return anchors
}

// VisualAnchors reads the owner's session anchors. A missing or foreign
// session is ErrSessionNotFound.
func (service *Service) VisualAnchors(ctx context.Context, ownerID, sessionID string) ([]VisualAnchor, error) {
	anchors, found, err := service.repository.VisualAnchors(ctx, ownerID, sessionID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, ErrSessionNotFound
	}
	return cloneVisualAnchors(anchors), nil
}

func cloneVisualAnchors(anchors []VisualAnchor) []VisualAnchor {
	result := make([]VisualAnchor, 0, len(anchors))
	for _, anchor := range anchors {
		anchor.References = append([]string(nil), anchor.References...)
		result = append(result, anchor)
	}
	return result
}

// VisualAnchorRequest is one explicit write the Agent asks for. Pointers keep
// an omitted update field distinct from a cleared one.
type VisualAnchorRequest struct {
	Action      string
	Name        string
	Kind        *string
	Description *string
	References  *[]string
}

// VisualAnchorToolResult is what the Agent reads after a write.
type VisualAnchorToolResult struct {
	Action     string
	Name       string
	Kind       string
	References int
	Message    string
	Names      []string
}

// WriteVisualAnchor applies one Agent request. The person's own anchors and
// their style lock are never modified: an anchor with the user origin refuses
// every Agent update or delete. References go through the same owner/session
// attachment verifier as the style lock, and a failed check never reveals
// which ID was rejected.
func (service *Service) WriteVisualAnchor(ctx context.Context, command ToolCommand) (ToolResult, error) {
	request, err := parseVisualAnchorRequest(command.Call.Arguments)
	if err != nil {
		return visualAnchorFailure(service.visualAnchors.InvalidTemplate, err.Error()), nil
	}
	result, err := service.applyVisualAnchor(ctx, command.OwnerID, command.SessionID, request, VisualAnchorOriginAgent)
	if err != nil {
		return visualAnchorFailure(service.visualAnchors.InvalidTemplate, visualAnchorReason(err)), nil
	}
	content, marshalErr := json.Marshal(map[string]any{
		"success":    true,
		"action":     result.Action,
		"name":       result.Name,
		"kind":       result.Kind,
		"references": result.References,
		"anchors":    result.Names,
		"message":    result.Message,
	})
	if marshalErr != nil {
		return ToolResult{Error: "visual anchor unavailable", NoSubmission: true}, nil
	}
	return ToolResult{AgentContent: string(content)}, nil
}

func visualAnchorFailure(template, reason string) ToolResult {
	if strings.TrimSpace(template) == "" {
		template = "视觉锚未保存：{reason}"
	}
	return ToolResult{Error: strings.ReplaceAll(template, "{reason}", reason), NoSubmission: true}
}

func visualAnchorReason(err error) string {
	switch {
	case errors.Is(err, ErrVisualAnchorLimit):
		return "数量已达上限"
	case errors.Is(err, ErrVisualAnchorNameTooLong):
		return "名称过长"
	case errors.Is(err, ErrVisualAnchorDescriptionLong):
		return "描述过长"
	case errors.Is(err, ErrVisualAnchorReferenceLimit):
		return "参考图过多"
	case errors.Is(err, ErrVisualAnchorReferenceInvalid):
		return "参考图不可用"
	case errors.Is(err, ErrVisualAnchorNotFound):
		return "找不到该锚点"
	case errors.Is(err, ErrVisualAnchorReadOnly):
		return "该锚点由用户设置，Agent 不能修改"
	default:
		return "参数无效"
	}
}

func parseVisualAnchorRequest(raw json.RawMessage) (VisualAnchorRequest, error) {
	var arguments struct {
		Action      string    `json:"action"`
		Name        string    `json:"name"`
		Kind        *string   `json:"kind"`
		Description *string   `json:"description"`
		References  *[]string `json:"references"`
	}
	if err := json.Unmarshal(raw, &arguments); err != nil {
		return VisualAnchorRequest{}, ErrVisualAnchorInvalid
	}
	return VisualAnchorRequest{
		Action:      strings.TrimSpace(arguments.Action),
		Name:        arguments.Name,
		Kind:        arguments.Kind,
		Description: arguments.Description,
		References:  arguments.References,
	}, nil
}

const (
	visualAnchorActionCreate  = "create"
	visualAnchorActionUpdate  = "update"
	visualAnchorActionDelete  = "delete"
	visualAnchorActionDecline = "decline"
)

func (service *Service) applyVisualAnchor(
	ctx context.Context,
	ownerID, sessionID string,
	request VisualAnchorRequest,
	origin string,
) (VisualAnchorToolResult, error) {
	schema := service.visualAnchors
	switch request.Action {
	case visualAnchorActionDecline:
		return VisualAnchorToolResult{Action: request.Action, Message: schema.DeclinedMessage}, nil
	case visualAnchorActionCreate, visualAnchorActionUpdate, visualAnchorActionDelete:
	default:
		return VisualAnchorToolResult{}, ErrVisualAnchorInvalid
	}
	anchors, found, err := service.repository.VisualAnchors(ctx, ownerID, sessionID)
	if err != nil {
		return VisualAnchorToolResult{}, err
	}
	if !found {
		return VisualAnchorToolResult{}, ErrSessionNotFound
	}
	name, err := service.validateVisualAnchorName(request.Name, schema)
	if err != nil {
		return VisualAnchorToolResult{}, err
	}
	index := -1
	for position, anchor := range anchors {
		if anchor.Name == name {
			index = position
			break
		}
	}
	if request.Action == visualAnchorActionDelete {
		if index < 0 {
			return VisualAnchorToolResult{}, ErrVisualAnchorNotFound
		}
		if anchors[index].Origin == VisualAnchorOriginUser {
			return VisualAnchorToolResult{}, ErrVisualAnchorReadOnly
		}
		anchors = append(anchors[:index], anchors[index+1:]...)
		if found, err := service.repository.SetVisualAnchors(ctx, ownerID, sessionID, anchors); err != nil || !found {
			if err != nil {
				return VisualAnchorToolResult{}, err
			}
			return VisualAnchorToolResult{}, ErrSessionNotFound
		}
		return VisualAnchorToolResult{Action: request.Action, Name: name,
			Message: schema.RemovedMessage, Names: visualAnchorNames(anchors)}, nil
	}
	if request.Action == visualAnchorActionCreate && index >= 0 {
		return VisualAnchorToolResult{}, ErrVisualAnchorInvalid
	}
	if request.Action == visualAnchorActionUpdate && index < 0 {
		return VisualAnchorToolResult{}, ErrVisualAnchorNotFound
	}
	if request.Action == visualAnchorActionUpdate && anchors[index].Origin == VisualAnchorOriginUser {
		return VisualAnchorToolResult{}, ErrVisualAnchorReadOnly
	}
	anchor := VisualAnchor{Name: name, Origin: origin}
	if index >= 0 {
		anchor = anchors[index]
	}
	if request.Kind != nil {
		kind := strings.TrimSpace(*request.Kind)
		declared := false
		for _, candidate := range schema.Kinds {
			if candidate.ID == kind {
				declared = true
				break
			}
		}
		if !declared {
			return VisualAnchorToolResult{}, ErrVisualAnchorInvalid
		}
		anchor.Kind = kind
	}
	if anchor.Kind == "" {
		return VisualAnchorToolResult{}, ErrVisualAnchorInvalid
	}
	if request.Description != nil {
		description := strings.TrimSpace(*request.Description)
		if maximum := schema.MaximumDescriptionCharacters; maximum <= 0 || utf8.RuneCountInString(description) > maximum {
			return VisualAnchorToolResult{}, ErrVisualAnchorDescriptionLong
		}
		anchor.Description = description
	}
	if request.References != nil {
		references, err := service.validateVisualAnchorReferences(ctx, ownerID, sessionID, *request.References)
		if err != nil {
			return VisualAnchorToolResult{}, err
		}
		anchor.References = references
	}
	if request.Action == visualAnchorActionCreate {
		if maximum := schema.MaximumAnchors; maximum <= 0 || len(anchors) >= maximum {
			return VisualAnchorToolResult{}, ErrVisualAnchorLimit
		}
		anchor.ID = service.newID("anchor")
		anchors = append(anchors, anchor)
	} else {
		anchors[index] = anchor
	}
	if found, err := service.repository.SetVisualAnchors(ctx, ownerID, sessionID, anchors); err != nil || !found {
		if err != nil {
			return VisualAnchorToolResult{}, err
		}
		return VisualAnchorToolResult{}, ErrSessionNotFound
	}
	action := request.Action
	message := schema.SavedMessage
	if action == visualAnchorActionCreate {
		action = visualAnchorActionCreate
	}
	return VisualAnchorToolResult{Action: action, Name: anchor.Name, Kind: anchor.Kind,
		References: len(anchor.References), Message: message, Names: visualAnchorNames(anchors)}, nil
}

func (service *Service) validateVisualAnchorName(name string, schema catalog.ChatVisualAnchorsSchema) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || (schema.MaximumNameCharacters > 0 && utf8.RuneCountInString(name) > schema.MaximumNameCharacters) {
		return "", ErrVisualAnchorNameTooLong
	}
	return name, nil
}

func (service *Service) validateVisualAnchorReferences(
	ctx context.Context,
	ownerID, sessionID string,
	references []string,
) ([]string, error) {
	schema := service.visualAnchors
	if maximum := schema.MaximumReferences; maximum <= 0 || len(references) > maximum {
		return nil, ErrVisualAnchorReferenceLimit
	}
	if len(references) > 0 && service.attachments == nil {
		return nil, ErrVisualAnchorReferenceInvalid
	}
	stored := make([]string, 0, len(references))
	seen := map[string]bool{}
	for _, reference := range references {
		reference = strings.TrimSpace(reference)
		if reference == "" || seen[reference] {
			continue
		}
		seen[reference] = true
		if _, err := service.attachments.VerifyAttachment(ctx, ownerID, sessionID, AttachmentRef{
			Kind: AttachmentKindImage, AssetID: reference,
		}); err != nil {
			return nil, ErrVisualAnchorReferenceInvalid
		}
		stored = append(stored, reference)
	}
	return stored, nil
}

func visualAnchorNames(anchors []VisualAnchor) []string {
	names := make([]string, 0, len(anchors))
	for _, anchor := range anchors {
		names = append(names, anchor.Name)
	}
	return names
}

// VisualAnchorIdentityNames lists the identity anchors by name. The turn's
// tool descriptions and the anchor prompt use it, so a name the Agent may pass
// in the anchor argument is always the stored one.
func VisualAnchorIdentityNames(anchors []VisualAnchor) []string {
	var names []string
	for _, anchor := range anchors {
		if anchor.Kind == VisualAnchorKindIdentity {
			names = append(names, anchor.Name)
		}
	}
	return names
}
