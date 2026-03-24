import { unlink } from 'node:fs/promises';
import path from 'node:path';

export async function removeLegacyMirroredDocuments(legacyOutputDirectory, mirroredDocuments) {
  await Promise.all(
    mirroredDocuments.map(async (document) => {
      const legacyOutputPath = path.join(legacyOutputDirectory, document.outputFileName);

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
