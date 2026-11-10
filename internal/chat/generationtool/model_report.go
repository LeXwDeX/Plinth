package generationtool

import (
	"context"
	"errors"

	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/modelinspect"
)

// ModelReport reads the bounded, cached inspection report of one saved model
// for a processing plan checkpoint (#251). The application wires it into the
// processing service; it reads only the caller's own Task and asset, applies
// the Schema's parse limits and shares the review cache with inspect_model, so
// a checkpoint never parses the same bytes twice.
func (executor *Executor) ModelReport(ctx context.Context, owner generation.OwnerID,
	taskID generation.TaskID, assetID generation.AssetID) (modelinspect.Report, error) {
	if executor.modelAssets == nil || executor.modelReview == nil {
		return modelinspect.Report{}, errors.New("model review is unavailable")
	}
	task, err := executor.gateway.Get(ctx, owner, taskID)
	if err != nil {
		return modelinspect.Report{}, err
	}
	asset, found := taskAsset(task, assetID)
	if !found || asset.StorageKey == "" {
		return modelinspect.Report{}, errors.New("saved model asset not found")
	}
	review := executor.chat.ModelReview
	command := chat.ToolCommand{OwnerID: string(owner)}
	entry, refusal, err := executor.loadReviewedModel(ctx, command, review, reviewedModel{task: task, asset: asset})
	if err != nil {
		return modelinspect.Report{}, err
	}
	if refusal != nil {
		return modelinspect.Report{}, errors.New(rejectionMessage(refusal))
	}
	if entry == nil {
		return modelinspect.Report{}, errors.New("saved model cannot be reviewed")
	}
	return entry.inspect(ctx, executor.modelReview, modelReviewInspectOptions(review))
}

// rejectionMessage turns a review refusal into an error string for the
// checkpoint path, which has no Agent turn to show a tool result in.
func rejectionMessage(refusal *chat.ToolResult) string {
	if refusal.Error != "" {
		return refusal.Error
	}
	return "saved model cannot be reviewed"
}
