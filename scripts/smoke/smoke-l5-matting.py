#!/usr/bin/env python3
"""L5: a multi-file matting batch through Plinth (#540).

One background-removal Task of three uploaded files, sent once through the public task API:

  1. a 512x512 PNG (a disc and a square on a pale background)
  2. a 256x256 JPEG
  3. a file named like a picture that is not one (plain text under a .png name)

with the Offering's default pipeline (birefnet and sdmatte process resolution 1024; never 2048: the matting
service shares its GPU, and an out-of-memory answer is a capacity problem, not a feature failure).

Expected on a real service (the reaction to file 3 is unobserved; the script records what happens):
  * the Task SUCCEEDED and delivers two PNG results with an alpha channel (colour type 6) whose pixel sizes
    are those of the sources they came from (512x512 and 256x256);
  * the Task's public notice names the picture that was not matted: code partial_inputs, inputs [3], with the
    Schema's own sentence (catalog task.notice.partial_inputs_named), and the task list shows the same;
  * the batch status words and file 3's task status and invalid_reason are the service's own and are not part
    of the public Task: they are read from the server's recorded exchanges (UPSTREAM_CAPTURE_DIR, #233) or from
    matting-probe.py. If the service refuses the whole batch for file 3 (422), the Task FAILED: that is the
    finding, recorded here; whether to split the probe is the user's call, not the script's.

On a fake target the same script runs at no cost. Real pictures cannot carry a fake fault, so file 3 is then a
valid PNG whose own bytes ask the matting fake to fail it (fake_faults.py, #301), which exercises the same
notice path. The fake answers an opaque white 64x64 square for every file (it has no alpha and no sizes yet):
the alpha and pixel-size checks are recorded as INFO there, not passed.

Gate (upstream_shape.py "fake-default"): a fake target runs it with no --live; a real or mixed target needs BOTH
--live and ART_AI_LIVE_ACK=1, or it exits 2 before any request. The batch is submitted once and never replayed;
there is no cost, but it occupies the shared GPU for tens of seconds, so run it with the user's consent.

Raw Task JSON and the delivered bytes go to a private 0700 directory outside the working tree and are removed
at the end (--private-dir, --keep-private). --report DIR writes the PASS/FAIL/SKIP report and
smoke-l5-matting-evidence.json (state sequence with timing, each result's format, colour type and pixel size,
the notice), without file names, addresses or keys.

  python3 scripts/release-env.py smoke dev-test -- python3 scripts/smoke/smoke-l5-matting.py
  ART_AI_LIVE_ACK=1 python3 scripts/release-env.py smoke dev-test -- python3 scripts/smoke/smoke-l5-matting.py --live --report DIR
"""
import json
import os
import sys

sys.dont_write_bytecode = True
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import fake_faults  # noqa: E402
import l5_common as common  # noqa: E402
import l5_direct as direct  # noqa: E402

MATTING = "matting/default/background-removal"
CAPABILITY = "background-removal"
DEFAULT_PROCESS_RESOLUTION = 1024
PARAMETER_NAMES = ("birefnet_process_res", "sdmatte_process_res")


# ------------------------------------------------------------------ pure logic

def build_sources(shape):
    """The three files of the batch, in upload order: [{name, filename, media_type, data, good, size}].

    good says whether the service should matte the file; size is its pixel size. File 3 is text under a .png
    name on a real target, and on a fake (or mixed) one a valid picture carrying the fake's own failure marker."""
    sources = [
        {"name": "png-512", "filename": "subject-512.png", "media_type": "image/png",
         "data": direct.subject_png(512), "good": True, "size": (512, 512)},
        {"name": "jpeg-256", "filename": "subject-256.jpg", "media_type": "image/jpeg",
         "data": direct.jpeg_256(), "good": True, "size": (256, 256)},
    ]
    if direct.generation_is_fake(shape):
        third = {"name": "marked-png", "filename": "third.png", "media_type": "image/png",
                 "data": fake_faults.solid_png(64, 64, ("failed",)), "good": False, "size": (64, 64)}
    else:
        third = {"name": "not-a-picture", "filename": "third.png", "media_type": "image/png",
                 "data": direct.not_a_picture(), "good": False, "size": None}
    return sources + [third]


