import { spawn } from 'node:child_process';
import { readFile } from 'node:fs/promises';
import net from 'node:net';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import { resolveDocsConfig } from './site-config.mjs';

const currentFilePath = fileURLToPath(import.meta.url);
const docsRoot = path.dirname(path.dirname(currentFilePath));
const docsConfig = resolveDocsConfig();

async function findOpenPort() {
  const server = net.createServer();
  await new Promise((resolve, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', resolve);
  });
  const address = server.address();
  await new Promise((resolve, reject) => server.close((error) => (error ? reject(error) : resolve())));

  if (!address || typeof address === 'string') {
    throw new Error('failed to allocate preview port');
  }

  return address.port;
}

async function waitForPreview(baseUrl, processOutput) {
  const deadline = Date.now() + 15_000;
  let lastError;

  while (Date.now() < deadline) {
    try {
      const response = await fetch(`${baseUrl}${docsConfig.basePath}/llms.txt`);
      if (response.ok) {
        return;
      }
      lastError = new Error(`preview returned HTTP ${response.status}`);
    } catch (error) {
      lastError = error;
    }

    await new Promise((resolve) => setTimeout(resolve, 200));
  }

  throw new Error(`preview did not become ready: ${lastError?.message ?? 'unknown error'}\n${processOutput()}`);
}

async function fetchText(url) {
  const response = await fetch(url);
  if (!response.ok) {
    throw new Error(`${url} returned HTTP ${response.status}`);
  }
  return response.text();
}

const port = await findOpenPort();
const baseUrl = `http://127.0.0.1:${port}`;
const outputChunks = [];
const astroBin = path.join(docsRoot, 'node_modules', '.bin', 'astro');
const preview = spawn(astroBin, ['preview', '--host', '127.0.0.1', '--port', String(port)], {
  cwd: docsRoot,
  stdio: ['ignore', 'pipe', 'pipe'],
});
const processOutput = () => Buffer.concat(outputChunks).toString('utf8');

preview.stdout.on('data', (chunk) => outputChunks.push(chunk));
preview.stderr.on('data', (chunk) => outputChunks.push(chunk));

try {
  await waitForPreview(baseUrl, processOutput);

  const markdownUrl = `${baseUrl}${docsConfig.basePath}/command-postprocessing.md`;
  const sandboxingUrl = `${baseUrl}${docsConfig.basePath}/sandboxing/`;
  const sandboxingMarkdownUrl = `${baseUrl}${docsConfig.basePath}/sandboxing.md`;
  const [markdownText, , sandboxingMarkdown, sourceMarkdown, sandboxingSourceMarkdown] = await Promise.all([
    fetchText(markdownUrl),
    fetchText(sandboxingUrl),
    fetchText(sandboxingMarkdownUrl),
    readFile(path.join(docsRoot, 'src', 'content', 'docs', 'command-postprocessing.md'), 'utf8'),
    readFile(path.join(docsRoot, 'src', 'content', 'docs', 'sandboxing.md'), 'utf8'),
  ]);

  if (markdownText !== sourceMarkdown) {
    throw new Error(`${markdownUrl} does not match source markdown`);
  }
  if (sandboxingMarkdown !== sandboxingSourceMarkdown) {
    throw new Error(`${sandboxingMarkdownUrl} does not match source markdown`);
  }
} finally {
  preview.kill('SIGTERM');
}
