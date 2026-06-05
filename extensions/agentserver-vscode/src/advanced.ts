import * as vscode from 'vscode';

export function registerAdvancedInterface(ctx: vscode.ExtensionContext): void {
  ctx.subscriptions.push(
    vscode.commands.registerCommand('agentserverVscode.showAdvancedInterface', async () => {
      const config = vscode.workspace.getConfiguration();
      await config.update('workbench.statusBar.visible', true, vscode.ConfigurationTarget.Global);
      await config.update('workbench.activityBar.location', 'default', vscode.ConfigurationTarget.Global);
      await config.update('window.menuBarVisibility', 'classic', vscode.ConfigurationTarget.Global);
      await config.update('workbench.layoutControl.enabled', true, vscode.ConfigurationTarget.Global);
      await vscode.commands.executeCommand('workbench.action.terminal.focus');
    }),
  );
}