def missing_positions(accepted):
    """The 1-based positions of the uploaded sources the service should not matte, in upload order."""
    return tuple(position for position, source in enumerate(accepted, 1) if not source["good"])


def expected_sizes(accepted):
    """The pixel sizes the delivered results should have: the good sources' sizes, in upload order."""
    return [source["size"] for source in accepted if source["good"]]


def frozen_default_problems(task):
    """The process resolutions of the frozen input: the Offering default (1024) or absent. Anything else
    means the script's batch was not the default pipeline."""
    frozen = common.frozen_parameters(task)
    return [f"{name}={frozen[name]}" for name in PARAMETER_NAMES
            if name in frozen and frozen[name] != DEFAULT_PROCESS_RESOLUTION]


def result_findings(images, expected):
    """Judge the delivered results against the good sources: (kind, item, ok, detail) tuples.

    Count and declared bytes are requirements everywhere; PNG format, the alpha channel (colour type 6) and
    the pixel sizes are what the real service must deliver (a fake that does not model them yet is recorded
    by the caller's Findings)."""
    findings = [("check", "matting delivers one result per matted file", len(images) == len(expected),
                 f"{len(images)} delivered, {len(expected)} expected")]
    broken = [entry["index"] for entry in images if not entry["matches_declared"]]
    findings.append(("check", "matting results match the declared size and hash", not broken and bool(images),
                     f"results {broken} differ" if broken else f"{len(images)} verified"))
    findings.append(("shape", "matting results are PNG pictures", all(entry.get("format") == "png" for entry in images),
                     ", ".join(str(entry.get("format")) for entry in images)))
    findings.append(("shape", "matting results carry an alpha channel (RGBA)",
                     bool(images) and all(entry.get("color_type") == 6 for entry in images),
                     ", ".join(f"type {entry.get('color_type')}" for entry in images)))
    got = sorted((entry.get("width"), entry.get("height")) for entry in images)
    want = sorted(tuple(size) for size in expected)
    findings.append(("shape", "matting results have the pixel size of their sources", got == want,
                     f"delivered {got}, sources {want}"))
    return findings


def notice_problems(task, messages, missing):
    """The Task's notice against the Schema's own sentence for the positions the service did not matte
    (fake_faults.success_problems, the check of #284): empty when the Task is right."""
    case = fake_faults.Case("batch", pictures=(), state="SUCCEEDED", delivered=len(task.get("assets") or []),
                            missing=tuple(missing))
    return fake_faults.success_problems(case, task, messages)


def evidence_entry(sources, uploaded, submission, task, states, images, problems):
    return {
        "sources": [{"name": source["name"], "uploaded": index in uploaded, "good": source["good"],
                     "bytes": len(source["data"]), "size": source["size"]}
                    for index, source in enumerate(sources)],
        "outcome": submission.outcome if submission else None, "stopped_at": submission.step if submission else None,
        "http_status": submission.status if submission else None, "states": states,
        "task": direct.task_summary(task) if task else None,
        "results": [{key: entry.get(key) for key in ("index", "status", "bytes", "media_type", "format", "width",
                                                     "height", "color_type", "alpha", "matches_declared")}
                    | {"sha256_prefix": direct.short_digest(entry.get("sha256"))} for entry in images],
        "notice_problems": problems,
    }


# ------------------------------------------------------------------ the run

def add_arguments(parser):
    parser.add_argument("--private-dir", default="",
                        help="existing 0700 directory outside the working tree for raw results (default: a temporary one)")
    parser.add_argument("--keep-private", action="store_true", help="keep the raw results instead of removing them")


def upload_sources(live, report, findings, sources):
    """Upload each source through the library upload. Returns the accepted sources and their asset ids; a file
    the server's own validation refuses is recorded, and the batch goes on without it."""
    accepted, assets, indexes = [], [], set()
    for index, source in enumerate(sources):
        status, asset_id = live.client.upload_library(source["data"], source["media_type"], source["filename"])
        if asset_id:
            indexes.add(index)
            accepted.append(source)
            assets.append(asset_id)
        elif source["good"]:
            report.failed(f"upload {source['name']}", f"HTTP {status}")
        else:
            findings.note(f"upload {source['name']} was refused by Plinth, so the batch has no bad file",
                          f"HTTP {status}")
    good = sum(1 for source in accepted if source["good"])
    report.check("upload the batch files", good == 2, f"{len(accepted)} of {len(sources)} accepted, {good} of the 2 good ones")
    return accepted, assets, indexes


