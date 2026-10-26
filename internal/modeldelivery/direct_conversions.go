package modeldelivery

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// directConversions reads the declared conversions the Agent (or any caller
// outside a Processing Plan) started directly for this session (#330): a
// succeeded Task with a READY file of the source Task fills that format, and
// a queued, running or materializing one holds its formats against a second
// run. Only a conversion whose frozen source is this exact Task counts; the
// source Task's own files stay ahead of every conversion.
func (s *Service) directConversions(ctx context.Context, v *view) error {
	lister, ok := s.gateway.(SessionTaskLister)
	if !ok || v.task.SessionID == "" {
		return nil
	}
	offerings := s.conversionOfferingIDs()
	if len(offerings) == 0 {
		return nil
	}
	scope := generation.OwnTasks(v.task.OwnerID)
	cursor := ""
	for {
		page, err := lister.List(ctx, scope, generation.ListQuery{
			SessionID:      v.task.SessionID,
			OfferingIDs:    offerings,
			ExcludedStates: []generation.TaskState{generation.TaskFailed, generation.TaskCanceled},
			Cursor:         cursor,
			Limit:          100,
		})
		if err != nil {
			return fmt.Errorf("列出会话的格式转换任务: %w", err)
		}
		for _, summary := range page.Tasks {
			if summary.ID == v.task.ID || summary.OwnerID != v.task.OwnerID || summary.SessionID != v.task.SessionID {
				continue
			}
			// The listing carries no frozen input; only a declared conversion
			// Offering is worth reading in full.
			if !s.ConversionOffering(summary.Offering.ID) {
				continue
			}
			task, err := s.gateway.GetScoped(ctx, scope, summary.ID)
			if err != nil {
				if generation.IsCode(err, generation.ErrNotFound) {
					continue
				}
				return fmt.Errorf("读取会话的格式转换任务 %s: %w", summary.ID, err)
			}
			if task.OwnerID != v.task.OwnerID || task.SessionID != v.task.SessionID ||
				!s.sameConversionSource(task, v.task) {
				continue
			}
			switch task.State {
			case generation.TaskSucceeded:
				classified, _ := s.classify(sortedAssets(task))
				for format, asset := range classified {
					s.keepObtained(v, format, asset, task)
				}
			case generation.TaskQueued, generation.TaskRunning, generation.TaskMaterializing:
				for _, format := range s.convertedFormats(task) {
					if _, taken := v.inFlight[format]; !taken {
						v.inFlight[format] = task
					}
				}
			}
		}
		if page.NextCursor == "" || page.NextCursor == cursor {
			return nil
		}
		cursor = page.NextCursor
	}
}

// conversionOfferingIDs narrows the session listing to declared conversion
// Offerings: a long session cannot turn the format projection into one full
// read per Task.
func (s *Service) conversionOfferingIDs() []generation.OfferingID {
	ids := make([]generation.OfferingID, 0, len(s.schema.Conversions))
	seen := map[generation.OfferingID]bool{}
	for _, conversion := range s.schema.Conversions {
		id := generation.OfferingID(conversion.OfferingID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	return ids
}

// sameConversionSource reports a Task running a declared conversion whose
// frozen input names this exact source: the source Task itself, or one of its
// READY model assets. A conversion of an upload or of another Task is not
// this version's delivery, and neither is a chain of conversions.
func (s *Service) sameConversionSource(task, source generation.Task) bool {
	if !s.IsConversion(task.Offering.ID, task.Input) {
		return false
	}
	var input map[string]json.RawMessage
	if json.Unmarshal(task.Input, &input) != nil {
		return false
	}
	taskID, assetID := conversionSource(task.Offering, input)
	if taskID != "" {
		return taskID == source.ID
	}
	if assetID == "" {
		return false
	}
	for _, asset := range source.Assets {
		if asset.ID == assetID {
			return asset.Role == modelRole && ready(asset)
		}
	}
	return false
}

// convertedFormats lists the formats a directly started Task will deliver.
// A per-format run names one; a whole-run Offering names everything it
// provides.
func (s *Service) convertedFormats(task generation.Task) []string {
	format, ok := s.conversionInput(task.Offering.ID, task.Input)
	if !ok {
		return nil
	}
	if format != "" {
		return []string{format}
	}
	for _, conversion := range s.schema.Conversions {
		if conversion.OfferingID == string(task.Offering.ID) && len(conversion.Provides) > 0 {
			return conversion.Provides
		}
	}
	return nil
}

// conversionSource reads the conversion's frozen source parameter. A Task
// source is the public Task ID; an asset source is the saved model asset of
// the source Task.
func conversionSource(offering generation.Offering, input map[string]json.RawMessage) (generation.TaskID, generation.AssetID) {
	for _, parameter := range offering.Parameters {
		role, isSource := schema.SourceParameterRole(parameter)
		if !isSource {
			continue
		}
		raw, present := input[parameter.Name]
		if !present {
			continue
		}
		var value string
		if json.Unmarshal(raw, &value) != nil || value == "" {
			continue
		}
		switch role {
		case schema.SemanticSourceTask:
			return generation.TaskID(value), ""
		case schema.SemanticSourceAsset:
			return "", generation.AssetID(value)
		}
	}
	return "", ""
}

// conversionOfTask names a directly started conversion the way an active plan
// is named, so the row says who converts and what it was estimated at. The
// Task's frozen Offering and NativeQuote are authoritative: a later Catalog
// revision or price must not rewrite the running Task's quote.
func (s *Service) conversionOfTask(task generation.Task) *Conversion {
	offering := task.Offering
	if offering.ID == "" {
		return nil
	}
	return &Conversion{
		OfferingID: offering.ID, ProviderID: offering.ProviderID,
		ProviderName: offering.ProviderName, ProviderShortLabel: offering.ProviderShortLabel,
		EstimatedCredits: offering.EstimateTotalCredits(task.Input),
		CreditsMetered:   offering.CreditsMetered,
		NativeQuote:      task.NativeQuote,
	}
}
