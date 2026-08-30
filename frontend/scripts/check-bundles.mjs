import { readFile, readdir, stat } from 'node:fs/promises';
import path from 'node:path';

const dist = path.resolve('dist');
const html = await readFile(path.join(dist, 'index.html'), 'utf8');
const entryMatch = html.match(/<script[^>]+src="([^"]+\.js)"/);
if (!entryMatch) throw new Error('built HTML has no JavaScript entry');

const entryPath = path.join(dist, entryMatch[1].replace(/^\//, ''));
const entry = await readFile(entryPath, 'utf8');
const entryBytes = (await stat(entryPath)).size;
const assets = await readdir(path.join(dist, 'assets'));
const playerChunks = assets.filter((name) => /^Player-.*\.js$/.test(name));
const hlsChunks = assets.filter((name) => /^hls-.*\.js$/.test(name));

if (playerChunks.length !== 1 || hlsChunks.length !== 1) {
  throw new Error(`expected one lazy Player chunk and one lazy hls chunk; got Player=${playerChunks.length}, hls=${hlsChunks.length}`);
}
if (entry.includes('This browser cannot play the compatibility stream.') || entry.includes('Playback was blocked. Try play again.')) {
  throw new Error('player implementation leaked into the initial entry chunk');
}
if (entryBytes > 300_000) {
  throw new Error(`initial entry chunk is ${entryBytes} bytes, over the 300000-byte bound`);
}

console.log(`bundle boundary passed: entry=${entryBytes}B player=${playerChunks[0]} hls=${hlsChunks[0]}`);
