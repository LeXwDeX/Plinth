package core

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
)

// The list and stream routes of every task kind (#608). Observed on the real
// API on 2026-11-25 (meshy-openapi-v1-list-stream-2026-11-25.candidate.json,
// image-to-3d and rigging; the other kinds share the route shape on the docs):
//
//   - GET /<kind> answers a bare JSON array of task objects, each the body a
//     GET /<kind>/{id} answers; a page past the end is []. page_num is 1 to
//     255 (0 or absent is page 1, 256 answers 400); page_size is 1 to 100
//     (0 or absent is 10, -1 or a non-number answers 400, 101 was accepted);
//     sort_by is "-created_at" (default, newest first) or "+created_at"; its
//     first character is read as the direction and the rest as the column, so
//     "created" answers 400 `invalid column: "reated"`.
//   - GET /<kind>/{id}/stream answers 200 text/event-stream: one
//     "event:message" block per task state, its data the GET body, and the
//     server closes the stream after the block of a final state (a finished
//     task yields exactly one block). An unknown id is an "event:error" block
//     {"message":"Task not found","status_code":404} and a malformed one
//     {"message":"Invalid ID","status_code":400}, both still HTTP 200; a
//     missing key is the usual 401 JSON. The blocks are written without a
//     space after the colon, as the official stream sends them.
//
// Constructed (not observed): the blocks of a task that is still running
// (the docs show PENDING then SUCCEEDED; the fake sends one block per poll
// state it would answer), negative or non-numeric page_num (answered with the
// observed page_num text), the 101-and-up page_size clamp to 100, and a
// direction character other than + or -.

const (
	listDefaultSize = 10
	listMaxSize     = 100
	listMaxPage     = 255
)

func (e *engine) list(kind Kind) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !e.auth(w, r) || e.cfg.Env.Intercept(w, r, fakeupstream.PhaseRead, e.render) {
			return
		}
		query := r.URL.Query()
		page, ok := listNumber(query.Get("page_num"), 1)
		if !ok || page < 0 || page > listMaxPage {
			e.fail(w, http.StatusBadRequest, "page_num must be an integer between 1 and 255")
			return
		}
		size, ok := listNumber(query.Get("page_size"), listDefaultSize)
		if !ok || size < 0 {
			e.fail(w, http.StatusBadRequest, "page_size must be an integer between 1 and 100")
			return
		}
		if page == 0 {
			page = 1
		}
		if size == 0 {
			size = listDefaultSize
		}
		size = min(size, listMaxSize)
		ascending := false
		if sortBy := query.Get("sort_by"); sortBy != "" {
			// "+" decodes to a space when the client does not escape it.
			ascending = sortBy[0] == '+' || sortBy[0] == ' '
			if column := sortBy[1:]; column != "created_at" {
				e.fail(w, http.StatusBadRequest, fmt.Sprintf("invalid column: %q", column))
				return
			}
		}
		store := e.cfg.Store
		store.mu.Lock()
		var tasks []*task
		for _, t := range store.tasks {
			if t.kind.Path == kind.Path {
				tasks = append(tasks, t)
			}
		}
		store.mu.Unlock()
		// Ids are issued in creation order (Store.newID).
		sort.Slice(tasks, func(i, j int) bool {
			if ascending {
				return tasks[i].id < tasks[j].id
			}
			return tasks[i].id > tasks[j].id
		})
		items := []any{}
		for i := (page - 1) * size; i < len(tasks) && i < page*size; i++ {
			// A listing reads the task's current state; it is not a poll.
			items = append(items, e.pollBody(r, tasks[i], e.statusOf(tasks[i])))
		}
		writeJSON(w, http.StatusOK, items)
	}
}

// listNumber reads an integer query value; "" is the fallback.
func listNumber(raw string, fallback int) (int, bool) {
	if raw == "" {
		return fallback, true
	}
	n, err := strconv.Atoi(raw)
	return n, err == nil
}

func (e *engine) stream(kind Kind) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !e.auth(w, r) || e.cfg.Env.Intercept(w, r, fakeupstream.PhaseRead, e.render) {
			return
		}
		id := r.PathValue("id")
		if !validID(id) {
			// The malformed-id refusal came with a charset, the unknown-id one
			// without (both observed).
			writeSSEError(w, "text/event-stream;charset=utf-8", http.StatusBadRequest, "Invalid ID")
			return
		}
		t := e.cfg.Store.lookup(id)
		if t == nil {
			writeSSEError(w, "text/event-stream", http.StatusNotFound, "Task not found")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		store := e.cfg.Store
		limit := e.cfg.Env.QueuedPolls + e.cfg.Env.RunningPolls + 1
		for i := 0; i < limit; i++ {
			store.mu.Lock()
			t.polls++
			n := t.polls
			store.mu.Unlock()
			status := e.statusAt(t, n)
			e.announce(r, t, status)
			writeSSEBlock(w, "message", e.pollBody(r, t, status))
			if flusher != nil {
				flusher.Flush()
			}
			if status != StatusPending && status != StatusInProgress {
				return
			}
		}
	}
}

func writeSSEError(w http.ResponseWriter, contentType string, code int, message string) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	writeSSEBlock(w, "error", map[string]any{"message": message, "status_code": code})
}

func writeSSEBlock(w http.ResponseWriter, event string, data any) {
	raw, _ := json.Marshal(data)
	_, _ = fmt.Fprintf(w, "event:%s\ndata:%s\n\n", event, raw)
}

// listOrStreamRoute reports whether a sampled GET is a list or stream answer;
// the engine builds those from the poll bodies, so they are not indexed.
func (e *engine) listOrStreamRoute(apiPath string) bool {
	if strings.HasSuffix(apiPath, "/stream") {
		return true
	}
	for _, kind := range e.cfg.Kinds {
		if apiPath == kind.Path {
			return true
		}
	}
	return false
}
