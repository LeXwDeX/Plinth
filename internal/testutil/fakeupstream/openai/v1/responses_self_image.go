package openaiv1

import (
	"encoding/base64"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
)

// Two answers of gpt-astra on /v1/responses that formal-test showed on
// 2026-11-26 (#638, the gateway's and Plinth's logs, read as sanitized
// structure only: no committed sample). Both are fake-only request
// conventions read in the last user message, like the markers of responses.go.
const (
	// SelfImageMarker answers with a picture the model drew itself, which the
	// request never asked for (Plinth declares no image_generation tool; the
	// gateway's upstream adds one): a reasoning item and then an
	// image_generation_call item, and no message and no function_call. Observed:
	// the events response.image_generation_call.in_progress, .generating and
	// .completed; the output item {type: image_generation_call, status, result}
	// whose result is a base64 PNG of megabytes; the response object's tools
	// echoing ImageGenerationTool after the request's own tools; tool_usage with
	// image_gen token counts and web_search.num_requests 0; a whole answer of 6
	// to 9 MB whose terminal line is over Plinth's stream line bound.
	// Constructed: the item id, the members of the progress events (item_id,
	// output_index), the token counts, the absence of partial_image events, the
	// tool echo on the in-progress objects too, and the picture itself (the fake
	// picture padded to SelfImagePictureBytes, or to a picture weight selector,
	// weight.go). The real round took 80 to 125 seconds; the fake holds before
	// .completed only when asked: [[fake:self-image:<millis>]] or the
	// X-Fake-Delay-Millis header (at most SelfImageMaxHold).
	SelfImageMarker = "[[fake:self-image]]"
	// EmptyCompletionMarker answers with a reasoning item only (with
	// encrypted_content) and a response.completed over the stream line bound,
	// so a reader that skips the line loses its usage: no message, no
	// function_call. Observed: the item set and the missing usage on Plinth's
	// side. Constructed: what makes the terminal line long (the fake pads the
	// final reasoning item's encrypted_content to EmptyCompletionLineBytes; the
	// real line's bulk was not logged). With X-Fake-Fail-Times: n only the
	// first n requests of an API key are empty and the later ones answer as
	// usual, so a test can tell a bounded retry that ends empty from one that
	// recovers.
	EmptyCompletionMarker = "[[fake:empty-completion]]"
)

const (
	// SelfImagePictureBytes is the PNG of a SelfImageMarker answer: its base64
	// result (about 4 MB) is on the item's done event and again on
	// response.completed, about 8 MB in all, inside the observed 6 to 9 MB.
	SelfImagePictureBytes = 3_000_000
	// SelfImageMaxHold bounds the hold before .completed.
	SelfImageMaxHold = 180 * time.Second
	// EmptyCompletionLineBytes is at least the size of the terminal line of an
	// EmptyCompletionMarker answer: over the shipped Schema's
	// chat.agent.scanner_max_bytes (1 MiB).
	EmptyCompletionLineBytes = 2 << 20
)

// ImageGenerationTool is the tool the response object echoes for a
// SelfImageMarker answer, member for member as formal-test logged it.
func ImageGenerationTool() map[string]any {
	return map[string]any{
		"type": "image_generation", "background": "auto", "model": "gpt-image-2.6-sunburst",
		"moderation": "auto", "n": 1, "output_compression": 100, "output_format": "png",
		"quality": "auto", "size": "auto",
	}
}

// emptyCompletionCount is the key the Env counts EmptyCompletionMarker
// requests under (Env.StillFails); it is not a scenario a header selects.
const emptyCompletionCount fakeupstream.Scenario = "openai-v1-empty-completion"

var selfImageHoldMarker = regexp.MustCompile(`\[\[fake:self-image:(\d+)\]\]`)

func (r chatRequest) selfImage() bool {
	last := r.lastText()
	return strings.Contains(last, SelfImageMarker) || selfImageHoldMarker.MatchString(last)
}

// selfImageHold is how long the fake waits before .completed.
func selfImageHold(r *http.Request, last string) time.Duration {
	hold := fakeupstream.DelayOf(r)
	if match := selfImageHoldMarker.FindStringSubmatch(last); match != nil {
		if millis, err := strconv.Atoi(match[1]); err == nil {
			hold = time.Duration(millis) * time.Millisecond
		}
	}
	return min(hold, SelfImageMaxHold)
}

