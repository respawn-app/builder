import test from 'node:test';
import assert from 'node:assert/strict';

import { mirrorReadme, mirrorRepoMarkdownDocument } from './readme-mirror.mjs';
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
    '- [Contributing](./CONTRIBUTING.md#before-opening-a-pull-request)',
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
    output.includes('https://github.com/respawn-app/builder/blob/main/CHANGELOG.md'),
    true,
  );
  assert.equal(output.includes('/contributing/#before-opening-a-pull-request'), true);
  assert.equal(
    output.includes('https://raw.githubusercontent.com/respawn-app/builder/main/docs/static/logo.svg'),
    true,
  );
  assert.equal(output.includes('- [Anchor](#features)'), true);
});

test('mirrorRepoMarkdownDocument removes the top-level heading and assigns custom metadata', () => {
  const input = [
    '# Security Policy',
    '',
    'Please report issues privately.',
    '',
    '- [Guide](./CONTRIBUTING.md)',
    '- [Home](./README.md#install)',
    '- [Security](./SECURITY.md)',
  ].join('\n');

  const output = mirrorRepoMarkdownDocument(input, resolveDocsConfig(), {
    title: 'Security',
    editPath: 'SECURITY.md',
  });

  assert.equal(output.includes('# Security Policy'), false);
  assert.equal(output.includes('title: Security'), true);
  assert.equal(
    output.includes('editUrl: https://github.com/respawn-app/builder/edit/main/SECURITY.md'),
    true,
  );
  assert.equal(
    output.includes('- [Guide](/contributing/)'),
    true,
  );
  assert.equal(
    output.includes('- [Home](/docs/#install)'),
    true,
  );
  assert.equal(
    output.includes('- [Security](/security/)'),
    true,
  );
});
