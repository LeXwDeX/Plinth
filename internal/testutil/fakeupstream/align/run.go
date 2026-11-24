// Package align sends one scripted request sequence to a target (the official
// API or the fake) and records what came back, so the two can be compared
// structurally (issue #96). It compares method, path, auth header, status
// code, JSON field set/types/nesting, task status enums and flow, and
// artifact content type and format. It never compares values.
package align

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"sort"
	"strconv"
	"strings"
	"time"
)

// File is a multipart upload body.
type File struct {
	Field     string
	Filename  string
	MediaType string
	Data      []byte
	// FromVar, when set, downloads the file's bytes from the URL held by that
	// variable (an artifact an earlier step produced) instead of using Data.
	FromVar string
}

// Field is one text part of a multipart form.
type Field struct {
	Name  string
	Value string
}

// Poll repeats a step until the status at StatusPath is one of Terminal.
type Poll struct {
	StatusPath string
	Terminal   []string
	// ProgressPath is optional and only recorded.
	ProgressPath string
	Max          int
	Interval     time.Duration
}

// Step is one scripted request.
type Step struct {
	Name   string
	Method string
	// Path may contain {var} placeholders filled from earlier Capture.
	Path string
	// Body is marshalled as JSON; string values may contain {var}.
	Body      any
	Multipart *File
	// Form and Files build a multipart form from several text fields (values
	// may contain {var}) and files; Multipart is the single-file shorthand.
	Form  []Field
	Files []File
	// NoAuth omits the authorization header (negative authentication probe).
	NoAuth bool
	// Paid marks a step that spends credits upstream; only counted/reported.
	Paid bool
	// OptIn marks a step an official or export run skips unless -steps names
	// it: an extra, costlier request (acceptance rounds such as #540) that a
	// routine refresh must not send without being asked. The fake and compare
	// commands ignore it: the fake costs nothing and compare reads the official
	// bundle.
	OptIn bool
	Poll  *Poll
	// Capture maps a variable name to a dot path in the final response body
	// ("data.task_id").
	Capture map[string]string
	// Fetch lists dot paths of artifact URL fields in the final response
	// ("data.output.model"); "*" at the end iterates every string value of
	// an object.
	Fetch []string
	// SkipIf makes the step run only when the named variable is set.
	RequireVar string
}

// Target is where the script is sent.
type Target struct {
	Name       string
	BaseURL    string
	Key        string
	AuthHeader string // default Authorization
	AuthPrefix string // default "Bearer "
	// Client is the API client (may be wrapped by a wirefixture recorder).
	Client *http.Client
	// Download fetches artifact URLs; it must not record or authenticate.
	Download *http.Client
	// BodyHook rewrites a step's JSON body before it is sent (the fake run uses
	// it for requests that need a fake-only marker).
	BodyHook func(step string, body any) any
	// FileHook supplies the bytes of a File whose FromVar names an artifact URL,
	// instead of downloading it (an offline export takes them from the saved
	// request). Return false to fall back to the download.
	FileHook func(field, filename string) ([]byte, bool)
	// FormHook rewrites a multipart text field's value before it is sent (the
	// exporter replaces ids the sanitizer refuses to keep).
	FormHook func(name, value string) string
	// SkipFetch leaves artifact URLs unfetched (offline export replays).
	SkipFetch bool
	// NoPollDelay polls without waiting (offline export replays).
	NoPollDelay bool
	// ArtifactDir, when set, keeps the bytes of every fetched artifact: each is
	// written to a new file (mode 0600) of this existing directory, named
	// <step>-<n>.<ext>, and its observation records the file name and SHA-256.
	// The caller checks the directory (private, outside the worktree). Unset, an
	// artifact is only observed (status, content type, format, size).
	ArtifactDir string
}

// PollObs is one observed poll.
type PollObs struct {
	Status   string `json:"status"`
	Progress string `json:"progress,omitempty"`
}

