import path from 'node:path';

import { unified } from 'unified';
import remarkGfm from 'remark-gfm';
import remarkParse from 'remark-parse';
import remarkStringify from 'remark-stringify';
import { visit } from 'unist-util-visit';

function isFragmentOnly(url) {
  return url.startsWith('#') || url.startsWith('?');
}

function isAbsoluteUrl(url) {
  try {
    return Boolean(new URL(url).protocol);
  } catch {
    return false;
  }
}

function shouldRewriteUrl(url) {
  if (typeof url !== 'string' || url.length === 0) {
    return false;
  }

  if (isFragmentOnly(url) || url.startsWith('/')) {
    return false;
  }

  return !isAbsoluteUrl(url);
}

function splitHash(url) {
  const hashIndex = url.indexOf('#');
  if (hashIndex === -1) {
    return { pathname: url, hash: '' };
  }

  return {
    pathname: url.slice(0, hashIndex),
    hash: url.slice(hashIndex),
  };
}

function rewriteRelativeUrl(url, docsConfig) {
  const { pathname, hash } = splitHash(url);
  const normalizedPath = path.posix.normalize(pathname);
  const isDirectory = pathname.endsWith('/');
  const extension = path.posix.extname(normalizedPath).toLowerCase();
  const isImage = ['.avif', '.gif', '.jpeg', '.jpg', '.png', '.svg', '.webp'].includes(extension);

  if (isImage) {
    return new URL(normalizedPath, docsConfig.repoRawRootUrl).toString() + hash;
  }

  const targetRoot = isDirectory ? docsConfig.repoUrl.replace('/agent', '/agent/tree/main') : docsConfig.repoBlobRootUrl;
  return new URL(normalizedPath, `${targetRoot}`).toString() + hash;
}

export function mirrorReadme(markdown, docsConfig) {
  const processor = unified()
    .use(remarkParse)
    .use(remarkGfm)
    .use(() => (tree) => {
      const firstTopLevelHeadingIndex = tree.children.findIndex(
        (node) => node.type === 'heading' && node.depth === 1,
      );

      if (firstTopLevelHeadingIndex >= 0) {
        tree.children.splice(firstTopLevelHeadingIndex, 1);
      }

      visit(tree, (node) => {
        if ((node.type === 'link' || node.type === 'image') && shouldRewriteUrl(node.url)) {
          node.url = rewriteRelativeUrl(node.url, docsConfig);
        }
      });
    })
    .use(remarkGfm)
    .use(remarkStringify, {
      bullet: '-',
      fences: true,
      listItemIndent: 'one',
      rule: '-',
      strong: '*',
    });

  const transformedBody = String(processor.processSync(markdown)).trim();
  const frontmatter = [
    '---',
    `title: ${docsConfig.docsHomeTitle}`,
    `editUrl: ${docsConfig.repoEditRootUrl}README.md`,
    '---',
    '',
  ].join('\n');

  return `${frontmatter}${transformedBody}\n`;
}
