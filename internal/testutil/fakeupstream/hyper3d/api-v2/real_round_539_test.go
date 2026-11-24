package hyper3dapiv2

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/align"
)

// #539: the six steps of the 1.9.0 real round. They are constructed from the
// official pages and run once, after the user agreed. These tests pin what each
// puts on the wire and what it can spend, so a real run is the request this file
// describes and a later change to the Profile is a knowing one.

// realRoundNames are the submissions of the real round, in profile order.
var realRoundNames = []string{"reject-seed-probe", "rodin-text-quadpose", "rodin-image-label-short", "bang-upload", "texture-images"}

func TestRealRoundSubmissionsArePaidAndTheirFollowUpsAreNot(t *testing.T) {
	for _, name := range realRoundNames {
		steps := profileSteps(t, name, name+"-status", name+"-download")
		submit := steps[0]
		if submit.Method != "POST" || !submit.Paid {
			t.Errorf("%s submission = %+v, want a paid POST", name, submit)
		}
		for _, follow := range steps[1:] {
			if follow.Paid {
				t.Errorf("%s must not be a paid step", follow.Name)
			}
		}
		if steps[1].RequireVar != name+"_key" || steps[2].RequireVar != name+"_uuid" {
			t.Errorf("%s follow-ups run without the submission's answer: %+v, %+v", name, steps[1], steps[2])
		}
	}
	balance := profileSteps(t, "balance-usage")[0]
	if balance.Paid || balance.Method != "GET" {
		t.Fatalf("balance-usage = %+v, want a free GET", balance)
	}
	parsed, err := url.Parse(balance.Path)
	if err != nil || parsed.Path != "/check_balance" || parsed.Query().Get("usage_page") != "1" {
		t.Fatalf("balance-usage path = %q, want /check_balance?usage_page=1", balance.Path)
	}
}

func fieldValues(fields []align.Field, name string) []string {
	var values []string
	for _, field := range fields {
		if field.Name == name {
			values = append(values, field.Value)
		}
	}
	return values
}

func TestRealRoundRequestShapes(t *testing.T) {
	// The seed probe is the cheapest request with the one value the documented range refuses.
	probe := profileSteps(t, "reject-seed-probe")[0]
	if !reflect.DeepEqual(fieldValues(probe.Form, "seed"), []string{"65536"}) ||
		!reflect.DeepEqual(fieldValues(probe.Form, "tier"), []string{"Gen-2.5-Extreme-Low"}) || len(probe.Files) != 0 {
		t.Errorf("reject-seed-probe = %+v", probe.Form)
	}

	// The game character request is the Catalog Offering's own: the Extreme-High tier, Quad, 10000 faces, the 2K texture
	// mode, both pose switches and the seed.
	game := profileSteps(t, "rodin-text-quadpose")[0]
	for name, want := range map[string]string{
		"tier": "Gen-2.5-Extreme-High", "mesh_mode": "Quad", "quality_override": "10000", "texture_mode": "high",
		"TAPose": "true", "quad_normal": "true", "seed": "1", "material": "PBR", "geometry_file_format": "glb",
	} {
		if got := fieldValues(game.Form, name); !reflect.DeepEqual(got, []string{want}) {
			t.Errorf("rodin-text-quadpose %s = %v, want %s", name, got, want)
		}
	}
	if len(game.Files) != 0 || len(fieldValues(game.Form, "prompt")) != 1 {
		t.Errorf("rodin-text-quadpose must be a text request: %d files", len(game.Files))
	}

	// Fewer image_label parts than pictures: the request the Adapter never sends, which is the point.
	short := profileSteps(t, "rodin-image-label-short")[0]
	if labels := fieldValues(short.Form, "image_label"); !reflect.DeepEqual(labels, []string{"F"}) || len(short.Files) != 2 {
		t.Errorf("rodin-image-label-short: labels %v, %d pictures", labels, len(short.Files))
	}
	for _, file := range short.Files {
		if file.Field != "images" {
			t.Errorf("picture %s is sent as %q", file.Filename, file.Field)
		}
	}

	// Bang in the upload mode and Texture take the model the bang step delivered; Bang keeps the singular image, and
	// the Texture step is the one that tries the page's plural field.
	upload := profileSteps(t, "bang-upload")[0]
	texture := profileSteps(t, "texture-images")[0]
	for _, step := range []align.Step{upload, texture} {
		if step.RequireVar != "bang_url" || len(step.Files) != 2 || step.Files[0].Field != "model" || step.Files[0].FromVar != "bang_url" {
			t.Errorf("%s = %+v", step.Name, step.Files)
		}
	}
	if upload.Files[1].Field != "image" || texture.Files[1].Field != "images" {
		t.Errorf("picture fields: bang-upload %q, texture-images %q", upload.Files[1].Field, texture.Files[1].Field)
	}
	if got := fieldValues(upload.Form, "material"); !reflect.DeepEqual(got, []string{"PBR"}) {
		t.Errorf("bang-upload material = %v, a material other than None needs the picture", got)
	}
	if got := fieldValues(texture.Form, "texture_mode"); !reflect.DeepEqual(got, []string{"extreme-low"}) {
		t.Errorf("texture-images texture_mode = %v, want the cheapest", got)
	}
}

