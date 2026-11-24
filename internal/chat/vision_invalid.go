package chat

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"slices"
	"strings"

	_ "golang.org/x/image/webp"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// Pictures the upstream cannot read (#522, after #497).
//
// An Agent request re-inlines the session's recent pictures every round, so one
// picture an upstream refuses (a one-pixel strip) would answer every later
// round with the same 400 and block the session until the picture slides out
// of the window. Two things keep a session usable:
//
//   - a picture the Product Schema's invalid_picture bounds already show to be
//     unreadable (an edge too short, a ratio too extreme) is not sent at all;
//   - a picture the upstream refused anyway is sent once more as a Schema note,
//     and remembered by the session as refused, so no later round sends it.
//
// A picture is remembered by the SHA-256 of the data URI the request carried,
// not by a handle: the same bytes stay refused wherever they come back (a
// branch of the session, an attachment of the same picture), and the key is
// short, bounded and never a picture.

// visionInvalidPolicy is the Schema's policy for pictures the upstream cannot
// read; the zero value keeps every picture and remembers no refusal.
func (orchestrator *Orchestrator) visionInvalidPolicy() catalog.ChatVisionInvalidPictureSchema {
	if orchestrator.schema.Context.Vision == nil {
		return catalog.ChatVisionInvalidPictureSchema{}
	}
	return orchestrator.schema.Context.Vision.InvalidPicture
}

// pictureKey is how a session remembers a refused picture: the SHA-256 of the
// picture URL (a data URI once inlined) as lower-case hex.
func pictureKey(url string) string {
	digest := sha256.Sum256([]byte(url))
	return hex.EncodeToString(digest[:])
}

// maximumPictureHeaderBytes bounds how much of a data URI's payload is decoded
// to read the picture's size; the header of every accepted format lies well
// within it. A picture whose header is not found within it is read whole.
const maximumPictureHeaderBytes = 256 << 10

// dataURIPictureSize reads the pixel size of a base64 data URI picture. ok is
// false for anything it cannot read — a URL that is not a data URI, an
// undecodable payload, a format it does not know — and such a picture is never
// judged: the upstream is the one to refuse it.
func dataURIPictureSize(url string) (width, height int, ok bool) {
	if !visionSafeImage(url) {
		return 0, 0, false
	}
	comma := strings.IndexByte(url, ',')
	if comma < 0 || !strings.HasSuffix(strings.ToLower(url[:comma]), ";base64") {
		return 0, 0, false
	}
	payload := url[comma+1:]
	if prefix := maximumPictureHeaderBytes / 3 * 4; len(payload) > prefix {
		if width, height, ok := pictureConfig(payload[:prefix]); ok {
			return width, height, true
		}
	}
	return pictureConfig(payload)
}

// pictureConfig reads the size of the picture a base64 payload, or a prefix of
// it cut at a quantum boundary, starts with.
func pictureConfig(payload string) (int, int, bool) {
	raw, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		raw, err = base64.RawStdEncoding.DecodeString(strings.TrimRight(payload, "="))
		if err != nil {
			return 0, 0, false
		}
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || config.Width <= 0 || config.Height <= 0 {
		return 0, 0, false
	}
	return config.Width, config.Height, true
}

// pictureTooSmall reports a picture the Schema's bounds show to be unreadable:
// a shortest edge under minimum_edge_pixels, or a ratio of the longest edge to
// the shortest above maximum_aspect_ratio. A picture whose size cannot be read
// here is not judged.
func pictureTooSmall(policy catalog.ChatVisionInvalidPictureSchema, url string) bool {
	if policy.MinimumEdgePixels <= 0 && policy.MaximumAspectRatio <= 0 {
		return false
	}
	width, height, ok := dataURIPictureSize(url)
	if !ok {
		return false
	}
	shortest, longest := min(width, height), max(width, height)
	if policy.MinimumEdgePixels > 0 && shortest < policy.MinimumEdgePixels {
		return true
	}
	return policy.MaximumAspectRatio > 0 && longest > shortest*policy.MaximumAspectRatio
}

