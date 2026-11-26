package openaiv1

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
)

// DataInspectionMarker, [[fake:data-inspection]] in the last user message of a
// streaming Chat Completions request that carries pictures, answers the
// content review refusal DashScope's compatible endpoint (Bailian) streamed on
// formal-test on 2026-11-26 (sanitized structure from Plinth's logs, no
// committed sample): HTTP 200, a stream whose frame is
//
//	{"error":{"code":"data_inspection_failed","param":null,"message":"Output data may contain inappropriate content.","type":"data_inspection_failed"},"id":"chatcmpl-<uuid>"}
//
// some 3 to 9 seconds after the request. The same request without its
// pictures was answered as usual, so a request with the marker and no picture
// gets the ordinary answer. Observed: the status, the frame's members and
// wording, and the picture dependence. Constructed: that no chunk precedes the
// frame, that the stream ends after it without [DONE], the uuid, and the wait
// (none unless X-Fake-Delay-Millis asks for one, at most DataInspectionMaxHold).
// A non-streaming request is not affected (not observed).
const DataInspectionMarker = "[[fake:data-inspection]]"

// DataInspectionMessage is the refusal text of that frame.
const DataInspectionMessage = "Output data may contain inappropriate content."

// DataInspectionMaxHold bounds the wait before the refusal frame.
const DataInspectionMaxHold = 30 * time.Second

func (r chatRequest) dataInspection() bool {
	return r.Stream && strings.Contains(r.lastText(), DataInspectionMarker) && len(r.pictureURLs()) > 0
}

// writeDataInspection streams the refusal frame and ends the stream.
func (h *handler) writeDataInspection(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	if flusher != nil {
		flusher.Flush()
	}
	if hold := min(fakeupstream.DelayOf(r), DataInspectionMaxHold); hold > 0 {
		timer := time.NewTimer(hold)
		select {
		case <-timer.C:
		case <-r.Context().Done():
			timer.Stop()
			return
		}
	}
	number := h.responses.nextResponseNumber()
	frame := map[string]any{
		"error": map[string]any{
			"code": "data_inspection_failed", "param": nil,
			"message": DataInspectionMessage, "type": "data_inspection_failed",
		},
		"id": fmt.Sprintf("chatcmpl-%08x-0000-4000-8000-%012x", 0xfa4e, number),
	}
	raw, _ := json.Marshal(frame)
	_, _ = fmt.Fprintf(w, "data: %s\n\n", raw)
	if flusher != nil {
		flusher.Flush()
	}
}
