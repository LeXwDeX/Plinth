package schema

import (
	"fmt"
	"testing"
)

// #329 note 99647: a refusal filled from a Schema template is written again
// in the requester's language — the template and each Schema value mapped,
// other values (numbers) kept — and the chain is searched as for the reason.
func TestUserFacingRefusalIsWrittenInTheReadersLanguage(t *testing.T) {
	english := map[string]string{
		"{from} 已停用，请改用 {to}。": "{from} is no longer offered; use {to}.",
		"图 {label} 超过 {size}":  "Picture {label} is over {size}",
		"参考图":                  "Reference picture",
	}
	text := func(source string) string {
		if translated, ok := english[source]; ok {
			return translated
		}
		return source
	}
	retired := &Error{Code: ErrInvalidInput, UserFacing: true, Detail: "Meshy 7 已停用，请改用 Meshy 7.1。",
		DetailTemplate: "{from} 已停用，请改用 {to}。", DetailValues: []string{"{from}", "Meshy 7", "{to}", "Meshy 7.1"}}
	wrapped := &Error{Code: ErrInvalidInput, Detail: "internal", Cause: retired}
	if got := UserFacingError(fmt.Errorf("prepare: %w", wrapped)); got != retired {
		t.Fatalf("UserFacingError = %+v", got)
	}
	if got := retired.LocalizedDetail(text); got != "Meshy 7 is no longer offered; use Meshy 7.1." {
		t.Fatalf("English retired reason = %q", got)
	}
	size := &Error{UserFacing: true, Detail: "图 参考图 超过 20 MB", DetailTemplate: "图 {label} 超过 {size}",
		DetailValues: []string{"{label}", "参考图", "{size}", "20 MB"}}
	if got := size.LocalizedDetail(text); got != "Picture Reference picture is over 20 MB" {
		t.Fatalf("English size reason = %q", got)
	}
	plain := &Error{UserFacing: true, Detail: "已停用"}
	if got := plain.LocalizedDetail(text); got != "已停用" {
		t.Fatalf("a reason without a template = %q", got)
	}
	var none *Error
	if UserFacingError(&Error{Detail: "internal"}) != nil || none.LocalizedDetail(text) != "" || UserFacingDetail(wrapped) != retired.Detail {
		t.Fatal("only a UserFacing detail is a reason")
	}
}
