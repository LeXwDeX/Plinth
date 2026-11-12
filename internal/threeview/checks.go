package threeview

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/imageinspect"
	"github.com/LeXwDeX/Plinth/internal/skillrun"
)

// measuredView is one view's picture read for the consistency decision: its
// geometry report and, when the Schema compares appearance, its look.
type measuredView struct {
	report     imageinspect.Report
	appearance imageinspect.Appearance
}

// viewCheck is one check of the decision. Partner is the label of the other
// view a mirror check compared the picture with.
type viewCheck struct {
	imageinspect.Check
	Partner string
}

// viewChecks lists the automatic checks of one finished view (#243, #283). The
// front view has no reference: it must be a visible subject on a plain
// background, the same deterministic facts the 3D input gate reads, in as many
// pieces as the Schema allows. A later view is compared with the accepted
// front view (geometry, then dominant colours and detached parts), and with
// the view opposite to it when that one is already accepted (mirrored
// silhouettes).
func (s *Service) viewChecks(ctx context.Context, owner generation.OwnerID, run skillrun.Run,
	step skillrun.Step,
) ([]viewCheck, error) {
	candidate, err := s.measure(ctx, owner, step, step.OutputAssetID)
	if err != nil {
		return nil, err
	}
	if step.Spec.SourceStepKey == "" {
		return s.frontChecks(candidate.report), nil
	}
	sourceIndex := stepIndexByKey(run, step.Spec.SourceStepKey)
	if sourceIndex < 0 {
		return nil, skillrun.ErrConflict
	}
	source := run.Steps[sourceIndex]
	sourceAsset, ready := acceptedAsset(source)
	if !ready {
		return nil, skillrun.ErrConflict
	}
	reference, err := s.measure(ctx, owner, source, sourceAsset)
	if err != nil {
		return nil, err
	}
	consistency := s.schema.Consistency
	checks := wrapChecks(imageinspect.Compare(reference.report, candidate.report, imageinspect.Consistency{
		MaxSubjectRatioDelta: consistency.MaxSubjectRatioDelta,
		MaxBackgroundSpread:  consistency.MaxBackgroundSpread,
		MaxBBoxHeightDelta:   consistency.MaxBBoxHeightDelta,
	}), "")
	if !s.comparesAppearance() {
		return checks, nil
	}
	checks = append(checks, wrapChecks(imageinspect.CompareAppearance(reference.appearance, candidate.appearance,
		imageinspect.AppearanceLimits{
			MaxPaletteDistance: consistency.Appearance.Palette.MaxDistance,
			MaxPartCountDelta:  consistency.Appearance.Parts.MaxCountDelta,
		}), "")...)
	mirror, err := s.mirrorChecks(ctx, owner, run, step, source, reference, candidate)
	if err != nil {
		return nil, err
	}
	return append(checks, mirror...), nil
}

// frontChecks are the checks of a view that has no earlier view to follow.
func (s *Service) frontChecks(report imageinspect.Report) []viewCheck {
	consistency := s.schema.Consistency
	checks := report.Review(imageinspect.Checklist{
		RequirePlainBackground: consistency.MaxBackgroundSpread > 0,
		MaxBackgroundSpread:    consistency.MaxBackgroundSpread,
		MinSubjectRatio:        0.01,
		MaxComponents:          consistency.Front.MaxComponents,
		RequireEdgeClear:       true,
	})
	if consistency.Front.MinMainComponentRatio > 0 {
		checks = append(checks, imageinspect.MainComponentCheck(report, consistency.Front.MinMainComponentRatio))
	}
	return wrapChecks(checks, "")
}

// mirrorChecks compare the view with the view opposite to it for every pair
// the Schema names. The opposite view must already be accepted (or replaced by
// the person); an optional view that was never made, or one still waiting, is
// skipped. The front view the candidate was measured against is not read twice.
func (s *Service) mirrorChecks(ctx context.Context, owner generation.OwnerID, run skillrun.Run, step, source skillrun.Step,
	reference, candidate measuredView,
) ([]viewCheck, error) {
	var checks []viewCheck
	for _, pair := range s.schema.Consistency.Appearance.Mirror.Pairs {
		if len(pair.Views) != 2 {
			continue
		}
		var partnerKey string
		switch step.Spec.Key {
		case pair.Views[0]:
			partnerKey = pair.Views[1]
		case pair.Views[1]:
			partnerKey = pair.Views[0]
		default:
			continue
		}
		index := stepIndexByKey(run, partnerKey)
		if index < 0 {
			continue
		}
		partner := run.Steps[index]
		asset, ready := acceptedAsset(partner)
		if !ready {
			continue
		}
		view := reference
		if partner.Spec.Key != source.Spec.Key {
			var err error
			if view, err = s.measure(ctx, owner, partner, asset); err != nil {
				return nil, err
			}
		}
		label := partner.Spec.Label
		if label == "" {
			label = partner.Spec.Key
		}
		checks = append(checks, wrapChecks(imageinspect.CompareMirror(view.appearance, candidate.appearance,
			imageinspect.MirrorLimits{MinOverlap: pair.MinOverlap, MaxOrientationMargin: pair.MaxOrientationMargin}), label)...)
	}
	return checks, nil
}

