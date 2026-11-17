package accesskeys_test

import (
	"slices"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/accesskeys"
	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// The shipped Schema declares exactly the scopes the code checks, and its
// bounds become the service's.
func TestConfigFromTheShippedSchemaMatchesTheVocabularyAndBounds(t *testing.T) {
	t.Parallel()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	declared := product.Application().AccessKeys
	if declared == nil {
		t.Fatal("the default Catalog has no application.access_keys")
	}
	config := accesskeys.ConfigFromSchema(*declared, nil)
	scopes := slices.Clone(config.Scopes)
	slices.Sort(scopes)
	if want := []string{accesskeys.ScopeGenerate, accesskeys.ScopeRead}; !slices.Equal(scopes, want) {
		t.Fatalf("scopes = %v; the code's vocabulary is %v", scopes, want)
	}
	if config.KeyPrefix != "plk" || config.MaxKeysPerUser != 10 || config.DefaultTTL != 90*24*time.Hour ||
		config.MaxTTL != 365*24*time.Hour || config.NameMaxLength != 80 || config.LastUsedInterval != time.Minute {
		t.Fatalf("config = %+v", config)
	}
	// The configuration is one the service accepts.
	if _, err := accesskeys.New(newMemoryStore(), config); err != nil {
		t.Fatal(err)
	}
}

func TestNewRefusesAnUnusableConfiguration(t *testing.T) {
	t.Parallel()
	good := accesskeys.Config{
		KeyPrefix: "plk", MaxKeysPerUser: 3, DefaultTTL: time.Hour, MaxTTL: 2 * time.Hour, NameMaxLength: 10,
		Scopes: []string{"read"}, LastUsedInterval: time.Minute,
	}
	if _, err := accesskeys.New(nil, good); err == nil {
		t.Error("a service without a store was built")
	}
	for name, mutate := range map[string]func(*accesskeys.Config){
		"no prefix":             func(c *accesskeys.Config) { c.KeyPrefix = "" },
		"prefix with separator": func(c *accesskeys.Config) { c.KeyPrefix = "a_b" },
		"uppercase prefix":      func(c *accesskeys.Config) { c.KeyPrefix = "PLK" },
		"no keys":               func(c *accesskeys.Config) { c.MaxKeysPerUser = 0 },
		"no name length":        func(c *accesskeys.Config) { c.NameMaxLength = 0 },
		"no default lifetime":   func(c *accesskeys.Config) { c.DefaultTTL = 0 },
		"maximum below default": func(c *accesskeys.Config) { c.MaxTTL = time.Minute },
		"no scopes":             func(c *accesskeys.Config) { c.Scopes = nil },
		"negative interval":     func(c *accesskeys.Config) { c.LastUsedInterval = -time.Second },
	} {
		config := good
		mutate(&config)
		if _, err := accesskeys.New(newMemoryStore(), config); err == nil {
			t.Errorf("%s: the service was built", name)
		}
	}
}
