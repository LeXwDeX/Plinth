package assetlinks_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/assetlinks"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

var testKey = []byte(strings.Repeat("k", 32))

func fixedClock(start time.Time) (func() time.Time, *time.Time) {
	current := start
	return func() time.Time { return current }, &current
}

func signRaw(t *testing.T, key []byte, payload any) string {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	encoded := base64.RawURLEncoding.EncodeToString(raw)
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(encoded))
	return encoded + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func mustSign(t *testing.T, signer *assetlinks.Signer) string {
	t.Helper()
	token, _, err := signer.Sign("task-1", "asset-1", "owner-1", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func TestSignAndVerify(t *testing.T) {
	now := time.Date(2026, 11, 17, 9, 30, 0, 0, time.UTC)
	clock, _ := fixedClock(now)
	signer := assetlinks.NewSignerWithClock(testKey, clock)

	token, expires, err := signer.Sign("task-1", "asset-1", "owner-1", 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	wantExpires := now.Add(5 * time.Minute)
	if !expires.Equal(wantExpires) || expires.Location() != time.UTC {
		t.Fatalf("expires = %v, want %v in UTC", expires, wantExpires)
	}

	payload, err := signer.Verify(token)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if payload.Kind != assetlinks.KindTaskAsset {
		t.Fatalf("kind = %q", payload.Kind)
	}
	if payload.TaskID != "task-1" || payload.AssetID != "asset-1" || payload.OwnerID != "owner-1" {
		t.Fatalf("payload = %#v", payload)
	}
	if payload.Expires != wantExpires.Unix() {
		t.Fatalf("payload expires = %d, want %d", payload.Expires, wantExpires.Unix())
	}
}

func TestVerifyRejectsTamperedToken(t *testing.T) {
	now := time.Date(2026, 11, 17, 9, 30, 0, 0, time.UTC)
	clock, _ := fixedClock(now)
	signer := assetlinks.NewSignerWithClock(testKey, clock)
	token := mustSign(t, signer)
	parts := strings.Split(token, ".")

	var tamperedPayload assetlinks.Payload
	tamperedPayload.Kind = assetlinks.KindTaskAsset
	tamperedPayload.TaskID = "task-1"
	tamperedPayload.AssetID = "asset-2"
	tamperedPayload.OwnerID = "owner-1"
	tamperedPayload.Expires = now.Add(time.Minute).Unix()
	raw, err := json.Marshal(tamperedPayload)
	if err != nil {
		t.Fatal(err)
	}
	payloadTampered := base64.RawURLEncoding.EncodeToString(raw) + "." + parts[1]

	signatureTampered := parts[0] + "." + flipBase64Char(parts[1])

	for name, candidate := range map[string]string{
		"payload":   payloadTampered,
		"signature": signatureTampered,
	} {
		if _, err := signer.Verify(candidate); !errors.Is(err, assetlinks.ErrInvalidToken) {
			t.Fatalf("%s tampered token error = %v", name, err)
		}
	}
}

func flipBase64Char(segment string) string {
	if segment == "" {
		return "A"
	}
	first := segment[0]
	if first == 'A' {
		return "B" + segment[1:]
	}
	return "A" + segment[1:]
}

func TestVerifyRejectsMalformedToken(t *testing.T) {
	now := time.Date(2026, 11, 17, 9, 30, 0, 0, time.UTC)
	clock, _ := fixedClock(now)
	signer := assetlinks.NewSignerWithClock(testKey, clock)
	token := mustSign(t, signer)
	parts := strings.Split(token, ".")

	for name, candidate := range map[string]string{
		"empty":        "",
		"single":       parts[0],
		"three":        token + "." + parts[1],
		"invalid-b64":  "!!!." + parts[1],
		"invalid-json": base64.RawURLEncoding.EncodeToString([]byte("not-json")) + "." + parts[1],
	} {
		if _, err := signer.Verify(candidate); !errors.Is(err, assetlinks.ErrInvalidToken) {
			t.Fatalf("%s token error = %v", name, err)
		}
	}
}

func TestVerifyRejectsExpiredToken(t *testing.T) {
	now := time.Date(2026, 11, 17, 9, 30, 0, 0, time.UTC)
	clock, current := fixedClock(now)
	signer := assetlinks.NewSignerWithClock(testKey, clock)
	token, expires, err := signer.Sign("task-1", "asset-1", "owner-1", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	*current = expires.Add(-time.Second)
	if _, err := signer.Verify(token); err != nil {
		t.Fatalf("Verify() before expiry error = %v", err)
	}
	*current = expires
	if _, err := signer.Verify(token); !errors.Is(err, assetlinks.ErrInvalidToken) {
		t.Fatalf("expired token error = %v", err)
	}
	*current = now.Add(2 * time.Minute)
	if _, err := signer.Verify(token); !errors.Is(err, assetlinks.ErrInvalidToken) {
		t.Fatalf("expired token error = %v", err)
	}
}

func TestVerifyRejectsForeignKey(t *testing.T) {
	now := time.Date(2026, 11, 17, 9, 30, 0, 0, time.UTC)
	clock, _ := fixedClock(now)
	other := assetlinks.NewSignerWithClock([]byte(strings.Repeat("x", 32)), clock)
	verifier := assetlinks.NewSignerWithClock(testKey, clock)
	if _, err := verifier.Verify(mustSign(t, other)); !errors.Is(err, assetlinks.ErrInvalidToken) {
		t.Fatalf("foreign key token error = %v", err)
	}
}

// The deployment's asset signing key also signs input-asset links whose
// verifier ignores unknown JSON fields. A task-asset token must therefore not
// verify under the raw key, or a task link could open an input asset.
func TestVerifyRejectsTokenSignedWithTheBaseKey(t *testing.T) {
	now := time.Date(2026, 11, 17, 9, 30, 0, 0, time.UTC)
	clock, _ := fixedClock(now)
	signer := assetlinks.NewSignerWithClock(testKey, clock)
	token := signRaw(t, testKey, assetlinks.Payload{
		Kind:    assetlinks.KindTaskAsset,
		TaskID:  "task-1",
		AssetID: "asset-1",
		OwnerID: "owner-1",
		Expires: now.Add(time.Minute).Unix(),
	})
	if _, err := signer.Verify(token); !errors.Is(err, assetlinks.ErrInvalidToken) {
		t.Fatalf("base-key token error = %v", err)
	}
}

func TestVerifyRejectsWrongKind(t *testing.T) {
	now := time.Date(2026, 11, 17, 9, 30, 0, 0, time.UTC)
	clock, _ := fixedClock(now)
	signer := assetlinks.NewSignerWithClock(testKey, clock)
	token := signRaw(t, assetlinks.KeyForTaskAssets(testKey), assetlinks.Payload{
		Kind:    "input_asset",
		TaskID:  "task-1",
		AssetID: "asset-1",
		OwnerID: "owner-1",
		Expires: now.Add(time.Minute).Unix(),
	})
	if _, err := signer.Verify(token); !errors.Is(err, assetlinks.ErrInvalidToken) {
		t.Fatalf("wrong kind token error = %v", err)
	}
}

func TestSignRejectsInvalidInput(t *testing.T) {
	now := time.Date(2026, 11, 17, 9, 30, 0, 0, time.UTC)
	clock, _ := fixedClock(now)

	cases := map[string]struct {
		signer  *assetlinks.Signer
		taskID  generation.TaskID
		assetID generation.AssetID
		ownerID generation.OwnerID
		ttl     time.Duration
	}{
		"empty key":    {assetlinks.NewSignerWithClock(nil, clock), "task-1", "asset-1", "owner-1", time.Minute},
		"empty task":   {assetlinks.NewSignerWithClock(testKey, clock), "", "asset-1", "owner-1", time.Minute},
		"empty asset":  {assetlinks.NewSignerWithClock(testKey, clock), "task-1", "", "owner-1", time.Minute},
		"empty owner":  {assetlinks.NewSignerWithClock(testKey, clock), "task-1", "asset-1", "", time.Minute},
		"zero ttl":     {assetlinks.NewSignerWithClock(testKey, clock), "task-1", "asset-1", "owner-1", 0},
		"negative ttl": {assetlinks.NewSignerWithClock(testKey, clock), "task-1", "asset-1", "owner-1", -time.Minute},
	}
	for name, tc := range cases {
		if _, _, err := tc.signer.Sign(tc.taskID, tc.assetID, tc.ownerID, tc.ttl); err == nil {
			t.Fatalf("%s: Sign() error = nil", name)
		}
	}
}

func TestNewSignerWithNilClockFallsBackToNow(t *testing.T) {
	signer := assetlinks.NewSignerWithClock(testKey, nil)
	token, expires, err := signer.Sign("task-1", "asset-1", "owner-1", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := signer.Verify(token); err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if time.Until(expires) <= 0 || time.Until(expires) > time.Minute {
		t.Fatalf("expires = %v, expected about one minute ahead", expires)
	}
}

func TestSignIsDeterministic(t *testing.T) {
	now := time.Date(2026, 11, 17, 9, 30, 0, 0, time.UTC)
	clock, _ := fixedClock(now)
	signer := assetlinks.NewSignerWithClock(testKey, clock)
	first, firstExpires, err := signer.Sign("task-1", "asset-1", "owner-1", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	second, secondExpires, err := signer.Sign("task-1", "asset-1", "owner-1", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if first != second || !firstExpires.Equal(secondExpires) {
		t.Fatal("same input and clock should produce the same token")
	}
}