// ArtifactObs is what an artifact URL served.
type ArtifactObs struct {
	Field       string `json:"field"`
	Status      int    `json:"status"`
	ContentType string `json:"content_type"`
	Format      string `json:"format"`
	Size        int64  `json:"size"`
	// File and SHA256 name the bytes kept under Target.ArtifactDir (a base name
	// and the hex digest); Error says why a fetch run with an ArtifactDir kept
	// none. All three are empty without an ArtifactDir.
	File   string `json:"file,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
	Error  string `json:"error,omitempty"`
}

// Obs is the recorded outcome of one Step.
type Obs struct {
	Step   string `json:"step"`
	Method string `json:"method"`
	Path   string `json:"path"`
	// Resolved is Path with the captured variables filled in; the sanitizing
	// exporter registers its ids. Never compared.
	Resolved   string          `json:"resolved_path,omitempty"`
	AuthHeader string          `json:"auth_header"`
	Status     int             `json:"status"`
	Content    string          `json:"content_type"`
	RetryAfter string          `json:"retry_after,omitempty"`
	Body       json.RawMessage `json:"body,omitempty"`
	// Request is the JSON request body that was sent (after placeholder
	// expansion); for a multipart request it is an object of the text fields
	// and file field names, where a name sent more than once holds the array
	// of its values in the order sent. PollBodies holds every poll response.
	// Both exist so the sanitizing exporter can register the structure it will
	// meet; they are never compared and never committed.
	// Chunks are the JSON payloads of an SSE response, Done whether it ended
	// with the [DONE] sentinel.
	Chunks     []json.RawMessage `json:"chunks,omitempty"`
	Done       bool              `json:"done,omitempty"`
	Request    json.RawMessage   `json:"request,omitempty"`
	PollBodies []json.RawMessage `json:"poll_bodies,omitempty"`
	Polls      []PollObs         `json:"polls,omitempty"`
	Artifacts  []ArtifactObs     `json:"artifacts,omitempty"`
	Skipped    string            `json:"skipped,omitempty"`
	Error      string            `json:"error,omitempty"`
}

// Bundle is everything a run recorded. A bundle of the official API is kept
// privately (never committed): it can hold account data.
type Bundle struct {
	Provider     string   `json:"provider"`
	API          string   `json:"api"`
	Target       string   `json:"target"`
	BaseURL      string   `json:"base_url"`
	RanAt        string   `json:"ran_at"`
	Requests     int      `json:"requests"`
	PaidRequests int      `json:"paid_requests"`
	Obs          []Obs    `json:"obs"`
	Vars         []string `json:"vars,omitempty"`
}

// Run executes steps against target in order.
func Run(ctx context.Context, provider, api string, target Target, steps []Step, now func() time.Time, seed map[string]string) (*Bundle, error) {
	if now == nil {
		now = time.Now
	}
	if target.AuthHeader == "" {
		target.AuthHeader = "Authorization"
		if target.AuthPrefix == "" {
			target.AuthPrefix = "Bearer "
		}
	}
	if target.Client == nil {
		target.Client = &http.Client{Timeout: 5 * time.Minute}
	}
	if target.Download == nil {
		target.Download = &http.Client{Timeout: 5 * time.Minute}
	}
	bundle := &Bundle{Provider: provider, API: api, Target: target.Name, BaseURL: target.BaseURL, RanAt: now().UTC().Format(time.RFC3339)}
	vars := map[string]string{}
	for name, value := range seed {
		vars[name] = value
	}
	for _, step := range steps {
		obs := Obs{Step: step.Name, Method: step.Method, Path: step.Path, AuthHeader: authLabel(target, step)}
		if step.RequireVar != "" && vars[step.RequireVar] == "" {
			obs.Skipped = "variable " + step.RequireVar + " not set"
			bundle.Obs = append(bundle.Obs, obs)
			continue
		}
		if err := runStep(ctx, target, step, vars, &obs, bundle); err != nil {
			obs.Error = err.Error()
			bundle.Obs = append(bundle.Obs, obs)
			return bundle, fmt.Errorf("align: step %s: %w", step.Name, err)
		}
		bundle.Obs = append(bundle.Obs, obs)
	}
	return bundle, nil
}

// pollState reads the task status of a poll response. A StatusPath containing
// ".*." (for example "jobs.*.status") covers every element of an array: the
// state is the first status that is not terminal yet, otherwise "Failed"-like
// terminal statuses win over the rest, otherwise the common terminal status.
func pollState(body []byte, poll *Poll) string {
	before, after, found := strings.Cut(poll.StatusPath, ".*.")
	if !found {
		return lookup(body, poll.StatusPath)
	}
	value, ok := walk(body, before)
	list, isList := value.([]any)
	if !ok || !isList || len(list) == 0 {
		return ""
	}
	terminal := ""
	for _, element := range list {
		raw, _ := json.Marshal(element)
		status := lookup(raw, after)
		if !contains(poll.Terminal, status) {
			return status
		}
		if terminal == "" || status != poll.Terminal[0] {
			terminal = status
		}
	}
	return terminal
}

// SeedFromBundle evaluates every step's Capture against the response the
// bundle recorded for it, so an export of a few steps knows the ids an earlier
// step of the same official run produced.
func SeedFromBundle(steps []Step, bundle *Bundle) map[string]string {
	vars := map[string]string{}
	byStep := map[string]Obs{}
	for _, obs := range bundle.Obs {
		byStep[obs.Step] = obs
	}
	for _, step := range steps {
		obs, ok := byStep[step.Name]
		if !ok || obs.Skipped != "" {
			continue
		}
		for name, dot := range step.Capture {
			if value := lookup(obs.Body, dot); value != "" {
				vars[name] = value
			}
		}
	}
	return vars
}

func authLabel(target Target, step Step) string {
	if step.NoAuth {
		return "none"
	}
	return target.AuthHeader + ": " + strings.TrimSpace(target.AuthPrefix) + " <key>"
}

func runStep(ctx context.Context, target Target, step Step, vars map[string]string, obs *Obs, bundle *Bundle) error {
	path := expand(step.Path, vars)
	if strings.Contains(path, "{") {
		return fmt.Errorf("unresolved placeholder in %s", path)
	}
	obs.Resolved = path
	attempts := 1
	if step.Poll != nil {
		attempts = step.Poll.Max
		if attempts < 1 {
			attempts = 1
		}
	}
	if step.Paid {
		bundle.PaidRequests++
	}
	var final []byte
	for i := 0; i < attempts; i++ {
		if i > 0 {
			if !target.NoPollDelay {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(step.Poll.Interval):
				}
			}
		}
		status, header, body, sent, err := send(ctx, target, step, path, vars)
		bundle.Requests++
		if err != nil {
			return err
		}
		obs.Status = status
		obs.Request = sent
		obs.Content = baseMedia(header.Get("Content-Type"))
		obs.RetryAfter = header.Get("Retry-After")
		final = body
		if step.Poll == nil || status/100 != 2 {
			break
		}
		state := pollState(body, step.Poll)
		progress := ""
		if step.Poll.ProgressPath != "" {
			progress = lookup(body, step.Poll.ProgressPath)
		}
		obs.Polls = append(obs.Polls, PollObs{Status: state, Progress: progress})
		obs.PollBodies = append(obs.PollBodies, append(json.RawMessage(nil), body...))
		if contains(step.Poll.Terminal, state) {
			break
		}
		if i == attempts-1 {
			return fmt.Errorf("poll did not reach a terminal status in %d requests (last %q)", attempts, state)
		}
	}
	if obs.Content == "text/event-stream" {
		obs.Chunks, obs.Done = parseSSE(final)
	}
	if jsonMedia(obs.Content) && len(final) > 0 {
		var probe any
		if err := json.Unmarshal(final, &probe); err != nil {
			return fmt.Errorf("response is not JSON: %w", err)
		}
		obs.Body = append(json.RawMessage(nil), final...)
	}
	for name, dot := range step.Capture {
		if value := lookup(final, dot); value != "" {
			vars[name] = value
			bundle.Vars = append(bundle.Vars, name)
		}
	}
	if obs.Status/100 == 2 && len(step.Fetch) > 0 && !target.SkipFetch {
		// The fields are fetched in name order, so the n of <step>-<n>.<ext> names
		// the same artifact in every run. An artifact that could not be written
		// locally ends the step (and the run) after every artifact was tried: the
		// poll that handed out the URLs is free to repeat, the paid steps after it
		// are not.
		var keepErr error
		n := 0
		for _, dot := range step.Fetch {
			urls := urlsAt(final, dot)
			fields := make([]string, 0, len(urls))
			for field := range urls {
				fields = append(fields, field)
			}
			sort.Strings(fields)
			for _, field := range fields {
				n++
				var save *artifactSave
				if target.ArtifactDir != "" {
					save = &artifactSave{Dir: target.ArtifactDir, Step: step.Name, N: n}
				}
				artifact, err := fetch(ctx, target.Download, field, urls[field], save)
				obs.Artifacts = append(obs.Artifacts, artifact)
				if err != nil && keepErr == nil {
					keepErr = err
				}
			}
		}
		return keepErr
	}
	return nil
}

func send(ctx context.Context, target Target, step Step, path string, vars map[string]string) (int, http.Header, []byte, json.RawMessage, error) {
	var body io.Reader
	var sent json.RawMessage
	contentType := ""
	switch {
	case step.Multipart != nil || len(step.Form) > 0 || len(step.Files) > 0:
		var buffer bytes.Buffer
		writer := multipart.NewWriter(&buffer)
		// sent lists the text fields and the file field names, so the sanitizing
		// exporter registers the part names and values it will meet; a name sent
		// more than once lists every value, one part each.
		fields := formRecord{}
		for _, field := range step.Form {
			value := expand(field.Value, vars)
			if target.FormHook != nil {
				value = target.FormHook(field.Name, value)
			}
			if err := writer.WriteField(field.Name, value); err != nil {
				return 0, nil, nil, nil, err
			}
			fields.add(field.Name, value)
		}
		files := append([]File(nil), step.Files...)
		if step.Multipart != nil {
			files = append(files, *step.Multipart)
		}
		for _, file := range files {
			if file.FromVar != "" {
				data, ok := []byte(nil), false
				if target.FileHook != nil {
					data, ok = target.FileHook(file.Field, file.Filename)
				}
				if !ok {
					var err error
					if data, err = download(ctx, target.Download, vars[file.FromVar]); err != nil {
						return 0, nil, nil, nil, fmt.Errorf("download %s: %w", file.FromVar, err)
					}
				}
				file.Data = data
			}
			header := make(textproto.MIMEHeader)
			header.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q; filename=%q`, file.Field, file.Filename))
			header.Set("Content-Type", file.MediaType)
			part, err := writer.CreatePart(header)
			if err != nil {
				return 0, nil, nil, nil, err
			}
			if _, err := part.Write(file.Data); err != nil {
				return 0, nil, nil, nil, err
			}
		}
		if err := writer.Close(); err != nil {
			return 0, nil, nil, nil, err
		}
		for _, file := range files {
			fields.add(file.Field, "")
		}
		sent = fields.encode()
		body, contentType = &buffer, writer.FormDataContentType()
	case step.Body != nil:
		value := step.Body
		if target.BodyHook != nil {
			value = target.BodyHook(step.Name, value)
		}
		raw, err := json.Marshal(value)
		if err != nil {
			return 0, nil, nil, nil, err
		}
		raw = []byte(expand(string(raw), vars))
		body, contentType, sent = bytes.NewReader(raw), "application/json", append(json.RawMessage(nil), raw...)
	}
	request, err := http.NewRequestWithContext(ctx, step.Method, strings.TrimRight(target.BaseURL, "/")+path, body)
	if err != nil {
		return 0, nil, nil, nil, err
	}
	request.Header.Set("Accept", "application/json")
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	if !step.NoAuth {
		request.Header.Set(target.AuthHeader, target.AuthPrefix+target.Key)
	}
	response, err := target.Client.Do(request)
	if err != nil {
		return 0, nil, nil, nil, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	return response.StatusCode, response.Header, raw, sent, err
}

