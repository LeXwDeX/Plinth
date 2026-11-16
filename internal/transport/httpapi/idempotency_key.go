package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// The browser routes that start a paid step are keyed by the client's
// Idempotency-Key header: repeating a request with the same key never starts a
// second step. A request without a usable key is refused here, by name, instead
// of reaching a service that can only call it invalid input (#302).
const (
	idempotencyKeyHeader = "Idempotency-Key"
	// idempotencyKeyRequiredCode is the public code of every such refusal, on
	// every route that reads the header.
	idempotencyKeyRequiredCode = "idempotency_key_required"
	// idempotencyKeyRequiredMessage is the Product Schema message the shared
	// refusal is worded with. Reroll words it with its own declared copy.
	idempotencyKeyRequiredMessage = "error.idempotency_key_required"
)

// idempotencyKeyRule is the length range, in bytes of the trimmed header, that
// the service behind a route enforces on the key itself. A zero maximum means
// that service sets no upper bound. The services keep their own checks as the
// last line of defense; the rule only lets the route refuse a key by name
// before it reaches them, so it has to mirror them.
type idempotencyKeyRule struct{ minimum, maximum int }

var (
	// taskIdempotencyKeyRule is the key of a task submission: SubmitPrepared and
	// the reroll service accept 1 to 200 bytes. A standalone grant is bound to
	// the key its submission repeats, so it follows the same rule. Reroll builds
	// its task key by prefixing the client's, which the kernel bounds again, so a
	// reroll key close to the limit can still be refused there as invalid input;
	// this rule only mirrors the service's own check on the header.
	taskIdempotencyKeyRule = idempotencyKeyRule{minimum: 1, maximum: 200}
	// planIdempotencyKeyRule is the key of a processing plan, whose ID derives
	// from it: processing.Create accepts 16 to 200 bytes.
	planIdempotencyKeyRule = idempotencyKeyRule{minimum: 16, maximum: 200}
	// runIdempotencyKeyRule is the key of a Skill run: skillrun.Create accepts
	// any key that is not blank.
	runIdempotencyKeyRule = idempotencyKeyRule{minimum: 1}
)

var errIdempotencyKeyBlank = errors.New("idempotency key: Idempotency-Key header is missing or blank")

// refusal is why key (the trimmed header) is not usable, or nil. The text goes
// to the access log only; it names the length, never the key.
func (rule idempotencyKeyRule) refusal(key string) error {
	switch {
	case key == "":
		return errIdempotencyKeyBlank
	case len(key) < rule.minimum:
		return fmt.Errorf("idempotency key: Idempotency-Key header is %d bytes, the route needs at least %d", len(key), rule.minimum)
	case rule.maximum > 0 && len(key) > rule.maximum:
		return fmt.Errorf("idempotency key: Idempotency-Key header is %d bytes, the route accepts at most %d", len(key), rule.maximum)
	}
	return nil
}

// requireIdempotencyKey returns the request's trimmed Idempotency-Key when the
// route's rule accepts it. Otherwise it answers 400 idempotency_key_required
// with detail, the Product Schema copy that names the header, records the
// internal reason in the access log and reports false; the caller then stops.
func requireIdempotencyKey(writer http.ResponseWriter, request *http.Request, rule idempotencyKeyRule, detail string) (string, bool) {
	key := strings.TrimSpace(request.Header.Get(idempotencyKeyHeader))
	if refusal := rule.refusal(key); refusal != nil {
		writeProblem(writer, http.StatusBadRequest, idempotencyKeyRequiredCode, detail, refusal)
		return "", false
	}
	return key, true
}
