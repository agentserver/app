import * as vscode from 'vscode';

/**
 * If no workspace is open, prompt for one and call vscode.openFolder.
 * Returns true if a folder open was triggered (extension host will reload).
 */
export async function maybePromptOpenFolder(): Promise<boolean> {
  if (vscode.workspace.workspaceFolders?.length) return false;
  const picked = await vscode.window.showOpenDialog({
    canSelectFolders: true,
    canSelectMany: false,
    openLabel: '打开',
    title: '选择要打开的项目文件夹',
  });
  if (!picked || picked.length === 0) return false;
  await vscode.commands.executeCommand('vscode.openFolder', picked[0], false);
  return true;
}
