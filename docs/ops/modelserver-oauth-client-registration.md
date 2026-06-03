# 请帮我在 modelserver (code.cs.ac.cn) 启用 Device Flow

(2026-06-03 修订版 v3 — 上一版要你动 Hydra `urls.device.verification`,
是我搞错了,**请回滚**。本版是正确的做法。)

为 `agentserver-vscode` 安装包做 RFC 8628 device flow,让用户扫码登录后
codex 直接拿到 modelserver API token,全程零复制粘贴。

---

## 关键决策:用 modelserver 自带的 device-flow 包装,不用 Hydra 原生的

**Hydra 原生的 device flow** (`/oauth2/device/*`) 需要配置一个外部"验证 UI"
(通过 `urls.device.verification`) 来引导用户。如果让 Hydra 直接 host UI,
它会陷入死循环;如果让 Hydra 转给 modelserver 的页面 UI,我们就要写一个
新页面 — 但其实 modelserver 项目已经写好了一套 device-flow 包装,直接用
最省事。

modelserver 自己在 `/oauth/device/*` 下提供了 5 个端点(`internal/admin/routes.go:53-62`):

```go
if cfg.Auth.OAuth.Hydra.DeviceFlow.ClientID != "" {
    deviceHandler, err := NewDeviceFlowHandler(hydraClient, st, encKey, cfg)
    if err != nil { panic(...) }
    r.Post("/oauth/device/code",     deviceHandler.HandleDeviceAuthorize)  // CLI 申请 device_code
    r.Get ("/oauth/device",          deviceHandler.HandleVerificationPage) // 用户访问的 UI
    r.Post("/oauth/device",          deviceHandler.HandleVerifyUserCode)   // 用户提交 user_code
    r.Get ("/oauth/device/callback", deviceHandler.HandleCallback)         // Hydra OAuth 完后回调
    r.Post("/oauth/device/token",    deviceHandler.HandleTokenPoll)        // CLI 轮询拿 token
    deviceHandler.StartCleanup(context.Background())
}
```

**只有 `cfg.Auth.OAuth.Hydra.DeviceFlow.ClientID` 不为空时,这些路由才注册。**
我们之前测出来 `/oauth/device/code` 是 404 就是因为这个 `if` 守卫不过。

---

## 需要做两件事

### 1. (回滚) Hydra `urls.device.verification`

上一版我让你加的这行,**请删掉或者改回原值**:

```yaml
# 删掉这一段 ↓
urls:
  device:
    verification: https://codeapi.cs.ac.cn/oauth2/device/verify
```

理由:这一项是给 Hydra 原生 device flow 用的。我们走 modelserver 的包装,
Hydra 不需要知道我们的 UI 在哪。留着会让 `codeapi.cs.ac.cn/oauth2/device/verify`
进入 302 死循环 (它指向自己)。

### 2. 启用 modelserver 自带的 device flow

在 modelserver 的 `config.yml` (或 Helm values / env,看你们部署用啥),
找到 `auth.oauth.hydra.device_flow` 字段,**只需要填一个 client_id 进去**:

```yaml
auth:
  oauth:
    hydra:
      # ...其他 hydra 配置...

      device_flow:
        client_id: 5321f7e6-3d79-4ac9-a742-04809dbf9025
        # 可选,默认是 600 / 5 (秒)
        # code_ttl_seconds: 600
        # poll_interval_seconds: 5
```

值就是你之前注册 OAuth Client 时的 `client_id`。一旦填进去,modelserver
重启后,5 个 `/oauth/device/*` 路由就会注册。

实际配置字段名见 `internal/config/config.go` 里的 `DeviceFlow struct`,
应该是 `client_id` (snake_case)。如果跑起来还是 404,把启动日志贴给我,
我看是不是字段名拼错。

---

## OAuth Client 注册 (已完成 ✅)

`5321f7e6-3d79-4ac9-a742-04809dbf9025` 已经注册好,grant_types 含 device_code,
token_endpoint_auth_method=none。这部分不用动。

唯一可以再清理的地方:**redirect_uris 留着对 device flow 没用**(device
flow 走 polling,不走 callback)。你如果想清理就把 `redirect_uris` 设为
`[]` 和 `grant_types` 去掉 `authorization_code`。不清理也完全 OK,留着
对 device flow 没影响。

---

## 启用后的验证步骤 (我做)

运维改完 config + 重启 modelserver,告诉我之后:

```bash
# 1. 验证路由注册了
curl -X POST 'https://codeapi.cs.ac.cn/oauth/device/code' \
  -H 'Content-Type: application/x-www-form-urlencoded' \
  -d 'client_id=5321f7e6-3d79-4ac9-a742-04809dbf9025&scope=project:inference+offline_access'

# 期望返回 JSON,包含 device_code/user_code/verification_uri/verification_uri_complete
# verification_uri 应该是 https://codeapi.cs.ac.cn/oauth/device (没有 /oauth2/)

# 2. 把 verification_uri_complete 用浏览器打开 → 应该看到一个登录页 (不报错、不死循环)

# 3. 登录 + 同意后,CLI 轮询 token endpoint:
curl -X POST 'https://codeapi.cs.ac.cn/oauth/device/token' \
  -H 'Content-Type: application/x-www-form-urlencoded' \
  -d 'grant_type=urn:ietf:params:oauth:grant-type:device_code&client_id=...&device_code=...'

# 期望返回 {access_token, refresh_token, expires_in}
```

---

## 完整用户体验

```
1. 用户双击桌面快捷方式 agentserver-vscode
2. 浏览器弹出本地引导页 (127.0.0.1:RANDPORT)
3. 用户点 "登录 modelserver"
4. 引导页弹新标签 →  https://codeapi.cs.ac.cn/oauth/device?user_code=ABCDE
5. 用户在 modelserver 网站完成登录 + 选 project + 点同意
6. 后台: 安装包轮询 token endpoint 直到拿到 access_token
7. 后台: 写 ~/.codex/config.toml + setx OPENAI_API_KEY=<token>
8. 完成。codex 直接能调 modelserver 的 LLM
```

**全程零复制粘贴。**

---

## 安全说明

- ✅ `client_id` 写进安装包不算泄露 — public client (RFC 8252) 设计就允许;
  GitHub CLI 的 client_id `178c6fc778ccc68e1d6a` 也是硬编码在源码里
- ✅ 拿到 client_id 不能伪造 token — 每个 token 都要真实用户在 consent
  page 同意才签发
- ✅ 限流按 user 算,不按 client_id 算
- ⚠️ 唯一风险是冒名 — consent page 上 client name 是否清晰是 UX 问题,
  不是 secret 泄露

---

## 参考

- modelserver device-flow 实现:
  `https://github.com/modelserver/modelserver/blob/main/internal/admin/device_flow.go`
- modelserver 路由注册:
  `https://github.com/modelserver/modelserver/blob/main/internal/admin/routes.go` (搜 `DeviceFlow`)
- RFC 8628 OAuth 2.0 Device Authorization Grant:
  https://datatracker.ietf.org/doc/html/rfc8628
- RFC 8252 OAuth 2.0 for Native Apps:
  https://datatracker.ietf.org/doc/html/rfc8252
