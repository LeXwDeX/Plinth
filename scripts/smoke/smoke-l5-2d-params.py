#!/usr/bin/env python3
"""L5: 2D parameter matrix, driven by the Schema's declared output sizes.

For every available text-to-image Offering the script reads its `output-size`
parameter options and runs one image per chosen option. The first Offering runs
the full matrix (square, portrait, landscape, one non-default ratio); the others
run the portrait option. The delivered image's real pixel ratio must match the
chosen option. In separate sessions it then checks image-to-image:
one reference keeps the source ratio, and two references make the agent state
`reference_purposes`. Model names are never hard-coded. Needs ART_AI_LIVE_ACK=1.
"""
import concurrent.futures
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import l5_common as common  # noqa: E402

SUBJECTS = {"square": "一枚圆形徽章图标", "portrait": "一座高塔的插画", "landscape": "一条宽阔河谷的风景插画",
            "other": "一只猫的肖像插画"}
SINGLE_REFERENCE = "把第 1 张图改成夜景，保持构图和主体不变。"
TWO_REFERENCES = ("把第 1 张图里的主体放进第 2 张图的场景里；请明确说明第 1 张图只提供主体、第 2 张图只提供场景，"
                  "用两张参考图一起生成一张新图。")


def generate(live, report, session, offering, shape, value):
    name, _ = common.size_options(offering)
    label = f"{offering['model_id']} {shape} {value}"
    prompt = f"生成{SUBJECTS[shape]}，简洁的矢量风格，画面中不要出现文字。"
    known = live.tasks(session)
    before = {t.get("id") for t in known}
    existing = common.tasks_by_capability(known).get("text-to-image", 0)
    reply, tasks, settled = live.run_turn(session, prompt, "image", offering["model_id"],
                                          {"text-to-image": existing + 1}, parameters={name: value})
    images = [t for t in tasks if t.get("id") not in before and t.get("capability") == "text-to-image"
              and t.get("state") == common.SUCCESS_STATE]
    if not report.check(f"{label} task SUCCEEDED", settled and bool(images), str(common.describe_tasks(tasks))):
        return None
    dimensions = live.first_required_image(images[0]["id"])
    expected = common.option_ratio(value)
    report.check(f"{label} pixel ratio matches option", common.ratio_matches(dimensions, expected),
                 f"pixels={dimensions}")
    if dimensions and "x" in value:
        exact = dimensions == tuple(int(part) for part in value.split("x"))
        report.record("PASS" if exact else "SKIP", f"{label} exact pixel size",
                      "exact" if exact else f"provider delivered {dimensions} (informational; ratio is the contract)")
    return images[0]


def run_offering(live, report, offering, full):
    options = common.size_options(offering)
    if not options:
        return report.skipped(f"{offering['model_id']} size matrix", "Offering declares no output-size parameter")
    default = next((p.get("default") for p in offering["parameters"] if p["name"] == options[0]), None)
    shapes = common.pick_shapes(options[1], default)
    session = live.new_session()
    wanted = list(shapes) if full else [s for s in ("portrait",) if s in shapes]
    for shape in wanted:
        generate(live, report, session, offering, shape, shapes[shape])
    return session if full else None


def reference_cases(live, report, offering):
    """Image-to-image checks, each in its own session so "image 1" is unambiguous."""
    options = common.size_options(offering)
    default = next((p.get("default") for p in offering["parameters"] if p["name"] == options[0]), None) if options else None
    shapes = common.pick_shapes(options[1], default) if options else {}
    if "portrait" not in shapes or "landscape" not in shapes:
        return report.skipped("image-to-image references", "the Offering declares no portrait and landscape size")
    model = offering["model_id"]
    session = live.new_session()
    source = generate(live, report, session, offering, "portrait", shapes["portrait"])
    if not source:
        return report.skipped("image-to-image references", "no source image")
    source_size = live.first_required_image(source["id"])
    before = {t.get("id") for t in live.tasks(session)}
    live.run_turn(session, SINGLE_REFERENCE, "image", model, {})
    new = [t for t in live.tasks(session) if t.get("id") not in before and t.get("state") == common.SUCCESS_STATE]
    report.check("one reference: image-to-image task SUCCEEDED", bool(new), str(common.describe_tasks(live.tasks(session)[:3])))
    if new and source_size:
        result = live.first_required_image(new[0]["id"])
        report.check("one reference keeps the source ratio",
                     common.ratio_matches(result, source_size[0] / source_size[1]), f"source={source_size} result={result}")
    session = live.new_session()
    generate(live, report, session, offering, "portrait", shapes["portrait"])
    generate(live, report, session, offering, "landscape", shapes["landscape"])
    mark = len(live.messages(session))
    live.run_turn(session, TWO_REFERENCES, "image", model, {})
    calls = common.calls_named(live.messages(session)[mark:], "refine_image")
    stated = [c for c in calls if common.tool_arguments(c).get("reference_purposes")]
    report.check("two references: reference_purposes stated", bool(stated), f"{len(calls)} refine_image call(s)")


def body(live, report):
    offerings = [o for o in live.offerings("text-to-image") if o.get("available") and o.get("model_id")]
    if not offerings:
        return report.failed("text-to-image Offerings available")
    primary = next((o for o in offerings if common.size_options(o)), offerings[0])
    with concurrent.futures.ThreadPoolExecutor(max_workers=len(offerings) + 1) as pool:
        futures = [pool.submit(run_offering, live, report, o, o is primary) for o in offerings]
        futures.append(pool.submit(reference_cases, live, report, primary))
        for future in futures:
            future.result()


if __name__ == "__main__":
    sys.exit(common.execute("smoke-l5-2d-params", body, __doc__))
