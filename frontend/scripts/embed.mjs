import { cp, mkdir, readFile, readdir, rm, writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import { gzipSync } from 'node:zlib';

const source = resolve('dist');
const target = resolve('../backend/web/assets');

await mkdir(target, { recursive: true });
for (const entry of await readdir(target)) {
  if (entry !== 'placeholder.txt') await rm(resolve(target, entry), { recursive: true, force: true });
}
await cp(source, target, { recursive: true });

async function compressHashedAssets(directory) {
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    const file = resolve(directory, entry.name);
    if (entry.isDirectory()) {
      await compressHashedAssets(file);
    } else if (/-[A-Za-z0-9_-]{8}\.(?:js|css)$/.test(entry.name)) {
      const contents = await readFile(file);
      await writeFile(`${file}.gz`, gzipSync(contents, { level: 9, mtime: 0 }));
    }
  }
}

await compressHashedAssets(target);
