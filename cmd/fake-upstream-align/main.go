// Command fake-upstream-align sends one scripted request sequence to the
// official upstream API and to the fake, and reports every structural
// difference (issue #96). Test tooling only: never built into an image.
//
//	fake-upstream-align official -version tripo/v3 -key-env TRIPO_API_KEY -base-url URL -confirm-paid -private DIR
//	fake-upstream-align export   -version tripo/v3 -private DIR -out DIR
//	fake-upstream-align fake     -version tripo/v3 -private DIR
//	fake-upstream-align compare  -version tripo/v3 -private DIR -report-dir DIR
//
// The official run spends credits (paid steps are counted and listed) and
// refuses to start without -confirm-paid. Its raw output stays under -private
// (outside the worktree, mode 0700); only the sanitized sample and the
// comparison report are committed.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/align"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/registry"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "fake-upstream-align:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: official|export|fake|compare -version provider/api ...")
	}
	command := args[0]
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	version := flags.String("version", "", "provider/api, for example tripo/v3")
	private := flags.String("private", "", "private working directory outside the worktree (mode 0700)")
	keyEnv := flags.String("key-env", "", "environment variable holding the official API key")
	baseURL := flags.String("base-url", "", "official base URL (default: the profile's)")
	fakeURL := flags.String("fake-url", "", "fake base URL including the version prefix (default: an in-process fake)")
	fakeKey := flags.String("fake-key", "fake-test-key", "API key sent to the fake")
	confirm := flags.Bool("confirm-paid", false, "acknowledge that the official run spends credits")
	out := flags.String("out", "", "export: 0700 directory for the sanitized sample")
	name := flags.String("name", "", "export: sample file name stem")
	offering := flags.String("offering", "", "export: Catalog Offering id the sample is filed under (coverage matrix)")
	reportDir := flags.String("report-dir", "", "compare: directory for the report")
	worktree := flags.String("worktree", ".", "repository checkout")
	seed := flags.String("vars", "", "official: seed variables, name=value,name=value (task ids of an earlier partial run)")
	only := flags.String("steps", "", "official/export: comma-separated step names to run (default all)")
	tags := flags.String("tags", "", "official/export: a single tag naming this partial run; compare: comma-separated tags to merge")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	for _, path := range []*string{worktree, private, out, reportDir} {
		if *path != "" {
			absolute, err := filepath.Abs(*path)
			if err != nil {
				return err
			}
			*path = absolute
		}
	}
	profile, ok := registry.Profiles()[*version]
	if !ok {
		return fmt.Errorf("unknown version %q", *version)
	}
	if *private == "" {
		return errors.New("-private is required")
	}
	if info, err := os.Stat(*private); err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		return errors.New("-private must be an existing directory with mode 0700")
	}
	stem := strings.ReplaceAll(*version, "/", "-")
	if *only != "" && command != "fake" && command != "compare" {
		profile.Steps = filterSteps(profile.Steps, strings.Split(*only, ","))
	}
	switch command {
	case "official":
		return runOfficial(profile, *private, stem+tagSuffix(*tags), *keyEnv, *baseURL, *confirm, *seed)
	case "export":
		return runExport(profile, *private, stem+tagSuffix(*tags), *out, *name, *worktree, *seed, *offering)
	case "fake":
		return runFake(profile, *private, stem, *fakeURL, *fakeKey)
	case "compare":
		return runCompare(profile, *private, stem, strings.Split(*tags, ","), *reportDir)
	}
	return fmt.Errorf("unknown command %q", command)
}

func seedVars(text string) map[string]string {
	vars := map[string]string{}
	for _, pair := range strings.Split(text, ",") {
		if name, value, ok := strings.Cut(pair, "="); ok && name != "" {
			vars[strings.TrimSpace(name)] = strings.TrimSpace(value)
		}
	}
	return vars
}

func tagSuffix(tag string) string {
	if tag == "" {
		return ""
	}
	return "." + tag
}

