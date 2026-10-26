package tripo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"hash"
	"io"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/httpjson"
)

// Only byte identity crosses the claim boundary. Routes, hosts, limits and
// product configuration remain in the frozen Offering, never in this marker.
type nativeSourceMaterializationBinding struct {
	Version int    `json:"version"`
	SHA256  string `json:"sha256"`
	Size    int64  `json:"size"`
}

func nativeSourceMaterializationMetadata(source nativeConversionSource) json.RawMessage {
	raw, _ := json.Marshal(map[string]any{"native_conversion_source": map[string]any{"version": 1, "sha256": source.SHA256, "size": source.Size}})
	return raw
}
func nativeSourceMaterializationError() error {
	return failure(generation.FailureIntegrity, false, "Tripo 原始处理产物与冻结转换来源字节不一致")
}
func decodeNativeSourceMaterializationBinding(raw json.RawMessage, maximum int64) (nativeSourceMaterializationBinding, error) {
	var empty nativeSourceMaterializationBinding
	var metadata map[string]json.RawMessage
	if strictUploadedJSON(raw, &metadata) != nil || len(metadata) != 1 {
		return empty, nativeSourceMaterializationError()
	}
	marker, exists := metadata["native_conversion_source"]
	if !exists {
		return empty, nativeSourceMaterializationError()
	}
	var fields map[string]json.RawMessage
	var binding nativeSourceMaterializationBinding
	if strictUploadedJSON(marker, &fields) != nil || len(fields) != 3 || len(fields["version"]) == 0 || len(fields["sha256"]) == 0 || len(fields["size"]) == 0 || json.Unmarshal(marker, &binding) != nil || binding.Version != 1 || !uploadedHashValid(binding.SHA256) || binding.Size < 12 || binding.Size > maximum {
		return empty, nativeSourceMaterializationError()
	}
	return binding, nil
}

// A new native-conversion revision requires identity evidence for its original
// GLB slot. Historical native and other claims retain the existing opener.
func nativeSourceMaterializationOptions(o generation.Offering, claim generation.ArtifactClaim) (*nativeConversionOptions, error) {
	var options providerOptions
	if smartSegmentProtocol(o) {
		var err error
		if options, err = smartSegmentOptions(o); err != nil {
			return nil, err
		}
		if options.NativeConversion == nil {
			return nil, nil
		}
	} else {
		if !nativeProcessingCapability(o.Capability) {
			return nil, nil
		}
		if strictUploadedJSON(o.ProviderOptions, &options) != nil {
			return nil, nativeProcessingProtocolError()
		}
		if options.NativeConversion == nil {
			return nil, nil
		}
		if _, _, err := nativeProcessingDeclaration(o); err != nil {
			return nil, err
		}
	}
	for _, alias := range options.ResponseAliases {
		if alias.Key == options.NativeConversion.SourceAlias && claim.Role == alias.Role && claim.Variant == alias.Variant {
			return options.NativeConversion, nil
		}
	}
	return nil, nil
}

func (a *Adapter) openNativeMaterializationSource(ctx context.Context, o generation.Offering, claim generation.ArtifactClaim, c *nativeConversionOptions) (io.ReadCloser, generation.AssetMetadata, error) {
	binding, err := decodeNativeSourceMaterializationBinding(claim.Metadata, c.MaxSourceBytes)
	if err != nil {
		return nil, generation.AssetMetadata{}, err
	}
	if !validNativeSourceClaim(claim) || a.nativeSourceClient == nil {
		return nil, generation.AssetMetadata{}, nativeSourceMaterializationError()
	}
	body, metadata, err := httpjson.OpenClaim(ctx, a.nativeSourceClient, claim, o.AssetHosts, false)
	if err != nil {
		return nil, generation.AssetMetadata{}, err
	}
	if metadata.Size >= 0 && metadata.Size != binding.Size {
		body.Close()
		return nil, generation.AssetMetadata{}, nativeSourceMaterializationError()
	}
	return &nativeBoundSourceReader{body: body, digest: sha256.New(), binding: binding}, metadata, nil
}

// Store.Put consumes Read through EOF before inspecting or renaming its temp
// file. Check identity here, rather than Close (whose error cannot prevent a
// commit), including readers that return their final bytes together with EOF.
type nativeBoundSourceReader struct {
	body     io.ReadCloser
	digest   hash.Hash
	binding  nativeSourceMaterializationBinding
	consumed int64
	verified bool
	failure  error
}

func (r *nativeBoundSourceReader) Read(p []byte) (int, error) {
	if r.failure != nil {
		return 0, r.failure
	}
	if r.verified {
		return 0, io.EOF
	}
	if len(p) == 0 {
		return 0, nil
	}
	// Read at most one excess byte; never consume an unbounded changed body.
	remaining := r.binding.Size - r.consumed + 1
	if int64(len(p)) > remaining {
		p = p[:int(remaining)]
	}
	n, err := r.body.Read(p)
	r.consumed += int64(n)
	if n > 0 {
		_, _ = r.digest.Write(p[:n])
	}
	if r.consumed > r.binding.Size {
		r.failure = nativeSourceMaterializationError()
		return n, r.failure
	}
	if err == io.EOF {
		if r.consumed != r.binding.Size || hex.EncodeToString(r.digest.Sum(nil)) != r.binding.SHA256 {
			r.failure = nativeSourceMaterializationError()
			return n, r.failure
		}
		r.verified = true
	} else if err != nil {
		r.failure = err
	}
	return n, err
}
func (r *nativeBoundSourceReader) Close() error { return r.body.Close() }
