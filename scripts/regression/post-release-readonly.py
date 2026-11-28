#!/usr/bin/env python3
"""prod 发布后的只读冒烟：L1 子集，免费，不调用任何上游（只走本服务的 API）。

层：prod-readonly。可对任何环境运行（prod、dev-test 都行），不发消息、不生成、不批准任何东西。唯一的写入：
在 Agent 专用账号下建一个标题为 regress-l1 的空会话并把它切到严格方式（核对会话接口与批准方式接口可写）；
另有一次故意被拒的跨站建会话请求。

期望：
  - /api/version 200，release 非空；设置了 EXPECT_SHA（release-env.py smoke 注入目标 revision）时，version 与它的
    前缀一致；
  - /healthz 与 /readyz 200；/api/v1/schema 200 且有 revision 与 chat；/login 响应带 Content-Security-Policy；
  - Agent 专用账号登录 200；跨站来源（Origin 与 Sec-Fetch-Site: cross-site）建会话被 403 拒绝；同源建会话 201；
  - 任务列表、text-to-image Offering 列表（非空）、待批准列表 200；把新会话切到严格方式 200。
v1.9.6 正式测试环境实测 11 项全部通过（版本检查是入库时新增的，此前没有）。

用法（地址与账号由 release-env.py 注入）：
  python3 scripts/release-env.py smoke prod -- python3 scripts/regression/post-release-readonly.py
最后一行 `=== post-release-readonly: PASS=n FAIL=n ===`；有 FAIL 时退出码 1，缺环境变量时退出码 2。
"""
import json
import os
import sys
import urllib.request

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from lib import Api, Report, base_problem, missing_env  # noqa: E402


def main():
    if "--live" in sys.argv[1:]:
        print("refusing to run: this script calls no upstream; --live is not accepted", file=sys.stderr)
        return 2
    missing = missing_env()
    problem = ("missing environment: " + ", ".join(missing)) if missing else base_problem(os.environ["BASE"])
    if problem:
        print(problem + " (run through scripts/release-env.py smoke <environment>)", file=sys.stderr)
        return 2
    api = Api()
    r = Report("post-release-readonly")
    status, version = api.json("GET", "/api/version")
    expected = (os.environ.get("EXPECT_SHA") or "").strip()
    current = str(version.get("version") or "")
    same = not expected or (current and (current.startswith(expected[:7]) or expected.startswith(current[:7])))
    r.check("version 200, release set" + (", matches EXPECT_SHA" if expected else ""),
            status == 200 and bool(version.get("release")) and same, f"release={version.get('release')} version={current[:12]}")
    r.check("healthz", api.raw("GET", "/healthz")[0] == 200)
    r.check("readyz", api.raw("GET", "/readyz")[0] == 200)
    status, schema = api.json("GET", "/api/v1/schema")
    schema = schema.get("schema", schema)
    r.check("schema revision and chat", status == 200 and bool(schema.get("revision")) and bool(schema.get("chat")))
    request = urllib.request.Request(api.base + "/login")
    try:
        with api.opener.open(request, timeout=30) as response:
            headers = {k.lower() for k in response.headers.keys()}
    except Exception:  # an error page still has to carry the header, but then the check fails
        headers = set()
    r.check("CSP header on /login", "content-security-policy" in headers)
    status, _ = api.json("POST", "/api/auth/login", {"username": os.environ["AGENT_TEST_USERNAME"],
                                                     "password": os.environ["AGENT_TEST_PASSWORD"]})
    if not r.check("login", status == 200, f"HTTP {status}"):
        return r.finish()
    status, _ = api.json("POST", "/api/chat/sessions", {"title": "regress-l1"},
                         headers={"Origin": "http://cross-site.invalid", "Sec-Fetch-Site": "cross-site"})
    r.check("cross-origin session create rejected", status == 403, f"HTTP {status}")
    status, session = api.json("POST", "/api/chat/sessions", {"title": "regress-l1"})
    sid = session.get("id") if status in (200, 201) else None
    r.check("create session", bool(sid), f"HTTP {status}")
    r.check("task list", api.json("GET", "/api/v1/tasks?limit=5")[0] == 200)
    status, body = api.json("GET", "/api/v1/offerings?capability=text-to-image")
    found = body.get("offerings") or []
    r.check("text-to-image offerings", status == 200 and len(found) > 0, f"n={len(found)}")
    if sid:
        r.check("pending approvals", api.json("GET", f"/api/v1/chat/sessions/{sid}/pending-approvals")[0] == 200)
        status, body = api.json("PUT", f"/api/v1/chat/sessions/{sid}/approval", {"approvalMode": "manual"})
        r.check("approval mode manual", status == 200, f"HTTP {status} {json.dumps(body)[:60]}")
    return r.finish()


if __name__ == "__main__":
    sys.exit(main())
