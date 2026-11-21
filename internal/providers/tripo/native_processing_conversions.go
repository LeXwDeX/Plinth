package tripo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// These declarations are frozen with the Offering; no product format/price
// table is maintained in the adapter. Each child supplies one required slot.
const maximumNativeConversionFormats = 8

// nativeConversionProgressBand is how many progress points the private export
// steps of a run share. The retry budgets are per format and unbounded by this
// band, so the reported progress is clamped rather than made to fit.
const nativeConversionProgressBand = 18

// nativeConversionProgressUnits is the number of private stages of a run: a
// submission and a success per format, plus one refusal per format. It fixes
// the pacing of the reported progress; a run that spends more retries than that
// simply stops advancing.
func nativeConversionProgressUnits(count int) int {
	return 2*count + min(maximumConversionRateLimitRetries*count, nativeConversionProgressBand-2*count)
}

// explicitRateLimit reports an unambiguous HTTP 429 refusal and its Retry-After.
func explicitRateLimit(err error) (time.Duration, bool) {
	var provider *generation.ProviderError
	if !errors.As(err, &provider) || provider.Failure == nil {
		return 0, false
	}
	f := provider.Failure
	if f.Ambiguous || f.UpstreamStatus != http.StatusTooManyRequests || f.Class != generation.FailureRateLimited {
		return 0, false
	}
	return f.RetryAfter, true
}

func conversionRateLimitWait(retryAfter time.Duration, retry int) time.Duration {
	if retryAfter > 0 {
		return min(retryAfter, maximumConversionRateLimitWait)
	}
	return conversionRetryBackoff(retry)
}

type nativeConversionOptions struct {
	SourceAlias    string                     `json:"source_alias"`
	MaxSourceBytes int64                      `json:"max_source_bytes"`
	Parameters     map[string]json.RawMessage `json:"parameters"`
	Formats        []nativeConversionFormat   `json:"formats"`
	// Bindings (conversion_parameters.go) forward public parameters of the frozen
	// input to the export requests. The generation Offerings declare them for
	// their export options (#278); a processing Offering declares them for the
	// options a session purpose bounds (#292), the texture size, and keeps the
	// rest of its export fixed in Parameters.
	Bindings []nativeConversionBinding `json:"bindings,omitempty"`
}
type nativeConversionFormat struct {
	Format    string `json:"format"`
	Role      string `json:"role"`
	Variant   string `json:"variant"`
	MediaType string `json:"media_type"`
}

// nativeConversionLedger is the private export record shared by every
// protocol that delivers declared formats from one upstream GLB result.
type nativeConversionLedger struct {
	ConversionSource   *nativeConversionSource    `json:"conversion_source,omitempty"`
	Conversions        map[string]conversionItem  `json:"conversions,omitempty"`
	ConversionRequests map[string]json.RawMessage `json:"conversion_requests,omitempty"`
	// ProgressUnits is fixed when the first child is planned. A ledger written
	// before refusals were retried has none and keeps its original stages.
	ProgressUnits int `json:"progress_units,omitempty"`
}

// nativeConversionRun binds a ledger to its owning checkpoint. encode must
// serialize the whole checkpoint, including the ledger's current contents, so
// each private dispatch intent is durable before its POST.
type nativeConversionRun struct {
	ledger         *nativeConversionLedger
	baseRemoteID   string
	sourceRemoteID string
	submissionKey  string
	encode         func() json.RawMessage
}

