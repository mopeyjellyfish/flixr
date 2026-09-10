import assert from 'node:assert/strict';
import { execFile } from 'node:child_process';
import { mkdtemp, mkdir, readFile, stat, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { resolve } from 'node:path';
import { promisify } from 'node:util';
import { gunzipSync } from 'node:zlib';
import test from 'node:test';

const execFileAsync = promisify(execFile);
const script = resolve(import.meta.dirname, 'embed.mjs');

test('embed creates deterministic gzip siblings only for hashed JavaScript and CSS', async (t) => {
  const root = await mkdtemp(resolve(tmpdir(), 'flixr-embed-'));
  const frontend = resolve(root, 'frontend');
  const distAssets = resolve(frontend, 'dist/assets');
  const embeddedAssets = resolve(root, 'backend/web/assets');
  t.after(async () => (await import('node:fs/promises')).rm(root, { recursive: true, force: true }));
  await mkdir(distAssets, { recursive: true });
  await mkdir(embeddedAssets, { recursive: true });
  await writeFile(resolve(embeddedAssets, 'placeholder.txt'), 'kept');
  const javascript = Buffer.from("console.log('playback');".repeat(64));
  const stylesheet = Buffer.from('body { color: white; }'.repeat(64));
  await writeFile(resolve(distAssets, 'hls-Ab-2_cd3.js'), javascript);
  await writeFile(resolve(distAssets, 'index-1234abcd.css'), stylesheet);
  await writeFile(resolve(distAssets, 'plain.js'), javascript);
  await writeFile(resolve(distAssets, 'logo-1234abcd.svg'), '<svg/>');
  await writeFile(resolve(frontend, 'dist/index.html'), '<main>Flixr</main>');

  const runEmbed = () => execFileAsync(process.execPath, [script], { cwd: frontend });
  await runEmbed();
  const firstJavaScriptGzip = await readFile(resolve(embeddedAssets, 'assets/hls-Ab-2_cd3.js.gz'));
  const firstStylesheetGzip = await readFile(resolve(embeddedAssets, 'assets/index-1234abcd.css.gz'));

  assert.deepEqual(gunzipSync(firstJavaScriptGzip), javascript);
  assert.deepEqual(gunzipSync(firstStylesheetGzip), stylesheet);
  assert.equal((await readFile(resolve(embeddedAssets, 'placeholder.txt'), 'utf8')), 'kept');
  await assert.rejects(stat(resolve(embeddedAssets, 'assets/plain.js.gz')), { code: 'ENOENT' });
  await assert.rejects(stat(resolve(embeddedAssets, 'assets/logo-1234abcd.svg.gz')), { code: 'ENOENT' });

  await runEmbed();
  assert.deepEqual(await readFile(resolve(embeddedAssets, 'assets/hls-Ab-2_cd3.js.gz')), firstJavaScriptGzip);
  assert.deepEqual(await readFile(resolve(embeddedAssets, 'assets/index-1234abcd.css.gz')), firstStylesheetGzip);
});
