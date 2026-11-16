package wirecapture

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"time"
)

// ValidateMetadata checks the public metadata of one exchange.
func ValidateMetadata(meta Metadata, p Policy) error {
	if meta.CapturedAt.IsZero() || meta.CapturedAt.Location() != time.UTC {
		return fmt.Errorf("%w: captured_at", ErrUnsafePublicValue)
	}
	switch meta.Provenance {
	case ProvenanceCaptured, ProvenanceOfficialExample, ProvenanceDerived, ProvenanceSynthetic:
	default:
		return fmt.Errorf("%w: provenance", ErrUnsafePublicValue)
	}
	for name, value := range map[string]string{
		"scenario": meta.Scenario, "connection": meta.Connection, "endpoint": meta.Endpoint,
		"api_version": meta.APIVersion, "requested_model": meta.RequestedModel,
		"reported_model": meta.ReportedModel, "server_build": meta.ServerBuild,
		"app_revision": meta.AppRevision, "catalog_revision": meta.CatalogRevision,
		"environment": meta.Environment,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%w: metadata %s", ErrUnsafePublicValue, name)
		}
		if err := checkText(value, false); err != nil {
			return err
		}
	}
	for _, value := range []string{meta.Scenario, meta.Connection, meta.Environment, meta.AppRevision, meta.CatalogRevision} {
		if !identifierRE.MatchString(value) {
			return fmt.Errorf("%w: metadata identifier", ErrUnsafePublicValue)
		}
	}
	for _, value := range []string{meta.APIVersion, meta.RequestedModel, meta.ReportedModel, meta.ServerBuild} {
		if !modelRE.MatchString(value) {
			return fmt.Errorf("%w: metadata model", ErrUnsafePublicValue)
		}
	}
	if meta.Protocol != "json" && meta.Protocol != "sse" && meta.Protocol != "none" &&
		meta.Protocol != "media" && meta.Protocol != "multipart" {
		return fmt.Errorf("%w: metadata protocol", ErrUnsafePublicValue)
	}
	method, path, ok := strings.Cut(meta.Endpoint, " ")
	if !ok || !methods[method] {
		return fmt.Errorf("%w: metadata endpoint", ErrUnsafePublicValue)
	}
	if clean, err := SanitizePath(path, p); err != nil || clean != path {
		return fmt.Errorf("%w: metadata endpoint path", ErrUnsafePublicValue)
	}
	if meta.Provenance == ProvenanceCaptured {
		for _, value := range []string{meta.AppRevision, meta.CatalogRevision, meta.Environment} {
			if value == "unknown" {
				return fmt.Errorf("%w: captured revision", ErrUnsafePublicValue)
			}
		}
	}
	return nil
}

// Validate checks that every exchange is complete, public and canonical under p:
// metadata, request, response and payloads are re-sanitized and must come out
// unchanged. Nothing unvalidated may be written or replayed.
func Validate(exchanges []Exchange, p Policy) error {
	if len(exchanges) == 0 {
		return fmt.Errorf("%w: empty fixture", ErrInvalidExchange)
	}
	seen := make(map[int]bool, len(exchanges))
	for i := range exchanges {
		sequence := exchanges[i].Sequence
		if sequence <= 0 || seen[sequence] {
			return fmt.Errorf("exchange %d: %w: sequence", i, ErrInvalidExchange)
		}
		seen[sequence] = true
		if err := validateExchange(exchanges[i], p); err != nil {
			return fmt.Errorf("exchange %d: %w", i, err)
		}
	}
	return nil
}

func validateExchange(e Exchange, p Policy) error {
	if !e.Complete || e.Invalid != "" {
		switch {
		case strings.Contains(e.Invalid, "unsupported"):
			return fmt.Errorf("%w: unsupported exchange", ErrUnsupportedBody)
		case strings.Contains(e.Invalid, "partial SSE"):
			return ErrPartialSSE
		case strings.Contains(e.Invalid, "unknown JSON key"):
			return fmt.Errorf("%w: %s", ErrUnknownKey, e.Invalid)
		default:
			return ErrInvalidExchange
		}
	}
	if err := ValidateMetadata(e.Metadata, p); err != nil {
		return err
	}
	if err := validateRequest(e.Request, e.Metadata, p); err != nil {
		return err
	}
	if err := validateResponse(e.Request.Method, e.Response, e.Metadata, p); err != nil {
		return err
	}
	if e.Metadata.Endpoint != e.Request.Method+" "+e.Request.Path {
		return fmt.Errorf("%w: metadata endpoint", ErrUnsafePublicValue)
	}
	if e.Metadata.Protocol != ProtocolFor(e.Response.ContentType) {
		return fmt.Errorf("%w: metadata protocol", ErrUnsafePublicValue)
	}
	return nil
}

