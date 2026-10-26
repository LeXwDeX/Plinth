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
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// These declarations are frozen with the Offering; no product format/price
// table is maintained in the adapter. Each child supplies one required slot.
const maximumNativeConversionFormats = 8

// An explicit HTTP 429 refuses a create request without accepting it (Tripo
// Rate Limits page, read 2026-10-22: code 1007 for the request rate, code 2000
// with Retry-After for a full concurrency pool; back off from 1 s, doubling,
// at most 32 s, up to 5 retries). The same private child is sent again under a
// new permit. Every refusal is one private progress stage, so a run's retry
// budget is also bounded by the 18-point export band.
const (
	conversionItemRateLimited         = "rate_limited"
	maximumConversionRateLimitRetries = 5
	maximumConversionRateLimitBackoff = 32 * time.Second
	maximumConversionRateLimitWait    = 5 * time.Minute
	nativeConversionProgressBand      = 18
)

// nativeConversionProgressUnits is the number of private stages of a run: a
// submission and a success per format plus the refusal budget.
func nativeConversionProgressUnits(count int) int {
	return 2*count + min(maximumConversionRateLimitRetries, nativeConversionProgressBand-2*count)
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
	return min(time.Second<<(retry-1), maximumConversionRateLimitBackoff)
}

type nativeConversionOptions struct {
	SourceAlias    string                     `json:"source_alias"`
	MaxSourceBytes int64                      `json:"max_source_bytes"`
	Parameters     map[string]json.RawMessage `json:"parameters"`
	Formats        []nativeConversionFormat   `json:"formats"`
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
	sourceFound := false
	for _, a := range options.ResponseAliases {
		if a.Key == c.SourceAlias && a.Role == "model" {
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
	for _, f := range c.Formats {
		if !uploadedTokenValid(f.Format) || seen[f.Format] || f.Role == "" || f.Variant == "" || f.MediaType == "" {
			return nil, nativeProcessingProtocolError()
		}
		seen[f.Format] = true
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

func nativeConversionBody(c *nativeConversionOptions, locator, format string) map[string]any {
	body := map[string]any{"input": locator, "format": format}
	for key, raw := range c.Parameters {
		var v any
		_ = json.Unmarshal(raw, &v)
		body[key] = v
	}
	return body
}

// reserved lists upstream IDs that a private export child can never reuse.
func nativeConversionCheckpoint(s nativeConversionLedger, options providerOptions, reserved ...string) error {
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
	remaining, inflight := false, false
	refusals := 0
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
		if remaining || inflight || !validNativeCredit(item.CreditsConsumed) || item.RateLimitRetries < 0 {
			return nativeProcessingProtocolError()
		}
		refusals += item.RateLimitRetries
		if refusals > 0 && (s.ProgressUnits == 0 || refusals > s.ProgressUnits-2*len(c.Formats)) {
			return nativeProcessingProtocolError()
		}
		switch item.Status {
		case conversionItemDispatching:
			if item.RemoteID != "" {
				return nativeProcessingProtocolError()
			}
			inflight = true
		case conversionItemSubmitted:
			if !uploadedTokenValid(item.RemoteID) {
				return nativeProcessingProtocolError()
			}
			inflight = true
		case conversionItemDone:
			if !uploadedTokenValid(item.RemoteID) || item.Locator == "" {
				return nativeProcessingProtocolError()
			}
		case conversionItemRateLimited:
			if item.RemoteID != "" || item.RateLimitRetries < 1 || item.Locator != "" {
				return nativeProcessingProtocolError()
			}
			inflight = true
		case conversionItemFailed:
			inflight = true
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
		if !ok || err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || !reflect.DeepEqual(request, nativeConversionBody(c, locator, f.Format)) {
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
		if item.Status == conversionItemDone {
			steps += 2
		}
	}
	obs.Phase = generation.UpstreamRunning
	obs.Progress = 80 + nativeConversionProgressBand*steps/units
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
	used := 0
	for _, item := range s.Conversions {
		used += item.RateLimitRetries
	}
	if used >= s.ProgressUnits-2*count {
		s.Conversions[format] = conversionItem{Status: conversionItemFailed, RateLimitRetries: retries}
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
	if format, item, ok := inflightConversion(s.Conversions); ok {
		child, err := a.observeNativeTask(ctx, options, item.RemoteID, string(request.AttemptID)+":convert:"+format)
		if err != nil {
			return generation.Observation{}, err
		}
		if len(child.CreditsConsumed) > 0 {
			item.CreditsConsumed = bytes.Clone(child.CreditsConsumed)
		}
		phase, _ := conversionChildPhase(a.statusMapping, child.Status)
		switch phase {
		case generation.UpstreamCompleted:
			item.Locator = firstHTTPOutput(child.Output, "model_url")
			if item.Locator == "" {
				return generation.Observation{}, nativeProcessingProtocolError()
			}
			item.Status = conversionItemDone
		case generation.UpstreamFailed, generation.UpstreamCanceled:
			item.Status = conversionItemFailed
			s.Conversions[format] = item
			return nativeConversionFailure(request, run, base, "Tripo 必需格式转换失败"), nil
		}
		s.Conversions[format] = item
		return nativeConversionRunning(request, run, base, len(c.Formats)), nil
	}
	for _, f := range c.Formats {
		retries := 0
		if item, ok := s.Conversions[f.Format]; ok {
			if item.Status == conversionItemDone {
				continue
			}
			if item.Status != conversionItemRateLimited {
				return nativeConversionFailure(request, run, base, "Tripo 必需转换结果无法确认"), nil
			}
			retries = item.RateLimitRetries
		}
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
		body := nativeConversionBody(c, source.Locator, f.Format)
		s.ConversionRequests[f.Format], _ = json.Marshal(body)
		s.Conversions[f.Format] = conversionItem{Status: conversionItemDispatching, RateLimitRetries: retries}
		permit, err := generation.PrivateDispatchPermit(ctx, generation.DispatchIntent{Bytes: run.encode()})
		if err != nil {
			return generation.Observation{}, err
		}
		var raw json.RawMessage
		key := run.submissionKey + ":convert:" + f.Format
		if err := a.client.DoJSON(ctx, http.MethodPost, options.ConvertEndpoint, body, key, &raw, true); err != nil {
			if retryAfter, refused := explicitRateLimit(err); refused && permit != nil && s.ProgressUnits != 0 {
				var provider *generation.ProviderError
				errors.As(err, &provider)
				return refusedNativeConversion(request, run, base, len(c.Formats), f.Format, retries, retryAfter, permit, provider.Failure), nil
			}
			return generation.Observation{}, err
		}
		data, err := a.rigCheckData(raw)
		if err != nil {
			var provider *generation.ProviderError
			if errors.As(err, &provider) && provider.Failure != nil && provider.Failure.Class != generation.FailureProtocol {
				s.Conversions[f.Format] = conversionItem{Status: conversionItemFailed}
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
		s.Conversions[f.Format] = conversionItem{Status: conversionItemSubmitted, RemoteID: remote, RateLimitRetries: retries}
		obs := nativeConversionRunning(request, run, base, len(c.Formats))
		obs.Dispatch = permit
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
