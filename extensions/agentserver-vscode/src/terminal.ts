import * as vscode from 'vscode';

let lastSpawn = 0;
const DEBOUNCE_MS = 200;

export interface OpenTerminalOptions {
  reveal?: boolean;
  preserveFocus?: boolean;
}

interface TerminalLike {
  show(preserveFocus?: boolean): void;
}

interface TerminalWindowLike {
  createTerminal(options: { name: string }): TerminalLike;
}

export function hasTerminalNamed(terminals: readonly Pick<vscode.Terminal, 'name'>[], name: string): boolean {
  return terminals.some(t => t.name === name);
}

export async function openCodexTerminalWithWindow(
  win: TerminalWindowLike,
  profileName: string,
  options: OpenTerminalOptions = {},
): Promise<void> {
  const term = win.createTerminal({ name: profileName });
  if (options.reveal) {
    term.show(options.preserveFocus ?? false);
  }
  lastSpawn = Date.now();
}

export async function openCodexTerminal(
  profileName: string,
  options: OpenTerminalOptions = {},
): Promise<void> {
  await openCodexTerminalWithWindow(vscode.window, profileName, options);
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
      await openCodexTerminal(profileName, { reveal: false });
    }),
  );
}
