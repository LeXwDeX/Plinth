package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"regexp"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/access"
	"github.com/LeXwDeX/Plinth/internal/agentconfiguration"
	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/lineage"
	"github.com/LeXwDeX/Plinth/internal/providers"
	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
)

var serverChineseText = regexp.MustCompile(`[\x{3400}-\x{9fff}]`)

func localizedTestServer(t *testing.T) (*Server, catalog.ApplicationSchema, catalog.LocaleBundle) {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	texts, err := catalog.NewTextLocalizer(application)
	if err != nil {
		t.Fatal(err)
	}
	return &Server{application: application, texts: texts}, application, application.Localization.Bundles["en"]
}

func TestTaskFailureDetailFollowsTheLocale(t *testing.T) {
	t.Parallel()
	server, application, english := localizedTestServer(t)
	failure := &generation.Failure{Class: generation.FailureUpstreamUnavailable, SafeDetail: "upstream said 503"}
	key := "failure." + string(failure.Class)

	if got := server.publicFailure("en", failure).Detail; got != english.Messages[key] {
		t.Fatalf("en failure detail = %q, want %q", got, english.Messages[key])
	}
	if got := server.publicFailure("", failure).Detail; got != application.Content.Messages[key] {
		t.Fatalf("default failure detail = %q", got)
	}
	// The upstream's own words never reach the person, in any locale.
	events := server.publicEvents("en", []generation.Event{})
	if len(events) != 0 {
		t.Fatalf("events = %v", events)
	}
	// Without a localizer (older wiring) the default copy is kept.
	bare := &Server{application: application}
	if got := bare.publicFailure("en", failure).Detail; got != application.Content.Messages[key] {
		t.Fatalf("detail without localizer = %q", got)
	}
}

func TestLineageGroupLabelFollowsTheLocale(t *testing.T) {
	t.Parallel()
	server, application, english := localizedTestServer(t)
	graph := lineage.Graph{Groups: []lineage.Group{{ID: "run", Label: application.Chat.ThreeView.Label}}}
	if got := server.lineageWire("en", graph).Groups[0].Label; got != english.Fields["chat.three_view.label"] {
		t.Fatalf("en group label = %q", got)
	}
	if got := server.lineageWire("", graph).Groups[0].Label; got != application.Chat.ThreeView.Label {
		t.Fatalf("default group label = %q", got)
	}
}

func TestAgentConfigurationAdministrationFollowsTheLocale(t *testing.T) {
	t.Parallel()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	schema := product.Application()
	skills, err := catalog.LoadDefaultSkillCatalog()
	if err != nil {
		t.Fatal(err)
	}
	repo, err := OpenMigratedTestRepository(filepath.Join(t.TempDir(), "config.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	service, err := agentconfiguration.New(repo, schema, skills, product.List(""))
	if err != nil {
		t.Fatal(err)
	}
	policy, err := access.NewPolicy(access.PolicyConfig{DefaultRole: "admin", AdminRole: "admin", Roles: []access.RoleDefinition{{Key: "admin", Permissions: []access.Permission{access.Permission(schema.AgentConfiguration.Permission)}}}})
	if err != nil {
		t.Fatal(err)
	}
	texts, err := catalog.NewTextLocalizer(schema)
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{
		identity:    DevelopmentIdentity{DefaultClaims: access.Claims{ID: "admin", Roles: []string{"admin"}}, Policy: policy},
		application: schema, agentConfiguration: service, texts: texts,
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)), mux: http.NewServeMux(),
	}
	server.registerAgentConfigurationContracts()
	read := func(locale string) []byte {
		request := httptest.NewRequest(http.MethodGet, schema.AgentConfiguration.Routes.Admin, nil)
		request = request.WithContext(catalog.WithLocale(context.Background(), locale))
		response := httptest.NewRecorder()
		server.mux.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("management %s: %d %s", locale, response.Code, response.Body.String())
		}
		return response.Body.Bytes()
	}
	type management struct {
		Tools []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		} `json:"tools"`
		MCPPresets []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		} `json:"mcp_presets"`
	}
	var english, chinese management
	decodeTestJSON(t, read("en"), &english)
	decodeTestJSON(t, read(""), &chinese)
	for _, tool := range english.Tools {
		if serverChineseText.MatchString(tool.Description) {
			t.Fatalf("en tool %s description has Chinese: %q", tool.Name, tool.Description)
		}
	}
	for _, preset := range english.MCPPresets {
		if serverChineseText.MatchString(preset.Name + preset.Description) {
			t.Fatalf("en MCP preset has Chinese: %+v", preset)
		}
	}
	if len(chinese.MCPPresets) == 0 || !serverChineseText.MatchString(chinese.MCPPresets[0].Name) {
		t.Fatalf("default MCP presets changed: %+v", chinese.MCPPresets)
	}

	// The effective configuration the parameter panel reads.
	request := httptest.NewRequest(http.MethodGet, schema.AgentConfiguration.Routes.Effective, nil)
	request = request.WithContext(catalog.WithLocale(context.Background(), "en"))
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)
	// name_zh is the skill's Chinese name by contract, shown next to its name.
	body := regexp.MustCompile(`"name_zh":"[^"]*"`).ReplaceAll(response.Body.Bytes(), nil)
	if response.Code != http.StatusOK || serverChineseText.Match(body) {
		t.Fatalf("en effective configuration: %d %s", response.Code, response.Body.String())
	}
}

