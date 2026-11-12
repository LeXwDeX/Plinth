package generation

import (
	"errors"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #358: a refused task source keeps expired vs unsupported distinct, names
// only the public Schema label, and wraps ErrSourceExpired for callers.
func TestTaskDependencyRefusalIsUserFacingAndLabelOnly(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering := Offering{ExecutionMessages: product.Application().Runtime.GenerationMessages}
	parameter := Parameter{Name: "source_task", Label: "Source model"}
	expired := expiredTaskDependency(offering, parameter)
	expiredRefusal := UserFacingError(expired)
	if expiredRefusal == nil || !errors.Is(expired, ErrSourceExpired) {
		t.Fatalf("expired = %+v", expired)
	}
	if !strings.Contains(expiredRefusal.Detail, "Source model") || strings.Contains(expiredRefusal.Detail, "source_task") {
		t.Fatalf("expired detail = %q", expiredRefusal.Detail)
	}
	unsupported := invalidTaskDependency(offering, parameter)
	unsupportedRefusal := UserFacingError(unsupported)
	if unsupportedRefusal == nil || strings.Contains(unsupportedRefusal.Detail, "source_task") {
		t.Fatalf("unsupported = %+v", unsupported)
	}
	if unsupportedRefusal.DetailTemplate != offering.ExecutionMessages.InputSourceTaskUnsupported {
		t.Fatalf("unsupported template = %q", unsupportedRefusal.DetailTemplate)
	}
}

// #276: a source the step does not take is refused before anything is paid with
// the route the parameter declares instead, in the requester's language; a
// parameter that declares none keeps the sentence it always had.
func TestUnsupportedTaskSourceRefusalNamesTheDeclaredRoute(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	texts, err := catalog.NewTextLocalizer(product.Application())
	if err != nil {
		t.Fatal(err)
	}
	english := func(text string) string { return texts.Text("en", text) }
	offering := Offering{ExecutionMessages: product.Application().Runtime.GenerationMessages}
	const route = "可改走的路径：先用 Tripo v3.1 重新生成高细节模型。"

	with := UserFacingError(invalidTaskDependency(offering, Parameter{Name: "source_task", Label: "来源模型", TaskSourceAlternative: route}))
	if with == nil || strings.Contains(with.Detail, "{") ||
		!strings.HasSuffix(with.Detail, "请重新选择来源。"+route) || !strings.Contains(with.Detail, "没有扣积分") {
		t.Fatalf("refusal with a route = %+v", with)
	}
	if got := with.LocalizedDetail(nil); got != with.Detail {
		t.Fatalf("without a translator the Schema text stays: %q", got)
	}
	// The English text translates the template and the declared route (a text the
	// Offering's own translations hold; here the shipped decimation's).
	decimate, _ := product.Get("tripo/geometry/decimate-triangle")
	var shipped Parameter
	for _, parameter := range decimate.Parameters {
		if parameter.Name == "source_task" {
			shipped = parameter
		}
	}
	shipped.Label = "来源模型"
	got := UserFacingError(invalidTaskDependency(offering, shipped))
	if got == nil || !strings.Contains(got.Detail, "Tripo v3.1") || !strings.Contains(got.Detail, "重拓扑") {
		t.Fatalf("the decimation's refusal does not name its route: %+v", got)
	}
	localized := got.LocalizedDetail(english)
	for _, want := range []string{"This step was not submitted and no credits were spent", "Select another source.", "Tripo v3.1", "Meshy remesh"} {
		if !strings.Contains(localized, want) {
			t.Errorf("English refusal %q lacks %q", localized, want)
		}
	}
	if strings.ContainsAny(localized, "{}") || strings.HasSuffix(localized, " ") || strings.Contains(localized, "可改走") {
		t.Errorf("English refusal is not complete English text: %q", localized)
	}

	// No route declared: the sentence is the old one, without a trailing space or
	// a placeholder, in both languages.
	plain := UserFacingError(invalidTaskDependency(offering, Parameter{Name: "source_task", Label: "model X"}))
	wantPlain := "来源任务 model X 不满足这一步的来源要求；这一步没有提交，也没有扣积分。请重新选择来源。"
	if plain == nil || plain.Detail != wantPlain {
		t.Fatalf("refusal without a route = %+v", plain)
	}
	wantEnglish := "Source model X does not meet this step's source requirements. This step was not submitted and no credits were spent. Select another source."
	if got := plain.LocalizedDetail(english); got != wantEnglish {
		t.Fatalf("English refusal without a route = %q, want %q", got, wantEnglish)
	}

	// A source whose job the provider no longer keeps is a different refusal and
	// says nothing of the route: it asks for a new source model.
	expired := UserFacingError(expiredTaskDependency(offering, Parameter{Name: "source_task", Label: "来源模型", TaskSourceAlternative: route}))
	if expired == nil || strings.Contains(expired.Detail, route) {
		t.Fatalf("expired refusal = %+v", expired)
	}
}

func TestTaskDependencyRefusalWithoutLabelUsesGenericParameters(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering := Offering{ExecutionMessages: product.Application().Runtime.GenerationMessages}
	parameter := Parameter{Name: "source_task"}
	err = invalidTaskDependency(offering, parameter)
	refusal := UserFacingError(err)
	if refusal == nil || refusal.DetailTemplate != offering.ExecutionMessages.InputParametersInvalid {
		t.Fatalf("error = %v", err)
	}
	if strings.Contains(refusal.Detail, "source_task") {
		t.Fatalf("detail names the internal parameter: %q", refusal.Detail)
	}
}
