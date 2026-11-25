#!/usr/bin/env python3
"""L5: the 2D image providers, one native request per task (#540).

Seven tasks, each sent once through the public task API (POST /api/v1/tasks/prepare, /grants, /tasks) and not
through the Agent, so what is measured is the provider wire and the Product Schema's mapping of it:

  openai-generate  gpt-image-2.6-sunburst text-to-image: 1536x1152, quality low, n=2 (two distinct pictures)
  openai-edits     image-to-image: two 512 pictures + a 512 mask, 1024x1024, quality low, n=1
  gemini-generate  gemini-3-pro-image text-to-image: 16:9, 1K (4K with --large)
  gemini-edit      image-to-image: two 512 pictures (one with alpha), 3:4, 1K
  jimeng-generate  seedream-4-5 text-to-image: 1920x1920 (4096x4096 with --large), inline pictures
  jimeng-edit      image-to-image: two 512 pictures, 1920x1920 (4096x4096 with --large)
  jimeng-sequence  text-to-image with sequential generation: max_images=2

Defaults and switches
  * The target's shape decides (upstream_shape.py "fake-default"). A fake target runs it at no cost and takes
    no --live; a real or mixed target needs BOTH --live and ART_AI_LIVE_ACK=1, or it exits 2 before any
    request. Each real run also needs the user's consent in the conversation first (AGENTS.md).
  * --large switches the large variants on: Gemini 4K and Seedream 4096x4096. They are slower and larger
    (a Gemini 4K answer is tens of megabytes; a lost answer may still have been billed), so they never run
    by default.
  * A content probe (a prompt meant to be refused by OpenAI moderation, the Gemini safety layer or Jimeng's
    sensitive-content check) never runs by default. --content-probe runs only that, for one --provider with
    one --probe-prompt-file, and needs a third switch ART_AI_CONTENT_PROBE_ACK=1 besides the two live
    switches. The prompt is read from the file, sent, and neither printed nor stored (only its length and
    SHA-256 go to the evidence). The fake refuses it: it never blocks content.

Cost (a real run; figures are second-hand estimates in US dollars, the gateways bill in their own credits,
unknown): openai-generate 0.02 and openai-edits 0.02 (low quality), gemini-generate 0.134 at 1K (0.24 at 4K),
gemini-edit 0.134, jimeng-generate 0.04, jimeng-edit 0.04, jimeng-sequence 0.08. The default round is about
0.47, with --large about 0.57. --budget-usd (default 0.75) is the ceiling of the whole run: a task that would
exceed it is skipped, never sent. --max-tasks bounds the count.

One submission each. A refused step is recorded; a submit whose outcome is unknown (no answer, or a 5xx) is
only observed through the session's task list; nothing is replayed, a failed Task is not retried.

Evidence. The server records the exchanges with the providers when it was started with UPSTREAM_CAPTURE_DIR
(docs/deployment.md, #233); feed those records to `fake-upstream-align draft` afterwards. This script writes
raw Task JSON and the delivered bytes to a private 0700 directory outside the working tree (--private-dir, or
a temporary one) and removes them at the end unless --keep-private. With --report DIR it writes the usual
PASS/FAIL/SKIP report and smoke-l5-2d-direct-evidence.json: per task the quote, the state sequence and timing,
each picture's format, size, SHA-256 prefix and pixel size, the frozen parameters without free text. No prompt,
signed-URL query, address or key is printed or stored.

What is asserted. A real target: the Task SUCCEEDED, the expected number of pictures is delivered READY with
matching bytes, n=2 pictures differ, the pixel ratio matches the request. An exact pixel size different from
the request is recorded as a deviation, not a failure. On a fake target the pictures are small fixed ones, so
ratio and count checks the fake cannot meet yet are recorded as INFO (not modelled by the fake yet), not passed.

  python3 scripts/release-env.py smoke dev-test -- python3 scripts/smoke/smoke-l5-2d-direct.py
  ART_AI_LIVE_ACK=1 python3 scripts/release-env.py smoke dev-test -- python3 scripts/smoke/smoke-l5-2d-direct.py --live --report DIR
"""
import json
import os
import sys

