import { defineCollection } from 'astro:content';
import path from 'node:path';

import { glob } from 'astro/loaders';
import { docsSchema } from '@astrojs/starlight/schema';

import { mirroredDocuments } from '../scripts/mirrored-documents.mjs';

function buildDocsGlobPatterns(): string[] {
  const legacyMirroredFileNames = mirroredDocuments.map((document) => path.posix.parse(document.outputFileName).name);
  const excludedLegacyMirroredFiles = `!src/content/docs/{${legacyMirroredFileNames.join(',')}}.md`;

  return ['src/content/docs/**/*.md', excludedLegacyMirroredFiles, '.generated/content/docs/**/*.md'];
}

function trimDocsPrefix(entryPath: string, prefix: string): string | undefined {
  return entryPath.startsWith(prefix) ? entryPath.slice(prefix.length) : undefined;
}

function stripMarkdownExtension(entryPath: string): string {
  const parsedPath = path.posix.parse(entryPath);
  return parsedPath.dir.length === 0 ? parsedPath.name : path.posix.join(parsedPath.dir, parsedPath.name);
}

function generateDocsEntryId({ entry }: { entry: string }): string {
  const normalizedEntryPath = entry.split(path.sep).join(path.posix.sep);
  const relativeDocsPath =
    trimDocsPrefix(normalizedEntryPath, 'src/content/docs/') ??
    trimDocsPrefix(normalizedEntryPath, '.generated/content/docs/');

  if (!relativeDocsPath) {
    throw new Error(`unsupported docs entry path: ${entry}`);
  }

  return stripMarkdownExtension(relativeDocsPath);
}

export const collections = {
  docs: defineCollection({
    loader: glob({
      base: '.',
      pattern: buildDocsGlobPatterns(),
      generateId: generateDocsEntryId,
    }),
    schema: docsSchema(),
  }),
};