func filterSteps(steps []align.Step, names []string) []align.Step {
	keep := map[string]bool{}
	for _, name := range names {
		keep[strings.TrimSpace(name)] = true
	}
	var out []align.Step
	for _, step := range steps {
		if keep[step.Name] {
			out = append(out, step)
		}
	}
	return out
}

func captureConfig(profile align.Profile, worktree, baseURL string) (align.CaptureConfig, error) {
	config := profile.Capture
	if config.AliasBaseHost {
		parsed, err := url.Parse(baseURL)
		if err != nil || parsed.Host == "" {
			return config, errors.New("base URL has no host to alias")
		}
		config.HostAlias = map[string]string{
			strings.ToLower(parsed.Host): "example.test", strings.ToLower(parsed.Hostname()): "example.test",
		}
	}
	revision, err := exec.Command("git", "-C", worktree, "rev-parse", "HEAD").Output()
	if err != nil {
		return config, err
	}
	catalog, err := os.ReadFile(filepath.Join(worktree, "internal/catalog/default_catalog.json"))
	if err != nil {
		return config, err
	}
	sum := sha256.Sum256(catalog)
	config.AppRevision = strings.TrimSpace(string(revision))
	config.CatalogRevision = hex.EncodeToString(sum[:])
	config.Environment = "isolated-wire-capture"
	config.CapturedAt = time.Now().UTC()
	return config, nil
}

func runOfficial(profile align.Profile, private, stem, keyEnv, baseURL string, confirm bool, seed string) error {
	if !confirm {
		paid := 0
		for _, step := range profile.Steps {
			if step.Paid {
				paid++
			}
		}
		return fmt.Errorf("the official run sends %d paid request(s); pass -confirm-paid", paid)
	}
	key := os.Getenv(keyEnv)
	if keyEnv == "" || key == "" {
		return errors.New("-key-env must name a set environment variable")
	}
	if baseURL == "" {
		baseURL = profile.Official
	}
	tee := align.NewTee(nil)
	defer func() { _ = tee.Save(filepath.Join(private, stem+".official.raw.json")) }()
	bundle, runErr := align.Run(context.Background(), profile.Provider, profile.API, align.Target{
		Name: "official", BaseURL: baseURL, Key: key,
		Client:   &http.Client{Transport: tee, Timeout: 5 * time.Minute},
		Download: &http.Client{Timeout: 5 * time.Minute},
	}, profile.Steps, nil, seedVars(seed))
	if bundle != nil {
		if err := align.SaveBundle(filepath.Join(private, stem+".official.bundle.json"), bundle); err != nil {
			return err
		}
		fmt.Printf("official: %d request(s), %d paid, bundle saved\n", bundle.Requests, bundle.PaidRequests)
	}
	return runErr
}

func runExport(profile align.Profile, private, stem, out, name, worktree, seed, offering string) error {
	if out == "" || name == "" {
		return errors.New("-out and -name are required")
	}
	bundle, err := align.LoadBundle(filepath.Join(private, stem+".official.bundle.json"))
	if err != nil {
		return err
	}
	replay, err := align.LoadReplay(filepath.Join(private, stem+".official.raw.json"))
	if err != nil {
		return err
	}
	config, err := captureConfig(profile, worktree, bundle.BaseURL)
	if err != nil {
		return err
	}
	if captured, parseErr := time.Parse(time.RFC3339, bundle.RanAt); parseErr == nil {
		config.CapturedAt = captured
	}
	evidence := filepath.Join(private, "evidence")
	if err := os.MkdirAll(evidence, 0o700); err != nil {
		return err
	}
	capture, err := align.NewCapture(config, worktree, evidence)
	if err != nil {
		return err
	}
	defer capture.Close()
	// Re-run the script offline against the saved official exchanges so the
	// recorder sees exactly what the official run exchanged.
	again, err := align.Run(context.Background(), profile.Provider, profile.API, align.Target{
		Name: "official-replay", BaseURL: bundle.BaseURL, Key: "replay",
		Client:    &http.Client{Transport: capture.Transport(replay)},
		SkipFetch: true, NoPollDelay: true,
	}, profile.Steps, nil, seedVars(seed))
	if err != nil {
		return err
	}
	again.Obs = mergeFetch(again.Obs, bundle.Obs)
	candidate, err := capture.Export(worktree, out, name, offering, again)
	if err != nil {
		return err
	}
	fmt.Println("exported", filepath.Base(candidate))
	return nil
}

