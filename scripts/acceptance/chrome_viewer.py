#!/usr/bin/env python3
"""Launcher for the large-GLB Chrome viewer spot check (chrome_viewer.mjs).

Opens an existing large GLB model and a small one from a test account in the system Chrome (or Playwright Chromium),
checks rotate/zoom/reset, clay/wire/texture view modes, byte-exact GLB download (size + SHA-256 taken from the public
Task) and, for one cell, the ZIP archive; then tampers with the small model's response (corrupted embedded texture,
truncated body) and checks the viewer shows its error state and recovers on retry. It creates no Task.

Run through the environment injector so credentials never touch files:

  python3 scripts/release-env.py smoke <env> -- python3 scripts/acceptance/chrome_viewer.py \\
      --session SESSION_ID --big-task TASK_ID --big-tile '3D v5' --small-task TASK_ID --small-tile '3D v6'

Environment (injected by release-env.py smoke): BASE, ADMIN_USERNAME, ADMIN_PASSWORD.
"""
import argparse
import os
import shutil
import subprocess
import sys
from pathlib import Path

HERE = Path(__file__).resolve().parent


def main(argv=None):
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--session", required=True, help="session id that holds both models")
    ap.add_argument("--big-task", required=True, help="Task id of a large GLB (tens of MB or more)")
    ap.add_argument("--big-tile", required=True, help="regex matching the version tile label of the big model, e.g. '3D v5'")
    ap.add_argument("--small-task", required=True, help="Task id of a small textured GLB used for the negative checks")
    ap.add_argument("--small-tile", required=True, help="regex matching the version tile label of the small model")
    ap.add_argument("--cells", help="locale:WxH list, default zh-CN/en at 1366x768 and 1920x1080")
    ap.add_argument("--full-download", help="cell that also downloads the ZIP archive, default zh-CN:1366x768")
    ap.add_argument("--channel", default="chrome", help="Playwright channel (chrome, or chromium for the bundled build)")
    ap.add_argument("--out", help="output directory for screenshots and results.json (default: system temp dir)")
    args = ap.parse_args(argv)
    for name in ("BASE", "ADMIN_USERNAME", "ADMIN_PASSWORD"):
        if not os.environ.get(name):
            print(f"missing environment {name}; run through: python3 scripts/release-env.py smoke <env> -- python3 {sys.argv[0]} ...", file=sys.stderr)
            return 2
    node = shutil.which("node")
    if not node:
        print("node not found", file=sys.stderr)
        return 2
    env = dict(os.environ, CV_SESSION=args.session, CV_BIG_TASK=args.big_task, CV_BIG_TILE=args.big_tile,
               CV_SMALL_TASK=args.small_task, CV_SMALL_TILE=args.small_tile, CV_CHANNEL=args.channel)
    if args.cells:
        env["CELLS"] = args.cells
    if args.full_download:
        env["FULLDL"] = args.full_download
    if args.out:
        env["OUT"] = args.out
    return subprocess.call([node, str(HERE / "chrome_viewer.mjs")], env=env)


if __name__ == "__main__":
    sys.exit(main())
