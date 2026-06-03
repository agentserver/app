# modelserver OAuth 客户端 — 已完成 ✅

(2026-06-03 v6 — 运维已交付如下 client, 安装包已对接, 全程跑通)

## 交付的 client (运维 2026-06-03 07:41 UTC 创建)

| 字段 | 值 |
|---|---|
| Client ID | `5321f7e6-3d79-4ac9-a742-04809dbf9025` |
| Token Endpoint Auth Method | `none` (public client, 用 PKCE 证身份) |
| Grant Types | `authorization_code`, `refresh_token` (额外 `device_code` 未使用) |
| Response Types | `code` |
| Scope | `project:inference offline_access` |
| Callback Path | `/oauth/modelserver/callback` |
| Redirect URIs (8 个固定端口) | `http://127.0.0.1:53428..53435/oauth/modelserver/callback` |

代码: `cmd/launcher/main.go` 的 `msOAuth := oauth.AuthCodeConfig{...}`。

## 完整链路 (8 步)

```
1. 用户双击桌面快捷方式 agentserver-vscode
2. 安装包: 本地 127.0.0.1:<53428..53435 第一个空闲> listen
3. 安装包: 浏览器打开
     https://codeapi.cs.ac.cn/oauth2/auth
       ?response_type=code
       &client_id=5321f7e6-3d79-4ac9-a742-04809dbf9025
       &redirect_uri=http://127.0.0.1:<port>/oauth/modelserver/callback
       &scope=project:inference%20offline_access
       &code_challenge=<sha256(verifier)>
       &code_challenge_method=S256
       &state=<nonce>
4. 用户在 Hydra 微信扫码登录、同意 (跟 dashboard 同一套 UI)
5. Hydra 302 → http://127.0.0.1:<port>/oauth/modelserver/callback?code=...&state=...
6. 安装包: POST https://codeapi.cs.ac.cn/oauth2/token
       grant_type=authorization_code
       code=<上一步拿到的>
       code_verifier=<开头生成的>         ← public 客户端的身份证明
       client_id=5321f7e6-3d79-4ac9-a742-04809dbf9025
       redirect_uri=http://127.0.0.1:<port>/oauth/modelserver/callback
   ← {access_token, refresh_token, expires_in}

7. 安装包: 拿 access_token 调 modelserver admin API:
       POST https://code.cs.ac.cn/api/v1/projects                  → 建/选 "default" project
       POST https://code.cs.ac.cn/api/v1/projects/{id}/keys        → 生成 ms-xxxxxx... 长期 API key

8. 安装包: 写 keyring (modelserver_api_key) + setx OPENAI_API_KEY=...
          写 ~/.codex/config.toml: base_url=https://code.ai.cs.ac.cn/v1, env_key=OPENAI_API_KEY
```

模型 server admin API 接受 OAuth Bearer 是因为
`internal/proxy/auth_middleware.go:148-282` 会在 API key 验证失败后 fallback 到
token introspection。

## 请回滚 (历史遗留)

如果之前按 v2/v3/v4 文档做过下面这些, 请回滚 — 当前方案不需要:

- **Hydra `urls.device.verification`** — 删掉, 留着会让 `/oauth2/device/verify` 进 302 死循环
- **modelserver config `auth.oauth.hydra.device_flow.{client_id,client_secret}`** — 删掉, 当前方案不走 modelserver wrapper

## 如果以后需要调整

| 想改什么 | 改哪 |
|---|---|
| 换 client_id | `cmd/launcher/main.go` 的 `msOAuth.ClientID` |
| 加/换 callback 端口 | `cmd/launcher/main.go` 的 `msOAuth.Ports` + 运维更新 redirect_uris 列表 |
| 改 scope | `cmd/launcher/main.go` 的 `msOAuth.Scope` |
| 改登录超时 | `cmd/launcher/main.go` 的 `msOAuth.LoginTimeout` (默认 10 分钟) |
| 改回调 path | `cmd/launcher/main.go` 的 `msOAuth.CallbackPath` + 运维更新 redirect_uris |
