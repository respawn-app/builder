import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, mkdir, readFile, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';

import { removeMirroredDocForSourcePath } from './docs-collection-loader.mjs';

test('removeMirroredDocForSourcePath deletes the generated mirror for a removed root doc', async () => {
  const tempRoot = await mkdtemp(path.join(os.tmpdir(), 'builder-docs-loader-'));
  const docsRoot = path.join(tempRoot, 'docs');
  const repoRoot = tempRoot;
  const generatedDocsDirectory = path.join(docsRoot, 'src', '.generated', 'content', 'docs');
  const generatedDocsPath = path.join(generatedDocsDirectory, 'docs.md');

  await mkdir(generatedDocsDirectory, { recursive: true });
  await writeFile(generatedDocsPath, 'generated\n', 'utf8');

  const removed = await removeMirroredDocForSourcePath(path.join(repoRoot, 'README.md'), docsRoot, repoRoot);

  assert.equal(removed, true);
  await assert.rejects(readFile(generatedDocsPath, 'utf8'));
});

test('removeMirroredDocForSourcePath ignores unrelated files', async () => {
  const tempRoot = await mkdtemp(path.join(os.tmpdir(), 'builder-docs-loader-'));
  const docsRoot = path.join(tempRoot, 'docs');
  const repoRoot = tempRoot;
  const generatedDocsDirectory = path.join(docsRoot, 'src', '.generated', 'content', 'docs');
  const generatedDocsPath = path.join(generatedDocsDirectory, 'docs.md');

  await mkdir(generatedDocsDirectory, { recursive: true });
  await writeFile(generatedDocsPath, 'generated\n', 'utf8');

  const removed = await removeMirroredDocForSourcePath(
    path.join(docsRoot, 'dev', 'decisions.md'),
    docsRoot,
    repoRoot,
  );

  assert.equal(removed, false);
  const generatedDocs = await readFile(generatedDocsPath, 'utf8');
  assert.equal(generatedDocs, 'generated\n');
});
