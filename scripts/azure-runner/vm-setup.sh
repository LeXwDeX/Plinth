#!/bin/bash
# Azure 按需 runner 的 VM 安装脚本（#78，规则见 dev_test.md §2「Azure 按需 runner」）。
# 以 root 经 `az vm run-command invoke --command-id RunShellScript` 执行，可重复执行。
# 执行前把 __REG_TOKEN__ 替换为仓库 runner 注册 token（`gh api -X POST
# repos/<owner>/<repo>/actions/runners/registration-token`，一小时有效），替换后的副本
# 用完即删，不提交、不打印。机器上不放 Plinth 运行密钥。
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive

RUNNER_VERSION=2.338.2
RUNNER_SHA256=baebf204307bac6193480852732f0aba2ae97b6c5597c244f4d5558390147c40
REG_TOKEN="__REG_TOKEN__"

apt-get update -q
apt-get install -y -q build-essential git curl jq unzip ca-certificates

# 空闲自动解除分配：systemd 定时器每分钟检查一次。
mkdir -p /opt/plinth-ci /var/lib/plinth-ci
cat > /opt/plinth-ci/idle-stop.sh <<'EOS'
#!/bin/bash
# 无 job 运行满 10 分钟就用 VM 自身托管身份解除分配。卡死的 job 由工作流的
# timeout-minutes 结束；平台侧另有每日定时关机兜底。
set -u
state=/var/lib/plinth-ci/last-busy
now=$(date +%s)
boot=$(( now - $(cut -d. -f1 /proc/uptime) ))
if [ ! -f "$state" ] || [ "$(cat "$state")" -lt "$boot" ]; then
  echo "$now" > "$state"
fi
if pgrep -f Runner.Worker > /dev/null; then
  echo "$now" > "$state"
fi
idle=$(( now - $(cat "$state") ))
if [ "$idle" -ge 600 ]; then
  token=$(curl -sf -H Metadata:true "http://169.254.169.254/metadata/identity/oauth2/token?api-version=2018-02-01&resource=https://management.azure.com/" | jq -r .access_token)
  resource=$(curl -sf -H Metadata:true "http://169.254.169.254/metadata/instance/compute/resourceId?api-version=2021-02-01&format=text")
  logger -t plinth-ci "deallocating: idle=${idle}s"
  curl -sf -X POST -H "Authorization: Bearer $token" -H "Content-Length: 0" \
    "https://management.azure.com${resource}/deallocate?api-version=2024-07-01" > /dev/null
fi
EOS
chmod 755 /opt/plinth-ci/idle-stop.sh
cat > /etc/systemd/system/plinth-ci-idle-stop.service <<'EOS'
[Unit]
Description=Deallocate the Plinth CI runner VM when idle

[Service]
Type=oneshot
ExecStart=/opt/plinth-ci/idle-stop.sh
EOS
cat > /etc/systemd/system/plinth-ci-idle-stop.timer <<'EOS'
[Unit]
Description=Check Plinth CI runner idleness every minute

[Timer]
OnBootSec=2min
OnUnitActiveSec=1min

[Install]
WantedBy=timers.target
EOS
systemctl daemon-reload
systemctl enable --now plinth-ci-idle-stop.timer

# GitHub Actions runner as an unprivileged user.
id runner > /dev/null 2>&1 || useradd -m -s /bin/bash runner
cd /home/runner
if [ ! -d actions-runner ]; then
  mkdir actions-runner
  curl -sfL -o /tmp/runner.tgz "https://github.com/actions/runner/releases/download/v${RUNNER_VERSION}/actions-runner-linux-x64-${RUNNER_VERSION}.tar.gz"
  echo "${RUNNER_SHA256}  /tmp/runner.tgz" | sha256sum -c -
  tar -xzf /tmp/runner.tgz -C actions-runner
  rm /tmp/runner.tgz
  ./actions-runner/bin/installdependencies.sh
fi
cat > /home/runner/job-started.sh <<'EOS'
#!/bin/bash
# 每个 job 开始前清空上一个 job 留在工作目录里的文件。
if [ -n "${GITHUB_WORKSPACE:-}" ] && [ -d "$GITHUB_WORKSPACE" ]; then
  find "$GITHUB_WORKSPACE" -mindepth 1 -maxdepth 1 -exec rm -rf {} +
fi
EOS
chmod 755 /home/runner/job-started.sh
chown -R runner:runner /home/runner
if [ ! -f actions-runner/.runner ]; then
  sudo -u runner bash -c "cd /home/runner/actions-runner && ./config.sh --unattended --url https://github.com/LeXwDeX/Plinth --token '${REG_TOKEN}' --name plinth-azure-f16 --labels plinth-azure --work _work --replace"
fi
grep -q ACTIONS_RUNNER_HOOK_JOB_STARTED actions-runner/.env 2>/dev/null || echo "ACTIONS_RUNNER_HOOK_JOB_STARTED=/home/runner/job-started.sh" >> actions-runner/.env
chown runner:runner actions-runner/.env
cd actions-runner
./svc.sh install runner || true
./svc.sh start
./svc.sh status | head -5
systemctl list-timers plinth-ci-idle-stop.timer --no-pager | head -3
nproc; free -g | head -2