func decodeTestJSON(t *testing.T, body []byte, target any) {
	t.Helper()
	if err := json.Unmarshal(body, target); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
}

// Fee item names on a frozen quote are offering copy: every read projects them
// in the request locale (task, chat, pending approvals, administration
// records) while the frozen quote itself keeps its words.
func TestFrozenQuoteLabelsFollowTheLocale(t *testing.T) {
	t.Parallel()
	server, _, english := localizedTestServer(t)
	frozen := &generation.NativeQuote{Version: 1, Unit: "tripo.credit", UnitLabel: "Tripo credits",
		BaseAmount: "30", ExtraAmount: "50", TotalAmount: "80",
		Components: []schemacontract.NativeCharge{{ID: "texture", Label: "标准纹理", Amount: "30"}}}
	localized := server.quoteIn("en", generation.Offering{}, frozen)
	if localized.Components[0].Label != english.OfferingTexts["标准纹理"] || localized.UnitLabel != "Tripo credits" {
		t.Fatalf("en quote = %+v", localized)
	}
	if frozen.Components[0].Label != "标准纹理" {
		t.Fatal("projecting the quote changed the frozen evidence")
	}
	if server.quoteIn("", generation.Offering{}, frozen) != frozen || server.quoteIn("en", generation.Offering{}, nil) != nil {
		t.Fatal("default locale or a missing quote must pass through")
	}
}

