import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdir, readFile, unlink, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { spawn } from 'node:child_process';
import { fileURLToPath } from 'node:url';

import { mirroredDocuments } from './mirrored-documents.mjs';

const pnpmCommand = process.platform === 'win32' ? 'pnpm.cmd' : 'pnpm';

function runCommand(command, args, workdir) {
  return new Promise((resolve, reject) => {
    const child = spawn(command, args, {
      cwd: workdir,
      env: process.env,
      stdio: ['ignore', 'ignore', 'pipe'],
    });

    let stderr = '';

    child.stderr.on('data', (chunk) => {
      stderr += chunk.toString();
    });

    child.on('close', (code) => {
      if (code === 0) {
        resolve();
        return;
      }

      reject(new Error(stderr || `command exited with code ${code}`));
    });

    child.on('error', reject);
  });
}

async function removeIfExists(filePath) {
  try {
    await unlink(filePath);
  } catch (error) {
    if (error?.code !== 'ENOENT') {
      throw error;
    }
  }
}

test('astro build ignores stale legacy mirrored docs in src/content/docs', async () => {
  const workdir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
  const legacyDocsPath = path.join(workdir, 'src', 'content', 'docs', 'docs.md');
  const generatedDocsDirectory = path.join(workdir, 'src', '.generated', 'content', 'docs');
  const generatedDocsPath = path.join(generatedDocsDirectory, 'docs.md');
  const previousGeneratedDocs = await readFile(generatedDocsPath, 'utf8').catch(() => undefined);

  await mkdir(generatedDocsDirectory, { recursive: true });
  await writeFile(legacyDocsPath, '# stale legacy mirror\n', 'utf8');
  await writeFile(generatedDocsPath, ['---', 'title: Home', 'editUrl: false', '---', '', 'Generated.'].join('\n'), 'utf8');

  try {
    await runCommand(pnpmCommand, ['astro', 'build'], workdir);
  } finally {
    await removeIfExists(legacyDocsPath);

    if (previousGeneratedDocs === undefined) {
      await removeIfExists(generatedDocsPath);
    } else {
      await writeFile(generatedDocsPath, previousGeneratedDocs, 'utf8');
    }
  }

  assert.equal(true, true);
});

test('astro build generates mirrored docs without a pre-sync step', async () => {
  const workdir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
  const generatedDocsDirectory = path.join(workdir, 'src', '.generated', 'content', 'docs');
  const previousMirroredDocs = await Promise.all(
    mirroredDocuments.map(async (document) => {
      const outputPath = path.join(generatedDocsDirectory, document.outputFileName);
      return {
        outputPath,
        contents: await readFile(outputPath, 'utf8').catch(() => undefined),
      };
    }),
  );

  await mkdir(generatedDocsDirectory, { recursive: true });
  await Promise.all(previousMirroredDocs.map(({ outputPath }) => removeIfExists(outputPath)));

  let generatedDocs;

  try {
    await runCommand(pnpmCommand, ['astro', 'build'], workdir);
    generatedDocs = await readFile(path.join(generatedDocsDirectory, 'docs.md'), 'utf8');
  } finally {
    await Promise.all(
      previousMirroredDocs.map(async ({ outputPath, contents }) => {
        if (contents === undefined) {
          await removeIfExists(outputPath);
          return;
        }

        await writeFile(outputPath, contents, 'utf8');
      }),
    );
  }

  assert.equal(generatedDocs.includes('title: Home'), true);
});
