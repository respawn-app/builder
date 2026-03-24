import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdir, readFile, unlink, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { spawn } from 'node:child_process';

function runCommand(command, args, workdir) {
  return new Promise((resolve, reject) => {
    const child = spawn(command, args, {
      cwd: workdir,
      env: process.env,
      stdio: ['ignore', 'pipe', 'pipe'],
    });

    let stderr = '';

    child.stderr.on('data', (chunk) => {
      stderr += chunk.toString();
    });

    child.on('exit', (code) => {
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
  const workdir = path.resolve(path.dirname(new URL(import.meta.url).pathname), '..');
  const legacyDocsPath = path.join(workdir, 'src', 'content', 'docs', 'docs.md');
  const generatedDocsDirectory = path.join(workdir, 'src', '.generated', 'content', 'docs');
  const generatedDocsPath = path.join(generatedDocsDirectory, 'docs.md');
  const previousGeneratedDocs = await readFile(generatedDocsPath, 'utf8').catch(() => undefined);

  await mkdir(generatedDocsDirectory, { recursive: true });
  await writeFile(legacyDocsPath, '# stale legacy mirror\n', 'utf8');
  await writeFile(generatedDocsPath, ['---', 'title: Home', 'editUrl: false', '---', '', 'Generated.'].join('\n'), 'utf8');

  try {
    await runCommand('pnpm', ['astro', 'build'], workdir);
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