// A fee item renamed after its quote was frozen (#264 dropped "8K" from
// Tripo's extreme texture) has no translation of its own; it reads the
// translation of the same-id fee item its Offering declares now.
func TestFrozenQuoteLabelRenamedSinceFollowsTheLocaleByComponentID(t *testing.T) {
	t.Parallel()
	server, _, english := localizedTestServer(t)
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	repository, err := OpenMigratedTestRepository(filepath.Join(t.TempDir(), "quotes.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	assets, err := local.New(filepath.Join(t.TempDir(), "assets"))
	if err != nil {
		t.Fatal(err)
	}
	server.generation, err = generation.New(generation.Dependencies{Repository: repository, Catalog: product, Assets: assets, Providers: providers.NewRegistry()})
	if err != nil {
		t.Fatal(err)
	}
	offering, found := product.Get("tripo/tripo-v3.1/text-to-3d")
	if !found {
		t.Fatal("missing Tripo text-to-3d Offering")
	}
	// This is the historical label seen on the dev-test administration list
	// (#299 note 97543), not a value generated from today's declaration.
	const historical = "8K 极致纹理"
	frozen := &generation.NativeQuote{Version: 1, Unit: "tripo.credit", UnitLabel: "Tripo credits",
		BaseAmount: "30", ExtraAmount: "20", TotalAmount: "50",
		Components: []schemacontract.NativeCharge{{ID: "extreme_texture", Label: historical, Amount: "20"}}}
	const want = "Extreme texture"
	if english.OfferingTexts["极致纹理"] != want {
		t.Fatal("current extreme texture translation differs from the regression fixture")
	}

	localized := server.quoteIn("en", offering, frozen)
	expected := *frozen
	expected.Components = []schemacontract.NativeCharge{{ID: "extreme_texture", Label: want, Amount: "20"}}
	if !reflect.DeepEqual(*localized, expected) {
		t.Fatalf("localized quote = %#v; only its label may change", localized)
	}
	// A direct translation preserves historical wording even when the same
	// component now has a different label. Selection fees use the same path.
	for _, test := range []struct{ id, label, want string }{
		{"standard_texture", "极致纹理", "Extreme texture"},
		{"format_conversions", "旧版导出格式转换", "Additional export format conversion"},
	} {
		quote := *frozen
		quote.Components = []schemacontract.NativeCharge{{ID: test.id, Label: test.label, Amount: "20"}}
		if got := server.quoteIn("en", offering, &quote).Components[0].Label; got != test.want {
			t.Errorf("fee item %q = %q, want %q", test.id, got, test.want)
		}
	}
	// The old model is no longer listed: do not borrow another model's fee.
	gone := offering
	gone.ID = "retired/" + offering.ID
	if got := server.quoteIn("en", gone, frozen).Components[0].Label; got != historical {
		t.Fatalf("fee item of a retired model = %q", got)
	}
	// An Offering ID changed while its model stayed: the current declaration
	// for that exact provider/model/capability answers.
	gone.ModelID = "tripo-v3.1-full"
	if got := server.quoteIn("en", gone, frozen).Components[0].Label; got != want {
		t.Fatalf("fee item of a retired Offering = %q, want %q", got, want)
	}
	// Neither declares the fee item, or nothing names the Offering: the frozen text stays.
	missing := *frozen
	missing.Components = []schemacontract.NativeCharge{{ID: "no_such_charge", Label: historical, Amount: "20"}}
	if got := server.quoteIn("en", offering, &missing).Components[0].Label; got != historical {
		t.Fatalf("undeclared fee item = %q", got)
	}
	unknown := generation.Offering{ID: "retired/elsewhere", ProviderID: "nobody", ModelID: "nothing", Capability: offering.Capability}
	if got := server.quoteIn("en", unknown, frozen).Components[0].Label; got != historical {
		t.Fatalf("fee item of an unknown Offering = %q", got)
	}
	if got := server.quoteIn("en", generation.Offering{}, frozen).Components[0].Label; got != historical {
		t.Fatalf("fee item without an Offering = %q", got)
	}
	// The default locale shows the frozen evidence, which is never rewritten.
	if server.quoteIn("", offering, frozen) != frozen || frozen.Components[0].Label != historical {
		t.Fatal("default locale or frozen evidence changed")
	}
}

// #62 I18N-03: the system Skill's name, description and default message follow
// the request locale; the English picker must not show the Chinese copy.
func TestSystemSkillCatalogFollowsTheLocale(t *testing.T) {
	t.Parallel()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	schema := product.Application()
	defaults, err := catalog.LoadDefaultSkillCatalog()
	if err != nil {
		t.Fatal(err)
	}
	repo, err := OpenMigratedTestRepository(filepath.Join(t.TempDir(), "skills.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	service, err := agentconfiguration.New(repo, schema, defaults, product.List(""))
	if err != nil {
		t.Fatal(err)
	}
	policy, err := access.NewPolicy(access.PolicyConfig{DefaultRole: "user", AdminRole: "admin", Roles: []access.RoleDefinition{{Key: "user"}, {Key: "admin"}}})
	if err != nil {
		t.Fatal(err)
	}
	texts, err := catalog.NewTextLocalizer(schema)
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{
		identity:    DevelopmentIdentity{DefaultClaims: access.Claims{ID: "u", Roles: []string{"user"}}, Policy: policy},
		application: schema, agentConfiguration: service, texts: texts,
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)), mux: http.NewServeMux(),
	}
	server.mux.HandleFunc("GET "+schema.Runtime.HTTP.Routes.ChatSkills, server.skillCatalogSchema)
	read := func(locale string) map[string]any {
		request := httptest.NewRequest(http.MethodGet, schema.Runtime.HTTP.Routes.ChatSkills, nil)
		request = request.WithContext(catalog.WithLocale(context.Background(), locale))
		response := httptest.NewRecorder()
		server.mux.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("skills %s: %d %s", locale, response.Code, response.Body.String())
		}
		var payload struct {
			Skills []map[string]any `json:"skills"`
		}
		decodeTestJSON(t, response.Body.Bytes(), &payload)
		for _, skill := range payload.Skills {
			if skill["id"] == "system-three-view" {
				return skill
			}
		}
		t.Fatalf("system-three-view missing for %q", locale)
		return nil
	}
	fields := schema.Chat.Skills.Fields
	english, chinese := read("en"), read("")
	for _, key := range []string{fields.NameZH, fields.Prompt, fields.DefaultMessage} {
		value, _ := english[key].(string)
		if value == "" || serverChineseText.MatchString(value) {
			t.Fatalf("en %s = %q", key, value)
		}
	}
	for _, key := range []string{fields.NameZH, fields.Prompt, fields.DefaultMessage} {
		if value, _ := chinese[key].(string); !serverChineseText.MatchString(value) {
			t.Fatalf("default %s = %q", key, value)
		}
	}
}