func validateRequest(r Request, metadata Metadata, p Policy) error {
	if !r.Complete || !methods[r.Method] {
		return fmt.Errorf("%w: request", ErrUnsafePublicValue)
	}
	if clean, err := SanitizeHost(r.Host, p); err != nil || clean != r.Host {
		return fmt.Errorf("%w: request host", ErrUnsafePublicValue)
	}
	if clean, err := SanitizePath(r.Path, p); err != nil || clean != r.Path {
		return fmt.Errorf("%w: request path", ErrUnsafePublicValue)
	}
	base, err := ContentBase(r.ContentType)
	if err != nil {
		return err
	}
	if base != r.ContentType {
		return fmt.Errorf("%w: request content type", ErrUnsafePublicValue)
	}
	headers, err := SanitizeHeaders(HeadersToHTTP(r.Headers), true, p)
	if err != nil || !reflect.DeepEqual(headers, r.Headers) {
		return fmt.Errorf("%w: request headers", ErrUnsafePublicValue)
	}
	return validatePayload(r.Body, r.ContentType, metadata, true, p)
}

func validateResponse(method string, r Response, metadata Metadata, p Policy) error {
	if !r.Complete || r.Status < 100 || r.Status > 599 {
		return fmt.Errorf("%w: response", ErrUnsafePublicValue)
	}
	if r.Status >= 300 && r.Status < 400 {
		return fmt.Errorf("%w: unsupported 3xx response", ErrUnsupportedBody)
	}
	emptyAllowed := method == http.MethodHead || r.Status < 200 || r.Status == http.StatusNoContent || r.Status == http.StatusNotModified
	emptyPayload := r.Body.Encoding == PayloadNone && r.Body.Transform == "" && len(r.Body.JSON) == 0 &&
		r.Body.SSE == "" && r.Body.Media == nil && r.Body.Multipart == nil
	if emptyAllowed && !emptyPayload {
		return fmt.Errorf("%w: response body is not allowed", ErrUnsupportedBody)
	}
	base, err := ContentBase(r.ContentType)
	if err != nil {
		return err
	}
	if base != r.ContentType {
		return fmt.Errorf("%w: response content type", ErrUnsafePublicValue)
	}
	headers, err := SanitizeHeaders(HeadersToHTTP(r.Headers), false, p)
	if err != nil || !reflect.DeepEqual(headers, r.Headers) {
		return fmt.Errorf("%w: response headers", ErrUnsafePublicValue)
	}
	return validatePayload(r.Body, r.ContentType, metadata, false, p)
}