// formRecord is what a multipart request put on the wire, by part name, in
// the order the parts were written: the value of every text part and an empty
// string for every file part (file bytes are never recorded). A name can repeat
// (an array of values such as Rodin's image_label, one part per picture); the
// upstream sees one part per value, so the record keeps them all.
type formRecord map[string][]string

func (f formRecord) add(name, value string) { f[name] = append(f[name], value) }

// encode writes the record as a JSON object. A name sent once keeps its plain
// string, so the record of a step without repeated names is what it always
// was; a name sent more than once is the array of its values in the order they
// were sent, which the sanitizing exporter registers one by one.
func (f formRecord) encode() json.RawMessage {
	fields := make(map[string]any, len(f))
	for name, values := range f {
		if len(values) == 1 {
			fields[name] = values[0]
			continue
		}
		fields[name] = values
	}
	raw, _ := json.Marshal(fields)
	return raw
}

func download(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	if !strings.HasPrefix(url, "http") {
		return nil, fmt.Errorf("variable holds no URL")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", response.StatusCode)
	}
	return io.ReadAll(io.LimitReader(response.Body, 64<<20))
}

// fetch downloads one artifact URL and records what it served. With save set
// the bytes of a 200 answer are also kept in a new file. The error is only for a
// file that could not be written locally; a network failure, a status other than
// 200 or an artifact over the cap is recorded in the observation (Error) and the
// run goes on.
func fetch(ctx context.Context, client *http.Client, field, url string, save *artifactSave) (ArtifactObs, error) {
	obs := ArtifactObs{Field: field}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return obs, nil
	}
	response, err := client.Do(request)
	if err != nil {
		if save != nil {
			obs.Error = "fetch: " + withoutURL(err)
		}
		return obs, nil
	}
	defer response.Body.Close()
	obs.Status = response.StatusCode
	obs.ContentType = baseMedia(response.Header.Get("Content-Type"))
	if save != nil && response.StatusCode == http.StatusOK {
		err := save.keep(&obs, url, response.Body)
		return obs, err
	}
	head := &headWriter{}
	_, _ = io.Copy(head, io.LimitReader(response.Body, fetchCap))
	obs.Size = head.total
	obs.Format = detectFormat(head.head, head.total)
	if save != nil {
		obs.Error = fmt.Sprintf("status %d: not saved", response.StatusCode)
	}
	return obs, nil
}

