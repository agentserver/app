# Local Token Proxy Design

## Goal

Prevent Codex Desktop from restarting when the Modelserver OAuth access token refreshes.

## Architecture

Codex Desktop should no longer read the short-lived Modelserver access token directly from `OPENAI_API_KEY`. Instead, Star Commander will expose a local loopback `/v1/*` proxy and configure Codex to use that local base URL with a stable local API key. The proxy reads the current Modelserver access token from the local secret store for every request and injects it into the upstream `Authorization` header.

## Components

- `internal/modelproxy`: local reverse proxy for Modelserver-compatible `/v1/*` requests.
- `internal/codex`: config writer changes so Modelserver settings can target the local proxy URL.
- launcher/open-folder startup paths: start the local proxy alongside the token refresher before launching Codex Desktop or the minimal VS Code frontend.

## Data Flow

1. User launches Codex Desktop.
2. Codex Desktop sends model requests to `http://127.0.0.1:<port>/v1`.
3. The local proxy loads `modelserver_api_key` from secrets for that request.
4. The proxy forwards the request to `https://code.ai.cs.ac.cn/v1` with `Authorization: Bearer <current access token>`.
5. The token refresher can update secrets in the background without requiring Codex Desktop to restart.

## Error Handling

If no access token is available, the proxy returns `401` so Codex surfaces an auth failure and the console can still guide the user to reconnect Modelserver. If upstream is unavailable, the proxy returns the upstream error status/body where possible.

## Testing

Tests should cover Codex config generation for local proxy settings and proxy request forwarding with dynamic token lookup. A test must prove that changing the stored token between two requests changes the upstream `Authorization` header without restarting the proxy.
