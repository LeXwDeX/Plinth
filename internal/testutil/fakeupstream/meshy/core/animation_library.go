package core

import (
	"encoding/json"
	"fmt"
)

// actionRange is an inclusive run of animation library action ids.
type actionRange struct{ first, last int }

// animationRigTypes is the rig_type of every action of the official animation
// library, as GET /openapi/v1/animations/library answered it on 2026-11-25
// (#608): 805 actions, 678 biped (ids 0 to 696 with gaps) and 127 quadruped
// (ids 1055 to 1264 with gaps). Only the ids and their rig_type are kept; the
// names, categories and preview URLs are not used by the fake.
var animationRigTypes = map[string][]actionRange{
	"biped": {
		{0, 331}, {333, 372}, {375, 379}, {381, 382}, {384, 399}, {401, 417}, {419, 422}, {425, 442},
		{444, 453}, {455, 468}, {470, 599}, {601, 601}, {604, 613}, {615, 632}, {635, 654}, {656, 696},
	},
	"quadruped": {
		{1055, 1068}, {1091, 1099}, {1104, 1117}, {1146, 1169}, {1172, 1200}, {1203, 1210}, {1220, 1229},
		{1242, 1244}, {1249, 1264},
	},
}

// ActionRigType is the rig_type of a library action id, or "" for an id the
// library does not list.
func ActionRigType(id int) string {
	for rigType, ranges := range animationRigTypes {
		for _, run := range ranges {
			if id >= run.first && id <= run.last {
				return rigType
			}
		}
	}
	return ""
}

// actionRigTypeError is the 400 of an action whose rig_type is not the rig's.
// The text for a quadruped action on a biped rig is the real one (2026-11-25,
// #608: action_id 1055, and action_ids [0, 1055] names 1055); the official API
// names the first mismatching action. The text for a biped action on a
// quadruped rig was not observed and is built the same way (constructed).
// An id the library does not list is left to the API (not modelled).
func actionRigTypeError(request map[string]any, rigType string) string {
	var ids []any
	if id, present := request["action_id"]; present {
		ids = append(ids, id)
	}
	if list, ok := request["action_ids"].([]any); ok {
		ids = append(ids, list...)
	}
	for _, value := range ids {
		number, isNumber := value.(json.Number)
		id, err := number.Int64()
		if !isNumber || err != nil {
			continue
		}
		switch actionType := ActionRigType(int(id)); {
		case actionType == "" || actionType == rigType:
		case actionType == "quadruped":
			return fmt.Sprintf("Animation %d is a quadruped animal animation and cannot be applied to this character. Pick an animation made for this character's rig.", id)
		default:
			return fmt.Sprintf("Animation %d is a biped animation and cannot be applied to this character. Pick an animation made for this character's rig.", id)
		}
	}
	return ""
}
