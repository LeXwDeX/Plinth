#!/usr/bin/env python3
"""Fault paths of the fake upstream on a deployed, fake-shaped stack (#301). Zero cost.

dev-test releases in the fake upstream shape: Plinth's own Adapters call a fake service in the
same Compose project, and the browser, the smoke scripts and the Agent only ever reach Plinth.
The fake's header and key scenarios (X-Fake-Scenario, "fake-<scenario>" keys) are out of reach
there, so the matting service's per-file faults are chosen by the uploaded pictures themselves: a
marker written into a PNG text chunk (fake_faults.solid_png). The script uploads such pictures
through the public library upload, submits background-removal Tasks, and asserts the public
projection against the Schema's own sentences:

  * #284: a batch that comes back with fewer results than pictures is a SUCCEEDED Task that
    delivers the matted pictures and carries a notice naming the others;
  * #299: the picture of the 2026-11-13 real run (2048x2048, GPU out of memory) next to a matted
    one is such a Task too, and a batch whose only picture ran out of memory is a FAILED Task of
    the capacity class that is not retried, with the class's shared public sentence;
  * pictures without a marker are matted and the Task says nothing.

Each run creates its own session and pictures; nothing depends on earlier data. The tasks are
background removal: no price, no paid upstream, and the script refuses (exit 2) unless the target
runs the fake upstream (upstream_shape.py), because the markers mean nothing to a real service.

  python3 scripts/release-env.py smoke dev-test -- python3 scripts/smoke/smoke-fake-faults.py
  BASE=... ADMIN_USERNAME=... ADMIN_PASSWORD=... ART_AI_UPSTREAM_SHAPE=fake python3 scripts/smoke/smoke-fake-faults.py

Exit 0 = every check passed, 1 = a check failed, 2 = refused or misused. It never prints
credentials, cookies or signed URLs. The same checks run on the local fake stack as the
"matting-faults" scenario of smoke-fake-stack.py.
"""
import os
import sys
import urllib.parse

sys.dont_write_bytecode = True  # run from the repository: leave no __pycache__ in scripts/smoke
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import fake_faults  # noqa: E402
import upstream_shape  # noqa: E402

results = []


def record(name, ok, detail):
    results.append((name, ok))
    print(f"{'PASS' if ok else 'FAIL'}: {name} | {detail}", flush=True)


def refusal(argv, environ):
    """Why the script must not run, or None. Decided before any request is sent."""
    if argv:
        return "this script takes no arguments"
    if upstream_shape.target_shape(environ) != "fake":
        return ("refusing to run: the target does not run the fake upstream, and the fault pictures only mean "
                "something to the fake (a real matting service would just process them)")
    missing = [name for name in ("BASE", "ADMIN_USERNAME", "ADMIN_PASSWORD") if not environ.get(name)]
    if missing:
        return "missing environment: " + ", ".join(missing)
    parsed = urllib.parse.urlparse(environ["BASE"])
    if parsed.scheme not in ("http", "https") or not parsed.netloc or parsed.path not in ("", "/") \
            or parsed.query or parsed.fragment or parsed.username or parsed.password:
        return "BASE must be an http(s) origin without a path, credentials, a query or a fragment"
    return None


def main(argv=None, environ=None):
    argv = sys.argv[1:] if argv is None else argv
    environ = os.environ if environ is None else environ
    if argv in (["-h"], ["--help"]):
        print(__doc__)
        return 0
    problem = refusal(argv, environ)
    if problem:
        print(problem, file=sys.stderr)
        return 2
    results.clear()
    client = fake_faults.Client(environ["BASE"])
    status, _ = client.login(environ["ADMIN_USERNAME"], environ["ADMIN_PASSWORD"])
    if status != 200:
        record("login", False, f"status {status}")
        return 1
    fake_faults.run(client, record)
    failed = [name for name, ok in results if not ok]
    print(f"RESULT: {len(results) - len(failed)} passed, {len(failed)} failed")
    return 1 if failed or not results else 0


if __name__ == "__main__":
    sys.exit(main())
