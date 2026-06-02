# agentserver-vscode (extension)

VS Code extension that ships with the `agentserver-vscode` installer.

Responsibilities:
- Prompt to open a folder when none is open
- Ensure a `codex` terminal exists; reopen it if closed
- Keep focus on Terminal / Output (away from other panel views)

Not a standalone extension; expects settings injected by the installer.
