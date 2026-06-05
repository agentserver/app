import * as vscode from 'vscode';

let lastSpawn = 0;
const DEBOUNCE_MS = 200;

export function hasTerminalNamed(terminals: readonly Pick<vscode.Terminal, 'name'>[], name: string): boolean {
  return terminals.some(t => t.name === name);
}

export async function openCodexTerminal(profileName: string, preserveFocus = true): Promise<void> {
  const term = vscode.window.createTerminal({ name: profileName });
  term.show(preserveFocus);
  lastSpawn = Date.now();
}

export function attachTerminalRespawn(
  ctx: vscode.ExtensionContext,
  profileName: string,
  enabled: () => boolean,
): void {
  ctx.subscriptions.push(
    vscode.window.onDidCloseTerminal(async (t) => {
      if (!enabled()) return;
      if (t.name !== profileName) return;
      if (Date.now() - lastSpawn < DEBOUNCE_MS) return; // avoid runaway
      // If the window itself is closing, do nothing.
      if (!vscode.window.state.focused) return;
      await openCodexTerminal(profileName, true);
    }),
  );
}
