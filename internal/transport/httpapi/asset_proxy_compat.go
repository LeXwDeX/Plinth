package httpapi

import (
	"net/http"

	"github.com/LeXwDeX/Plinth/internal/access"
)

// The URL-addressed compatibility proxy exposed provider asset locators in
// browser requests and could fetch an upstream asset during a read. New and
// migrated assets are delivered only by the owner-scoped task asset route.
func (server *Server) legacyAssetProxy(
	writer http.ResponseWriter,
	_ *http.Request,
	_ access.Principal,
) {
	schema := server.application.Runtime.AssetDelivery.Proxy
	server.setPrivateAssetCacheControl(writer)
	writeJSON(writer, schema.API.Statuses.NotFound, map[string]string{
		schema.API.Fields.Error: schema.Messages.FetchFailed,
	})
}
