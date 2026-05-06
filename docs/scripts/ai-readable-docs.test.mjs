import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, mkdir, readFile, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';

import {
  appendMarkdownDiscovery,
  docsEntryId,
  emitMarkdownEndpoints,
  markdownOutputPath,
} from './ai-readable-docs.mjs';

test('docsEntryId removes markdown extension and preserves nested slugs', () => {
  const sourceDirectory = path.join('docs', 'src', 'content', 'docs');

  assert.equal(docsEntryId(sourceDirectory, path.join(sourceDirectory, 'quickstart.md')), 'quickstart');
  assert.equal(docsEntryId(sourceDirectory, path.join(sourceDirectory, 'nested', 'page.mdx')), 'nested/page');
});

test('markdownOutputPath writes extensionless entry ids as root .md files', () => {
  assert.equal(markdownOutputPath('/dist', 'command-postprocessing'), path.join('/dist', 'command-postprocessing.md'));
  assert.equal(markdownOutputPath('/dist', 'nested/page'), path.join('/dist', 'nested', 'page.md'));
});

test('emitMarkdownEndpoints copies source markdown, excludes docs 404 page, and records manifest', async () => {
  const tempRoot = await mkdtemp(path.join(os.tmpdir(), 'builder-ai-docs-'));
  const sourceDirectory = path.join(tempRoot, 'src');
  const generatedDirectory = path.join(tempRoot, 'generated');
  const outputDirectory = path.join(tempRoot, 'dist');

  await mkdir(path.join(sourceDirectory, 'nested'), { recursive: true });
  await mkdir(generatedDirectory, { recursive: true });
  await writeFile(path.join(sourceDirectory, 'quickstart.md'), 'quickstart source\n', 'utf8');
  await writeFile(path.join(sourceDirectory, 'nested', 'page.mdx'), 'nested source\n', 'utf8');
  await writeFile(path.join(sourceDirectory, '404.md'), 'not published raw\n', 'utf8');
  await writeFile(path.join(generatedDirectory, 'docs.md'), 'generated docs source\n', 'utf8');

  const entryIds = await emitMarkdownEndpoints({
    sourceDirectories: [sourceDirectory, generatedDirectory],
    outputDirectory,
  });

  assert.deepEqual(entryIds, ['docs', 'nested/page', 'quickstart']);
  assert.equal(await readFile(path.join(outputDirectory, 'quickstart.md'), 'utf8'), 'quickstart source\n');
  assert.equal(await readFile(path.join(outputDirectory, 'nested', 'page.md'), 'utf8'), 'nested source\n');
  assert.equal(await readFile(path.join(outputDirectory, 'docs.md'), 'utf8'), 'generated docs source\n');
  assert.deepEqual(JSON.parse(await readFile(path.join(outputDirectory, '.builder-docs-markdown-endpoints.json'), 'utf8')), {
    entryIds: ['docs', 'nested/page', 'quickstart'],
  });
  await assert.rejects(readFile(path.join(outputDirectory, '404.md'), 'utf8'));
});

test('emitMarkdownEndpoints fails fast on duplicate raw markdown endpoint slugs', async () => {
  const tempRoot = await mkdtemp(path.join(os.tmpdir(), 'builder-ai-docs-'));
  const sourceDirectory = path.join(tempRoot, 'src');
  const generatedDirectory = path.join(tempRoot, 'generated');
  const outputDirectory = path.join(tempRoot, 'dist');

  await mkdir(sourceDirectory, { recursive: true });
  await mkdir(generatedDirectory, { recursive: true });
  await writeFile(path.join(sourceDirectory, 'quickstart.md'), 'source quickstart\n', 'utf8');
  await writeFile(path.join(generatedDirectory, 'quickstart.md'), 'generated quickstart\n', 'utf8');

  await assert.rejects(
    emitMarkdownEndpoints({
      sourceDirectories: [sourceDirectory, generatedDirectory],
      outputDirectory,
    }),
    /duplicate docs markdown endpoint "quickstart"/,
  );
});

test('emitMarkdownEndpoints removes stale endpoints from the previous manifest', async () => {
  const tempRoot = await mkdtemp(path.join(os.tmpdir(), 'builder-ai-docs-'));
  const sourceDirectory = path.join(tempRoot, 'src');
  const outputDirectory = path.join(tempRoot, 'dist');

  await mkdir(sourceDirectory, { recursive: true });
  await mkdir(outputDirectory, { recursive: true });
  await writeFile(path.join(sourceDirectory, 'quickstart.md'), 'quickstart source\n', 'utf8');
  await writeFile(path.join(outputDirectory, 'stale.md'), 'stale endpoint\n', 'utf8');
  await writeFile(
    path.join(outputDirectory, '.builder-docs-markdown-endpoints.json'),
    `${JSON.stringify({ entryIds: ['quickstart', 'stale'] })}\n`,
    'utf8',
  );

  const entryIds = await emitMarkdownEndpoints({
    sourceDirectories: [sourceDirectory],
    outputDirectory,
  });

  assert.deepEqual(entryIds, ['quickstart']);
  assert.equal(await readFile(path.join(outputDirectory, 'quickstart.md'), 'utf8'), 'quickstart source\n');
  await assert.rejects(readFile(path.join(outputDirectory, 'stale.md'), 'utf8'));
});

test('appendMarkdownDiscovery appends absolute raw markdown links to llms.txt', async () => {
  const tempRoot = await mkdtemp(path.join(os.tmpdir(), 'builder-ai-docs-'));
  const llmsPath = path.join(tempRoot, 'llms.txt');

  await writeFile(llmsPath, '# Builder\n', 'utf8');
  await appendMarkdownDiscovery({
    llmsPath,
    markdownEntryIds: ['quickstart', 'nested/page'],
    docsConfig: {
      getPublicUrl(pathname) {
        return `https://example.com/builder${pathname}`;
      },
    },
  });

  assert.equal(
    await readFile(llmsPath, 'utf8'),
    [
      '# Builder',
      '',
      '## Raw Markdown Pages',
      '',
      '- [quickstart](https://example.com/builder/quickstart.md)',
      '- [nested/page](https://example.com/builder/nested/page.md)',
      '',
    ].join('\n'),
  );
});
