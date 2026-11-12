package catalog

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// The verdicts an Agent may give a visual item of a picture checklist (#286).
// The vocabulary is closed: the tool's parameter enum and the server both read
// it, and the Catalog check below keeps the two equal.
const (
	ImageVisualPass          = "pass"
	ImageVisualFail          = "fail"
	ImageVisualNotApplicable = "not_applicable"
)

var imageVisualID = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// validateImageVisualItems checks the Agent-judged items of the 3d_input
// checklist (#286) once the inspect_image tool is declared. Every item needs an
// identifier, a label, what to look at and what to do about a failure; the
// argument carrying the verdicts must not collide with another inspect_image
// argument, and the tool must declare it with the ids and verdicts the server
// reads, so the model is offered exactly what the server accepts.
func validateImageVisualItems(schema ChatSchema) error {
	review := schema.ImageReview
	items := review.Input3DVisualItems
	if len(items) == 0 {
		if review.VisualReviewArgument != "" || review.VisualNoteMaxCharacters != 0 {
			return errors.New("chat.image_review 声明了 visual_review_argument 或 visual_note_max_characters，却没有 input_3d_visual_items")
		}
		return nil
	}
	ids := make([]string, 0, len(items))
	seen := make(map[string]bool, len(items))
	for index, item := range items {
		if !imageVisualID.MatchString(item.ID) {
			return fmt.Errorf("chat.image_review.input_3d_visual_items[%d].id 必须为小写标识: %q", index, item.ID)
		}
		if seen[item.ID] {
			return fmt.Errorf("chat.image_review.input_3d_visual_items 中 %q 重复", item.ID)
		}
		seen[item.ID] = true
		ids = append(ids, item.ID)
		for name, value := range map[string]string{
			"label": item.Label, "instruction": item.Instruction, "fix_hint": item.FixHint,
		} {
			if strings.TrimSpace(value) == "" {
				return fmt.Errorf("chat.image_review.input_3d_visual_items[%d].%s 不能为空", index, name)
			}
		}
	}
	argument := review.VisualReviewArgument
	if !imageVisualID.MatchString(argument) {
		return fmt.Errorf("chat.image_review.visual_review_argument 必须为小写标识: %q", argument)
	}
	taken := []string{"image", "checklist", review.OverrideArgument}
	if vision := schema.Context.Vision; vision != nil {
		taken = append(taken, vision.Crop.RegionArgument, vision.Crop.ShowPictureArgument)
	}
	for _, name := range taken {
		if argument == name {
			return fmt.Errorf("chat.image_review.visual_review_argument %q 与 inspect_image 的另一个参数重名", argument)
		}
	}
	if review.VisualNoteMaxCharacters <= 0 {
		return errors.New("chat.image_review.visual_note_max_characters 必须为正数")
	}
	var tool string
	for _, candidate := range schema.AgentTools {
		if candidate.Execution.ImplementationKey == "image.inspect" {
			tool = candidate.Name
			break
		}
	}
	parameters, _ := declaredToolParameters(schema, tool)
	properties, _ := parameters["properties"].(map[string]any)
	property, _ := properties[argument].(map[string]any)
	if property["type"] != "array" {
		return fmt.Errorf("%s 必须把 %q 声明为数组参数", tool, argument)
	}
	element, _ := property["items"].(map[string]any)
	fields, _ := element["properties"].(map[string]any)
	if err := requireImageVisualEnum(tool, argument, "item", fields, ids); err != nil {
		return err
	}
	verdicts := []string{ImageVisualPass, ImageVisualFail, ImageVisualNotApplicable}
	if err := requireImageVisualEnum(tool, argument, "verdict", fields, verdicts); err != nil {
		return err
	}
	if note, _ := fields["note"].(map[string]any); note["type"] != "string" {
		return fmt.Errorf("%s.%s 的元素必须声明字符串成员 note", tool, argument)
	}
	return nil
}

// requireImageVisualEnum checks that one string member of a verdict element
// declares exactly the wanted values, in order.
func requireImageVisualEnum(tool, argument, member string, fields map[string]any, want []string) error {
	field, _ := fields[member].(map[string]any)
	if field["type"] != "string" {
		return fmt.Errorf("%s.%s 的元素必须声明字符串成员 %s", tool, argument, member)
	}
	values, ok := field["enum"].([]any)
	if !ok || len(values) != len(want) {
		return fmt.Errorf("%s.%s 的 %s enum 必须是 %s", tool, argument, member, strings.Join(want, "、"))
	}
	for index, value := range values {
		if value != want[index] {
			return fmt.Errorf("%s.%s 的 %s enum 第 %d 项必须是 %q", tool, argument, member, index+1, want[index])
		}
	}
	return nil
}
