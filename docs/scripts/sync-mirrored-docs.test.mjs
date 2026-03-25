import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, mkdir, readFile, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';

import { removeLegacyMirroredDocuments } from './legacy-mirrored-documents.mjs';
import { writeFileAtomically } from './sync-mirrored-docs.mjs';

test('writeFileAtomically only exposes complete file contents during repeated writes', async () => {
  const tempRoot = await mkdtemp(path.join(os.tmpdir(), 'builder-docs-atomic-'));
  const filePath = path.join(tempRoot, 'docs.md');
  const initialContents = ['---', 'title: Home', '---', '', 'initial'].join('\n');
  const nextContents = ['---', 'title: Home', '---', '', 'next '.repeat(20000)].join('\n');
  const allowedContents = new Set([initialContents, nextContents]);

  await writeFile(filePath, initialContents, 'utf8');

  let writing = true;
  const reader = (async () => {
    while (writing) {
      const contents = await readFile(filePath, 'utf8');
      assert.equal(allowedContents.has(contents), true);
    }
  })();

  for (let index = 0; index < 20; index += 1) {
    await writeFileAtomically(filePath, index % 2 === 0 ? nextContents : initialContents);
  }

  writing = false;
  await reader;

  const finalContents = await readFile(filePath, 'utf8');
  assert.equal(allowedContents.has(finalContents), true);
});

test('removeLegacyMirroredDocuments rejects paths that escape the legacy docs directory', async () => {
  const tempRoot = await mkdtemp(path.join(os.tmpdir(), 'builder-docs-legacy-'));
  const legacyDocsDirectory = path.join(tempRoot, 'src', 'content', 'docs');

  await mkdir(legacyDocsDirectory, { recursive: true });

  await assert.rejects(
    removeLegacyMirroredDocuments(legacyDocsDirectory, [{ outputFileName: '../outside.md' }]),
    /refusing to remove mirrored document outside/,
  );
});
