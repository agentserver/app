import * as vscode from 'vscode';

export type CommandExecutor = (command: string, ...args: unknown[]) => Thenable<unknown> | unknown;

export const minimalChromeCommands: string[] = [
  'workbench.action.closeAuxiliaryBar',
];

export const minimalViewContextIds: string[] = [
  'outline',
  'timeline',
];

async function runBestEffort(execute: CommandExecutor, command: string, ...args: unknown[]): Promise<void> {
  try {
    await execute(command, ...args);
  } catch {
    // Some VS Code versions do not expose every workbench command. Missing
    // cleanup commands should not block startup.
  }
}

export async function hideMinimalChrome(
  execute: CommandExecutor = vscode.commands.executeCommand.bind(vscode.commands),
): Promise<void> {
  for (const id of minimalViewContextIds) {
    await runBestEffort(execute, 'setContext', `${id}.visible`, false);
  }
  for (const command of minimalChromeCommands) {
    await runBestEffort(execute, command);
  }
}