// DetectFormat names a delivered file's format from its bytes.
func DetectFormat(data []byte) string {
	return detectFormat(data[:min(len(data), formatHead)], int64(len(data)))
}

// formatHead is how many leading bytes detectFormat reads.
const formatHead = 512

// detectFormat is DetectFormat for a file of total bytes whose first bytes are
// head (at least min(total, formatHead) of them): a large artifact is named
// without holding it in memory.
func detectFormat(head []byte, total int64) string {
	switch {
	case bytes.HasPrefix(head, []byte("glTF")):
		return "glb"
	case bytes.HasPrefix(head, []byte("Kaydara FBX Binary")):
		return "fbx-binary"
	case bytes.HasPrefix(head, []byte("\x89PNG\r\n\x1a\n")):
		return "png"
	case len(head) >= 12 && string(head[:4]) == "RIFF" && string(head[8:12]) == "WEBP":
		return "webp"
	case bytes.HasPrefix(head, []byte("\xff\xd8\xff")):
		return "jpeg"
	case bytes.HasPrefix(head, []byte("PK\x03\x04")), bytes.HasPrefix(head, []byte("PK\x05\x06")):
		return "zip"
	case bytes.HasPrefix(head, []byte("solid")):
		return "stl-ascii"
	case total >= 84 && len(head) >= 84 && int64(uint32(head[80])|uint32(head[81])<<8|uint32(head[82])<<16|uint32(head[83])<<24)*50+84 == total:
		return "stl-binary"
	case total > 0 && !bytes.Contains(head, []byte{0}):
		return "text"
	}
	return "binary"
}

