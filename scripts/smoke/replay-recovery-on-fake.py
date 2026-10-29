#!/usr/bin/env python3
"""Replay the D-class recovery chain on the fake upstream (issue #96).

run-controlled-acceptance.py normally needs genuine source Tasks of a real
database and spends real Meshy credits (a middle Rigging failure and its explicit
retry; a Rigging submit whose response is lost, then reconciled). This script
produces the sources with the fake instead and runs the same runner against the
same source-bound test binary, with the Meshy base URL pointing at the fake:

  1. start the fake upstream and a Plinth stack in live mode (as smoke-fake-stack.py);
  2. through that stack create the genuine sources: a Meshy text-to-3d "humanoid",
     a second one for the format scenario, and a Meshy Remesh (10000 triangles) of
     the humanoid in the same session;
  3. seed two private fixtures from the stack's database with
     seed-acceptance-fixture.py (the real seed tool, the real copied assets);
  4. run the runner with --only chain on the first and --only lost-response on the
     second (--source-lost-task = the Remesh), PROVIDER_MODE=live, no browser
     helper (the runner falls back to the same HTTP actions the page buttons call).

Zero cost, loopback only, no real Key, no dev-test. The format and image scenarios
are not replayed: the first needs a source the real Meshy Convert left short of
formats, the second a local wire fault that never reaches an upstream.

Usage: python3 scripts/smoke/replay-recovery-on-fake.py [--work DIR] [--keep]
Needs `go`. Exit 0 = both runner reports say "passed".
"""
import argparse
import importlib.util
import json
import os
import shutil
import stat
import subprocess
import sys
import tempfile

HERE = os.path.dirname(os.path.abspath(__file__))
REPO = os.path.dirname(os.path.dirname(HERE))
spec = importlib.util.spec_from_file_location("fakestack", os.path.join(HERE, "smoke-fake-stack.py"))
fs = importlib.util.module_from_spec(spec)
spec.loader.exec_module(fs)

ACCEPTANCE_PORT = int(os.environ.get("FAKE_RECOVERY_PORT", "19412"))


def git_sha():
    return subprocess.run(["git", "rev-parse", "HEAD"], cwd=REPO, capture_output=True, text=True, check=True).stdout.strip()


def build_acceptance_binary(work, sha):
    binary = os.path.join(work, "acceptance.test")
    subprocess.check_call(["go", "test", "-c", "-ldflags", f"-X github.com/LeXwDeX/Plinth/internal/app.acceptanceSourceSHA={sha}",
                           "-o", binary, "./internal/app"], cwd=REPO)
    return binary


def make_sources(processes, stack_binary, work, catalog):
    """Create the genuine sources through the fake-backed stack."""
    child, api = fs.start_stack(processes, stack_binary, work, "sources", catalog, fs.GOOD_KEY)
    offerings = {o["id"]: o for o in api.json("GET", "/api/v1/offerings")[1]["offerings"]}
    session = fs.new_session(api)
    humanoid = fs.step(api, offerings, "source: meshy text-to-3d (humanoid)", fs.MESHY_TEXT_TO_3D, "text-to-3d",
                       {"prompt": "a plain humanoid robot"}, session)
    remesh = humanoid and fs.step(api, offerings, "source: meshy remesh (10000 triangles)", fs.MESHY_REMESH, "remesh",
                                  {"source_task": humanoid["id"], "topology": "triangle", "target_polycount": 10000}, session)
    other = fs.step(api, offerings, "source: meshy text-to-3d (format)", fs.MESHY_TEXT_TO_3D, "text-to-3d",
                    {"prompt": "a plain cube"}, fs.new_session(api))
    processes.stop(child)
    if not (humanoid and remesh and other) or any(t.get("state") != "SUCCEEDED" for t in (humanoid, remesh, other)):
        raise RuntimeError("could not create the source Tasks on the fake")
    return humanoid["id"], other["id"], remesh["id"], os.path.join(work, "sources")


def seed(work, name, stack_root, sources):
    destination = os.path.join(work, name)
    argv = [sys.executable, os.path.join(HERE, "seed-acceptance-fixture.py"),
            "--source-db", os.path.join(stack_root, "app.db"), "--storage-root", os.path.join(stack_root, "storage"),
            "--destination", destination, "--apply"]
    for task in sources:
        argv += ["--source-task", task]
    subprocess.check_call(argv, cwd=REPO, stdout=subprocess.DEVNULL)
    return destination


