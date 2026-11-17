package accesskeys

import (
	"log/slog"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// ConfigFromSchema is the service configuration application.access_keys
// declares. The logger receives the warning of a failed last-use write.
func ConfigFromSchema(declared catalog.AccessKeysSchema, logger *slog.Logger) Config {
	scopes := make([]string, 0, len(declared.Scopes))
	for _, scope := range declared.Scopes {
		scopes = append(scopes, scope.Value)
	}
	return Config{
		KeyPrefix:        declared.KeyPrefix,
		MaxKeysPerUser:   declared.MaxKeysPerUser,
		DefaultTTL:       time.Duration(declared.DefaultTTLDays) * 24 * time.Hour,
		MaxTTL:           time.Duration(declared.MaxTTLDays) * 24 * time.Hour,
		NameMaxLength:    declared.NameMaxLength,
		Scopes:           scopes,
		LastUsedInterval: time.Duration(declared.LastUsedUpdateSeconds) * time.Second,
		Logger:           logger,
	}
}