// The steps run against the fake: the balance read answers, the seed probe is
// refused as an invalid request (so nothing is generated and its follow-ups are
// skipped), and the game character, the short-label request and Bang's upload
// mode are accepted, finish and download. texture-images is not run: the fake
// accepts the singular image field only until the real round decides which field
// the service takes (#539), so the step's wire shape is pinned above instead.
func TestRealRoundStepsRunAgainstTheFake(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	chain := []string{"balance-usage",
		"reject-seed-probe", "reject-seed-probe-status", "reject-seed-probe-download",
		"rodin-text-quadpose", "rodin-text-quadpose-status", "rodin-text-quadpose-download",
		"rodin-image-label-short", "rodin-image-label-short-status", "rodin-image-label-short-download",
		"rodin-text", "rodin-text-status", "rodin-text-download",
		"bang", "bang-status", "bang-download",
		"bang-upload", "bang-upload-status", "bang-upload-download"}
	bundle, err := align.Run(context.Background(), "hyper3d", "api-v2", align.Target{
		Name: "fake", BaseURL: f.Server.URL + Prefix, Key: key, NoPollDelay: true,
	}, profileSteps(t, chain...), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	byStep := map[string]align.Obs{}
	for _, obs := range bundle.Obs {
		byStep[obs.Step] = obs
	}
	if obs := byStep["balance-usage"]; obs.Status != http.StatusOK {
		t.Errorf("balance-usage = %d %s", obs.Status, obs.Body)
	}
	if obs := byStep["reject-seed-probe"]; !strings.Contains(string(obs.Body), "INVALID_REQUEST") {
		t.Errorf("reject-seed-probe = %d %s, want INVALID_REQUEST", obs.Status, obs.Body)
	}
	for _, name := range []string{"reject-seed-probe-status", "reject-seed-probe-download"} {
		if byStep[name].Skipped == "" {
			t.Errorf("%s ran after a refused submission", name)
		}
	}
	for _, name := range []string{"rodin-text-quadpose", "rodin-image-label-short", "bang-upload"} {
		var accepted map[string]any
		obs := byStep[name]
		if err := json.Unmarshal(obs.Body, &accepted); err != nil || obs.Status != http.StatusCreated || accepted["error"] != nil || accepted["uuid"] == nil {
			t.Errorf("%s = %d %s (%v)", name, obs.Status, obs.Body, err)
			continue
		}
		poll := byStep[name+"-status"]
		if poll.Skipped != "" || len(poll.Polls) == 0 || poll.Polls[len(poll.Polls)-1].Status != "Done" {
			t.Errorf("%s status = %+v", name, poll)
		}
		if download := byStep[name+"-download"]; download.Skipped != "" || len(download.Artifacts) == 0 {
			t.Errorf("%s download = %+v", name, download)
		}
	}
	// Four paid submissions here besides the setup ones: the seed probe, the game character, the short labels, Bang's upload,
	// plus the rodin-text and bang that feed it.
	if bundle.PaidRequests != 6 {
		t.Errorf("paid requests = %d, want 6", bundle.PaidRequests)
	}
}
