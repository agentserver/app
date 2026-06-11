import * as fs from 'fs';

type RemoveWorkspace = (workspacePath: string) => void;
type Warn = (message: string) => void;

function removeWorkspacePath(workspacePath: string): void {
  fs.rmSync(workspacePath, {
    recursive: true,
    force: true,
    maxRetries: 10,
    retryDelay: 200,
  });
}

export function cleanupTestWorkspace(
  workspacePath: string,
  removeWorkspace: RemoveWorkspace = removeWorkspacePath,
  warn: Warn = console.warn,
): void {
  if (!workspacePath) {
    return;
  }

  try {
    removeWorkspace(workspacePath);
  } catch (error) {
    const detail = error instanceof Error ? error.message : String(error);
    warn(`Could not remove temporary test workspace "${workspacePath}": ${detail}`);
  }
}
