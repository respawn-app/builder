import { mkdir, readFile, writeFile } from 'node:fs/promises';
import path from 'node:path';

import { removeLegacyMirroredDocuments } from './legacy-mirrored-documents.mjs';
import { mirrorRepoMarkdownDocument } from './readme-mirror.mjs';
import { mirroredDocuments } from './mirrored-documents.mjs';
import { resolveDocsConfig } from './site-config.mjs';

export function resolveMirroredDocsPaths(docsRoot) {
  return {
    outputDirectory: path.join(docsRoot, 'src', '.generated', 'content', 'docs'),
    legacyOutputDirectory: path.join(docsRoot, 'src', 'content', 'docs'),
    deprecatedGeneratedOutputDirectory: path.join(docsRoot, '.generated', 'content', 'docs'),
  };
}

export async function syncMirroredDocs({ docsRoot, repoRoot, docsConfig = resolveDocsConfig() }) {
  const { outputDirectory, legacyOutputDirectory, deprecatedGeneratedOutputDirectory } =
    resolveMirroredDocsPaths(docsRoot);

  await mkdir(outputDirectory, { recursive: true });
  await removeLegacyMirroredDocuments(legacyOutputDirectory, mirroredDocuments);
  await removeLegacyMirroredDocuments(deprecatedGeneratedOutputDirectory, mirroredDocuments);

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
}