// applyObserved replaces the plan of a request that asks for one of the two
// observed answers.
func (h *handler) applyObserved(r *http.Request, request responsesRequest, chat chatRequest, plan *responsesPlan, number int64) error {
	last := chat.lastText()
	reasoning := fakeItem{kind: "reasoning", id: placeholderID("rs_", 50, number), encrypted: true,
		summary: request.summaryRequested()}
	switch {
	case chat.selfImage():
		weight := h.env.PictureWeightOf(r, last, 1024, 1024)
		if weight.Bytes == 0 {
			weight = fakeupstream.PictureWeight{Bytes: SelfImagePictureBytes, Filler: fakeupstream.FillerNoise}
		}
		picture, err := fakeupstream.WeightedPicture(fakeupstream.PictureSpec{Format: "png", Width: 1024, Height: 1024}, weight)
		if err != nil {
			return err
		}
		plan.items = []fakeItem{reasoning, {kind: imageGenerationKind, index: 1, id: placeholderID("ig_", 50, number),
			text: base64.StdEncoding.EncodeToString(picture.Data)}}
		plan.reasoning, plan.selfImage = true, true
		plan.hold, plan.done = selfImageHold(r, last), r.Context().Done()
	case strings.Contains(last, EmptyCompletionMarker) && h.env.StillFails(r, emptyCompletionCount):
		reasoning.finalPadding = EmptyCompletionLineBytes
		plan.items = []fakeItem{reasoning}
		plan.reasoning = true
	default:
		return nil
	}
	plan.status = "completed"
	return nil
}

// imageGenerationKind is the output item type of a picture the model drew.
const imageGenerationKind = "image_generation_call"

// imageGenerationObject is the image_generation_call item at a stage: the
// observed type, status and result, and the fake's id.
func imageGenerationObject(item fakeItem, stage itemStage) map[string]any {
	object := map[string]any{"type": imageGenerationKind, "id": item.id, "status": "completed", "result": item.text}
	if stage == stageAdded {
		object["status"], object["result"] = "in_progress", nil
	}
	return object
}

// imageGenerationEvents are the events of the picture: its item added, the
// three observed progress events and its item done.
func (h *handler) imageGenerationEvents(item fakeItem) []map[string]any {
	progress := func(phase string) map[string]any {
		return map[string]any{"type": "response.image_generation_call." + phase, "item_id": item.id, "output_index": item.index}
	}
	return []map[string]any{
		{"type": "response.output_item.added", "item": imageGenerationObject(item, stageAdded), "output_index": item.index},
		progress("in_progress"),
		progress("generating"),
		progress("completed"),
		{"type": "response.output_item.done", "item": imageGenerationObject(item, stageDone), "output_index": item.index},
	}
}

// selfImageResponse adds what a SelfImageMarker response object echoes: the
// image_generation tool and its tool_usage.
func selfImageResponse(body map[string]any) {
	tools, _ := body["tools"].([]any)
	body["tools"] = append(tools, ImageGenerationTool())
	usage, _ := body["tool_usage"].(map[string]any)
	if usage == nil {
		usage = map[string]any{"web_search": map[string]any{"num_requests": 0}}
		body["tool_usage"] = usage
	}
	usage["image_gen"] = map[string]any{
		"input_tokens": 52, "input_tokens_details": map[string]any{"image_tokens": 0, "text_tokens": 52},
		"output_tokens": 4160, "output_tokens_details": map[string]any{"image_tokens": 4160, "text_tokens": 0},
		"total_tokens": 4212,
	}
}

// paddedEncrypted is the final encrypted_content of an EmptyCompletionMarker
// reasoning item: the fake's final token followed by token characters, at
// least n bytes.
func paddedEncrypted(n int) string {
	if n <= len(FixedEncryptedFinal) {
		return FixedEncryptedFinal
	}
	return FixedEncryptedFinal + strings.Repeat("A", n-len(FixedEncryptedFinal))
}
