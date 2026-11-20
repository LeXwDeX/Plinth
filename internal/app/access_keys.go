package app

import (
	"log/slog"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/access"
	"github.com/LeXwDeX/Plinth/internal/accesskeys"
	"github.com/LeXwDeX/Plinth/internal/administration"
	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// accessKeysFromSchema builds the personal access key service (#332) and the
// authenticator that resolves "Authorization: Bearer plk_..." to the key
// owner's scope. Both are nil when the Catalog does not declare
// application.access_keys.
//
// Only the key management routes of the main server use the service. The main
// server's identity never accepts a key: the authenticator is for a separate
// listener, such as the external MCP one.
func accessKeysFromSchema(
	store accesskeys.Store, directory accesskeys.Directory, policy *access.Policy,
	declared *catalog.AccessKeysSchema, logger *slog.Logger,
) (*accesskeys.Service, *accesskeys.Authenticator, error) {
	if declared == nil {
		return nil, nil, nil
	}
	service, err := accesskeys.New(store, accesskeys.ConfigFromSchema(*declared, logger))
	if err != nil {
		return nil, nil, err
	}
	return service, &accesskeys.Authenticator{Keys: service, Users: directory, Policy: policy}, nil
}

// AccessKeyAuthenticator is the identity resolver for a listener that accepts
// personal access keys; nil when the Catalog declares no access keys.
func (a *Application) AccessKeyAuthenticator() *accesskeys.Authenticator {
	return a.accessKeyAuthenticator
}

// accessKeyAdminFromSchema builds the administrator's view of every personal
// access key and the service's overall switch (#332). It is nil when the
// Catalog declares neither the keys nor the administration view, which keeps
// historical documents working.
func accessKeyAdminFromSchema(
	repository administration.AccessKeyAdminRepository, audit administration.AuditRecorder,
	schema catalog.ApplicationSchema,
) (*administration.AccessKeyAdminService, error) {
	declared := schema.AccessKeys
	view := schema.Administration.AccessKeys
	if declared == nil || strings.TrimSpace(view.Permission) == "" {
		return nil, nil
	}
	service, err := administration.NewAccessKeyAdminService(administration.AccessKeyAdminConfig{
		Repository: repository, Audit: audit,
		DefaultEnabled:  declared.Enabled,
		DefaultPageSize: view.Limits.DefaultPageSize, MaxPageSize: view.Limits.MaxPageSize,
		AuditActions: administration.AccessKeyAdminAuditActions{
			Read: view.Audit.ReadAction, Revoke: view.Audit.RevokeAction,
			Enable: view.Audit.EnableAction, Disable: view.Audit.DisableAction, Type: view.Audit.ResourceType,
		},
		Clock: time.Now,
	})
	if err != nil {
		return nil, err
	}
	return service, nil
}
