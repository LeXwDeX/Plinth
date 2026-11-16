package sqlite_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #300: the claim a Provider declared byte-identical to another is stored with it
// and comes back with the declaration; claims that declare nothing come back
// unchanged, and a claim replaces the whole set as before.
func TestClaimSameBytesDeclarationRoundTripsThroughTheRepository(t *testing.T) {
	fixture := newCheckpointFixture(t)
	ctx := context.Background()
	claims := []generation.ArtifactClaim{
		{ID: "claim-model", Role: "model", Locator: "https://assets.example.test/a.glb", MediaType: "model/gltf-binary", SameBytesAs: "claim-gltf"},
		{ID: "claim-gltf", Role: "converted_model", Variant: "converted.gltf", Locator: "https://assets.example.test/a.glb", MediaType: "model/gltf-binary"},
		{ID: "claim-fbx", Role: "converted_model", Variant: "converted.fbx", Locator: "https://assets.example.test/a.fbx", MediaType: "application/octet-stream"},
	}
	commit := func(leaseToken string, version int64, claims []generation.ArtifactClaim) {
		t.Helper()
		task := fixture.lease.Task
		task.Version = version + 1
		task.UpdatedAt = fixture.now.Add(time.Duration(version) * time.Second)
		attempt := *fixture.attempt
		attempt.UpdatedAt = task.UpdatedAt
		if err := fixture.repository.Commit(ctx, generation.Commit{
			LeaseToken:      leaseToken,
			ExpectedVersion: version,
			Task:            task,
			Attempt:         &attempt,
			ReplaceClaims:   true,
			Claims:          claims,
		}); err != nil {
			t.Fatal(err)
		}
	}
	version := fixture.commitTaskVersion(t)
	commit(fixture.lease.Token, version, claims)

	aggregate, err := fixture.repository.Load(ctx, fixture.task.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Claims load in claim ID order.
	got := map[string]string{}
	for _, claim := range aggregate.Claims {
		got[claim.ID] = claim.SameBytesAs
	}
	want := map[string]string{"claim-model": "claim-gltf", "claim-gltf": "", "claim-fbx": ""}
	if !reflect.DeepEqual(got, want) || len(aggregate.Claims) != 3 {
		t.Fatalf("declarations = %v over %d claims, want %v", got, len(aggregate.Claims), want)
	}

	// A refreshed observation replaces the set, declaration included.
	commit("", aggregate.Task.Version, claims[1:])
	aggregate, err = fixture.repository.Load(ctx, fixture.task.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, claim := range aggregate.Claims {
		if claim.SameBytesAs != "" {
			t.Fatalf("%s kept a declaration %q from the replaced set", claim.ID, claim.SameBytesAs)
		}
	}
	if len(aggregate.Claims) != 2 {
		t.Fatalf("claims = %d, want the 2 of the refresh", len(aggregate.Claims))
	}
}
