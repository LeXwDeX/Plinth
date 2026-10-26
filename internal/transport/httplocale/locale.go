// Package httplocale resolves the UI locale of one HTTP request from the
// Product Schema's localization declaration.
package httplocale

import (
	"net/http"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// FromRequest resolves the request's UI locale: the persisted preference
// cookie, then Accept-Language, then the Schema default.
func FromRequest(application catalog.ApplicationSchema, request *http.Request) string {
	preferred := ""
	if localization := application.Localization; localization != nil {
		if cookie, err := request.Cookie(localization.Preference.Cookie); err == nil {
			preferred = cookie.Value
		}
	}
	return application.ResolveLocale(preferred, request.Header.Get("Accept-Language"))
}

// MarkResponse declares that a response depends on the locale inputs, so a
// shared cache never serves one person's language to another.
func MarkResponse(header http.Header, locale string) {
	for _, name := range []string{"Accept-Language", "Cookie"} {
		if !varies(header, name) {
			header.Add("Vary", name)
		}
	}
	header.Set("Content-Language", locale)
}

func varies(header http.Header, name string) bool {
	for _, value := range header.Values("Vary") {
		for _, field := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(field), name) {
				return true
			}
		}
	}
	return false
}
