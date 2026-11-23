#!/usr/bin/env python3
"""Upstream shape gate for the smoke and acceptance scripts (#219).

dev-test releases in the fake upstream shape by default (scripts/release-env.py):
every Provider and chat connection points at the fake upstream service. Scripts
default to that shape and refuse to reach real upstreams unless both live
switches are set: the --live argument AND ART_AI_LIVE_ACK=1. Either alone, or
neither, refuses (exit 2). Each live run also needs the user's consent in the
conversation first (AGENTS.md "上游接口与 Fake").

The mixed shape (#416, release-env.py --live-chat) is reported as "mixed": its
generation Providers are fake but its chat connections are a real gateway, so a
script run against it spends real chat tokens. Both gates therefore treat it as
not fake: fake-default wants the two live switches, live-only refuses it because
the generation side is not real. Neither gate lets a script assume it.

The target's shape comes from ART_AI_UPSTREAM_SHAPE (release-env.py smoke sets
it from the environment's production/compose.env), else from
$ART_AI_SERVER_ROOT/production/compose.env on the host. An unknown shape counts
as live, so a script never assumes the fake and calls a real upstream.

  upstream_shape.py fake-default [--live]   # L1: fake target runs; live target needs both switches
  upstream_shape.py live-only [--live]      # L2-L5: real upstream only, both switches, refuses a fake target
  upstream_shape.py chat-live [--live]      # chat-only measures: live or mixed, refuses a fake chat target
  upstream_shape.py shape                   # print the target shape only (no gate)

Prints the shape (fake, mixed or live) on success.
"""
import os
import sys

ACK_VARIABLE = "ART_AI_LIVE_ACK"
LIVE_FLAG = "--live"
SHAPE_VARIABLE = "ART_AI_UPSTREAM_SHAPE"
SHAPES = ("fake", "mixed", "live")


def target_shape(environ):
    value = environ.get(SHAPE_VARIABLE, "")
    if value in SHAPES:
        return value
    root = environ.get("ART_AI_SERVER_ROOT", "")
    if root:
        try:
            with open(os.path.join(root, "production", "compose.env"), encoding="utf-8") as handle:
                for line in handle:
                    key, _, raw = line.strip().partition("=")
                    if key == "RELEASE_UPSTREAM_SHAPE" and raw in SHAPES:
                        return raw
        except OSError:
            pass
    return "live"


def live_switch_error(live_flag, environ):
    if live_flag and environ.get(ACK_VARIABLE) == "1":
        return None
    missing = [name for name, present in ((LIVE_FLAG, live_flag), (f"{ACK_VARIABLE}=1", environ.get(ACK_VARIABLE) == "1"))
               if not present]
    return ("refusing to run: the target calls real, paid upstreams; set both --live and "
            f"{ACK_VARIABLE}=1 (missing: {', '.join(missing)}) after the user agreed to this run")


def resolve(kind, live_flag, environ):
    """Return (shape, refusal). kind is fake-default or live-only."""
    shape = target_shape(environ)
    if kind == "fake-default":
        if shape == "fake":
            if live_flag:
                return shape, "refusing to run: --live was given but the target runs the fake upstream"
            return shape, None
        return shape, live_switch_error(live_flag, environ)
    if kind == "live-only":
        if shape != "live":
            return shape, ("refusing to run: the target does not call real, paid upstreams "
                           f"(shape {shape}); release dev-test with --live-upstream and {ACK_VARIABLE}=1 first "
                           "(user consent required)")
        return shape, live_switch_error(live_flag, environ)
    if kind == "chat-live":
        # The chat side must be a real gateway; the generation side may be fake.
        # A script that only measures how the model decides (the prompt and tool
        # table it sees, the turns it takes, the questions it asks) is comparable
        # across live and mixed, while its artifact metrics are not (#407).
        if shape == "fake":
            return shape, ("refusing to run: the target runs the fake chat model "
                           f"(shape {shape}); release dev-test with --live-chat or --live-upstream and "
                           f"{ACK_VARIABLE}=1 first (user consent required)")
        return shape, live_switch_error(live_flag, environ)
    raise ValueError(f"unknown gate {kind}")


def main(argv=None, environ=None):
    argv = sys.argv[1:] if argv is None else argv
    environ = os.environ if environ is None else environ
    if argv == ["shape"]:
        print(target_shape(environ))
        return 0
    if not argv or argv[0] not in ("fake-default", "live-only", "chat-live") or any(a != LIVE_FLAG for a in argv[1:]):
        print(__doc__, file=sys.stderr)
        return 2
    shape, refusal = resolve(argv[0], LIVE_FLAG in argv[1:], environ)
    if refusal:
        print(refusal, file=sys.stderr)
        return 2
    print(shape)
    return 0


if __name__ == "__main__":
    sys.exit(main())
