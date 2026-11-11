#!/usr/bin/env python3
"""L5: 2D parameter matrix, driven by the Schema's declared output sizes.

For every available text-to-image Offering the script reads its `output-size`
parameter options and runs one image per chosen option. The first Offering runs
the full matrix (square, portrait, landscape, one non-default ratio); the others
run the portrait option. The delivered image's real pixel ratio must match the
chosen option. In separate sessions it then checks image-to-image:
one reference keeps the source ratio, and two references make the agent state
`reference_purposes`. Model names are never hard-coded. Needs ART_AI_LIVE_ACK=1.

Each session first locks its 2D model with the model picker (model2d), and the delivered task
is matched by model_id rather than only by capability: after #242 a turn may draw concept
sketches and deliver an image-to-image final. L5_2D_MODELS, L5_2D_SHAPES,
L5_2D_SKIP_MATRIX and L5_2D_SKIP_REFERENCES bound a run so a live check can stay within its
per-provider budget.
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
    # #270: bind the model with the session's 2D picker. The per-message ai_model is only the
    # workspace value; without this lock the Agent may submit another model with a reason.
    live.lock_model_2d(session, offering["model_id"])
    before = {t.get("id") for t in live.tasks(session)}
    match = lambda task: (task.get("model_id") == offering["model_id"]
                          and task.get("state") == common.SUCCESS_STATE)
    # #242: the turn may draw concept sketches, answer a direction card, then deliver the final
    # image with a sketch as reference. Match a new task by model, not only text-to-image.
    reply, tasks, task = live.run_generation(session, prompt, "image", offering["model_id"], before, match,
                                             parameters={name: value})
    if not report.check(f"{label} task SUCCEEDED", bool(task), str(common.describe_tasks(tasks))):
        return None
    report.check(f"{label} used the requested model", task.get("model_id") == offering["model_id"],
                 f"model={task.get('model_id')} offering={task.get('offering_id')}")
    frozen = common.frozen_parameters(live.task(task["id"]))
    if task.get("offering_id") == offering["id"] or name in frozen:
        report.check(f"{label} size parameter reached the frozen task input", frozen.get(name) == value,
                     f"frozen={frozen.get(name)}")
    else:
        print(f"INFO: {label} frozen input has no {name}; task offering {task.get('offering_id')}", flush=True)
    dimensions = live.first_required_image(task["id"])
    expected = common.option_ratio(value)
    report.check(f"{label} pixel ratio matches option", common.ratio_matches(dimensions, expected),
                 f"pixels={dimensions}")
    if dimensions and "x" in value:
        exact = dimensions == tuple(int(part) for part in value.split("x"))
        if exact:
            report.passed(f"{label} exact pixel size", "exact")
        else:
            # The ratio is the contract; an exact pixel size is informational, not a skipped check.
            print(f"INFO: {label} exact pixel size - provider delivered {dimensions}", flush=True)
    return task


def wanted_shapes(full, shapes):
    """Shapes this offering should run, narrowed by L5_2D_SHAPES (#270)."""
    keys = list(shapes) if full else [s for s in ("portrait",) if s in shapes]
    only = {s.strip() for s in os.environ.get("L5_2D_SHAPES", "").split(",") if s.strip()}
    return [s for s in keys if not only or s in only]


def run_offering(live, report, offering, full):
    options = common.size_options(offering)
    if not options:
        return report.skipped(f"{offering['model_id']} size matrix", "Offering declares no output-size parameter")
    default = next((p.get("default") for p in offering["parameters"] if p["name"] == options[0]), None)
    shapes = common.pick_shapes(options[1], default)
    wanted = wanted_shapes(full, shapes)
    if not wanted:
        return report.skipped(f"{offering['model_id']} shape matrix", "L5_2D_SHAPES selects no declared shape")
    session = live.new_session()
    if not session:
        return report.failed(f"{offering['model_id']} session", "could not create a session")
    live.lock_model_2d(session, offering["model_id"])
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
    if not session:
        return report.failed("image-to-image references session", "could not create a session")
    live.lock_model_2d(session, model)
    source = generate(live, report, session, offering, "portrait", shapes["portrait"])
    if not source:
        return report.skipped("image-to-image references", "no source image")
    source_size = live.first_required_image(source["id"])
    before = {t.get("id") for t in live.tasks(session)}
    _, tasks, settled = live.run_turn(session, SINGLE_REFERENCE, "image", model, {"image-to-image": 1})
    new = [t for t in tasks if t.get("id") not in before and t.get("capability") == "image-to-image"
           and t.get("state") == common.SUCCESS_STATE]
    report.check("one reference: image-to-image (reference-using) task SUCCEEDED", settled and bool(new),
                 f"settled={settled} " + str(common.describe_tasks(tasks[:3])))
    if new and source_size:
        result = live.first_required_image(new[0]["id"])
        report.check("one reference keeps the source ratio",
                     common.ratio_matches(result, source_size[0] / source_size[1]), f"source={source_size} result={result}")
    session = live.new_session()
    if not session:
        return report.failed("two-reference session", "could not create a session")
    live.lock_model_2d(session, model)
    generate(live, report, session, offering, "portrait", shapes["portrait"])
    generate(live, report, session, offering, "landscape", shapes["landscape"])
    mark = len(live.messages(session))
    live.run_turn(session, TWO_REFERENCES, "image", model, {})
    calls = common.calls_named(live.messages(session)[mark:], "refine_image")
    stated = [c for c in calls if common.tool_arguments(c).get("reference_purposes")]
    report.check("two references: reference_purposes stated", bool(stated), f"{len(calls)} refine_image call(s)")


def body(live, report):
    offerings = [o for o in live.offerings("text-to-image") if o.get("available") and o.get("model_id")]
    wanted = {token.strip() for token in os.environ.get("L5_2D_MODELS", "").split(",") if token.strip()}
    if wanted:
        offerings = [o for o in offerings
                     if o.get("model_id") in wanted or o.get("provider_id") in wanted
                     or o.get("id") in wanted or o.get("id", "").split("/")[0] in wanted]
    if not offerings:
        return report.failed("text-to-image Offerings available", "L5_2D_MODELS selected none")
    primary = next((o for o in offerings if common.size_options(o)), offerings[0])
    with concurrent.futures.ThreadPoolExecutor(max_workers=len(offerings) + 1) as pool:
        futures = []
        if os.environ.get("L5_2D_SKIP_MATRIX") != "1":
            futures = [pool.submit(run_offering, live, report, o, o is primary) for o in offerings]
        if os.environ.get("L5_2D_SKIP_REFERENCES") != "1":
            futures.append(pool.submit(reference_cases, live, report, primary))
        for future in futures:
            future.result()


if __name__ == "__main__":
    sys.exit(common.execute("smoke-l5-2d-params", body, __doc__))
