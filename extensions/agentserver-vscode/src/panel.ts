import * as vscode from 'vscode';

const TERMINAL_FOCUS_CMD = 'workbench.action.terminal.focus';
let panelCommandsRegistered = false;

export type CommandExecutor = (command: string, ...args: unknown[]) => Thenable<unknown> | unknown;
export type PanelHideScheduler = (run: () => Promise<void>, delayMs: number) => vscode.Disposable;

export const panelHideRetryDelaysMs = [250, 1000, 2500];

const hiddenViewIdAliases = new Map<string, string>([
  ['workbench.panel.markers', 'workbench.panel.markers.view'],
  ['workbench.panel.repl', 'workbench.panel.repl.view'],
  ['workbench.debug.console', 'workbench.panel.repl.view'],
  ['ports', '~remote.forwardedPorts'],
]);

export function normalizeHiddenViewId(id: string): string {
  return hiddenViewIdAliases.get(id) ?? id;
}

export function hiddenViewCommandIds(viewIds: string[]): string[] {
  const seen = new Set<string>();
  const commands: string[] = [];
  for (const id of viewIds.map(normalizeHiddenViewId)) {
    if (panelContainerIdForHiddenView(id)) continue;
    const command = `${id}.removeView`;
    if (seen.has(command)) continue;
    seen.add(command);
    commands.push(command);
  }
  return commands;
}

function panelContainerIdForHiddenView(viewId: string): string | undefined {
  if (viewId === '~remote.forwardedPorts') return '~remote.forwardedPortsContainer';
  if (!viewId.startsWith('workbench.panel.')) return undefined;
  return viewId.endsWith('.view') ? viewId.slice(0, -'.view'.length) : viewId;
}

async function runBestEffort(execute: CommandExecutor, command: string, ...args: unknown[]): Promise<void> {
  try {
    await execute(command, ...args);
  } catch {
    // View ids and internal workbench commands vary across VS Code versions.
    // Missing cleanup commands should not block startup.
  }
}

function scheduleTimeout(run: () => Promise<void>, delayMs: number): vscode.Disposable {
  const timer = setTimeout(() => {
    void run();
  }, delayMs);
  return new vscode.Disposable(() => clearTimeout(timer));
}

export async function hidePanelViews(
  hideViewIds: string[],
  execute: CommandExecutor = vscode.commands.executeCommand.bind(vscode.commands),
): Promise<void> {
  const contexts = new Set<string>();
  for (const originalId of hideViewIds) {
    contexts.add(originalId);
    contexts.add(normalizeHiddenViewId(originalId));
  }
  for (const id of contexts) {
    await runBestEffort(execute, 'setContext', `${id}.visible`, false);
  }
  for (const command of hiddenViewCommandIds(hideViewIds)) {
    await runBestEffort(execute, command);
  }
}

export function schedulePanelViewHiding(
  ctx: Pick<vscode.ExtensionContext, 'subscriptions'>,
  hideViewIds: string[],
  execute: CommandExecutor = vscode.commands.executeCommand.bind(vscode.commands),
  scheduler: PanelHideScheduler = scheduleTimeout,
  delaysMs = panelHideRetryDelaysMs,
): void {
  if (hideViewIds.length === 0) return;

  for (const delayMs of delaysMs) {
    ctx.subscriptions.push(scheduler(() => hidePanelViews(hideViewIds, execute), delayMs));
  }
}

export function registerPanelCommands(ctx: vscode.ExtensionContext): void {
  if (panelCommandsRegistered) return;
  panelCommandsRegistered = true;
  ctx.subscriptions.push(
    vscode.commands.registerCommand('agentserverVscode.focusTerminal', async () => {
      await vscode.commands.executeCommand(TERMINAL_FOCUS_CMD);
    }),
  );
}

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
  registerPanelCommands(ctx);
  void hidePanelViews(hideViewIds);
}