func expand(text string, vars map[string]string) string {
	for name, value := range vars {
		text = strings.ReplaceAll(text, "{"+name+"}", value)
	}
	return text
}

func baseMedia(value string) string {
	media, _, err := mime.ParseMediaType(value)
	if err != nil {
		return strings.ToLower(strings.TrimSpace(value))
	}
	return media
}

func jsonMedia(media string) bool {
	return media == "application/json" || strings.HasSuffix(media, "+json")
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// lookup reads the scalar at a dot path as text ("" when absent or not a
// scalar).
func lookup(raw []byte, dot string) string {
	value, ok := walk(raw, dot)
	if !ok {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	case json.Number:
		return typed.String()
	case bool:
		return strconv.FormatBool(typed)
	}
	return ""
}

func walk(raw []byte, dot string) (any, bool) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, false
	}
	for _, part := range strings.Split(dot, ".") {
		switch typed := value.(type) {
		case map[string]any:
			next, ok := typed[part]
			if !ok {
				return nil, false
			}
			value = next
		case []any:
			// A numeric segment indexes an array ("image_urls.0").
			index, err := strconv.Atoi(part)
			if err != nil || index < 0 || index >= len(typed) {
				return nil, false
			}
			value = typed[index]
		default:
			return nil, false
		}
	}
	return value, true
}

