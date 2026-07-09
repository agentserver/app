const { spawnSync } = require('child_process');
const { version } = require('../package.json');

const vsce = process.platform === 'win32' ? 'vsce.cmd' : 'vsce';
const args = [
  'package',
  '--out',
  `agentserver-app-${version}.vsix`,
  '--no-dependencies',
  '--skip-license',
];

const result = spawnSync(vsce, args, { stdio: 'inherit' });
if (result.error) {
  console.error(result.error.message);
  process.exit(1);
}
process.exit(result.status ?? 1);
