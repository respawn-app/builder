import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, mkdir, readFile, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { removeLegacyMirroredDocuments } from './legacy-mirrored-documents.mjs';
import { mirroredDocuments } from './mirrored-documents.mjs';

test('removeLegacyMirroredDocuments removes stale generated files from src/content/docs', async () => {
  const tempRoot = await mkdtemp(path.join(os.tmpdir(), 'builder-docs-sync-'));
  const legacyDocsDirectory = path.join(tempRoot, 'src', 'content', 'docs');

  await mkdir(legacyDocsDirectory, { recursive: true });
  await writeFile(path.join(legacyDocsDirectory, 'docs.md'), '# stale\n', 'utf8');
  await writeFile(path.join(legacyDocsDirectory, 'contributing.md'), '# stale\n', 'utf8');
  await writeFile(path.join(legacyDocsDirectory, 'security.md'), '# stale\n', 'utf8');
  await writeFile(path.join(legacyDocsDirectory, 'quickstart.md'), '# keep me\n', 'utf8');

  await removeLegacyMirroredDocuments(legacyDocsDirectory, mirroredDocuments);

  await assert.rejects(readFile(path.join(legacyDocsDirectory, 'docs.md'), 'utf8'));
  await assert.rejects(readFile(path.join(legacyDocsDirectory, 'contributing.md'), 'utf8'));
  await assert.rejects(readFile(path.join(legacyDocsDirectory, 'security.md'), 'utf8'));
  const retainedDocument = await readFile(path.join(legacyDocsDirectory, 'quickstart.md'), 'utf8');
  assert.equal(retainedDocument, '# keep me\n');
});
