package wirefixture

import "github.com/LeXwDeX/Plinth/internal/wirecapture"

// The sanitizer, Validate, synthetic media, the draft derivation and the
// upstream recorder are implemented once, in internal/wirecapture (#233),
// because the application links them too. This file re-exports them under the
// names fixtures, fakes and test tools already import, so there is a single
// implementation and no second copy of any rule.

type (
	Provenance           = wirecapture.Provenance
	Metadata             = wirecapture.Metadata
	Payload              = wirecapture.Payload
	Request              = wirecapture.Request
	Response             = wirecapture.Response
	Exchange             = wirecapture.Exchange
	Policy               = wirecapture.Policy
	Media                = wirecapture.Media
	MultipartPart        = wirecapture.MultipartPart
	Multipart            = wirecapture.Multipart
	UpstreamRecord       = wirecapture.UpstreamRecord
	UpstreamRecordConfig = wirecapture.UpstreamRecordConfig
	UpstreamRecorder     = wirecapture.UpstreamRecorder
	DraftCandidate       = wirecapture.DraftCandidate
)

const (
	ProvenanceCaptured        = wirecapture.ProvenanceCaptured
	ProvenanceOfficialExample = wirecapture.ProvenanceOfficialExample
	ProvenanceDerived         = wirecapture.ProvenanceDerived
	ProvenanceSynthetic       = wirecapture.ProvenanceSynthetic

	PayloadNone      = wirecapture.PayloadNone
	PayloadJSON      = wirecapture.PayloadJSON
	PayloadSSE       = wirecapture.PayloadSSE
	PayloadMedia     = wirecapture.PayloadMedia
	PayloadMultipart = wirecapture.PayloadMultipart

	TransformNormalizedSSE = wirecapture.TransformNormalizedSSE

	// UpstreamRecordSuffix names every record file in the private directory.
	UpstreamRecordSuffix = wirecapture.UpstreamRecordSuffix
)

var (
	ErrUnsafePublicValue     = wirecapture.ErrUnsafePublicValue
	ErrUnknownKey            = wirecapture.ErrUnknownKey
	ErrUnsupportedBody       = wirecapture.ErrUnsupportedBody
	ErrPartialSSE            = wirecapture.ErrPartialSSE
	ErrInvalidExchange       = wirecapture.ErrInvalidExchange
	ErrInsideWorktree        = wirecapture.ErrInsideWorktree
	ErrExistingCandidate     = wirecapture.ErrExistingCandidate
	ErrMediaEvidence         = wirecapture.ErrMediaEvidence
	ErrMediaResolver         = wirecapture.ErrMediaResolver
	ErrMediaMismatch         = wirecapture.ErrMediaMismatch
	ErrUpstreamRecordRefused = wirecapture.ErrUpstreamRecordRefused
)

var (
	Validate            = wirecapture.Validate
	ValidatePolicy      = wirecapture.ValidatePolicy
	WriteCandidate      = wirecapture.WriteCandidate
	WriteDraftCandidate = wirecapture.WriteDraftCandidate
	DraftReplayPolicy   = wirecapture.DraftReplayPolicy
	LoadUpstreamRecords = wirecapture.LoadUpstreamRecords
	NewUpstreamRecorder = wirecapture.NewUpstreamRecorder
	SyntheticMedia      = wirecapture.SyntheticMedia
)

// The sanitizer building blocks the test-side Recorder, deferred capture,
// Replay and private evidence compose. They are the wirecapture functions
// under the unexported names that code already uses; nothing here re-implements
// a rule.
var (
	sanitizeHost       = wirecapture.SanitizeHost
	sanitizePath       = wirecapture.SanitizePath
	sanitizeHeaders    = wirecapture.SanitizeHeaders
	sanitizeBody       = wirecapture.SanitizeBody
	parseMultipart     = wirecapture.ParseMultipart
	contentBase        = wirecapture.ContentBase
	protocolFor        = wirecapture.ProtocolFor
	validateMetadata   = wirecapture.ValidateMetadata
	publicQueryKeys    = wirecapture.PublicQueryKeys
	queryKeysAllowed   = wirecapture.QueryKeysAllowed
	publicRequestURL   = wirecapture.PublicRequestURL
	payloadKind        = wirecapture.PayloadKind
	baseMedia          = wirecapture.BaseMedia
	cloneMedia         = wirecapture.CloneMedia
	sortedExchanges    = wirecapture.SortedExchanges
	clonePolicy        = wirecapture.ClonePolicy
	sseFramed          = wirecapture.SSEFramed
	headersToHTTP      = wirecapture.HeadersToHTTP
	inside             = wirecapture.PathInside
	sameFileAncestor   = wirecapture.SameFileAncestor
	isMethod           = wirecapture.IsMethod
	supportedMediaType = wirecapture.SupportedMediaType
)
