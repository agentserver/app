import * as vscode from 'vscode';
import { readConfig } from './config';
import { maybePromptOpenFolder } from './folderPicker';
import { attachTerminalRespawn, openCodexTerminal } from './terminal';
import { lockPanelToTerminal } from './panel';
import { registerOpenWithSystem } from './systemOpen';
import { registerAdvancedInterface } from './advanced';

export async function activate(ctx: vscode.ExtensionContext): Promise<void> {
  const cfg = readConfig();

  // 1. If no folder, prompt and bail (extension host will reload)
  if (cfg.startupOpenFolderIfEmpty) {
    const opened = await maybePromptOpenFolder();
    if (opened) return;
  }

  // 2. Panel lockdown
  lockPanelToTerminal(ctx, cfg.panelHideViews);

  // 3. File context commands
  registerOpenWithSystem(ctx);

  // 4. Ensure a codex terminal exists
  if (vscode.window.terminals.length === 0) {
    await openCodexTerminal(cfg.terminalProfileName, true);
  }

  // 5. Respawn on close
  attachTerminalRespawn(ctx, cfg.terminalProfileName,
    () => readConfig().terminalRespawnOnClose);

  // 6. Commands
  registerAdvancedInterface(ctx);
  ctx.subscriptions.push(
    vscode.commands.registerCommand('agentserverVscode.reopenCodexTerminal',
      () => openCodexTerminal(readConfig().terminalProfileName, false)),
    vscode.commands.registerCommand('agentserverVscode.doctor', async () => {
      const info = {
        terminals: vscode.window.terminals.map(t => t.name),
        workspace: vscode.workspace.workspaceFolders?.map(f => f.uri.fsPath),
        language:  vscode.env.language,
      };
      const channel = vscode.window.createOutputChannel('agentserver-vscode');
      channel.appendLine(JSON.stringify(info, null, 2));
      channel.show();
    }),
  );
}

export function deactivate(): void {}