sys.dont_write_bytecode = True
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import l5_common as common  # noqa: E402
import l5_direct as direct  # noqa: E402

PROVIDERS = ("openai", "gemini", "jimeng")
DEFAULT_BUDGET_USD = 0.75
DEFAULT_MAX_TASKS = 7
REFERENCE_SIZE = 512
SMALL_JIMENG_SIZE = "1920x1920"  # 3686400 pixels, the smallest area Seedream 4.5 takes
QUALITY_REASON = "wire acceptance run: the cheapest quality is enough to observe the request and answer shape"
COUNT_REASON = "wire acceptance run: two pictures are needed to observe a multi-picture answer"
SEQUENCE_REASON = "wire acceptance run: the sequence mode is the case under test"
IMAGE_SIZE_REASON = "wire acceptance run: the cheapest resolution is enough to observe the request and answer shape"

# Each case is one submission. `cost` is the estimated US dollars of the default variant, `large_cost` of
# the --large variant; `refs` is the number of reference pictures, `mask` whether an edit mask is sent.
# `count` is the expected number of delivered pictures as (minimum, maximum); `ratio` the width/height the
# request asks for; `size` the exact pixel size it asks for (a different one is recorded, not failed);
# `format` the picture format the wire is expected to deliver (recorded when it differs).
CASES = (
    {"key": "openai-generate", "provider": "openai", "label": "OpenAI text-to-image n=2",
     "offering": "openai/gpt-image-2/text-to-image", "capability": "text-to-image", "cost": 0.02,
     "refs": 0, "count": (2, 2), "size": (1536, 1152), "ratio": 1536 / 1152, "format": "png"},
    {"key": "openai-edits", "provider": "openai", "label": "OpenAI image-to-image with mask",
     "offering": "openai/gpt-image-2/image-to-image", "capability": "image-to-image", "cost": 0.02,
     "refs": 2, "mask": True, "count": (1, 1), "size": (1024, 1024), "ratio": 1.0, "format": "png"},
    {"key": "gemini-generate", "provider": "gemini", "label": "Gemini text-to-image 16:9",
     "offering": "gemini/gemini-3-pro-image/text-to-image", "capability": "text-to-image", "cost": 0.134,
     "large_cost": 0.24, "refs": 0, "count": (1, 1), "ratio": 16 / 9},
    {"key": "gemini-edit", "provider": "gemini", "label": "Gemini image-to-image 3:4",
     "offering": "gemini/gemini-3-pro-image/image-to-image", "capability": "image-to-image", "cost": 0.134,
     "refs": 2, "count": (1, 1), "ratio": 3 / 4},
    {"key": "jimeng-generate", "provider": "jimeng", "label": "Jimeng text-to-image",
     "offering": "jimeng/seedream-4-5/text-to-image", "capability": "text-to-image", "cost": 0.04,
     "refs": 0, "count": (1, 1), "size": (1920, 1920), "large_size": (4096, 4096), "ratio": 1.0, "format": "jpeg"},
    {"key": "jimeng-edit", "provider": "jimeng", "label": "Jimeng image-to-image two references",
     "offering": "jimeng/seedream-4-5/image-to-image", "capability": "image-to-image", "cost": 0.04,
     "refs": 2, "count": (1, 1), "size": (1920, 1920), "large_size": (4096, 4096), "ratio": 1.0, "format": "jpeg"},
    {"key": "jimeng-sequence", "provider": "jimeng", "label": "Jimeng sequential images max 2",
     "offering": "jimeng/seedream-4-5/text-to-image", "capability": "text-to-image", "cost": 0.08,
     "refs": 0, "count": (1, 2), "size": (1920, 1920), "ratio": 1.0, "format": "jpeg"},
)

PROMPTS = {
    "text": "a plain red cube on a white background, flat studio lighting, no text",
    "edit": "recolor the shapes to bright green and keep the layout of the picture unchanged, no text",
    "mask": "fill the transparent square of the mask with a small yellow star and keep everything else unchanged",
    "sequence": "two simple icons of a red cube, one seen from the front and one from above, white background, no text",
}


