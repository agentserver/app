import * as vscode from 'vscode';

export interface ExtConfig {
  startupOpenFolderIfEmpty: boolean;
  terminalRespawnOnClose: boolean;
  terminalProfileName: string;
  panelHideViews: string[];
}

export function readConfig(): ExtConfig {
  const c = vscode.workspace.getConfiguration('agentserverApp');
  return {
    startupOpenFolderIfEmpty: c.get<boolean>('startup.openFolderIfEmpty', true),
    terminalRespawnOnClose:   c.get<boolean>('terminal.respawnOnClose', true),
    terminalProfileName:      c.get<string>('terminal.profileName', 'codex'),
    panelHideViews:           c.get<string[]>('panel.hideViews', []),
  };
}