func validatePayload(payload Payload, contentType string, metadata Metadata, request bool, p Policy) error {
	kind, err := PayloadKind(contentType)
	if err != nil {
		return err
	}
	if !request && kind == PayloadMultipart {
		return fmt.Errorf("%w: multipart response", ErrUnsupportedBody)
	}
	if request && kind == PayloadMedia {
		return fmt.Errorf("%w: binary request", ErrUnsupportedBody)
	}
	if payload.Encoding == PayloadNone {
		switch kind {
		case PayloadMultipart:
			return fmt.Errorf("%w: multipart payload requires parts", ErrUnsupportedBody)
		case PayloadMedia:
			return fmt.Errorf("%w: empty binary payload", ErrUnsupportedBody)
		}
		if payload.Transform != "" || kind == PayloadSSE || len(payload.JSON) != 0 || payload.SSE != "" ||
			payload.Media != nil || payload.Multipart != nil {
			return fmt.Errorf("%w: empty payload", ErrUnsafePublicValue)
		}
		return nil
	}
	if payload.Encoding != kind {
		return fmt.Errorf("%w: payload encoding", ErrUnsafePublicValue)
	}
	switch kind {
	case PayloadJSON:
		if payload.Transform != "" || payload.SSE != "" || payload.Media != nil || payload.Multipart != nil || !json.Valid(payload.JSON) {
			return fmt.Errorf("%w: JSON payload", ErrUnsafePublicValue)
		}
		clean, err := sanitizeJSON(payload.JSON, p)
		if err != nil {
			return err
		}
		if !bytes.Equal(clean, payload.JSON) {
			return fmt.Errorf("%w: JSON payload is not canonical", ErrUnsafePublicValue)
		}
		return nil
	case PayloadSSE:
		if payload.Transform != TransformNormalizedSSE || payload.Encoding != PayloadSSE || payload.SSE == "" ||
			len(payload.JSON) != 0 || payload.Media != nil || payload.Multipart != nil {
			return fmt.Errorf("%w: SSE payload", ErrUnsafePublicValue)
		}
		clean, err := sanitizeSSE([]byte(payload.SSE), p)
		if err != nil {
			return err
		}
		if clean != payload.SSE {
			return fmt.Errorf("%w: SSE payload is not canonical", ErrUnsafePublicValue)
		}
		return nil
	case PayloadMultipart:
		if !request || payload.Multipart == nil || len(payload.JSON) != 0 || payload.SSE != "" ||
			payload.Transform != "" || payload.Media != nil {
			return fmt.Errorf("%w: multipart payload", ErrUnsafePublicValue)
		}
		return validateMultipart(*payload.Multipart, metadata, p)
	case PayloadMedia:
		if request || payload.Media == nil || len(payload.JSON) != 0 || payload.SSE != "" ||
			payload.Transform != "" || payload.Multipart != nil {
			return fmt.Errorf("%w: media payload", ErrUnsafePublicValue)
		}
		if err := validateMedia(*payload.Media, metadata); err != nil {
			return err
		}
		if payload.Media.MediaType != contentType {
			return fmt.Errorf("%w: media payload content type", ErrUnsafePublicValue)
		}
		return nil
	default:
		return fmt.Errorf("%w: body without content type", ErrUnsupportedBody)
	}
}

func validateMedia(media Media, metadata Metadata) error {
	if !supportedMediaTypes[media.MediaType] || media.Length <= 0 {
		return fmt.Errorf("%w: media descriptor", ErrUnsafePublicValue)
	}
	if media.Recipe == "" {
		if media.Substituted || metadata.Provenance != ProvenanceCaptured {
			return fmt.Errorf("%w: private media provenance", ErrUnsafePublicValue)
		}
		return nil
	}
	spec, ok := syntheticRecipes[media.Recipe]
	if !ok || media.MediaType != spec.mediaType || media.Length != spec.length || !media.Substituted {
		return fmt.Errorf("%w: media recipe descriptor", ErrUnsafePublicValue)
	}
	if metadata.Provenance != ProvenanceDerived && metadata.Provenance != ProvenanceSynthetic {
		return fmt.Errorf("%w: derived media provenance", ErrUnsafePublicValue)
	}
	return nil
}

func validateMultipart(multipart Multipart, metadata Metadata, p Policy) error {
	if len(multipart.Parts) == 0 {
		return fmt.Errorf("%w: empty multipart", ErrUnsafePublicValue)
	}
	for _, part := range multipart.Parts {
		if !identifierRE.MatchString(part.Name) || !p.Keys[part.Name] {
			return fmt.Errorf("%w: multipart part name", ErrUnknownKey)
		}
		if part.Media != nil {
			if part.Value != "" {
				return fmt.Errorf("%w: multipart part is both field and file", ErrUnsafePublicValue)
			}
			if err := validateMedia(*part.Media, metadata); err != nil {
				return err
			}
			continue
		}
		if part.Value == "" {
			continue
		}
		if err := checkText(part.Value, false); err != nil {
			return err
		}
		clean, err := p.resolve(part.Value, 0)
		if err != nil || clean != part.Value {
			return fmt.Errorf("%w: multipart field", ErrUnsafePublicValue)
		}
	}
	return nil
}

// HeadersToHTTP turns a public header map back into an http.Header.
func HeadersToHTTP(headers map[string]string) http.Header {
	out := http.Header{}
	for name, value := range headers {
		out.Set(name, value)
	}
	return out
}
