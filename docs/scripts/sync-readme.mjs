import { mkdir, readFile, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import { removeLegacyMirroredDocuments } from './legacy-mirrored-documents.mjs';
import { mirrorRepoMarkdownDocument } from './readme-mirror.mjs';
import { mirroredDocuments } from './mirrored-documents.mjs';
import { resolveDocsConfig } from './site-config.mjs';

const currentFilePath = fileURLToPath(import.meta.url);
const docsRoot = path.dirname(path.dirname(currentFilePath));
const repoRoot = path.dirname(docsRoot);
const outputDirectory = path.join(docsRoot, '.generated', 'content', 'docs');
const legacyOutputDirectory = path.join(docsRoot, 'src', 'content', 'docs');

await mkdir(outputDirectory, { recursive: true });
await removeLegacyMirroredDocuments(legacyOutputDirectory, mirroredDocuments);

const docsConfig = resolveDocsConfig();

await Promise.all(
  mirroredDocuments.map(async (document) => {
    const sourceFilePath = path.join(repoRoot, document.sourcePath);
    const outputFilePath = path.join(outputDirectory, document.outputFileName);
    const sourceMarkdown = await readFile(sourceFilePath, 'utf8');
    const mirroredMarkdown = mirrorRepoMarkdownDocument(sourceMarkdown, docsConfig, {
      title: document.title,
      editPath: document.editPath,
    });
    await writeFile(outputFilePath, mirroredMarkdown, 'utf8');
  }),
);
