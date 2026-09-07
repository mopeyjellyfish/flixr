import { deflateSync } from 'node:zlib';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';

const target = process.argv[2];
if (!target) throw new Error('Usage: generate-artwork-measurement-demo.mjs DIRECTORY');

const width = 1200;
const height = 1800;
const crcTable = Uint32Array.from({ length: 256 }, (_, index) => {
  let value = index;
  for (let bit = 0; bit < 8; bit += 1) value = value & 1 ? 0xedb88320 ^ (value >>> 1) : value >>> 1;
  return value >>> 0;
});

function crc32(buffer) {
  let value = 0xffffffff;
  for (const byte of buffer) value = crcTable[(value ^ byte) & 0xff] ^ (value >>> 8);
  return (value ^ 0xffffffff) >>> 0;
}

function chunk(type, body) {
  const typeBytes = Buffer.from(type, 'ascii');
  const header = Buffer.alloc(8);
  header.writeUInt32BE(body.length, 0);
  typeBytes.copy(header, 4);
  const checksum = Buffer.alloc(4);
  checksum.writeUInt32BE(crc32(Buffer.concat([typeBytes, body])));
  return Buffer.concat([header, body, checksum]);
}

function poster(index) {
  const stride = width * 3;
  const pixels = Buffer.alloc((stride + 1) * height);
  for (let y = 0; y < height; y += 1) {
    const row = y * (stride + 1);
    pixels[row] = 0; // PNG's "no filter" byte.
    for (let x = 0; x < width; x += 1) {
      const offset = row + 1 + x * 3;
      const tile = (Math.floor(x / 24) * 17 + Math.floor(y / 24) * 31 + index * 43) & 0xff;
      pixels[offset] = tile;
      pixels[offset + 1] = (tile * 3 + index * 19) & 0xff;
      pixels[offset + 2] = (tile * 7 + y / 18) & 0xff;
    }
  }
  const header = Buffer.alloc(13);
  header.writeUInt32BE(width, 0);
  header.writeUInt32BE(height, 4);
  header[8] = 8; // bits per channel
  header[9] = 2; // RGB
  return Buffer.concat([
    Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]),
    chunk('IHDR', header),
    chunk('IDAT', deflateSync(pixels, { level: 1 })),
    chunk('IEND', Buffer.alloc(0)),
  ]);
}

await mkdir(join(target, 'assets'), { recursive: true });
const films = [];
const series = [];
for (let index = 0; index < 100; index += 1) {
  const asset = `poster-${String(index).padStart(3, '0')}.png`;
  await writeFile(join(target, 'assets', asset), poster(index));
  const item = {
    title: `${index < 50 ? 'Film' : 'Series'} Fixture ${String(index + 1).padStart(3, '0')}`,
    year: 2026,
    synopsis: 'Deterministic local artwork measurement fixture.',
    genres: ['Measurement'],
    poster: asset,
    backdrop: '',
  };
  if (index < 50) films.push(item);
  else series.push(item);
}
await writeFile(join(target, 'catalog.json'), `${JSON.stringify({ source: 'Generated local artwork measurement fixture', films, series }, null, 2)}\n`);
