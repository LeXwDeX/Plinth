package catalog

import (
	"errors"
	"fmt"
	"strings"
)

// Names of the checks the three-view consistency decision reports (#243,
// #283). They are a wire contract with internal/imageinspect, which produces
// them, and internal/threeview, which words them with
// three_view.consistency.reasons.
const (
	threeViewCheckNonblank             = "nonblank"
	threeViewCheckBackground           = "background"
	threeViewCheckSubjectSize          = "subject_size"
	threeViewCheckEdge                 = "edge"
	threeViewCheckComponents           = "components"
	threeViewCheckMainComponent        = "main_component"
	threeViewCheckSubjectSizeDelta     = "subject_size_delta"
	threeViewCheckSilhouetteHeightDiff = "silhouette_height_delta"
	threeViewCheckPaletteDistance      = "palette_distance"
	threeViewCheckPartCountDelta       = "part_count_delta"
	threeViewCheckMirrorOverlap        = "mirror_overlap"
	threeViewCheckMirrorOrientation    = "mirror_orientation"
)

// threeViewKnownChecks are all check names a reason may be declared for.
var threeViewKnownChecks = []string{
	threeViewCheckNonblank, threeViewCheckBackground, threeViewCheckSubjectSize, threeViewCheckEdge,
	threeViewCheckComponents, threeViewCheckMainComponent, threeViewCheckSubjectSizeDelta,
	threeViewCheckSilhouetteHeightDiff, threeViewCheckPaletteDistance, threeViewCheckPartCountDelta,
	threeViewCheckMirrorOverlap, threeViewCheckMirrorOrientation,
}

// threeViewEmittedChecks lists the checks the declared bounds make the
// decision report; each of them needs a reason once the Schema words any.
func threeViewEmittedChecks(consistency ChatThreeViewConsistencySchema) []string {
	names := []string{threeViewCheckNonblank, threeViewCheckSubjectSize, threeViewCheckEdge}
	if consistency.MaxBackgroundSpread > 0 {
		names = append(names, threeViewCheckBackground)
	}
	if consistency.MaxSubjectRatioDelta > 0 {
		names = append(names, threeViewCheckSubjectSizeDelta)
	}
	if consistency.MaxBBoxHeightDelta > 0 {
		names = append(names, threeViewCheckSilhouetteHeightDiff)
	}
	if consistency.Front.MaxComponents > 0 {
		names = append(names, threeViewCheckComponents)
	}
	if consistency.Front.MinMainComponentRatio > 0 {
		names = append(names, threeViewCheckMainComponent)
	}
	appearance := consistency.Appearance
	if appearance.Palette.MaxDistance > 0 {
		names = append(names, threeViewCheckPaletteDistance)
	}
	if appearance.Parts.MaxCountDelta > 0 {
		names = append(names, threeViewCheckPartCountDelta)
	}
	for _, pair := range appearance.Mirror.Pairs {
		if pair.MinOverlap > 0 {
			names = append(names, threeViewCheckMirrorOverlap)
			break
		}
	}
	for _, pair := range appearance.Mirror.Pairs {
		if pair.MaxOrientationMargin > 0 {
			names = append(names, threeViewCheckMirrorOrientation)
			break
		}
	}
	return names
}

// validateThreeViewConsistency checks the automatic check's bounds (#243) and
// the appearance comparison, the front-view acceptance and the words of every
// reason (#283).
func validateThreeViewConsistency(schema ChatThreeViewSchema, recipe ChatSkillRecipeSchema) error {
	consistency := schema.Consistency
	if consistency.MaxSubjectRatioDelta < 0 || consistency.MaxBackgroundSpread < 0 || consistency.MaxBBoxHeightDelta < 0 {
		return errors.New("three_view.consistency 的阈值不能为负")
	}
	if schema.Continuation.Enabled && consistency.MaxAttemptsPerView < 1 {
		return errors.New("three_view.consistency.max_attempts_per_view 必须为正")
	}
	front := consistency.Front
	if front.MaxComponents < 0 || front.MinMainComponentRatio < 0 || front.MinMainComponentRatio > 1 {
		return errors.New("three_view.consistency.front 的 max_components 不能为负，min_main_component_ratio 必须在 0..1 之间")
	}
	if err := validateThreeViewAppearance(consistency.Appearance, recipe); err != nil {
		return err
	}
	return validateThreeViewReasons(consistency)
}

