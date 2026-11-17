// Package assetlinks 为任务产物签发短时下载令牌。
package assetlinks

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// KindTaskAsset 标记令牌用途为任务产物下载，避免与输入资产令牌混用。
const KindTaskAsset = "task_asset"

// ErrInvalidToken 表示令牌格式、签名、字段或有效期校验未通过。
var ErrInvalidToken = errors.New("签名下载令牌无效或已过期")

type Payload struct {
	Kind    string             `json:"kind"`
	TaskID  generation.TaskID  `json:"task_id"`
	AssetID generation.AssetID `json:"asset_id"`
	OwnerID generation.OwnerID `json:"owner_id"`
	Expires int64              `json:"expires"`
}

type Signer struct {
	key []byte
	now func() time.Time
}

func NewSigner(key []byte) *Signer {
	return NewSignerWithClock(key, time.Now)
}

func NewSignerWithClock(key []byte, now func() time.Time) *Signer {
	if now == nil {
		now = time.Now
	}
	return &Signer{key: KeyForTaskAssets(key), now: now}
}

// KeyForTaskAssets derives the task-asset signing key from the deployment's
// asset signing key. Both token families share one configured secret; the
// derivation keeps a token of one family from verifying in the other (the
// input-asset verifier ignores unknown JSON fields, so a shared raw key alone
// would not separate them).
func KeyForTaskAssets(base []byte) []byte {
	if len(base) == 0 {
		return nil
	}
	mac := hmac.New(sha256.New, base)
	_, _ = mac.Write([]byte("plinth/task-asset-download/v1"))
	return mac.Sum(nil)
}

func (s *Signer) Sign(
	taskID generation.TaskID,
	assetID generation.AssetID,
	ownerID generation.OwnerID,
	ttl time.Duration,
) (string, time.Time, error) {
	if len(s.key) == 0 {
		return "", time.Time{}, errors.New("签名下载密钥未配置")
	}
	if taskID == "" || assetID == "" || ownerID == "" {
		return "", time.Time{}, errors.New("签名下载缺少任务、产物或所有者标识")
	}
	if ttl <= 0 {
		return "", time.Time{}, errors.New("签名下载有效期必须为正")
	}
	expires := s.now().Add(ttl).UTC()
	payload, err := json.Marshal(Payload{
		Kind:    KindTaskAsset,
		TaskID:  taskID,
		AssetID: assetID,
		OwnerID: ownerID,
		Expires: expires.Unix(),
	})
	if err != nil {
		return "", time.Time{}, err
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	token := encoded + "." + base64.RawURLEncoding.EncodeToString(s.signature(encoded))
	return token, expires, nil
}

func (s *Signer) Verify(token string) (Payload, error) {
	if len(s.key) == 0 {
		return Payload{}, ErrInvalidToken
	}
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return Payload{}, ErrInvalidToken
	}
	provided, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || !hmac.Equal(provided, s.signature(parts[0])) {
		return Payload{}, ErrInvalidToken
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return Payload{}, ErrInvalidToken
	}
	var payload Payload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return Payload{}, ErrInvalidToken
	}
	if payload.Kind != KindTaskAsset ||
		payload.TaskID == "" ||
		payload.AssetID == "" ||
		payload.OwnerID == "" ||
		payload.Expires <= s.now().Unix() {
		return Payload{}, ErrInvalidToken
	}
	return payload, nil
}

func (s *Signer) signature(payload string) []byte {
	mac := hmac.New(sha256.New, s.key)
	_, _ = mac.Write([]byte(payload))
	return mac.Sum(nil)
}