// sessionVisionExclusions is the optional storage of a session's refused
// pictures. A repository without it still lets a turn strip a refused picture;
// it only forgets the refusal when the turn ends.
type sessionVisionExclusions interface {
	// VisionExclusions reads the keys of the pictures the owner's session
	// remembers as refused, oldest first; found is false for a missing or
	// foreign session. A session that remembers none (including every session
	// written before this existed) reports none.
	VisionExclusions(ctx context.Context, owner, session string) ([]string, bool, error)
	// UpdateVisionExclusions replaces the remembered keys with what change
	// returns for the stored ones, in one read-modify-write, scoped to the
	// owner. It does not touch updated_at.
	UpdateVisionExclusions(ctx context.Context, owner, session string, change func([]string) []string) (bool, error)
}

// EncodeVisionExclusions stores the refused-picture keys as the session
// column's JSON.
func EncodeVisionExclusions(keys []string) (string, error) {
	if keys == nil {
		keys = []string{}
	}
	raw, err := json.Marshal(keys)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// DecodeVisionExclusions reads the stored keys. A malformed or absent value is
// an empty list: an older session, or a value that cannot be read, remembers
// nothing and the upstream decides again.
func DecodeVisionExclusions(raw string) []string {
	var keys []string
	if json.Unmarshal([]byte(raw), &keys) != nil {
		return []string{}
	}
	return keys
}

// AppendVisionExclusions adds keys to the end of stored, then keeps only the
// newest maximum of them. A key that is already stored moves to the end: the
// oldest refusal is the one forgotten first.
func AppendVisionExclusions(stored, keys []string, maximum int) []string {
	merged := make([]string, 0, len(stored)+len(keys))
	for _, key := range append(append([]string(nil), stored...), keys...) {
		if key == "" {
			continue
		}
		merged = slices.DeleteFunc(merged, func(earlier string) bool { return earlier == key })
		merged = append(merged, key)
	}
	if maximum > 0 && len(merged) > maximum {
		merged = merged[len(merged)-maximum:]
	}
	return merged
}

// VisionExclusions reads the keys of the pictures the owner's session
// remembers as refused. A repository that cannot store them, a missing session
// and a foreign one all report none.
func (service *Service) VisionExclusions(ctx context.Context, ownerID, sessionID string) (map[string]bool, error) {
	store, ok := service.repository.(sessionVisionExclusions)
	if !ok {
		return nil, nil
	}
	keys, found, err := store.VisionExclusions(ctx, ownerID, sessionID)
	if err != nil || !found {
		return nil, err
	}
	excluded := make(map[string]bool, len(keys))
	for _, key := range keys {
		excluded[key] = true
	}
	return excluded, nil
}

// ExcludeVisionPictures remembers pictures the upstream refused, keeping the
// newest maximum of them for the session. It reports whether they were
// stored: false for a repository that cannot, or a missing or foreign session.
func (service *Service) ExcludeVisionPictures(ctx context.Context, ownerID, sessionID string, keys []string, maximum int) (bool, error) {
	store, ok := service.repository.(sessionVisionExclusions)
	if !ok || len(keys) == 0 || maximum <= 0 {
		return false, nil
	}
	return store.UpdateVisionExclusions(ctx, ownerID, sessionID, func(stored []string) []string {
		return AppendVisionExclusions(stored, keys, maximum)
	})
}

// visionExclusions is the session's refused pictures; nil when the orchestrator
// has no service or the session remembers none. A failed read is logged and
// leaves every picture eligible: the upstream refuses it again and the turn
// strips it again, which costs one request, not the turn.
func (orchestrator *Orchestrator) visionExclusions(ctx context.Context, ownerID, sessionID string) map[string]bool {
	if orchestrator.service == nil || sessionID == "" {
		return nil
	}
	excluded, err := orchestrator.service.VisionExclusions(ctx, ownerID, sessionID)
	if err != nil {
		orchestrator.logger.Warn(
			"读取会话已被上游拒绝的图片失败，本轮按未排除处理",
			"session_id", sessionID,
			"error", err,
		)
		return nil
	}
	return excluded
}

// pictureRefusal says which stripping answers an upstream refusal of a request
// with pictures.
type pictureRefusal int

const (
	// pictureRefusalNone is every other failure: no picture is touched.
	pictureRefusalNone pictureRefusal = iota
	// pictureRefusalContent is the upstream's content-safety review (#35).
	pictureRefusalContent
	// pictureRefusalInvalid is a picture the upstream read and refused (#497).
	pictureRefusalInvalid
	// pictureRefusalUnsupported is a model that takes no picture input (#253).
	pictureRefusalUnsupported
)

// classifyPictureRefusal reads an upstream error as one of the refusals a
// picture-free retry can answer. Only an error of that class counts: a refused
// tool declaration or a context that is too long has nothing to do with the
// pictures, and stripping them would hide the real failure.
func classifyPictureRefusal(err error) pictureRefusal {
	switch {
	case err == nil:
		return pictureRefusalNone
	case errors.Is(err, agents.ErrContentRejected):
		return pictureRefusalContent
	case errors.Is(err, agents.ErrImageInvalid):
		return pictureRefusalInvalid
	case errors.Is(err, agents.ErrImagesRejected):
		return pictureRefusalUnsupported
	}
	return pictureRefusalNone
}

// requestPictureKeys lists the keys of the request's pictures, first to last,
// without repeats.
func requestPictureKeys(request agents.Request) []string {
	var keys []string
	seen := map[string]bool{}
	for _, message := range request.Messages {
		for _, part := range message.Parts {
			if part.URL == "" {
				continue
			}
			if key := pictureKey(part.URL); !seen[key] {
				seen[key] = true
				keys = append(keys, key)
			}
		}
	}
	return keys
}

// unreadablePictureKeys are the keys of the request's pictures that the Schema's
// bounds show to be unreadable (see pictureTooSmall): the likely cause of an
// upstream refusal of a picture it does not name.
func (orchestrator *Orchestrator) unreadablePictureKeys(request agents.Request) map[string]bool {
	policy := orchestrator.visionInvalidPolicy()
	var keys map[string]bool
	for _, message := range request.Messages {
		for _, part := range message.Parts {
			if part.URL == "" || !pictureTooSmall(policy, part.URL) {
				continue
			}
			if keys == nil {
				keys = map[string]bool{}
			}
			keys[pictureKey(part.URL)] = true
		}
	}
	return keys
}

// replacePictures copies request with each picture part that only(key) accepts
// replaced by a text part with note; a nil only replaces every picture. The
// parts a request keeps are the same values, so the result differs from the
// request exactly where it was stripped.
func (orchestrator *Orchestrator) replacePictures(request agents.Request, note string, only map[string]bool) agents.Request {
	messages := make([]agents.Message, len(request.Messages))
	for index, message := range request.Messages {
		messages[index] = message
		if len(message.Parts) == 0 {
			continue
		}
		parts := make([]agents.ContentPart, 0, len(message.Parts))
		for _, part := range message.Parts {
			if part.URL != "" && (only == nil || only[pictureKey(part.URL)]) {
				part = agents.ContentPart{Kind: orchestrator.schema.Context.TextPartKind, Text: note}
			}
			parts = append(parts, part)
		}
		messages[index].Parts = parts
	}
	request.Messages = messages
	return request
}

// carryStrippedPictures copies the stripping a streamed request went through
// onto the request that later rounds start from. sent is follow with some of
// its picture parts turned into text by streamAgent (the messages and parts
// keep their places); base is the shorter request follow was built from, so
// its messages are a prefix of follow's. Without this, a round whose picture
// the upstream refused would hand the same picture to the next round.
func carryStrippedPictures(base, follow, sent agents.Request) agents.Request {
	messages := make([]agents.Message, len(base.Messages))
	copy(messages, base.Messages)
	for index := range messages {
		if index >= len(follow.Messages) || index >= len(sent.Messages) ||
			len(follow.Messages[index].Parts) != len(messages[index].Parts) ||
			len(sent.Messages[index].Parts) != len(messages[index].Parts) {
			continue
		}
		var parts []agents.ContentPart
		for position, part := range messages[index].Parts {
			if part.URL == "" || sent.Messages[index].Parts[position].URL != "" {
				continue
			}
			if parts == nil {
				parts = append([]agents.ContentPart(nil), messages[index].Parts...)
			}
			parts[position] = sent.Messages[index].Parts[position]
		}
		if parts != nil {
			messages[index].Parts = parts
		}
	}
	base.Messages = messages
	return base
}
