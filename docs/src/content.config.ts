import { defineCollection } from 'astro:content';
import path from 'node:path';

import { glob } from 'astro/loaders';
import { docsSchema } from '@astrojs/starlight/schema';

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
      pattern: ['src/content/docs/**/*.md', '.generated/content/docs/**/*.md'],
      generateId: generateDocsEntryId,
    }),
    schema: docsSchema(),
  }),
};
