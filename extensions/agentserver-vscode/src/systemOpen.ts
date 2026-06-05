import * as vscode from 'vscode';

export function registerOpenWithSystem(ctx: vscode.ExtensionContext): void {
  ctx.subscriptions.push(
    vscode.commands.registerCommand('agentserverVscode.openWithSystem', async (uri?: vscode.Uri) => {
      const target = uri || vscode.window.activeTextEditor?.document.uri;
      if (!target || target.scheme !== 'file') {
        await vscode.window.showErrorMessage('请选择一个本地文件后再打开。');
        return;
      }
      try {
        const ok = await vscode.env.openExternal(target);
        if (!ok) {
          await vscode.window.showErrorMessage(`无法用系统应用打开：${target.fsPath}`);
        }
      } catch (e) {
        const detail = e instanceof Error ? e.message : String(e);
        await vscode.window.showErrorMessage(`无法用系统应用打开：${target.fsPath}。${detail}`);
      }
    }),
  );
}
