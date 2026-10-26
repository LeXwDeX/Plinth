package generation

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/LeXwDeX/Plinth/internal/schema"
)

// SourceRetry is what running a step again would do with its model source
// under today's rules (#326). A failed plan step's retry, a paused plan step's
// continue and a failed chat step's regenerate share this one judgment, so a
// paid action certain to fail is never offered and one that now takes the
// saved model file says so.
type SourceRetry string

const (
	// SourceRetryAsBefore: the step binds its source as the earlier attempt
	// did (or nothing was frozen yet to compare with).
	SourceRetryAsBefore SourceRetry = ""
	// SourceRetrySavedFile: the earlier attempt froze the source's original
	// Provider task, which the Provider no longer keeps; running again binds
	// the saved model file instead.
	SourceRetrySavedFile SourceRetry = "saved_file"
	// SourceRetryExpired: the source's original task is gone and the step
	// takes no saved model file that fits; running again cannot succeed.
	SourceRetryExpired SourceRetry = "source_expired"
	// SourceRetryIncompatible: the source no longer fits the step at all.
	SourceRetryIncompatible SourceRetry = "source_incompatible"
)

// Runnable reports whether running again can succeed as far as the source
// goes.
func (judgment SourceRetry) Runnable() bool {
	return judgment == SourceRetryAsBefore || judgment == SourceRetrySavedFile
}

// JudgeSourceRetry is JudgeSourceRetryAt now.
func JudgeSourceRetry(offering Offering, source Task, chosen AssetID, frozen json.RawMessage) SourceRetry {
	return JudgeSourceRetryAt(offering, source, chosen, frozen, time.Now())
}

// JudgeSourceRetryAt binds source to offering as a new attempt would
// (BindModelSourceAt; chosen as there) and compares the result with frozen,
// the input the earlier attempt froze (a failed Task's input, a paused step's
// PreparedInput; nil when nothing was frozen): it names the source in a
// source-task parameter whose upstream job can no longer be reached, while
// today's binding takes the saved file.
func JudgeSourceRetryAt(offering Offering, source Task, chosen AssetID, frozen json.RawMessage, now time.Time) SourceRetry {
	binding, err := BindModelSourceAt(offering, source, chosen, nil, now)
	switch {
	case errors.Is(err, ErrSourceExpired):
		return SourceRetryExpired
	case err != nil:
		return SourceRetryIncompatible
	case binding.Role != schema.SemanticSourceAsset || len(frozen) == 0:
		return SourceRetryAsBefore
	}
	var input map[string]json.RawMessage
	if json.Unmarshal(frozen, &input) != nil {
		return SourceRetryAsBefore
	}
	for _, parameter := range offering.Parameters {
		role, isSource := schema.SourceParameterRole(parameter)
		var named string
		if !isSource || role != schema.SemanticSourceTask || json.Unmarshal(input[parameter.Name], &named) != nil {
			continue
		}
		if named == string(source.ID) && UpstreamJobExpired(parameter, source, now) {
			return SourceRetrySavedFile
		}
	}
	return SourceRetryAsBefore
}

// TaskSourceRetry judges a Task that failed because its source's original
// Provider task is gone (source_expired) the way a new attempt would bind that
// source today (#326): the step's current Offering, the source the failed
// input names (read as the Task's owner) and the input the Task froze. The
// chat card uses it to offer a paid regenerate only when one can succeed.
// SourceRetryAsBefore for any other Task, or when the Offering or the source
// cannot be read now.
func (m *Module) TaskSourceRetry(ctx context.Context, task Task) SourceRetry {
	if task.State != TaskFailed || task.Failure == nil || task.Failure.Class != FailureSourceExpired || task.OwnerID == "" {
		return SourceRetryAsBefore
	}
	offering, ok := m.catalog.Get(task.Offering.ID)
	if !ok {
		return SourceRetryAsBefore
	}
	var input map[string]json.RawMessage
	if json.Unmarshal(task.Input, &input) != nil {
		return SourceRetryAsBefore
	}
	for _, parameter := range offering.Parameters {
		role, isSource := schema.SourceParameterRole(parameter)
		var named string
		if !isSource || role != schema.SemanticSourceTask || json.Unmarshal(input[parameter.Name], &named) != nil || named == "" {
			continue
		}
		source, err := m.GetScoped(ctx, OwnTasks(task.OwnerID), TaskID(named))
		if err != nil || source.OwnerID != task.OwnerID {
			return SourceRetryAsBefore
		}
		return JudgeSourceRetryAt(offering, source, "", task.Input, m.dependencyNow())
	}
	return SourceRetryAsBefore
}
