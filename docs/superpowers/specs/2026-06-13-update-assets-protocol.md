# 星池指挥官更新资产发布协议

**Date**: 2026-06-13
**Status**: Draft
**Scope**: Defines how Windows installer artifacts and update manifests are stored under `assets.agent.cs.ac.cn` for 星池指挥官 automatic updates.

## Purpose

星池指挥官客户端通过固定地址检查更新：

```text
https://assets.agent.cs.ac.cn/agentserver-app/windows/latest.json
```

这个文档定义 `assets.agent.cs.ac.cn` 上的对象命名、manifest 格式、不可变规则和发布顺序，确保客户端能安全地发现、下载、校验并启动不同版本的 Windows 安装包。

## Object Layout

Windows 更新资产位于：

```text
https://assets.agent.cs.ac.cn/agentserver-app/windows/
```

必需对象：

```text
/agentserver-app/windows/latest.json
/agentserver-app/windows/agentserver-app-<version>-setup.exe
/agentserver-app/windows/releases/<version>.json
```

示例：

```text
/agentserver-app/windows/latest.json
/agentserver-app/windows/agentserver-app-0.1.2-setup.exe
/agentserver-app/windows/releases/0.1.2.json
```

Rules:

- `latest.json` 是可变指针，只指向当前推荐版本。
- `agentserver-app-<version>-setup.exe` 是不可变安装包；发布后不得覆盖同名文件。
- `releases/<version>.json` 是该版本 manifest 的不可变归档；内容应与该版本首次发布时的 `latest.json` 一致。
- `<version>` 使用 `MAJOR.MINOR.PATCH`，例如 `0.1.2`。不使用预发布、build metadata 或零填充数字。
- 文件名中的版本不带 `v` 前缀，即使客户端版本比较允许小写 `v` 前缀。

## Manifest Schema

`latest.json` 和 `releases/<version>.json` 使用同一 JSON schema：

```json
{
  "version": "0.1.2",
  "url": "https://assets.agent.cs.ac.cn/agentserver-app/windows/agentserver-app-0.1.2-setup.exe",
  "sha256": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
  "size": 123456789,
  "notes": "修复若干安装和启动问题。"
}
```

Fields:

- `version`: 必填。`MAJOR.MINOR.PATCH` SemVer 数字段版本；必须大于客户端当前版本才会被视为可更新。
- `url`: 必填。安装包 HTTPS URL；host 必须是 `assets.agent.cs.ac.cn`。
- `sha256`: 必填。安装包 SHA-256，小写或大写 64 位 hex。
- `size`: 必填。安装包字节数，必须大于 0；客户端下载后会校验长度。
- `notes`: 可选。展示给用户的版本说明，建议简短中文文本。

客户端当前安全约束：

- 只读取固定的 `latest.json` URL。
- 拒绝非 HTTPS 安装包 URL。
- 拒绝 host 不是 `assets.agent.cs.ac.cn` 的安装包 URL。
- 跟随重定向时，重定向后的 host 也必须仍是 `assets.agent.cs.ac.cn`。
- 拒绝缺失或格式错误的 `sha256`。
- 拒绝 `size <= 0`。
- 下载完成后同时校验 `size` 和 SHA-256，任一不匹配都不会启动安装包。

## Publishing Order

发布新版本时按这个顺序执行：

1. 构建安装包：

   ```text
   packaging/windows/Output/agentserver-app-<version>-setup.exe
   ```

2. 计算安装包 `size` 和 `sha256`。
3. 上传版本化安装包：

   ```text
   /agentserver-app/windows/agentserver-app-<version>-setup.exe
   ```

4. 从 `assets.agent.cs.ac.cn` 下载刚上传的安装包，重新计算 `size` 和 `sha256`，确认与本地构建一致。
5. 上传不可变归档 manifest：

   ```text
   /agentserver-app/windows/releases/<version>.json
   ```

6. 最后更新可变入口：

   ```text
   /agentserver-app/windows/latest.json
   ```

`latest.json` 必须最后更新。这样客户端不会在安装包尚未可下载时看到新版本。

## HTTP and CDN Requirements

Recommended headers:

```text
/agentserver-app/windows/latest.json
  Content-Type: application/json
  Cache-Control: no-cache

/agentserver-app/windows/releases/<version>.json
  Content-Type: application/json
  Cache-Control: public, max-age=31536000, immutable

/agentserver-app/windows/agentserver-app-<version>-setup.exe
  Content-Type: application/octet-stream
  Cache-Control: public, max-age=31536000, immutable
```

Operational requirements:

- TLS certificate must be valid for `assets.agent.cs.ac.cn`.
- Avoid redirecting installer downloads to another host. The client will reject cross-host redirects.
- If `latest.json` is cached by a CDN, purge or revalidate it after every release.
- Keep old versioned installers available. Existing clients or support workflows may still need a historical installer.

## Version Immutability

Once a version is published:

- Do not overwrite `agentserver-app-<version>-setup.exe`.
- Do not rewrite `releases/<version>.json`.
- Do not reuse a version number for a rebuilt installer.

If a build is bad, publish a higher patch version, for example `0.1.3`. Downgrade is not supported by the updater; clients only install versions greater than their current version.

If `latest.json` was accidentally pointed to a bad version before users installed it, it may be pointed back to the previous stable version. Users who already installed the bad version still need a higher fixed version.

## Release Checklist

For version `0.1.2`:

```bash
version=0.1.2
exe="packaging/windows/Output/agentserver-app-${version}-setup.exe"
sha256sum "$exe"
stat -c%s "$exe"
```

Create manifest:

```json
{
  "version": "0.1.2",
  "url": "https://assets.agent.cs.ac.cn/agentserver-app/windows/agentserver-app-0.1.2-setup.exe",
  "sha256": "<sha256>",
  "size": <size>,
  "notes": "<release notes>"
}
```

After upload, verify from the public origin:

```bash
curl --fail --location --output "/tmp/agentserver-app-${version}-setup.exe" \
  "https://assets.agent.cs.ac.cn/agentserver-app/windows/agentserver-app-${version}-setup.exe"
sha256sum "/tmp/agentserver-app-${version}-setup.exe"
stat -c%s "/tmp/agentserver-app-${version}-setup.exe"
curl --fail "https://assets.agent.cs.ac.cn/agentserver-app/windows/latest.json"
```

Acceptance criteria:

- Downloaded installer hash equals manifest `sha256`.
- Downloaded installer byte length equals manifest `size`.
- `latest.json` parses as JSON and points to `assets.agent.cs.ac.cn`.
- A client on the previous version reports `status: "available"`.
- A client on the same version reports `status: "latest"` or normalizes stale `available` state to latest.