// acceptedAsset is the picture that stands for a finished view: the one the
// person put in its place, else the accepted output. A view that is not
// finally usable has none.
func acceptedAsset(step skillrun.Step) (generation.AssetID, bool) {
	if step.ReplacementAssetID != "" {
		return step.ReplacementAssetID, true
	}
	if step.Phase == skillrun.OutputReady && step.Review == skillrun.Accepted && step.OutputAssetID != "" {
		return step.OutputAssetID, true
	}
	return "", false
}

func wrapChecks(checks []imageinspect.Check, partner string) []viewCheck {
	wrapped := make([]viewCheck, 0, len(checks))
	for _, check := range checks {
		wrapped = append(wrapped, viewCheck{Check: check, Partner: partner})
	}
	return wrapped
}

// comparesAppearance reports whether the Schema compares what the views look
// like at all; without it no picture is decoded beyond its geometry report.
func (s *Service) comparesAppearance() bool {
	appearance := s.schema.Consistency.Appearance
	return appearance.SubjectThreshold > 0 &&
		(appearance.Palette.MaxDistance > 0 || appearance.Parts.MaxCountDelta > 0 || len(appearance.Mirror.Pairs) > 0)
}

func (s *Service) appearanceOptions() imageinspect.AppearanceOptions {
	appearance := s.schema.Consistency.Appearance
	return imageinspect.AppearanceOptions{
		SubjectThreshold:     appearance.SubjectThreshold,
		PaletteColours:       appearance.Palette.Colours,
		PaletteMinShare:      appearance.Palette.MinShare,
		PaletteMergeDistance: appearance.Palette.MergeDistance,
		PartMinShare:         appearance.Parts.MinShare,
		NormalizedSide:       appearance.Mirror.NormalizedSide,
	}
}

// measure reads one view's picture: a generated Task output, or the input
// asset the person put in its place.
func (s *Service) measure(ctx context.Context, owner generation.OwnerID, step skillrun.Step,
	assetID generation.AssetID,
) (measuredView, error) {
	data, err := s.assetData(ctx, owner, step, assetID)
	if err != nil {
		return measuredView{}, err
	}
	limits := imageinspect.Limits{MaxBytes: s.review.Limits.MaxBytes, MaxPixels: s.review.Limits.MaxPixels}
	report, err := imageinspect.Inspect(data, limits)
	if err != nil {
		return measuredView{}, err
	}
	view := measuredView{report: report}
	if s.comparesAppearance() {
		picture, err := imageinspect.Decode(data, limits)
		if err != nil {
			return measuredView{}, err
		}
		view.appearance = imageinspect.MeasureAppearance(picture, s.appearanceOptions())
	}
	return view, nil
}

// assetData reads the bytes of one view's picture, within the review limits.
func (s *Service) assetData(ctx context.Context, owner generation.OwnerID, step skillrun.Step,
	assetID generation.AssetID,
) ([]byte, error) {
	if assetID == "" {
		return nil, skillrun.ErrConflict
	}
	var body io.ReadCloser
	if step.ReplacementAssetID != "" && step.ReplacementAssetID == assetID {
		_, opened, err := s.assets.OpenOwned(ctx, owner, assetID)
		if err != nil {
			return nil, err
		}
		body = opened
	} else {
		content, err := s.assets.OpenAsset(ctx, owner, step.TaskID, assetID)
		if err != nil {
			return nil, err
		}
		body = content.Body
	}
	if body == nil {
		return nil, fmt.Errorf("threeview: asset %s has no reader", assetID)
	}
	defer body.Close()
	var reader io.Reader = body
	if limit := s.review.Limits.MaxBytes; limit > 0 {
		reader = io.LimitReader(body, limit+1)
	}
	return io.ReadAll(reader)
}

// reasonText words one failed check with the Schema's reason for it. A check
// the Schema has no words for is reported by its name and measurement.
func (s *Service) reasonText(check viewCheck) string {
	template := s.schema.Consistency.Reasons[check.Name]
	if template == "" {
		return check.Name + " (" + check.Actual + ")"
	}
	return strings.NewReplacer("{actual}", check.Actual, "{expected}", check.Expected,
		"{partner}", check.Partner).Replace(template)
}
