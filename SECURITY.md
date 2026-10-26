# 安全策略 / Security policy

中文在前，[English below](#english)。

## 报告漏洞

请**不要**在公开 Issue、Pull Request 或讨论区里报告安全漏洞。

请用 GitHub 私密漏洞报告提交：打开仓库的 **Security** 标签页，点
**Report a vulnerability**（[直达链接](https://github.com/LeXwDeX/Plinth/security/advisories/new)）。
报告只有你和维护者可见，后续沟通、修复和公告都在这份安全公告草稿里进行。这是唯一的报告渠道，
我们不通过邮件接收漏洞报告。

报告里请尽量包含：

- 受影响的版本或提交 SHA，以及部署方式（Docker Compose 或其他）；
- 复现步骤或概念验证；
- 影响范围（例如越权读取他人资产、绕过登录、泄露 Provider key）；
- 你是否希望在公告中署名。

不要在报告里附上真实的 API key、密码、Cookie 或他人数据；需要时用脱敏的示例。

## 处理流程

- 我们会在 7 天内确认收到，并在确认问题后与你商定修复和公开的时间。
- 修复发布后会发布安全公告，并按你的意愿致谢。
- 请在修复发布前不要公开细节。

## 支持范围

在 1.0 之前只修复最新的 `main`。1.0 发布后，安全修复针对最新的正式版本。

部署时的安全配置（HTTPS、反向代理、会话密钥、对外监听）见
[docs/self-deploy-security.md](docs/self-deploy-security.md)。第三方 Provider 自身的漏洞请直接报告给对应厂商。

---

<a id="english"></a>

## Reporting a vulnerability

Please **do not** report security vulnerabilities in public issues, pull requests or
discussions.

Report them with GitHub private vulnerability reporting: open the repository's **Security**
tab and click **Report a vulnerability**
([direct link](https://github.com/LeXwDeX/Plinth/security/advisories/new)). Only you and the
maintainers can see the report; the discussion, the fix and the advisory all happen in that
draft security advisory. This is the only reporting channel; we do not take vulnerability
reports by email.

Where possible, include:

- the affected version or commit SHA and how you deploy (Docker Compose or otherwise);
- steps to reproduce or a proof of concept;
- the impact (for example reading another user's assets, bypassing sign-in, leaking a
  Provider key);
- whether you would like to be credited in the advisory.

Do not include real API keys, passwords, cookies or other people's data in the report;
use redacted examples where needed.

## Process

- We acknowledge reports within 7 days and, once the issue is confirmed, agree with you on
  the timing of the fix and its disclosure.
- After the fix is released we publish a security advisory and credit you if you wish.
- Please keep the details private until the fix is released.

## Supported versions

Before 1.0, only the latest `main` receives fixes. After 1.0, security fixes target the
latest release.

Security settings for a deployment (HTTPS, reverse proxy, session secret, listening
address) are described in [docs/self-deploy-security.md](docs/self-deploy-security.md) (in
Chinese). Report vulnerabilities in a third-party Provider's own service to that vendor.
