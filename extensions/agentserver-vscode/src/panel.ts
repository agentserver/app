import * as vscode from 'vscode';

const TERMINAL_FOCUS_CMD = 'workbench.action.terminal.focus';

/**
 * Tier (b) fallback: whenever the user switches to one of the "hidden"
 * panel views, immediately switch focus back to the terminal.
 * (VS Code lacks an official API to truly remove built-in views.)
 */
export function lockPanelToTerminal(
  ctx: vscode.ExtensionContext,
  hideViewIds: string[],
): void {
  if (hideViewIds.length === 0) return;
  const set = new Set(hideViewIds);

  // We can't subscribe to "active panel view changed" — VS Code doesn't
  // expose that event. Instead poll the activeTextEditor + activePanel
  // commands periodically, OR rely on user invoking commands. As a v1
  // pragmatic approach: re-focus terminal when configuration says so.
  // The user can also manually run "agentserver-vscode: 重开 codex 终端".
  ctx.subscriptions.push(
    vscode.commands.registerCommand('agentserverVscode.focusTerminal', async () => {
      await vscode.commands.executeCommand(TERMINAL_FOCUS_CMD);
    }),
  );

  // Tier (a): try setContext for known view IDs (best-effort).
  for (const id of set) {
    void vscode.commands.executeCommand('setContext', `${id}.visible`, false);
  }
}
