package hyper3d

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func TestInsufficientFundsIsItsOwnFailureClass(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	var mapping *generation.ErrorMapping
	for _, connection := range product.Application().Runtime.ProviderConnections {
		if connection.ID == "hyper3d" {
			mapping = connection.ErrorMapping
		}
	}
	if mapping == nil {
		t.Fatal("hyper3d connection has no error mapping")
	}
	adapter := &Adapter{errorMapping: mapping}
	err = adapter.businessError(rodinError{Error: json.RawMessage(`"API_INSUFFICIENT_FUNDS"`)})
	var providerErr *generation.ProviderError
	if !errors.As(err, &providerErr) || providerErr.Failure.Class != generation.FailureInsufficientBalance || providerErr.Failure.Retryable {
		t.Fatalf("failure = %v", err)
	}
	// Other business refusals keep their classes.
	err = adapter.businessError(rodinError{Error: json.RawMessage(`"IMAGE_CONTENT_VIOLATION"`)})
	if !errors.As(err, &providerErr) || providerErr.Failure.Class != generation.FailureRejected {
		t.Fatalf("content violation = %v", err)
	}
}
