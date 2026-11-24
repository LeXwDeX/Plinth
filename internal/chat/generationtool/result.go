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
// the prompt by it. A failure the upstream answered with an HTTP error opens
// with the Schema's upstream text naming provider (the connection's display
// name), the status and its reason; a class text that guides the Agent
// follows it, the generic failure text is replaced. A metered failure keeps
// its own text.
func LocalizedFailureMessage(chat catalog.ChatSchema, provider string, metered bool, failure *generation.Failure, localize func(string) string) string {
	if localize == nil {
		localize = func(text string) string { return text }
	}
	message := FailureMessage(chat, metered, failure)
	if MeteredFailureMessage(chat, metered, failure) == "" {
		if upstream := UpstreamFailureMessage(chat, provider, failure, localize); upstream != "" {
			if message == chat.Presentation.Messages.GenerationFailed {
				return upstream
			}
			return chat.Presentation.Messages.UpstreamHTTPError.Combined(upstream, localize(message), localize)
		}
	}
	return localize(message) +
		contentRejectedReasons(chat.Presentation.Messages, generation.ContentRejectedReasons(failure), localize)
}

// UpstreamFailureMessage is the Schema's upstream HTTP error text for a
// failed Task, the one wording a task card, the Agent's tool result and a
// posted approval result share: "{provider} 网关返回 {status}，原因：{reason}"
// for a failure whose upstream answered an error status, or the unreachable
// text for a connection failure with no answer. Only a failure class the
// Schema lists, or an ambiguous failure, gets it; provider is the display
// name of the Task's connection. "" otherwise, and the caller keeps its text.
// The status is internal evidence the kernel kept; the upstream's own words
// never reach this text.
func UpstreamFailureMessage(chat catalog.ChatSchema, provider string, failure *generation.Failure, localize func(string) string) string {
	if failure == nil {
		return ""
	}
	texts := chat.Presentation.Messages.UpstreamHTTPError
	ambiguous := upstreamFailureAmbiguous(failure)
	if !ambiguous && !texts.Covers(string(failure.Class)) {
		return ""
	}
	if failure.UpstreamStatus >= 400 {
		return texts.Message(provider, failure.UpstreamStatus, localize)
	}
	if failure.Class == generation.FailureTransport && !ambiguous {
		return texts.UnreachableMessage(provider, localize)
	}
	return ""
}

// PublicFailureDetail is a failed Task's public detail (detail, already in
// the reader's locale) with the upstream text: in place of a listed class's
// text, and in front of an ambiguous failure's text, which must keep saying
// the upstream may already have charged (#136).
func PublicFailureDetail(chat catalog.ChatSchema, provider string, failure *generation.Failure, detail string, localize func(string) string) string {
	upstream := UpstreamFailureMessage(chat, provider, failure, localize)
	if upstream == "" {
		return detail
	}
	if upstreamFailureAmbiguous(failure) {
		return chat.Presentation.Messages.UpstreamHTTPError.Combined(upstream, detail, localize)
	}
	return upstream
}

func upstreamFailureAmbiguous(failure *generation.Failure) bool {
	return failure.Ambiguous || failure.Class == generation.FailureSubmissionUnknown
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

// TaskFailureReport is the platform's one attribution of a failed Task (#517):
// the actionable category and next action of its failure class (#251), with
// the person's decision required and no retry offered when the Task may
// already have been charged. That is a credit-metered Offering whose class the
// Schema declares a no-auto-retry text for, or an ambiguous failure (#136).
// The second result is that Schema reason in the default locale, "" for any
// other failure; a Task without a failure has the zero report. The platform
// Agent's primary result and task view and the external MCP all project it.
func TaskFailureReport(chat catalog.ChatSchema, task generation.Task) (generation.FailureReport, string) {
	if task.Failure == nil {
		return generation.FailureReport{}, ""
	}
	report := generation.ClassifyFailure(task.Failure.Class, task.Failure.Retryable, task.Failure.Ambiguous)
	reason := MeteredFailureMessage(chat, task.Offering.CreditsMetered, task.Failure)
	if reason == "" {
		reason = AmbiguousFailureMessage(chat, task.Failure)
	}
	if reason != "" {
		report.Retryable, report.UserDecisionRequired = false, true
		report.Action = generation.ActionAskUser
	}
	return report, reason
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
