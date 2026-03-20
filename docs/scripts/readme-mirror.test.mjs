import test from 'node:test';
import assert from 'node:assert/strict';

import { mirrorReadme } from './readme-mirror.mjs';
import { resolveDocsConfig } from './site-config.mjs';

test('mirrorReadme removes the top-level heading and rewrites relative links', () => {
  const input = [
    '# Builder',
    '',
    'Intro paragraph.',
    '',
    '- [x] Done item',
    '- [ ] Todo item',
    '',
    '- [Changelog](./CHANGELOG.md)',
    '- [Logo](./docs/static/logo.svg)',
    '- [Anchor](#features)',
    '',
    '## Features',
    '',
    'Stuff.',
  ].join('\n');

  const output = mirrorReadme(input, resolveDocsConfig());

  assert.equal(output.includes('# Builder'), false);
  assert.equal(output.includes('title: Home'), true);
  assert.equal(output.includes('- [x] Done item'), true);
  assert.equal(output.includes('- [ ] Todo item'), true);
  assert.equal(
    output.includes('https://github.com/respawn-app/agent/blob/main/CHANGELOG.md'),
    true,
  );
  assert.equal(
    output.includes('https://raw.githubusercontent.com/respawn-app/agent/main/docs/static/logo.svg'),
    true,
  );
  assert.equal(output.includes('- [Anchor](#features)'), true);
});
