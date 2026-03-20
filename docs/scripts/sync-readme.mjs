import { mkdir, readFile, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import { mirrorReadme } from './readme-mirror.mjs';
import { resolveDocsConfig } from './site-config.mjs';

const currentFilePath = fileURLToPath(import.meta.url);
const docsRoot = path.dirname(path.dirname(currentFilePath));
const repoRoot = path.dirname(docsRoot);
const outputDirectory = path.join(docsRoot, 'src', 'content', 'docs');
const outputFilePath = path.join(outputDirectory, 'docs.md');
const readmePath = path.join(repoRoot, 'README.md');

await mkdir(outputDirectory, { recursive: true });

const readmeMarkdown = await readFile(readmePath, 'utf8');
const mirroredMarkdown = mirrorReadme(readmeMarkdown, resolveDocsConfig());

await writeFile(outputFilePath, mirroredMarkdown, 'utf8');
