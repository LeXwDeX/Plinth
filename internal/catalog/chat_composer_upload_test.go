package catalog_test

import (
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #292: the composer uploads an attached picture as an input asset when the
// turn is sent and names it like the server names a data URI it uploads as a
// reference, so the client needs that name; it is not a secret.
func TestPublicSchemaExposesTheComposerUploadName(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	app := c.Application()
	public := app.Public()
	if public.Chat.Context.ReferenceFilename == "" ||
		public.Chat.Context.ReferenceFilename != app.Chat.Context.ReferenceFilename {
		t.Fatalf("public reference filename %q, schema %q",
			public.Chat.Context.ReferenceFilename, app.Chat.Context.ReferenceFilename)
	}
	for _, key := range []string{"ui.chat.input.attachment_upload_failed", "ui.chat.input.attachment_too_large"} {
		if public.Content.Messages[key] == "" {
			t.Fatalf("message %s must be public", key)
		}
	}
}
