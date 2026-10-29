package catalog

import "testing"

func TestSessionRetryIsValidatedAndProjected(t *testing.T) {
	t.Parallel()
	product, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	retry := application.Authentication.SessionRetry
	if retry.InitialDelayMilliseconds <= 0 {
		t.Fatalf("default session_retry = %+v", retry)
	}
	for name, bad := range map[string]SessionRetrySchema{
		"zero":            {},
		"backoff below":   {InitialDelayMilliseconds: 1000, MaxBackoffMilliseconds: 500, MaxRetryAfterMilliseconds: 5000},
		"retry-after low": {InitialDelayMilliseconds: 1000, MaxBackoffMilliseconds: 5000, MaxRetryAfterMilliseconds: 2000},
	} {
		broken := application.Authentication
		broken.SessionRetry = bad
		if validateAuthentication(broken) == nil {
			t.Fatalf("%s: invalid session_retry accepted", name)
		}
	}
}