# ------------------------------------------------------------------ pure logic

def case_cost(case, large):
    """Estimated US dollars of one case in the chosen variant."""
    return case.get("large_cost", case["cost"]) if large else case["cost"]


def case_size(case, large):
    return case.get("large_size", case.get("size")) if large else case.get("size")


def select_cases(only):
    """The cases named by --only (comma separated keys), in table order; all when empty. Raises ValueError
    for an unknown key."""
    wanted = [token.strip() for token in (only or "").split(",") if token.strip()]
    keys = {case["key"] for case in CASES}
    unknown = [token for token in wanted if token not in keys]
    if unknown:
        raise ValueError("unknown task key(s) " + ", ".join(unknown) + "; known: " + ", ".join(sorted(keys)))
    return [case for case in CASES if not wanted or case["key"] in wanted]


def plan_run(cases, large, budget, max_tasks):
    """Split the cases into (run, skipped) under the budget and the task ceiling. A case that would push the
    estimated spend over the budget, or the count over the ceiling, is skipped with its reason; it is never
    sent."""
    run, skipped, spent = [], [], 0.0
    for case in cases:
        cost = case_cost(case, large)
        if len(run) >= max_tasks:
            skipped.append((case, f"over the task ceiling {max_tasks}"))
        elif spent + cost > budget + 1e-9:
            skipped.append((case, f"estimated {spent + cost:.3f} USD would exceed the budget {budget:.2f} USD"))
        else:
            run.append(case)
            spent += cost
    return run, skipped, round(spent, 4)


def build_input(case, assets, large):
    """(input, parameter_reasons) of one case. `assets` holds the uploaded asset ids by name: ref_a, ref_b
    (a picture with alpha) and mask. Only parameters the Offering declares are sent; a change of an advised
    parameter (quality, image_size, n, sequential_image_generation; #586) carries its reason, as the public API demands (#295)."""
    key = case["key"]
    refs = [assets["ref_a"], assets["ref_b"]][:case.get("refs", 0)]
    if key == "openai-generate":
        return ({"prompt": PROMPTS["text"], "size": "1536x1152", "quality": "low", "n": 2},
                {"quality": QUALITY_REASON, "n": COUNT_REASON})
    if key == "openai-edits":
        return ({"prompt": PROMPTS["mask"], "reference_assets": refs, "mask": assets["mask"],
                 "size": "1024x1024", "quality": "low", "n": 1}, {"quality": QUALITY_REASON})
    if key == "gemini-generate":
        if large:  # 4K is the Offering's own default: no change, no reason
            return {"prompt": PROMPTS["text"], "aspect_ratio": "16:9", "image_size": "4K"}, None
        return ({"prompt": PROMPTS["text"], "aspect_ratio": "16:9", "image_size": "1K"},
                {"image_size": IMAGE_SIZE_REASON})
    if key == "gemini-edit":
        return ({"prompt": PROMPTS["edit"], "reference_assets": refs, "aspect_ratio": "3:4", "image_size": "1K"},
                {"image_size": IMAGE_SIZE_REASON})
    size = "x".join(str(side) for side in case_size(case, large))
    if key == "jimeng-generate":
        return {"prompt": PROMPTS["text"], "size": size, "sequential_image_generation": "disabled"}, None
    if key == "jimeng-edit":
        return ({"prompt": PROMPTS["edit"], "reference_assets": refs, "size": size,
                 "sequential_image_generation": "disabled"}, None)
    if key == "jimeng-sequence":
        return ({"prompt": PROMPTS["sequence"], "size": size, "sequential_image_generation": "auto",
                 "max_images": 2}, {"sequential_image_generation": SEQUENCE_REASON,
                                                                    "max_images": SEQUENCE_REASON})
    raise ValueError(f"no input for {key}")


