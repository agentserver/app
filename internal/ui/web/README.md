# onboarding-ui

Vue 3 + Vite + Element Plus front-end for the agentserver-app onboarding flow.

## Quick start

```bash
# install deps
npm ci

# dev server with hot-reload (proxies /api/* to a running launcher)
# Start a launcher first, note its port, then:
VITE_API_PROXY=http://127.0.0.1:<launcher-port> npm run dev
# open http://localhost:5173

# build for embedding (writes to ../assets/dist/)
npm run build

# run unit tests
npm test
```

## Layout

- `src/main.ts` — entry, mounts `App.vue`
- `src/App.vue` — top-level layout
- `src/api.ts` — fetch wrappers + types
- `src/stepConfig.ts` — the 5-step definition
- `src/composables/useOnboarding.ts` — state machine
- `src/composables/useSSE.ts` — EventSource wrapper
- `src/components/StepCard.vue` — single step shell
- `src/components/{Oauth,Progress,Action}Step.vue` — kind-specific behavior
- `src/components/ErrorPanel.vue` — inline error + retry
- `src/components/SuccessBanner.vue` — completion banner + CTA

## Backend contract

The front-end calls:
- `GET /api/state` — returns `ServerState` (see `src/api.ts`)
- `POST /api/step/{id}` — kick off a step
- `GET /api/step/{id}/status` — poll OAuth step status
- `GET /api/events?stream=<id>` — SSE for progress events
- `POST /api/finalize` — finalize the install
- `POST /api/launch-vscode` — launch VS Code, server shuts down after
