package project_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/project"
)

// selectConventionService is a project service whose conventions declare the
// target platform as a select (#286).
func selectConventionService(t *testing.T, repository project.Repository) *project.Service {
	t.Helper()
	schema := testSchema()
	schema.Conventions.Fields = append(schema.Conventions.Fields, catalog.ChatProductionBriefField{
		Name: "target_platform", Type: "select", Label: "目标平台",
		Options: []catalog.ChatProductionBriefOption{
			{ID: "mobile", Label: "手机"}, {ID: "pc_console", Label: "PC / 主机"},
			{ID: "web", Label: "网页"}, {ID: "xr", Label: "XR"},
		},
	})
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	service, err := project.New(repository, project.Config{
		Schema: schema, Profiles: []string{"game_character"}, ImageProfiles: []string{"concept_art"},
		Clock: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

// #286: a select convention stores an option id. A new free-text value is
// refused, but a project that stored free text before the field became a select
// keeps it through an edit of another convention until an option replaces it.
func TestSelectConventionKeepsAStoredFreeTextValue(t *testing.T) {
	ctx := context.Background()
	repository := newMemoryRepository()
	service := selectConventionService(t, repository)
	value, err := service.Create(ctx, "owner-a", "Star")
	if err != nil {
		t.Fatal(err)
	}
	set := func(brief string) error {
		_, err := service.SetConventions(ctx, "owner-a", value.ID, project.Conventions{Brief: []byte(brief)})
		return err
	}

	if err := set(`{"target_platform":"mobile","style":"ink"}`); err != nil {
		t.Fatalf("an option id was refused: %v", err)
	}
	if err := set(`{"target_platform":""}`); err != nil {
		t.Fatalf("an empty select was refused: %v", err)
	}
	for _, brief := range []string{`{"target_platform":"手机"}`, `{"target_platform":"Mobile"}`, `{"target_platform":3}`} {
		if err := set(brief); !errors.Is(err, project.ErrInvalid) {
			t.Fatalf("%s: err = %v, want ErrInvalid", brief, err)
		}
	}

	// A project saved with free text before the field became a select.
	if _, err := repository.SetConventions(ctx, "owner-a", value.ID, project.Conventions{
		Brief: []byte(`{"target_platform":"手机 + Switch","style":"ink"}`),
	}); err != nil {
		t.Fatal(err)
	}
	if err := set(`{"target_platform":" 手机 + Switch ","style":"toon"}`); err != nil {
		t.Fatalf("the stored free text was not kept through an edit of another field: %v", err)
	}
	conventions, err := service.Conventions(ctx, "owner-a", value.ID)
	if err != nil || !strings.Contains(string(conventions.Brief), "手机 + Switch") || !strings.Contains(string(conventions.Brief), "toon") {
		t.Fatalf("conventions = %+v err=%v", conventions, err)
	}
	if err := set(`{"target_platform":"平板"}`); !errors.Is(err, project.ErrInvalid) {
		t.Fatalf("a different free-text value: err = %v", err)
	}
	if err := set(`{"target_platform":"xr"}`); err != nil {
		t.Fatalf("an option over the stored free text was refused: %v", err)
	}
	if err := set(`{"target_platform":"手机 + Switch"}`); !errors.Is(err, project.ErrInvalid) {
		t.Fatalf("the replaced free text was accepted again: err = %v", err)
	}
}