// mergeFetch keeps the artifacts the live run fetched (the replay skips them).
func mergeFetch(replayed, live []align.Obs) []align.Obs {
	byStep := map[string][]align.ArtifactObs{}
	for _, obs := range live {
		byStep[obs.Step] = obs.Artifacts
	}
	for i := range replayed {
		replayed[i].Artifacts = byStep[replayed[i].Step]
	}
	return replayed
}

func runFake(profile align.Profile, private, stem, fakeURL, fakeKey string) error {
	target := align.Target{Name: "fake", Key: fakeKey, NoPollDelay: true, BodyHook: profile.FakeBodyHook}
	if fakeURL == "" {
		handler, prefix, err := registry.Handler(profile.Provider + "/" + profile.API)
		if err != nil {
			return err
		}
		server := httptest.NewServer(handler)
		defer server.Close()
		fakeURL = server.URL + prefix
	}
	target.BaseURL = fakeURL
	bundle, err := align.Run(context.Background(), profile.Provider, profile.API, target, profile.Steps, nil, nil)
	if bundle != nil {
		if saveErr := align.SaveBundle(filepath.Join(private, stem+".fake.bundle.json"), bundle); saveErr != nil {
			return saveErr
		}
		fmt.Printf("fake: %d request(s)\n", bundle.Requests)
	}
	return err
}

func runCompare(profile align.Profile, private, stem string, tags []string, reportDir string) error {
	if reportDir == "" {
		return errors.New("-report-dir is required")
	}
	var official *align.Bundle
	for _, tag := range tags {
		part, err := align.LoadBundle(filepath.Join(private, stem+tagSuffix(strings.TrimSpace(tag))+".official.bundle.json"))
		if err != nil {
			return err
		}
		if official == nil {
			official = part
			continue
		}
		official.Obs = append(official.Obs, part.Obs...)
		official.Requests += part.Requests
		official.PaidRequests += part.PaidRequests
	}
	if official == nil {
		return errors.New("-tags is required")
	}
	fake, err := align.LoadBundle(filepath.Join(private, stem+".fake.bundle.json"))
	if err != nil {
		return err
	}
	report := align.Compare(official, fake, profile.Official, official.RanAt[:10], profile.Notes, profile.RequestNotes...)
	for _, version := range registry.Versions() {
		if version.Provider+"/"+version.API != profile.Provider+"/"+profile.API {
			continue
		}
		for _, endpoint := range version.Endpoints {
			report.Endpoints = append(report.Endpoints, align.EndpointRow{
				Endpoint: endpoint.Key(), Evidence: string(endpoint.Evidence), Sample: endpoint.Sample,
				DocURL: endpoint.DocURL, CheckedAt: endpoint.CheckedAt, Note: endpoint.Note,
			})
		}
	}
	markdown := report.Markdown()
	if err := os.WriteFile(filepath.Join(reportDir, "alignment-report.md"), []byte(markdown), 0o644); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(reportDir, "alignment-report.json"), append(raw, '\n'), 0o644); err != nil {
		return err
	}
	unexplained := report.Unexplained()
	fmt.Printf("diffs: %d (unexplained %d)\n", len(report.Diffs), len(unexplained))
	for _, diff := range unexplained {
		fmt.Println("  UNEXPLAINED", diff.Key())
	}
	if len(unexplained) > 0 {
		return errors.New("unexplained differences")
	}
	return nil
}
