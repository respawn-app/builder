import { unlink } from 'node:fs/promises';
import path from 'node:path';

function resolveLegacyOutputPath(legacyOutputDirectory, outputFileName) {
  const resolvedBaseDirectory = path.resolve(legacyOutputDirectory);
  const resolvedTargetPath = path.resolve(resolvedBaseDirectory, outputFileName);
  const relativeTargetPath = path.relative(resolvedBaseDirectory, resolvedTargetPath);

  if (
    relativeTargetPath === '..' ||
    relativeTargetPath.startsWith(`..${path.sep}`) ||
    path.isAbsolute(relativeTargetPath)
  ) {
    throw new Error(`refusing to remove mirrored document outside ${resolvedBaseDirectory}: ${outputFileName}`);
  }

  return resolvedTargetPath;
}

export async function removeLegacyMirroredDocuments(legacyOutputDirectory, mirroredDocuments) {
  await Promise.all(
    mirroredDocuments.map(async (document) => {
      const legacyOutputPath = resolveLegacyOutputPath(legacyOutputDirectory, document.outputFileName);

      try {
        await unlink(legacyOutputPath);
      } catch (error) {
        if (error?.code !== 'ENOENT') {
          throw error;
        }
      }
    }),
  );
}