type nativeConversionSource struct {
	Alias  string `json:"alias"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// nativeFormatNames are the export formats a native conversion declaration
// produces, in declaration order (the frozen order every private step follows).
func nativeFormatNames(c *nativeConversionOptions) []string {
	names := make([]string, 0, len(c.Formats))
	for _, f := range c.Formats {
		names = append(names, f.Format)
	}
	return names
}

// failedFormat returns the first format whose retries are exhausted, or "".
func failedFormat(names []string, conversions map[string]conversionItem) string {
	for _, format := range names {
		if item, ok := conversions[format]; ok && item.Status == conversionItemFailed {
			return format
		}
	}
	return ""
}

// countSubmitted counts the formats whose child is in flight.
func countSubmitted(names []string, conversions map[string]conversionItem) int {
	count := 0
	for _, format := range names {
		if item, ok := conversions[format]; ok && item.Status == conversionItemSubmitted {
			count++
		}
	}
	return count
}

func nativeConversionControl(o generation.Offering, options providerOptions, name string) bool {
	return options.NativeConversion != nil && o.Cost != nil && (name == o.Cost.SelectionParameter || name == o.Cost.ConsentParameter)
}
func nativeConversionDeclaration(o generation.Offering, options providerOptions) (map[int]bool, error) {
	matched := map[int]bool{}
	c := options.NativeConversion
	if c == nil {
		if options.ConvertEndpoint != "" {
			return nil, nativeProcessingProtocolError()
		}
		return matched, nil
	}
	if o.PostSuccess != nil || o.Cost == nil || o.Cost.ExtraCredits <= 0 || o.Cost.SelectionParameter == "" || o.Cost.ConsentParameter == "" || o.Cost.SelectionParameter == o.Cost.ConsentParameter || o.CostPolicy == nil || !validRigCheckRoute(options.ConvertEndpoint, false) || c.MaxSourceBytes <= 0 || c.MaxSourceBytes > maximumTripoModelBytes || len(c.Formats) == 0 || len(c.Formats) > maximumNativeConversionFormats || len(c.Parameters) == 0 {
		return nil, nativeProcessingProtocolError()
	}
	// The file every export is made from is the processing result, kept as its own
	// required binary GLB slot: the model slot itself (the original Offerings) or, when the
	// Offering exports the GLB too (#305), the original file beside it.
	sourceFound := false
	for _, a := range options.ResponseAliases {
		if a.Key == c.SourceAlias {
			for _, out := range o.Outputs {
				if out.Role == a.Role && out.Variant == a.Variant && len(out.MediaTypes) == 1 && out.MediaTypes[0] == "model/gltf-binary" && out.Delivery == schema.AssetDeliveryRequired && out.Minimum != nil && *out.Minimum == 1 {
					sourceFound = true
				}
			}
		}
	}
	if !sourceFound {
		return nil, nativeProcessingProtocolError()
	}
	for key, raw := range c.Parameters {
		if key == "input" || key == "format" || key == "model" {
			return nil, nativeProcessingProtocolError()
		}
		var v any
		if strictUploadedJSON(raw, &v) != nil || v == nil {
			return nil, nativeProcessingProtocolError()
		}
		switch v.(type) {
		case string, bool, float64:
		default:
			return nil, nativeProcessingProtocolError()
		}
		if strings.Contains(key, ".") || !setNativeWire(map[string]any{}, key, v) {
			return nil, nativeProcessingProtocolError()
		}
	}
	seen := map[string]bool{}
	exportFormats := make([]string, 0, len(c.Formats))
	for _, f := range c.Formats {
		if !uploadedTokenValid(f.Format) || seen[f.Format] || f.Role == "" || f.Variant == "" || f.MediaType == "" {
			return nil, nativeProcessingProtocolError()
		}
		seen[f.Format] = true
		exportFormats = append(exportFormats, f.Format)
		index := -1
		for i, out := range o.Outputs {
			if out.Role == f.Role && out.Variant == f.Variant {
				if index != -1 {
					return nil, nativeProcessingProtocolError()
				}
				index = i
				if out.Delivery != schema.AssetDeliveryRequired || out.Minimum == nil || *out.Minimum != 1 || out.MaxBytes <= 0 || len(out.MediaTypes) != 1 || out.MediaTypes[0] != f.MediaType {
					return nil, nativeProcessingProtocolError()
				}
			}
		}
		if index < 0 || matched[index] {
			return nil, nativeProcessingProtocolError()
		}
		matched[index] = true
	}
	// The bound parameters are public parameters of the Offering that a purpose
	// sets (#292); the export request writes them, the processing request never
	// does. A binding that cannot be written is refused with the declaration.
	if detail := conversionBindingsDetail(o, c, exportFormats); detail != "" {
		return nil, failure(generation.FailureProtocol, false, "Tripo 原生处理转换声明无效: "+detail)
	}
	selection, consent := false, false
	for _, p := range o.Parameters {
		if p.Name == o.Cost.SelectionParameter {
			var formats []string
			if p.Type != "string-array" || !p.Required || !p.Fixed || len(p.WireValues) > 0 || strictUploadedJSON(p.Default, &formats) != nil || len(formats) != len(c.Formats) || len(p.Options) != len(formats) {
				return nil, nativeProcessingProtocolError()
			}
			optionsSeen := map[string]bool{}
			for _, option := range p.Options {
				if !seen[option.Value] || optionsSeen[option.Value] {
					return nil, nativeProcessingProtocolError()
				}
				optionsSeen[option.Value] = true
			}
			for i, f := range c.Formats {
				if formats[i] != f.Format {
					return nil, nativeProcessingProtocolError()
				}
			}
			selection = true
		}
		if p.Name == o.Cost.ConsentParameter {
			if p.Type != "boolean" || p.Fixed || len(p.WireValues) > 0 {
				return nil, nativeProcessingProtocolError()
			}
			consent = true
		}
	}
	if !selection || !consent {
		return nil, nativeProcessingProtocolError()
	}
	return matched, nil
}

// nativeConversionBody is the models/convert request of one export: the source
// and the format, the declaration's constants and the values the bindings read
// from the frozen input (the texture size a purpose bounds, #292). An Offering
// without bindings never reads the input, so its request is exactly the constant
// one every Task frozen before the bindings existed was accepted with.
func nativeConversionBody(o generation.Offering, c *nativeConversionOptions, input json.RawMessage, locator, format string) (map[string]any, error) {
	body := map[string]any{"input": locator, "format": format}
	for key, raw := range c.Parameters {
		var v any
		_ = json.Unmarshal(raw, &v)
		body[key] = v
	}
	if len(c.Bindings) == 0 {
		return body, nil
	}
	bound, err := conversionBoundValues(o, c, input, format)
	if err != nil {
		return nil, err
	}
	for key, value := range bound {
		body[key] = value
	}
	return body, nil
}

// reserved lists upstream IDs that a private export child can never reuse. The
// recorded requests are checked against the frozen Offering and input, so a
// ledger cannot carry an export the Task was not accepted with.
func nativeConversionCheckpoint(s nativeConversionLedger, o generation.Offering, input json.RawMessage, options providerOptions, reserved ...string) error {
	c := options.NativeConversion
	if len(s.Conversions) == 0 {
		if s.ConversionSource != nil || len(s.ConversionRequests) != 0 || s.ProgressUnits != 0 {
			return nativeProcessingProtocolError()
		}
		return nil
	}
	if c != nil && s.ProgressUnits != 0 && s.ProgressUnits != nativeConversionProgressUnits(len(c.Formats)) {
		return nativeProcessingProtocolError()
	}
	if c == nil || s.ConversionSource == nil || s.ConversionSource.Alias != c.SourceAlias || !uploadedHashValid(s.ConversionSource.SHA256) || s.ConversionSource.Size <= 0 || s.ConversionSource.Size > c.MaxSourceBytes || len(s.ConversionRequests) != len(s.Conversions) {
		return nativeProcessingProtocolError()
	}
	remaining := false
	remotes := map[string]bool{}
	for _, id := range reserved {
		if id != "" {
			remotes[id] = true
		}
	}
	for _, f := range c.Formats {
		item, ok := s.Conversions[f.Format]
		if !ok {
			remaining = true
			continue
		}
		// Formats are recorded in the frozen order: once one is missing, no
		// later format may be present. Several formats may be in flight at once
		// (bounded concurrency), so an unfinished earlier format does not
		// invalidate a later one.
		if remaining || !validNativeCredit(item.CreditsConsumed) ||
			item.RateLimitRetries < 0 || item.RateLimitRetries > maximumConversionRateLimitRetries ||
			item.RetryAttempts < 0 || item.RetryAttempts > maximumConversionChildRetries {
			return nativeProcessingProtocolError()
		}
		switch item.Status {
		case conversionItemDispatching:
			if item.RemoteID != "" || item.Locator != "" {
				return nativeProcessingProtocolError()
			}
		case conversionItemSubmitted:
			if !uploadedTokenValid(item.RemoteID) || item.Locator != "" {
				return nativeProcessingProtocolError()
			}
		case conversionItemDone:
			if !uploadedTokenValid(item.RemoteID) || item.Locator == "" {
				return nativeProcessingProtocolError()
			}
		case conversionItemRateLimited:
			if item.RemoteID != "" || item.Locator != "" || item.RateLimitRetries < 1 {
				return nativeProcessingProtocolError()
			}
		case conversionItemRetryPending:
			if item.RemoteID != "" || item.Locator != "" {
				return nativeProcessingProtocolError()
			}
		case conversionItemFailed, conversionItemUnknown:
			if item.RemoteID != "" || item.Locator != "" {
				return nativeProcessingProtocolError()
			}
		default:
			return nativeProcessingProtocolError()
		}
		if item.RemoteID != "" {
			if remotes[item.RemoteID] {
				return nativeProcessingProtocolError()
			}
			remotes[item.RemoteID] = true
		}
		var request map[string]any
		if strictUploadedJSON(s.ConversionRequests[f.Format], &request) != nil {
			return nativeProcessingProtocolError()
		}
		locator, ok := request["input"].(string)
		parsed, err := url.Parse(locator)
		if !ok || err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil {
			return nativeProcessingProtocolError()
		}
		if want, err := nativeConversionBody(o, c, input, locator, f.Format); err != nil || !reflect.DeepEqual(request, want) {
			return nativeProcessingProtocolError()
		}
	}
	count := 0
	for _, f := range c.Formats {
		if _, ok := s.Conversions[f.Format]; ok {
			count++
		}
	}
	if count != len(s.Conversions) {
		return nativeProcessingProtocolError()
	}
	return nil
}
func validNativeCredit(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return true
	}
	var n float64
	return !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) && json.Unmarshal(raw, &n) == nil && n >= 0 && !math.IsInf(n, 0) && !math.IsNaN(n)
}

// The same strict v3 task parser serves the base job and all private children.
func (a *Adapter) observeNativeTask(ctx context.Context, options providerOptions, id, key string) (task, error) {
	var raw json.RawMessage
	if err := a.client.DoJSON(ctx, http.MethodGet, strings.ReplaceAll(options.ObserveEndpoint, "{task_id}", url.PathEscape(id)), nil, key, &raw, false); err != nil {
		return task{}, err
	}
	data, err := a.rigCheckData(raw)
	if err != nil {
		var provider *generation.ProviderError
		if errors.As(err, &provider) && provider.Failure != nil && provider.Failure.Class == generation.FailureProtocol {
			return task{}, nativeProcessingProtocolError()
		}
		return task{}, err
	}
	var value task
	if json.Unmarshal(data["task_id"], &value.TaskID) != nil || value.TaskID != id || json.Unmarshal(data["status"], &value.Status) != nil || value.Status == "" {
		return task{}, nativeProcessingProtocolError()
	}
	if p, ok := data["progress"]; ok {
		if bytes.Equal(bytes.TrimSpace(p), []byte("null")) || json.Unmarshal(p, &value.Progress) != nil || value.Progress < 0 || value.Progress > 100 {
			return task{}, nativeProcessingProtocolError()
		}
	}
	if output, ok := data["output"]; ok && !bytes.Equal(bytes.TrimSpace(output), []byte("null")) {
		if json.Unmarshal(output, &value.Output) != nil {
			return task{}, nativeProcessingProtocolError()
		}
	}
	if credit, ok := data["credits_consumed"]; ok {
		if len(credit) == 0 || !validNativeCredit(credit) {
			return task{}, nativeProcessingProtocolError()
		}
		value.CreditsConsumed = bytes.Clone(credit)
	}
	rule, ok := declaredStatusRule(a.statusMapping, strings.ToLower(strings.TrimSpace(value.Status)))
	if !ok {
		return task{}, nativeProcessingProtocolError()
	}
	switch rule.Phase {
	case "accepted", "running", "completed", "failed", "canceled":
	default:
		return task{}, nativeProcessingProtocolError()
	}
	return value, nil
}
func nativeConversionObservation(request generation.ObserveRequest, s *nativeConversionRun, obs generation.Observation) generation.Observation {
	obs.Opaque = s.encode()
	obs.CheckpointBaseSHA256 = uploadedDigest(request.Opaque)
	obs.DedupeKey = privateDedupeKey(string(obs.Phase), uploadedDigest(obs.Opaque))
	return obs
}

// Reserve base progress from the first poll. Each confirmed child submission
// and success advances one workflow stage, so a paid receipt is committed with
// its remote ID without fabricated source sequence or timestamps.
func nativeConversionRunning(request generation.ObserveRequest, s *nativeConversionRun, obs generation.Observation, count int) generation.Observation {
	steps, units := 0, s.ledger.ProgressUnits
	if units == 0 {
		units = 2 * count
	}
	for _, item := range s.ledger.Conversions {
		steps += item.RateLimitRetries
		if item.Status == conversionItemSubmitted {
			steps++
		}
		// A format waiting to be resubmitted, or one whose outcome is unknown,
		// is one more stage: the reported progress must advance with the state,
		// or the coordinator would discard the round as stale.
		if item.Status == conversionItemRetryPending || item.Status == conversionItemUnknown {
			steps++
		}
		if item.Status == conversionItemDone {
			steps += 2
		}
	}
	obs.Phase = generation.UpstreamRunning
	// A run that spends more retries than the band allows stops advancing at
	// the top of the export band rather than reporting past it.
	obs.Progress = min(80+nativeConversionProgressBand*steps/units, 80+nativeConversionProgressBand)
	obs.Artifacts = nil
	return nativeConversionObservation(request, s, obs)
}
func nativeConversionFailure(request generation.ObserveRequest, s *nativeConversionRun, obs generation.Observation, detail string) generation.Observation {
	obs.Phase = generation.UpstreamFailed
	obs.Artifacts = nil
	obs.Failure = &generation.Failure{Class: generation.FailureRejected, SafeDetail: detail}
	return nativeConversionObservation(request, s, obs)
}

// refusedNativeConversion settles an explicit 429 as not dispatched. Within the
// run's budget the child waits as rate_limited for Retry-After or the backoff;
// after it, the export fails as a definite, non-retryable rate limit, so no
// new Attempt repeats the paid base operation.
func refusedNativeConversion(request generation.ObserveRequest, run *nativeConversionRun, base generation.Observation, count int, format string, retries int, retryAfter time.Duration, permit *generation.DispatchReceipt, cause *generation.Failure) generation.Observation {
	s := run.ledger
	permit.Result = generation.DispatchResultNotDispatched
	if retries >= maximumConversionRateLimitRetries {
		item := s.Conversions[format]
		item.Status = conversionItemFailed
		item.RemoteID = ""
		s.Conversions[format] = item
		obs := nativeConversionFailure(request, run, base, "Tripo 必需格式转换持续被限流，有限重试已用尽")
		refusal := *cause
		refusal.Class, refusal.Retryable, refusal.Ambiguous, refusal.RetryAfter = generation.FailureRateLimited, false, false, 0
		refusal.SafeDetail = obs.Failure.SafeDetail
		obs.Failure = &refusal
		obs.Dispatch = permit
		return obs
	}
	retries++
	s.Conversions[format] = conversionItem{Status: conversionItemRateLimited, RateLimitRetries: retries}
	obs := nativeConversionRunning(request, run, base, count)
	obs.PollAfter = conversionRateLimitWait(retryAfter, retries)
	obs.Dispatch = permit
	return obs
}

func (a *Adapter) observeNativeConversions(ctx context.Context, request generation.ObserveRequest, options providerOptions, run *nativeConversionRun, base generation.Observation) (generation.Observation, error) {
	c := options.NativeConversion
	s := run.ledger
	if base.Phase != generation.UpstreamCompleted {
		if len(s.Conversions) > 0 && (base.Phase == generation.UpstreamAccepted || base.Phase == generation.UpstreamRunning) {
			return generation.Observation{}, nativeProcessingProtocolError()
		}
		if base.Progress > 80 {
			base.Progress = 80
		}
		return nativeConversionObservation(request, run, base), nil
	}
	var source generation.ArtifactClaim
	for _, alias := range options.ResponseAliases {
		if alias.Key == c.SourceAlias {
			for _, claim := range base.Artifacts {
				if claim.Role == alias.Role && claim.Variant == alias.Variant {
					source = claim
				}
			}
		}
	}
	if source.Locator == "" {
		return generation.Observation{}, nativeProcessingProtocolError()
	}
	if s.Conversions == nil {
		s.Conversions = map[string]conversionItem{}
		s.ConversionRequests = map[string]json.RawMessage{}
		s.ProgressUnits = nativeConversionProgressUnits(len(c.Formats))
	}
	names := nativeFormatNames(c)
	// Poll every in-flight child (bounded concurrency, stable order). A child
	// that ended failed or cancelled enters the backoff wait; the format is
	// resubmitted only while its own budget lasts (Tripo bills neither). One
	// round that learned an outcome reports it and dispatches nothing, so a
	// stage always belongs to exactly one child.
	changed := false
	for _, f := range c.Formats {
		item, ok := s.Conversions[f.Format]
		if !ok || item.Status != conversionItemSubmitted {
			continue
		}
		child, err := a.observeNativeTask(ctx, options, item.RemoteID, string(request.AttemptID)+":convert:"+f.Format)
		if err != nil {
			return generation.Observation{}, err
		}
		status := conversionStatusWord(child.Status)
		rule, known := declaredStatusRule(a.statusMapping, status)
		if !known {
			return generation.Observation{}, nativeProcessingProtocolError()
		}
		if a.unknownStatusExpired(item.RemoteID, status, rule) {
			item.Attempts = append(item.Attempts, conversionAttempt{
				RemoteID:        item.RemoteID,
				CreditsConsumed: bytes.Clone(child.CreditsConsumed),
				Outcome:         conversionAttemptUnknown,
			})
			item.Status = conversionItemUnknown
			item.Locator = ""
			item.RemoteID = ""
			s.Conversions[f.Format] = item
			return nativeConversionFailure(request, run, base, "Tripo 必需格式转换结果不明"), nil
		}
		phase, _ := conversionChildPhase(a.statusMapping, child.Status)
		locator := ""
		if phase == generation.UpstreamCompleted {
			locator = firstHTTPOutput(child.Output, "model_url")
		}
		previous := item.Status
		updated, _ := applyConversionChild(item, phase, locator, child.CreditsConsumed)
		s.Conversions[f.Format] = updated
		if updated.Status != previous || len(updated.Attempts) != len(item.Attempts) {
			changed = true
		}
	}
	if failed := failedFormat(names, s.Conversions); failed != "" {
		// A required format spent its retry budget: the step fails now, before
		// another paid export is started.
		return nativeConversionFailure(request, run, base, "Tripo 必需格式转换失败"), nil
	}
	if changed {
		// A child reached an outcome this round: report it and dispatch
		// nothing, so a stage belongs to exactly one child. The refresh and the
		// claims follow in the next round.
		return nativeConversionRunning(request, run, base, len(c.Formats)), nil
	}
	if allFormatsDone(names, s.Conversions) {
		// Every required format is converted: refresh below.
	} else if inflight := countSubmitted(names, s.Conversions); inflight < conversionConcurrency(options) {
		if format, ok := nextConversionDispatch(names, s.Conversions); ok {
			if _, ok := generation.DispatchCheckpointerFromContext(ctx); !ok {
				return generation.Observation{}, failure(generation.FailureRejected, false, "Tripo 私有转换缺少持久执行派发许可")
			}
			if err := planPostSuccessConversion(request.Offering, request.Input); err != nil {
				return generation.Observation{}, err
			}
			binding, err := a.bindNativeConversionSource(ctx, request.Offering, source, c)
			if err != nil {
				return generation.Observation{}, err
			}
			if s.ConversionSource != nil && *s.ConversionSource != binding {
				return generation.Observation{}, failure(generation.FailureIntegrity, false, "Tripo 处理产物字节与冻结转换来源不一致")
			}
			s.ConversionSource = &binding
			body, err := nativeConversionBody(request.Offering, c, request.Input, source.Locator, format)
			if err != nil {
				return generation.Observation{}, err
			}
			s.ConversionRequests[format], _ = json.Marshal(body)
			previous := s.Conversions[format]
			attempts := previous.RetryAttempts
			if previous.Status == conversionItemRetryPending || previous.Status == conversionItemRateLimited {
				attempts++
			}
			s.Conversions[format] = conversionItem{
				Status:           conversionItemDispatching,
				RateLimitRetries: previous.RateLimitRetries,
				RetryAttempts:    attempts,
				Attempts:         previous.Attempts,
			}
			key := run.submissionKey + ":convert:" + format
			if attempts > 0 {
				key += ":retry:" + strconv.Itoa(attempts)
			}
			permit, err := generation.PrivateDispatchPermit(ctx, generation.DispatchIntent{Bytes: run.encode()})
			if err != nil {
				return generation.Observation{}, err
			}
			var raw json.RawMessage
			if err := a.client.DoJSON(ctx, http.MethodPost, options.ConvertEndpoint, body, key, &raw, true); err != nil {
				if retryAfter, refused := explicitRateLimit(err); refused && permit != nil && s.ProgressUnits != 0 {
					var provider *generation.ProviderError
					errors.As(err, &provider)
					return refusedNativeConversion(request, run, base, len(c.Formats), format, previous.RateLimitRetries, retryAfter, permit, provider.Failure), nil
				}
				return generation.Observation{}, err
			}
			data, err := a.rigCheckData(raw)
			if err != nil {
				var provider *generation.ProviderError
				if errors.As(err, &provider) && provider.Failure != nil && provider.Failure.Class != generation.FailureProtocol {
					s.Conversions[format] = conversionItem{
						Status:           conversionItemFailed,
						RateLimitRetries: previous.RateLimitRetries,
						RetryAttempts:    previous.RetryAttempts,
						Attempts:         previous.Attempts,
					}
					obs := nativeConversionFailure(request, run, base, "Tripo 必需格式转换请求被拒绝")
					// Keep upstream diagnostics private while exposing only bounded product text.
					rejection := *provider.Failure
					rejection.SafeDetail = obs.Failure.SafeDetail
					obs.Failure = &rejection
					obs.Dispatch = permit
					return obs, nil
				}
				return generation.Observation{}, submissionUnknown("Tripo 私有转换创建响应无法确认，已停止重发")
			}
			var remote string
			if json.Unmarshal(data["task_id"], &remote) != nil || !uploadedTokenValid(remote) || remote == run.baseRemoteID {
				return generation.Observation{}, submissionUnknown("Tripo 私有转换创建响应缺少 task_id，已停止重发")
			}
			for _, item := range s.Conversions {
				if item.RemoteID == remote {
					return generation.Observation{}, submissionUnknown("Tripo 私有转换返回重复任务标识，已停止重发")
				}
			}
			if run.sourceRemoteID != "" && remote == run.sourceRemoteID {
				return generation.Observation{}, submissionUnknown("Tripo 私有转换返回来源任务标识，已停止重发")
			}
			s.Conversions[format] = conversionItem{
				Status:           conversionItemSubmitted,
				RemoteID:         remote,
				RateLimitRetries: previous.RateLimitRetries,
				RetryAttempts:    attempts,
				Attempts:         previous.Attempts,
			}
			obs := nativeConversionRunning(request, run, base, len(c.Formats))
			obs.Dispatch = permit
			return obs, nil
		}
	}
	if !allFormatsDone(names, s.Conversions) {
		// Nothing is in flight, no retry is waiting and no format is left to
		// start: a required format ended failed (its budget spent) or its
		// outcome is unknown. The step fails; it never delivers a partial set.
		if !hasPendingRetry(names, s.Conversions) &&
			countSubmitted(names, s.Conversions) == 0 &&
			len(unstartedFormats(names, s.Conversions)) == 0 {
			return nativeConversionFailure(request, run, base, "Tripo 必需格式转换失败"), nil
		}
		// Still waiting: children in flight or a retry in its backoff.
		obs := nativeConversionRunning(request, run, base, len(c.Formats))
		if wait := conversionRetryWait(s.Conversions); wait > 0 {
			obs.PollAfter = wait
		}
		return obs, nil
	}
	// Refresh URLs through GET only, then recheck the original source bytes. The
	// source GLB remains its own claim; a conversion never replaces it.
	binding, err := a.bindNativeConversionSource(ctx, request.Offering, source, c)
	if err != nil {
		return generation.Observation{}, err
	}
	if s.ConversionSource == nil || *s.ConversionSource != binding {
		return generation.Observation{}, failure(generation.FailureIntegrity, false, "Tripo 原始处理产物字节已改变")
	}
	for _, f := range c.Formats {
		item := s.Conversions[f.Format]
		child, err := a.observeNativeTask(ctx, options, item.RemoteID, string(request.AttemptID)+":convert:"+f.Format+":refresh")
		if err != nil {
			return generation.Observation{}, err
		}
		phase, _ := conversionChildPhase(a.statusMapping, child.Status)
		locator := firstHTTPOutput(child.Output, "model_url")
		if phase != generation.UpstreamCompleted || locator == "" {
			return generation.Observation{}, nativeProcessingProtocolError()
		}
		if len(child.CreditsConsumed) > 0 {
			item.CreditsConsumed = bytes.Clone(child.CreditsConsumed)
		}
		item.Locator = locator
		s.Conversions[f.Format] = item
		base.Artifacts = append(base.Artifacts, generation.ArtifactClaim{ID: "convert:" + f.Format, Role: f.Role, Variant: f.Variant, MediaType: f.MediaType, Locator: locator})
	}
	for i := range base.Artifacts {
		claim := &base.Artifacts[i]
		if claim.ID == source.ID && claim.Role == source.Role && claim.Variant == source.Variant {
			claim.Metadata = nativeSourceMaterializationMetadata(*s.ConversionSource)
		}
	}
	return nativeConversionObservation(request, run, base), nil
}
