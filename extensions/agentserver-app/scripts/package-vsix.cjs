const { spawnSync } = require('child_process');
const { version } = require('../package.json');

const npx = process.platform === 'win32' ? 'npx.cmd' : 'npx';
const args = [
  'vsce',
  'package',
  '--out',
  `agentserver-app-${version}.vsix`,
  '--no-dependencies',
  '--skip-license',
];

const result = spawnSync(npx, args, {
  stdio: 'inherit',
  shell: process.platform === 'win32',
});
if (result.error) {
  console.error(result.error.message);
  process.exit(1);
}
process.exit(result.status ?? 1);
