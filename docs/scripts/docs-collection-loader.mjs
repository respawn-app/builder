import path from 'node:path';
import { fileURLToPath } from 'node:url';

import { glob } from 'astro/loaders';

import { mirroredDocuments } from './mirrored-documents.mjs';
import { syncMirroredDocs } from './sync-mirrored-docs.mjs';

function buildDocsGlobPatterns() {
  const legacyMirroredFileNames = mirroredDocuments.map((document) => path.posix.parse(document.outputFileName).name);
  const excludedLegacyMirroredFiles = `!content/docs/{${legacyMirroredFileNames.join(',')}}.md`;

  return ['content/docs/**/*.md', excludedLegacyMirroredFiles, '.generated/content/docs/**/*.md'];
}

function trimDocsPrefix(entryPath, prefix) {
  return entryPath.startsWith(prefix) ? entryPath.slice(prefix.length) : undefined;
}

function stripMarkdownExtension(entryPath) {
  const parsedPath = path.posix.parse(entryPath);
  return parsedPath.dir.length === 0 ? parsedPath.name : path.posix.join(parsedPath.dir, parsedPath.name);
}

function generateDocsEntryId({ entry }) {
  const normalizedEntryPath = entry.split(path.sep).join(path.posix.sep);
  const relativeDocsPath =
    trimDocsPrefix(normalizedEntryPath, 'content/docs/') ??
    trimDocsPrefix(normalizedEntryPath, '.generated/content/docs/');

  if (!relativeDocsPath) {
    throw new Error(`unsupported docs entry path: ${entry}`);
  }

  return stripMarkdownExtension(relativeDocsPath);
}

function createMirrorSourcePaths(repoRoot) {
  return mirroredDocuments.map((document) => path.join(repoRoot, document.sourcePath));
}

function isMirrorSourcePath(changedPath, mirrorSourcePaths) {
  const normalizedChangedPath = path.resolve(changedPath);
  return mirrorSourcePaths.some((sourcePath) => path.resolve(sourcePath) === normalizedChangedPath);
}

export function createDocsCollectionLoader() {
  const delegatedLoader = glob({
    base: 'src',
    pattern: buildDocsGlobPatterns(),
    generateId: generateDocsEntryId,
  });
  const initializedWatchers = new WeakSet();

  return {
    name: 'builder-docs-collection-loader',
    async load(context) {
      const docsRoot = fileURLToPath(context.config.root);
      const repoRoot = path.dirname(docsRoot);

      await syncMirroredDocs({ docsRoot, repoRoot });
      await delegatedLoader.load(context);

      if (!context.watcher || initializedWatchers.has(context.watcher)) {
        return;
      }

      initializedWatchers.add(context.watcher);
      const mirrorSourcePaths = createMirrorSourcePaths(repoRoot);
      context.watcher.add(mirrorSourcePaths);

      const reloadMirroredDocs = async (changedPath) => {
        if (!isMirrorSourcePath(changedPath, mirrorSourcePaths)) {
          return;
        }

        try {
          await syncMirroredDocs({ docsRoot, repoRoot });
        } catch (error) {
          context.logger.error(`Error syncing mirrored docs from ${changedPath}: ${error.message}`);
        }
      };

      context.watcher.on('add', reloadMirroredDocs);
      context.watcher.on('change', reloadMirroredDocs);
    },
  };
}
