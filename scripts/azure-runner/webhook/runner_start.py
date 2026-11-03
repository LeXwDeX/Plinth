"""Azure 按需 runner 的 webhook 开机逻辑（#78，规则见 dev_test.md §2「Azure 按需 runner」）。

GitHub 仓库 webhook 在每个 workflow_job 事件上调用本函数。只有签名有效、来自本仓库、
动作为 queued 且标签含 plinth-azure 的作业才开机；开机用函数应用自身托管身份，
该身份只能读取、开机、解除分配这一台 VM。不需要也不保存任何 GitHub token。
"""

import hashlib
import hmac
import json
import os
import urllib.request

ARM = "https://management.azure.com"


def signature_valid(secret: bytes, body: bytes, header: str | None) -> bool:
    if not secret or not header or not header.startswith("sha256="):
        return False
    expected = "sha256=" + hmac.new(secret, body, hashlib.sha256).hexdigest()
    return hmac.compare_digest(expected, header)


def should_start(event: str | None, payload: dict, repository: str, label: str) -> bool:
    if event != "workflow_job" or payload.get("action") != "queued":
        return False
    if (payload.get("repository") or {}).get("full_name") != repository:
        return False
    return label in ((payload.get("workflow_job") or {}).get("labels") or [])


def arm_token() -> str:
    endpoint = os.environ["IDENTITY_ENDPOINT"]
    request = urllib.request.Request(
        f"{endpoint}?resource={ARM}/&api-version=2019-08-01",
        headers={"X-IDENTITY-HEADER": os.environ["IDENTITY_HEADER"]},
    )
    with urllib.request.urlopen(request, timeout=10) as response:
        return json.load(response)["access_token"]


def start_vm(vm_id: str) -> int:
    # 已在运行时 start 直接成功返回；ARM 以 202 异步执行，不等待开机完成。
    request = urllib.request.Request(
        f"{ARM}{vm_id}/start?api-version=2024-07-01",
        method="POST",
        data=b"",
        headers={"Authorization": f"Bearer {arm_token()}"},
    )
    with urllib.request.urlopen(request, timeout=20) as response:
        return response.status


def handle(headers, body: bytes, start=start_vm) -> tuple[int, str]:
    secret = os.environ.get("WEBHOOK_SECRET", "").encode()
    if not signature_valid(secret, body, headers.get("x-hub-signature-256")):
        return 401, "invalid signature"
    try:
        payload = json.loads(body)
    except ValueError:
        return 400, "invalid payload"
    event = headers.get("x-github-event")
    if event == "ping":
        return 200, "pong"
    if not should_start(event, payload, os.environ["RUNNER_REPOSITORY"], os.environ["RUNNER_LABEL"]):
        return 200, "ignored"
    status = start(os.environ["RUNNER_VM_ID"])
    return 202, f"start requested ({status})"