def probe_input(provider):
    """The cheapest valid input of one provider's text-to-image Offering for a content probe, with the prompt
    left to the caller. Returns (offering, input without prompt, reasons)."""
    if provider == "openai":
        return ("openai/gpt-image-2/text-to-image", {"size": "1024x1024", "quality": "low", "n": 1},
                {"quality": QUALITY_REASON})
    if provider == "gemini":
        return ("gemini/gemini-3-pro-image/text-to-image", {"aspect_ratio": "1:1", "image_size": "1K"},
                {"image_size": IMAGE_SIZE_REASON})
    if provider == "jimeng":
        return ("jimeng/seedream-4-5/text-to-image",
                {"size": SMALL_JIMENG_SIZE, "sequential_image_generation": "disabled"}, None)
    raise ValueError(f"unknown provider {provider}")


def picture_findings(case, large, images):
    """Judge the delivered pictures of one SUCCEEDED Task against its case. Returns a list of
    (kind, item, ok, detail) with kind "check" (a requirement) or "note" (a recorded deviation):

      * the delivered count is within the case's (minimum, maximum);
      * every picture arrived READY with the declared size and hash;
      * several pictures of one task are distinct;
      * the pixel ratio matches the request (a requirement);
      * an exact pixel size and the picture format differing from the request are notes, not failures."""
    findings = []
    low, high = case["count"]
    count = len(images)
    findings.append(("check", f"{case['label']} delivers {low if low == high else f'{low}-{high}'} picture(s)",
                     low <= count <= high, f"{count} delivered"))
    broken = [entry["index"] for entry in images if not entry["matches_declared"]]
    findings.append(("check", f"{case['label']} bytes match the declared size and hash", not broken and count > 0,
                     f"pictures {broken} differ" if broken else f"{count} verified"))
    if count > 1:
        digests = {entry["sha256"] for entry in images}
        findings.append(("check", f"{case['label']} pictures are distinct", len(digests) == count,
                         f"{len(digests)} distinct of {count}"))
    ratio = case.get("ratio")
    for entry in images:
        width, height = entry.get("width"), entry.get("height")
        if ratio and width and height:
            ok = common.ratio_matches((width, height), ratio)
            findings.append(("check", f"{case['label']} picture {entry['index']} pixel ratio matches the request", ok,
                             f"{width}x{height}"))
        elif ratio:
            findings.append(("check", f"{case['label']} picture {entry['index']} pixel size is readable", False,
                             f"format {entry.get('format')}"))
        size = case_size(case, large)
        if size and width and height and (width, height) != tuple(size):
            findings.append(("note", f"{case['label']} picture {entry['index']} pixel size deviates",
                             False, f"asked {size[0]}x{size[1]}, delivered {width}x{height}"))
        expected_format = case.get("format")
        if expected_format and entry.get("format") and entry["format"] != expected_format:
            findings.append(("note", f"{case['label']} picture {entry['index']} format differs from the usual",
                             False, f"expected {expected_format}, delivered {entry['format']}"))
    return findings


def evidence_entry(case, large, submission, task, states, images, observed):
    """The scrubbed evidence of one case."""
    return {
        "key": case["key"], "offering": case["offering"], "large": bool(large),
        "estimated_usd": case_cost(case, large), "outcome": submission.outcome, "stopped_at": submission.step,
        "http_status": submission.status, "refusal": submission.detail if submission.outcome != "accepted" else "",
        "quote": direct.scrub_value(submission.quote), "adjustments": direct.scrub_value(submission.adjustments),
        "states": states, "task": direct.task_summary(task) if task else None,
        "pictures": [{key: entry.get(key) for key in ("index", "variant", "status", "bytes", "media_type", "format",
                                                      "width", "height", "color_type", "alpha", "matches_declared")}
                     | {"sha256_prefix": direct.short_digest(entry.get("sha256"))} for entry in images],
        "observed_unknown": [direct.task_summary(item) for item in observed],
    }


# ------------------------------------------------------------------ the run

