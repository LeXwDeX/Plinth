package generationtool

import (
	"strings"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/moderation"
)

// ResultAssetURLs are the pictures of a finished Task that the tool's Schema
// shows in chat: the assets of the declared roles, through the Schema's URL
// template. A turn and a late approval result use the same projection.
//
// A 3D tool (result_attachment_kind "model") shows only the first matching
// picture: the model's cover. A provider may save more pictures for the same
// Task, such as the 2D picture drawn before modelling; they stay among the
// Task's assets (task detail, download package) but do not join the message,
// the stream's images event or the Agent's vision context.
func ResultAssetURLs(schema catalog.ChatSchema, tool catalog.ChatToolSchema, task generation.Task) []string {
	roles := make(map[string]struct{}, len(tool.ResultAssetRoles))
	for _, role := range tool.ResultAssetRoles {
		roles[role] = struct{}{}
	}
	var result []string
	for _, asset := range task.Assets {
		if _, accepted := roles[asset.Role]; !accepted {
			continue
		}
		result = append(result, strings.NewReplacer(
			"{task_id}", string(task.ID),
			"{asset_id}", string(asset.ID),
		).Replace(schema.Context.AssetURLTemplate))
		if tool.ResultAttachmentKind == chat.AttachmentKindModel {
			break
		}
	}
	return result
}

// MeteredFailureMessage is the Schema text that tells the Agent and the user a credit-metered
// Task failed in a way that must not be resubmitted automatically (for example the upstream
// finished and charged but our storage or integrity check rejected the result). "" when the
// Schema declares no such text for this Offering and failure class.
func MeteredFailureMessage(chat catalog.ChatSchema, metered bool, failure *generation.Failure) string {
	if !metered || failure == nil {
		return ""
	}
	return chat.Presentation.Messages.GenerationFailedMeteredByClass[string(failure.Class)]
}

// LocalizedFailureMessage is FailureMessage with each Schema text passed
// through localize (nil keeps the default locale), followed for a
// content-safety failure by the Schema's reasons template naming what the
// review gave (#35: "safety_violations=[sexual]"), so the Agent can rewrite
// the prompt by it.
func LocalizedFailureMessage(chat catalog.ChatSchema, metered bool, failure *generation.Failure, localize func(string) string) string {
	if localize == nil {
		localize = func(text string) string { return text }
	}
	return localize(FailureMessage(chat, metered, failure)) +
		contentRejectedReasons(chat.Presentation.Messages, generation.ContentRejectedReasons(failure), localize)
}

// contentRejectedReasons is the Schema's reasons text for a content-safety
// refusal (#35).
func contentRejectedReasons(messages catalog.ChatMessageTexts, reasons []string, localize func(string) string) string {
	return moderation.Explain(messages.ContentRejectedReasons, reasons,
		messages.ContentRejectedReasonLabels, messages.ContentRejectedReasonSeparator, localize)
}

// FailureMessage is the public text of a failed Task: the Schema's metered no-auto-retry text
// when the Offering bills in credits and the class declares one, else the Schema's text for
// its failure class, else the generic failure text.
func FailureMessage(chat catalog.ChatSchema, metered bool, failure *generation.Failure) string {
	if message := MeteredFailureMessage(chat, metered, failure); message != "" {
		return message
	}
	if failure != nil {
		if message := chat.Presentation.Messages.GenerationFailedByClass[string(failure.Class)]; message != "" {
			return message
		}
		if message := chat.Presentation.Messages.GenerationFailedAmbiguous; failure.Ambiguous && message != "" {
			return message
		}
	}
	return chat.Presentation.Messages.GenerationFailed
}

// AmbiguousFailureMessage is the Schema text the Agent relays for a failure
// whose upstream may already have run and charged (#136): the class text,
// else the shared ambiguous text. Empty for any other failure.
func AmbiguousFailureMessage(chat catalog.ChatSchema, failure *generation.Failure) string {
	if failure == nil || !failure.Ambiguous {
		return ""
	}
	if message := chat.Presentation.Messages.GenerationFailedByClass[string(failure.Class)]; message != "" {
		return message
	}
	return chat.Presentation.Messages.GenerationFailedAmbiguous
}
