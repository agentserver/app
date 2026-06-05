import { execFile } from 'child_process';
import * as vscode from 'vscode';

export interface SystemOpenCommandSpec {
  command: string;
  args: string[];
  options: { windowsHide?: boolean };
}

export type SystemOpenExecFile = (
  command: string,
  args: string[],
  options: { windowsHide?: boolean },
  callback: (error: Error | null) => void,
) => unknown;

const defaultExecFile: SystemOpenExecFile = (command, args, options, callback) => {
  execFile(command, args, options, error => callback(error));
};

export function buildSystemOpenCommand(
  fsPath: string,
  platform: NodeJS.Platform | string = process.platform,
): SystemOpenCommandSpec {
  if (platform === 'win32') {
    return {
      command: 'powershell.exe',
      args: [
        '-NoProfile',
        '-ExecutionPolicy',
        'Bypass',
        '-Command',
        'Start-Process -LiteralPath $args[0]',
        fsPath,
      ],
      options: { windowsHide: true },
    };
  }
  if (platform === 'darwin') {
    return { command: 'open', args: [fsPath], options: {} };
  }
  return { command: 'xdg-open', args: [fsPath], options: {} };
}

export function openPathWithSystemApplication(
  fsPath: string,
  platform: NodeJS.Platform | string = process.platform,
  execFileFn: SystemOpenExecFile = defaultExecFile,
): Promise<void> {
  const spec = buildSystemOpenCommand(fsPath, platform);
  return new Promise((resolve, reject) => {
    execFileFn(spec.command, spec.args, spec.options, error => {
      if (error) {
        reject(error);
        return;
      }
      resolve();
    });
  });
}

export function registerOpenWithSystem(ctx: vscode.ExtensionContext): void {
  ctx.subscriptions.push(
    vscode.commands.registerCommand('agentserverVscode.openWithSystem', async (uri?: vscode.Uri) => {
      const target = uri || vscode.window.activeTextEditor?.document.uri;
      if (!target || target.scheme !== 'file') {
        await vscode.window.showErrorMessage('请选择一个本地文件后再打开。');
        return;
      }
      try {
        await openPathWithSystemApplication(target.fsPath);
      } catch (e) {
        const detail = e instanceof Error ? e.message : String(e);
        await vscode.window.showErrorMessage(`打开外部程序时出错：${target.fsPath}。${detail}`);
      }
    }),
  );
}