def add_arguments(parser):
    parser.add_argument("--only", default="", help="comma-separated task keys to run (default all seven)")
    parser.add_argument("--large", action="store_true",
                        help="run the large variants: Gemini 4K and Seedream 4096x4096 (slower, bigger, dearer)")
    parser.add_argument("--budget-usd", type=float, default=DEFAULT_BUDGET_USD,
                        help=f"ceiling of the estimated spend of the whole run (default {DEFAULT_BUDGET_USD})")
    parser.add_argument("--max-tasks", type=int, default=DEFAULT_MAX_TASKS,
                        help=f"ceiling of submissions (default {DEFAULT_MAX_TASKS})")
    parser.add_argument("--private-dir", default="",
                        help="existing 0700 directory outside the working tree for raw results (default: a temporary one)")
    parser.add_argument("--keep-private", action="store_true", help="keep the raw results instead of removing them")
    parser.add_argument("--content-probe", action="store_true",
                        help="run only a content probe (needs --provider, --probe-prompt-file and ART_AI_CONTENT_PROBE_ACK=1)")
    parser.add_argument("--provider", default="", help="content probe: openai, gemini or jimeng")
    parser.add_argument("--probe-prompt-file", default="", help="content probe: file with the prompt (never printed or stored)")


def precheck(arguments, shape, environ):
    return direct.content_probe_refusal(arguments, shape, environ, PROVIDERS)


def upload_references(live, report, findings):
    """Upload the reference pictures and the mask. Returns {name: asset id} or None."""
    pictures = {"ref_a": ("reference-a.png", direct.subject_png(REFERENCE_SIZE)),
                "ref_b": ("reference-b.png", direct.translucent_png(REFERENCE_SIZE)),
                "mask": ("mask.png", direct.mask_png(REFERENCE_SIZE))}
    assets = {}
    for name, (filename, data) in pictures.items():
        status, asset_id = live.client.upload_library(data, "image/png", filename)
        if not asset_id:
            report.failed(f"upload {name}", f"HTTP {status}")
            return None
        assets[name] = asset_id
    report.passed("upload the reference pictures and the mask", f"{len(assets)} assets")
    return assets


def run_case(live, report, findings, private, session, assets, case, large):
    """One case: submit once, observe the Task, judge the pictures. Returns its evidence entry."""
    label = case["label"]
    payload, reasons = build_input(case, assets, large)
    before = {task.get("id") for task in live.tasks(session)}
    submission = direct.direct_submit(live, session, case["offering"], case["capability"], payload, reasons)
    observed, task, states, images = [], {}, [], []
    if submission.outcome == "refused":
        report.failed(f"{label} submitted", f"stopped at {submission.step}: HTTP {submission.status} {submission.detail}")
    elif submission.outcome == "unknown":
        report.failed(f"{label} submitted", f"outcome unknown at {submission.step} (HTTP {submission.status}); "
                                            "not replayed, observing the session's tasks")
        observed = direct.observe_new_tasks(live, session, before)
        findings.note(f"{label} observed tasks after an unknown submit", f"{len(observed)} new task(s)")
    else:
        report.passed(f"{label} submitted", f"HTTP {submission.status}")
        task, states, timed_out = direct.await_task(live, submission.task_id, live.arguments.task_timeout,
                                                  interval=1 if direct.generation_is_fake(live.shape) else 5)
        private.write(f"{case['key']}-task.json", json.dumps(task, ensure_ascii=False, indent=2))
        if report.check(f"{label} reached a terminal state", not timed_out and bool(task),
                        f"states {states}"):
            if report.check(f"{label} SUCCEEDED", task.get("state") == common.SUCCESS_STATE,
                            f"state {task.get('state')} failure {json.dumps(task.get('failure'), ensure_ascii=False)}"):
                images = direct.fetch_images(live, task, private, case["key"])
                for kind, item, ok, detail in picture_findings(case, large, images):
                    if kind == "note":
                        findings.note(item, detail)
                    else:
                        findings.check(item, ok, detail)
    return evidence_entry(case, large, submission, task, states, images, observed)