def body(live, report):
    arguments = live.arguments
    findings = direct.Findings(report, not direct.generation_is_fake(live.shape))
    try:
        private = direct.PrivateDir(arguments.private_dir or None, arguments.keep_private)
    except ValueError as error:
        report.failed("private directory", str(error))
        return
    evidence = {"script": "smoke-l5-matting", "shape": live.shape}
    try:
        offerings = {item.get("id"): item for item in live.offerings(CAPABILITY)}
        if not (offerings.get(MATTING) or {}).get("available"):
            report.skipped("matting batch", f"Offering {MATTING} is not available on this target")
            return
        session = live.new_session("l5 matting")
        sources = build_sources(live.shape)
        accepted, assets, indexes = upload_sources(live, report, findings, sources)
        if len(accepted) < 2:
            return
        before = {task.get("id") for task in live.tasks(session)}
        submission = direct.direct_submit(live, session, MATTING, CAPABILITY, {"source_assets": assets})
        task, states, images, problems = {}, [], [], []
        if submission.outcome == "refused":
            report.failed("matting batch submitted", f"stopped at {submission.step}: HTTP {submission.status} {submission.detail}")
        elif submission.outcome == "unknown":
            report.failed("matting batch submitted", f"outcome unknown (HTTP {submission.status}); not replayed")
            observed = direct.observe_new_tasks(live, session, before)
            findings.note("tasks observed after an unknown submit", f"{len(observed)} new task(s)")
        else:
            report.passed("matting batch submitted", f"HTTP {submission.status}")
            task, states, timed_out = direct.await_task(live, submission.task_id, arguments.task_timeout,
                                                 interval=1 if direct.generation_is_fake(live.shape) else 3)
            private.write("matting-task.json", json.dumps(task, ensure_ascii=False, indent=2))
            if report.check("matting batch reached a terminal state", not timed_out and bool(task), f"states {states}"):
                failure = json.dumps(task.get("failure"), ensure_ascii=False)
                if report.check("matting batch SUCCEEDED", task.get("state") == common.SUCCESS_STATE,
                                f"state {task.get('state')} failure {failure}"):
                    images, problems = judge(live, report, findings, private, session, task, accepted)
        evidence["batch"] = evidence_entry(sources, indexes, submission, task, states, images, problems)
    finally:
        evidence["info"] = findings.info
        direct.write_evidence(report.directory, "smoke-l5-matting", evidence)
        private.close()


def judge(live, report, findings, private, session, task, accepted):
    """The checks of a SUCCEEDED batch: frozen defaults, the delivered results, the notice and the list.
    Returns (the described results, the notice and list problems) for the evidence."""
    defaults = frozen_default_problems(task)
    report.check("matting batch used the default pipeline", not defaults, ", ".join(defaults) or "1024/1024")
    images = direct.fetch_images(live, task, private, "matting")
    for kind, item, ok, detail in result_findings(images, expected_sizes(accepted)):
        if kind == "check":
            report.check(item, ok, detail)
        else:
            findings.check(item, ok, detail)
    messages = (live.public_schema().get("content") or {}).get("messages") or {}
    missing = missing_positions(accepted)
    problems = notice_problems(task, messages, missing)
    report.check("matting notice names the files that were not matted" if missing else "matting notice is empty when every file is matted",
                 not problems, "; ".join(problems) or (f"positions {list(missing)}" if missing else "no notice"))
    listed = next((entry for entry in live.tasks(session) if entry.get("id") == task.get("id")), None)
    listed_problems = fake_faults.listed_problems(listed, task)
    report.check("matting task list shows the same state and notices", not listed_problems, "; ".join(listed_problems))
    return images, problems + listed_problems


if __name__ == "__main__":
    sys.exit(common.execute("smoke-l5-matting", body, __doc__, add_arguments, gate="fake-default"))