// validateThreeViewAppearance checks the comparison bounds and the pairs of
// views the mirror check names.
func validateThreeViewAppearance(appearance ChatThreeViewAppearanceSchema, recipe ChatSkillRecipeSchema) error {
	palette, parts, mirror := appearance.Palette, appearance.Parts, appearance.Mirror
	if palette.MaxDistance < 0 || palette.MaxDistance > 1 {
		return errors.New("three_view.consistency.appearance.palette.max_distance 必须在 0..1 之间")
	}
	if parts.MaxCountDelta < 0 {
		return errors.New("three_view.consistency.appearance.parts.max_count_delta 不能为负")
	}
	paletteOn, partsOn, mirrorOn := palette.MaxDistance > 0, parts.MaxCountDelta > 0, len(mirror.Pairs) > 0
	if (paletteOn || partsOn || mirrorOn) && appearance.SubjectThreshold <= 0 {
		return errors.New("three_view.consistency.appearance.subject_threshold 必须为正")
	}
	if paletteOn {
		if palette.Colours < 1 || palette.Colours > 16 || palette.MinShare <= 0 || palette.MinShare >= 1 ||
			palette.MergeDistance <= 0 || palette.MergeDistance > 442 {
			return errors.New("three_view.consistency.appearance.palette 的 colours 必须在 1..16，min_share 在 0..1 之间（不含端点），merge_distance 在 0..442 之间")
		}
	}
	if partsOn && (parts.MinShare <= 0 || parts.MinShare >= 1) {
		return errors.New("three_view.consistency.appearance.parts.min_share 必须在 0..1 之间（不含端点）")
	}
	if !mirrorOn {
		return nil
	}
	if mirror.NormalizedSide < 8 || mirror.NormalizedSide > 256 {
		return errors.New("three_view.consistency.appearance.mirror.normalized_side 必须在 8..256 之间")
	}
	// A historical recipe (no subject templates) keeps the views it was
	// published with: a pair naming a view it lacks is never evaluated, so
	// only a current recipe is held to naming its own steps.
	current := recipeUsesSubjectPlaceholders(recipe)
	steps := map[string]bool{}
	for _, step := range recipe.Steps {
		steps[step.Key] = true
	}
	for _, pair := range mirror.Pairs {
		if len(pair.Views) != 2 || strings.TrimSpace(pair.Views[0]) == "" || pair.Views[0] == pair.Views[1] ||
			(current && (!steps[pair.Views[0]] || !steps[pair.Views[1]])) {
			return fmt.Errorf("three_view.consistency.appearance.mirror.pairs 的 views 必须是该 recipe 中两个不同的步骤，现为 %v", pair.Views)
		}
		if pair.MinOverlap < 0 || pair.MinOverlap > 1 || pair.MaxOrientationMargin < 0 || pair.MaxOrientationMargin > 1 ||
			(pair.MinOverlap == 0 && pair.MaxOrientationMargin == 0) {
			return fmt.Errorf("three_view.consistency.appearance.mirror.pairs %v 的 min_overlap 与 max_orientation_margin 须在 0..1 之间且至少声明一个", pair.Views)
		}
	}
	return nil
}

// validateThreeViewReasons checks the words of the failed checks: no unknown
// check, every reason carries {actual}, and once any new bound is declared
// (or any reason is) every check the bounds can report is worded.
func validateThreeViewReasons(consistency ChatThreeViewConsistencySchema) error {
	known := map[string]bool{}
	for _, name := range threeViewKnownChecks {
		known[name] = true
	}
	for name, text := range consistency.Reasons {
		if !known[name] {
			return fmt.Errorf("three_view.consistency.reasons 含未知的检查 %q", name)
		}
		if strings.TrimSpace(text) == "" || !strings.Contains(text, "{actual}") {
			return fmt.Errorf("three_view.consistency.reasons.%s 必须非空并包含 {actual}", name)
		}
	}
	appearance := consistency.Appearance
	wording := len(consistency.Reasons) > 0 || consistency.Front.MaxComponents > 0 ||
		consistency.Front.MinMainComponentRatio > 0 || appearance.Palette.MaxDistance > 0 ||
		appearance.Parts.MaxCountDelta > 0 || len(appearance.Mirror.Pairs) > 0
	if !wording {
		return nil
	}
	if consistency.ReasonSeparator == "" {
		return errors.New("three_view.consistency.reason_separator 不能为空")
	}
	for _, name := range threeViewEmittedChecks(consistency) {
		if strings.TrimSpace(consistency.Reasons[name]) == "" {
			return fmt.Errorf("three_view.consistency.reasons.%s 不能为空", name)
		}
	}
	return nil
}

