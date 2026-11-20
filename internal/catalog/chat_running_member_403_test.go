package catalog_test

import (
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #403: the message projection now writes the running mark under the Schema's
// `is_pending` member, so a Catalog without a name for it is refused instead of
// silently losing the mark.
func TestTheRunningMessageMemberIsRequired403(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	app := c.Application()
	if app.Chat.API.ResponseFields.IsPending == "" {
		t.Fatal("the default Catalog names no member for a pending message")
	}
	app.Chat.API.ResponseFields.IsPending = " "
	if err := catalog.ValidateApplicationSchema(app); err == nil {
		t.Fatal("ValidateApplicationSchema accepted a Catalog without the running message member")
	}
}
