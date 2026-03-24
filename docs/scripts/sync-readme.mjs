import { mkdir, readFile, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import { mirrorReadme, mirrorRepoMarkdownDocument } from './readme-mirror.mjs';
import { resolveDocsConfig } from './site-config.mjs';

const currentFilePath = fileURLToPath(import.meta.url);
const docsRoot = path.dirname(path.dirname(currentFilePath));
const repoRoot = path.dirname(docsRoot);
const outputDirectory = path.join(docsRoot, '.generated', 'content', 'docs');

const mirroredDocuments = [
  {
    sourcePath: 'README.md',
    outputFileName: 'docs.md',
    mirror(markdown, docsConfig) {
      return mirrorReadme(markdown, docsConfig);
    },
  },
  {
    sourcePath: 'CONTRIBUTING.md',
    outputFileName: 'contributing.md',
    mirror(markdown, docsConfig) {
      return mirrorRepoMarkdownDocument(markdown, docsConfig, {
        title: 'Contributing',
        editPath: 'CONTRIBUTING.md',
      });
    },
  },
  {
    sourcePath: 'SECURITY.md',
    outputFileName: 'security.md',
    mirror(markdown, docsConfig) {
      return mirrorRepoMarkdownDocument(markdown, docsConfig, {
        title: 'Security',
        editPath: 'SECURITY.md',
      });
    },
  },
];

await mkdir(outputDirectory, { recursive: true });

const docsConfig = resolveDocsConfig();

await Promise.all(
  mirroredDocuments.map(async (document) => {
    const sourceFilePath = path.join(repoRoot, document.sourcePath);
    const outputFilePath = path.join(outputDirectory, document.outputFileName);
    const sourceMarkdown = await readFile(sourceFilePath, 'utf8');
    const mirroredMarkdown = document.mirror(sourceMarkdown, docsConfig);
    await writeFile(outputFilePath, mirroredMarkdown, 'utf8');
  }),
);
