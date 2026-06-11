# 星池指挥官

Windows installer (`agentserver-app`) that sets up VS Code + codex pre-configured against
modelserver (`code.cs.ac.cn`) and agentserver (`agent.cs.ac.cn`).

See `docs/superpowers/specs/2026-06-02-agentserver-app-installer-design.md`
for the v1 design spec.

## Building (Linux dev)

```
make build      # cross-compile Windows binaries to dist/
make test       # unit + integration tests
make package    # requires Wine + Inno Setup for full pipeline
```
