package threeview

import (
	"context"
	"encoding/json"
	"io"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/imageinspect"
)

// FrameInput is the static input that gives every view of a new group the
// shape of its source subject (#283). Without it each view takes the
// Offering's default frame, a square on every image model, and a standing
// character fills a quarter of it.
//
// The shape is the source subject's box plus the Schema's margin on every side,
// kept inside the bounds of the subject type (a humanoid asked for an A-pose
// is wider than the arms-down source, a quadruped's side view is wider than
// its front view). Every output-size parameter the Offering lets a person
// choose then gets the legal value closest to that shape, by the rule of the
// canvas and of a single reference picture. The result is nil, and the
// Offering's defaults apply, when the Schema does not enable the rule, the
// source cannot be read, or the Offering has no output size to follow.
func (s *Service) FrameInput(ctx context.Context, owner generation.OwnerID, offering generation.Offering,
	subjectType string, source generation.AssetID,
) map[string]json.RawMessage {
	frame := s.schema.Frame
	if !frame.Enabled || s.assets == nil || source == "" {
		return nil
	}
	ratio, ok := s.sourceShape(ctx, owner, source)
	if !ok {
		return nil
	}
	sizes := generationtool.FrameOutputSizes(offering, s.boundFrameRatio(ratio, subjectType))
	if len(sizes) == 0 {
		return nil
	}
	input := make(map[string]json.RawMessage, len(sizes))
	for name, value := range sizes {
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil
		}
		input[name] = encoded
	}
	return input
}

// sourceShape is the width over height of the frame the source subject asks
// for: the subject's box with the Schema's margin, or the whole picture when
// no subject stands out from its background.
func (s *Service) sourceShape(ctx context.Context, owner generation.OwnerID, source generation.AssetID) (float64, bool) {
	_, body, err := s.assets.OpenOwned(ctx, owner, source)
	if err != nil || body == nil {
		return 0, false
	}
	defer body.Close()
	var reader io.Reader = body
	if limit := s.review.Limits.MaxBytes; limit > 0 {
		reader = io.LimitReader(body, limit+1)
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		return 0, false
	}
	picture, err := imageinspect.Decode(data, imageinspect.Limits{
		MaxBytes: s.review.Limits.MaxBytes, MaxPixels: s.review.Limits.MaxPixels,
	})
	if err != nil {
		return 0, false
	}
	bounds := picture.Bounds()
	frame := s.schema.Frame
	subject := imageinspect.SubjectSegments(picture, frame.SubjectThreshold)
	if !subject.HasSubject || subject.Ratio < frame.MinSubjectRatio {
		return float64(bounds.Dx()) / float64(bounds.Dy()), true
	}
	return frameShape(subject.Box[2]-subject.Box[0]+1, subject.Box[3]-subject.Box[1]+1, frame.MarginRatio), true
}

// frameShape is the width over height of a frame that holds a box of the given
// size with margin (a share of the box's longer side) on every side.
func frameShape(width, height int, margin float64) float64 {
	pad := 2 * margin * float64(max(width, height))
	return (float64(width) + pad) / (float64(height) + pad)
}

// boundFrameRatio keeps the shape inside the frame bounds of the subject type.
func (s *Service) boundFrameRatio(ratio float64, subjectType string) float64 {
	wanted := strings.TrimSpace(subjectType)
	if wanted == "" {
		wanted = s.schema.DefaultSubjectType
	}
	for _, subject := range s.schema.SubjectTypes {
		if subject.ID != wanted {
			continue
		}
		if subject.FrameMinimumRatio > 0 {
			ratio = max(ratio, subject.FrameMinimumRatio)
		}
		if subject.FrameMaximumRatio > 0 {
			ratio = min(ratio, subject.FrameMaximumRatio)
		}
	}
	return ratio
}
