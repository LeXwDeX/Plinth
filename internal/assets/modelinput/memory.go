package modelinput

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"

	"github.com/LeXwDeX/Plinth/internal/assets/local"
)

// ValidateGLBBytes applies the same structural and isolation contract as
// ValidateGLB without staging a file. The caller must keep raw immutable for
// the duration of this call. No dependency is opened and raw is never modified.
func ValidateGLBBytes(ctx context.Context, raw []byte, policy Policy) (Profile, error) {
	if err := policy.Validate(); err != nil {
		return Profile{}, err
	}
	if ctx.Err() != nil {
		return Profile{}, failure(Canceled, "context_canceled")
	}
	if int64(len(raw)) > policy.Limits.MaxBytes {
		return Profile{}, failure(Limit, "byte_limit")
	}
	if len(raw) < 28 {
		return Profile{}, failure(Invalid, "glb_framing")
	}
	if len(raw)%4 != 0 || !bytes.Equal(raw[:4], []byte("glTF")) || binary.LittleEndian.Uint32(raw[4:8]) != 2 || int64(binary.LittleEndian.Uint32(raw[8:12])) != int64(len(raw)) {
		return Profile{}, failure(Invalid, "glb_framing")
	}
	jsonLength := int64(binary.LittleEndian.Uint32(raw[12:16]))
	if string(raw[16:20]) != "JSON" || jsonLength <= 0 || jsonLength%4 != 0 || jsonLength > int64(len(raw))-28 {
		return Profile{}, failure(Invalid, "json_chunk")
	}
	if jsonLength > policy.Limits.MaxJSONBytes {
		return Profile{}, failure(Limit, "json_byte_limit")
	}
	binHeader := 20 + int(jsonLength)
	binLength := int64(binary.LittleEndian.Uint32(raw[binHeader : binHeader+4]))
	if string(raw[binHeader+4:binHeader+8]) != "BIN\x00" || binLength%4 != 0 || int64(binHeader)+8+binLength != int64(len(raw)) {
		return Profile{}, failure(Invalid, "bin_chunk")
	}
	v := &validator{ctx: ctx, policy: policy, remaining: policy.Limits.MaxWork, bin: raw[binHeader+8:], imageKeys: map[string]bool{}, profile: Profile{ProfileID: policy.ProfileID, Revision: policy.Revision, Size: int64(len(raw))}}
	// Reserve read, isolation traversal and the two identity hash passes.
	v.work(4 * int64(len(raw)))
	doc := v.parseJSON(raw[20:binHeader])
	if v.err != nil {
		return Profile{}, v.err
	}
	v.document(doc)
	if v.err != nil {
		return Profile{}, v.err
	}

	// A generated source was isolation-checked when its Task stored it, and the
	// store checks every conversion output again before commit. Repeating the
	// check here would fully decode each 8K texture a second time.
	if !policy.generated() {
		if err := local.ValidateDeliveredGLBBytes(raw); err != nil {
			return Profile{}, failure(Invalid, "isolation_check")
		}
	}
	if ctx.Err() != nil {
		return Profile{}, failure(Canceled, "context_canceled")
	}
	expected := sha256.Sum256(raw)
	v.profile.SHA256 = hex.EncodeToString(expected[:])
	return v.profile, nil
}