def run_content_probe(live, report, findings, private, session, arguments):
    """One provider, one prompt from the user's file, one submission; the outcome (a Task that succeeds, or
    fails with the Schema's failure class) is observed, not judged."""
    text, digest, length = direct.load_probe_prompt(arguments.probe_prompt_file)
    offering, payload, reasons = probe_input(arguments.provider)
    payload = dict(payload, prompt=text)
    before = {task.get("id") for task in live.tasks(session)}
    submission = direct.direct_submit(live, session, offering, "text-to-image", payload, reasons)
    label = f"content probe {arguments.provider}"
    evidence = {"provider": arguments.provider, "offering": offering, "prompt_length": length,
                "prompt_sha256_prefix": direct.short_digest(digest), "outcome": submission.outcome,
                "stopped_at": submission.step, "http_status": submission.status, "states": [], "task": None}
    if submission.outcome == "refused":
        # A refusal before the provider saw anything (for example the prompt is over the Offering's limit).
        report.check(f"{label} reached the provider", False,
                     f"stopped at {submission.step}: HTTP {submission.status} {submission.detail}")
        evidence["refusal"] = submission.detail
    elif submission.outcome == "unknown":
        report.failed(f"{label} submitted", f"outcome unknown at {submission.step}; not replayed")
        evidence["observed_unknown"] = [direct.task_summary(task)
                                        for task in direct.observe_new_tasks(live, session, before)]
    else:
        task, states, timed_out = direct.await_task(live, submission.task_id, arguments.task_timeout)
        # The server echoes the frozen input, prompt included: only the scrubbed summary is written.
        private.write("content-probe-task.json", json.dumps(direct.task_summary(task), ensure_ascii=False, indent=2))
        report.check(f"{label} reached a terminal state", not timed_out and bool(task), f"states {states}")
        evidence.update({"states": states, "task": direct.task_summary(task)})
        failure = task.get("failure") or {}
        findings.note(f"{label} outcome", f"state {task.get('state')} failure class {failure.get('class')}")
    return evidence


def body(live, report):
    arguments = live.arguments
    strict = not direct.generation_is_fake(live.shape)
    findings = direct.Findings(report, strict)
    try:
        private = direct.PrivateDir(arguments.private_dir or None, arguments.keep_private)
    except ValueError as error:
        report.failed("private directory", str(error))
        return
    evidence = {"script": "smoke-l5-2d-direct", "shape": live.shape, "large": arguments.large, "cases": []}
    try:
        session = live.new_session("l5 2d direct")
        if arguments.content_probe:
            evidence["content_probe"] = run_content_probe(live, report, findings, private, session, arguments)
            return
        try:
            cases = select_cases(arguments.only)
        except ValueError as error:
            report.failed("task selection", str(error))
            return
        run, skipped, estimate = plan_run(cases, arguments.large, arguments.budget_usd, arguments.max_tasks)
        for case, reason in skipped:
            report.skipped(case["label"], reason)
        print(f"INFO: {len(run)} task(s), estimated {estimate:.3f} USD of {arguments.budget_usd:.2f} USD "
              f"(shape {live.shape}{', large' if arguments.large else ''})", flush=True)
        assets = upload_references(live, report, findings) if run else {}
        if assets is None:
            return
        offerings = {item.get("id"): item for item in live.offerings("text-to-image") + live.offerings("image-to-image")}
        for case in run:
            offering = offerings.get(case["offering"])
            if not offering or not offering.get("available"):
                report.skipped(case["label"], f"Offering {case['offering']} is not available on this target")
                continue
            entry = run_case(live, report, findings, private, session, assets, case, arguments.large)
            evidence["cases"].append(entry)
        evidence["estimated_usd"] = estimate
        evidence["skipped"] = [{"key": case["key"], "reason": reason} for case, reason in skipped]
    finally:
        evidence["info"] = findings.info
        direct.write_evidence(report.directory, "smoke-l5-2d-direct", evidence)
        private.close()


if __name__ == "__main__":
    sys.exit(common.execute("smoke-l5-2d-direct", body, __doc__, add_arguments, gate="fake-default", precheck=precheck))