// validateThreeViewFrame checks the frame rule and the frame bounds of the
// subject types (#283).
func validateThreeViewFrame(schema ChatThreeViewSchema) error {
	frame := schema.Frame
	if frame.Enabled {
		if frame.SubjectThreshold <= 0 || frame.MinSubjectRatio <= 0 || frame.MinSubjectRatio >= 1 ||
			frame.MarginRatio < 0 || frame.MarginRatio > 0.5 {
			return errors.New("three_view.frame 启用时 subject_threshold 必须为正，min_subject_ratio 必须在 0..1 之间（不含端点），margin_ratio 必须在 0..0.5 之间")
		}
	} else if frame.SubjectThreshold != 0 || frame.MinSubjectRatio != 0 || frame.MarginRatio != 0 {
		return errors.New("three_view.frame 未启用时不能声明其他字段")
	}
	for _, subject := range schema.SubjectTypes {
		minimum, maximum := subject.FrameMinimumRatio, subject.FrameMaximumRatio
		if minimum < 0 || maximum < 0 || (minimum > 0 && maximum > 0 && minimum > maximum) {
			return fmt.Errorf("three_view.subject_types %q 的 frame_minimum_ratio 与 frame_maximum_ratio 不能为负，且最小值不能大于最大值", subject.ID)
		}
	}
	return nil
}

// validateThreeViewReview checks the Agent's visual checklist (#283): the
// templates carry their fields, the items are unique, the notes name recipe
// views, and the completion receipt carries {checklist} so the checklist
// reaches the Agent.
func validateThreeViewReview(schema ChatThreeViewSchema, recipe ChatSkillRecipeSchema) error {
	review := schema.Review
	carried := strings.Contains(schema.Messages.AgentCompleted, "{checklist}")
	declared := review.ChecklistTemplate != "" || review.ItemTemplate != "" || len(review.Items) > 0 ||
		review.ViewNoteTemplate != "" || len(review.ViewNotes) > 0
	if !declared {
		if carried {
			return errors.New("three_view.messages.agent_completed 使用 {checklist}，但没有声明 three_view.review")
		}
		return nil
	}
	if !carried {
		return errors.New("three_view.messages.agent_completed 必须包含 {checklist}，否则 three_view.review 到不了 Agent")
	}
	if len(review.Items) == 0 || !strings.Contains(review.ChecklistTemplate, "{views}") ||
		!strings.Contains(review.ChecklistTemplate, "{items}") || !strings.Contains(review.ItemTemplate, "{text}") {
		return errors.New("three_view.review 必须声明 items，checklist_template 须含 {views} 与 {items}，item_template 须含 {text}")
	}
	seen := map[string]bool{}
	for _, item := range review.Items {
		id := strings.TrimSpace(item.ID)
		if id == "" || seen[id] || strings.TrimSpace(item.Text) == "" {
			return fmt.Errorf("three_view.review.items 含无效或重复的项 %q", item.ID)
		}
		seen[id] = true
	}
	if len(review.ViewNotes) == 0 {
		return nil
	}
	if !strings.Contains(review.ChecklistTemplate, "{view_notes}") ||
		!strings.Contains(review.ViewNoteTemplate, "{view}") || !strings.Contains(review.ViewNoteTemplate, "{note}") {
		return errors.New("three_view.review 声明 view_notes 时，checklist_template 须含 {view_notes}，view_note_template 须含 {view} 与 {note}")
	}
	current := recipeUsesSubjectPlaceholders(recipe) // see validateThreeViewAppearance
	steps := map[string]bool{}
	for _, step := range recipe.Steps {
		steps[step.Key] = true
	}
	for key, note := range review.ViewNotes {
		if (current && !steps[key]) || strings.TrimSpace(key) == "" || strings.TrimSpace(note) == "" {
			return fmt.Errorf("three_view.review.view_notes 的 %q 必须是该 recipe 的步骤且说明非空", key)
		}
	}
	return nil
}