// urlsAt returns field-name → URL for a dot path; a trailing "*" collects every
// URL string below the object or array at the base path, nested ones included
// ("result.basic_animations.walking_glb_url", "image_urls.0").
func urlsAt(raw []byte, dot string) map[string]string {
	found := map[string]string{}
	if base, ok := strings.CutSuffix(dot, ".*"); ok {
		value, present := walk(raw, base)
		if !present {
			return found
		}
		collectURLs(base, value, found)
		return found
	}
	if text := lookup(raw, dot); strings.HasPrefix(text, "http") {
		found[dot] = text
	}
	return found
}

func collectURLs(path string, value any, found map[string]string) {
	switch typed := value.(type) {
	case string:
		if strings.HasPrefix(typed, "http") {
			found[path] = typed
		}
	case map[string]any:
		for key, child := range typed {
			collectURLs(path+"."+key, child, found)
		}
	case []any:
		for index, child := range typed {
			collectURLs(path+"."+strconv.Itoa(index), child, found)
		}
	}
}

// Profile is a version's alignment script together with the sanitizing hints
// and the written explanations of known differences.
type Profile struct {
	Provider string
	API      string
	// Official is the official base URL (documentation only).
	Official string
	// Steps is the single script sent to both targets.
	Steps []Step
	// Capture seeds the sanitizing exporter (Connection, Scenario, Projection,
	// PublicHosts, HostAlias).
	Capture CaptureConfig
	// FakeBodyHook, when set, rewrites request bodies for the fake run only;
	// every difference it introduces must be listed in RequestNotes.
	FakeBodyHook func(step string, body any) any
	// RequestNotes documents deliberate differences between the request sent
	// to the official API and the one sent to the fake.
	RequestNotes []string
	// Notes explains known, accepted differences by Diff.Key().
	Notes Notes
}

// parseSSE returns the JSON payloads of "data:" events and whether the stream
// ended with [DONE].
func parseSSE(raw []byte) (chunks []json.RawMessage, done bool) {
	for _, line := range strings.Split(string(raw), "\n") {
		payload, ok := strings.CutPrefix(strings.TrimRight(line, "\r"), "data:")
		if !ok {
			continue
		}
		payload = strings.TrimSpace(payload)
		if payload == "[DONE]" {
			done = true
			continue
		}
		var probe any
		if json.Unmarshal([]byte(payload), &probe) == nil {
			chunks = append(chunks, json.RawMessage(payload))
		}
	}
	return chunks, done
}