def write_manifest(root, sha, owner, catalog):
    private_catalog = os.path.join(root, "catalog.json")
    shutil.copyfile(catalog, private_catalog)
    os.chmod(private_catalog, 0o600)
    web = os.environ.get("WEB_ASSETS_ROOT") or os.path.join(REPO, "build", "go-web")
    if not os.path.isdir(web):
        common = subprocess.run(["git", "rev-parse", "--path-format=absolute", "--git-common-dir"], cwd=REPO,
                                capture_output=True, text=True).stdout.strip()
        web = os.path.join(os.path.dirname(common), "build", "go-web")
    manifest = {"owner_id": owner, "source_sha": sha, "address": f"127.0.0.1:{ACCEPTANCE_PORT}",
                "web_assets_root": web, "catalog_file": private_catalog, "scenarios": []}
    path = os.path.join(root, "manifest.json")
    with open(path, "w", encoding="utf-8") as handle:
        json.dump(manifest, handle)
    os.chmod(path, stat.S_IRUSR | stat.S_IWUSR)


def run_runner(binary, root, env, extra):
    argv = [sys.executable, os.path.join(HERE, "run-controlled-acceptance.py"), "--root", root, "--binary", binary,
            "--execute", "--timeout", "600"] + extra
    result = subprocess.run(argv, cwd=REPO, env=env, capture_output=True, text=True, timeout=900)
    report_path = os.path.join(root, "controlled-acceptance-report.json")
    outcome = None
    if os.path.exists(report_path):
        with open(report_path, encoding="utf-8") as handle:
            outcome = json.load(handle).get("result")
    return result.returncode, outcome, (result.stdout + result.stderr)[-1500:]


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--work", default="", help="work directory (default: a temporary one, removed afterwards)")
    parser.add_argument("--keep", action="store_true", help="keep the work directory")
    args = parser.parse_args()
    work = args.work or tempfile.mkdtemp(prefix="fake-recovery-")
    os.makedirs(work, exist_ok=True)
    os.chmod(work, 0o700)
    processes = fs.Processes()
    try:
        sha = git_sha()
        fake_binary, stack_binary = fs.build(work)
        acceptance_binary = build_acceptance_binary(work, sha)
        catalog = fs.private_catalog(work)
        fs.require_free(fs.FAKE_PORT, "fake upstream")
        fs.require_free(fs.STACK_PORT, "stack")
        fs.require_free(ACCEPTANCE_PORT, "acceptance fixture")
        fake_child = processes.start([fake_binary, "-addr", f"127.0.0.1:{fs.FAKE_PORT}", "-artifact-origin", f"https://{fs.ASSET_HOST}"],
                                     {"PATH": os.environ["PATH"]}, os.path.join(work, "fake.log"))
        if not fs.wait_port(fs.FAKE_PORT, fake_child):
            raise RuntimeError(f"the fake upstream did not listen; see {work}/fake.log")
        humanoid, other, remesh, stack_root = make_sources(processes, stack_binary, work, catalog)
        owner = "fake_stack"  # the stack's development mock user (fs.stack_env)
        env, _ = fs.stack_env(work, "acceptance-env", catalog, fs.GOOD_KEY)
        env.update(PROVIDER_MODE="live", AGENT_MODE="mock", APP_ADDR=f"127.0.0.1:{ACCEPTANCE_PORT}")
        env.pop("DATABASE_PATH", None)
        runs = [("chain", ["--only", "chain", "--source-humanoid", humanoid, "--source-format", other]),
                ("lost-response", ["--only", "lost-response", "--source-humanoid", humanoid, "--source-format", other,
                                   "--source-lost-task", remesh])]
        failed = []
        for name, extra in runs:
            root = seed(work, "acceptance-" + name, stack_root, [humanoid, other, remesh])
            write_manifest(root, sha, owner, catalog)
            code, outcome, tail = run_runner(acceptance_binary, root, env, extra)
            ok = code == 0 and outcome == "passed"
            print(f"{'PASS' if ok else 'FAIL'}: recovery {name} on the fake | runner exit {code}, report result {outcome}", flush=True)
            if not ok:
                failed.append(name)
                print(tail, flush=True)
        print(f"# {len(runs) - len(failed)} passed, {len(failed)} failed")
        return 1 if failed else 0
    finally:
        processes.stop_all()
        if not args.keep and not args.work:
            shutil.rmtree(work, ignore_errors=True)


if __name__ == "__main__":
    sys.exit(main())
