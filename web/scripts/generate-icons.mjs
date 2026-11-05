// 生成 PWA 图标 PNG（public/icons/icon-*.png），与 public/icons/icon.svg 同一图形。
//
// 机器上没有 rsvg-convert / ImageMagick / sharp，又不想为几张图标引入依赖，
// 所以直接按几何形状光栅化：青松绿满底 + 米白「H」，H 落在 maskable 安全区
// （中心 80% 圆）内。只用 node 内置的 zlib 编码 PNG。
//
// 用法：node scripts/generate-icons.mjs（改了 icon.svg 的形状要同步改这里）
import { mkdirSync, writeFileSync } from 'node:fs'
import { deflateSync } from 'node:zlib'

const BG = [0x4a, 0x77, 0x29] // --palette-pine
const FG = [0xf5, 0xf5, 0xdc] // --palette-cream
// 以 512 为基准的矩形：[x, y, w, h]，与 icon.svg 一致
const RECTS = [
  [156, 140, 56, 232],
  [300, 140, 56, 232],
  [212, 228, 88, 56],
]
const SAMPLES = 4

function coverage(px, py, scale) {
  let hit = 0
  for (let sy = 0; sy < SAMPLES; sy++) {
    for (let sx = 0; sx < SAMPLES; sx++) {
      const x = (px + (sx + 0.5) / SAMPLES) / scale
      const y = (py + (sy + 0.5) / SAMPLES) / scale
      if (RECTS.some(([rx, ry, rw, rh]) => x >= rx && x < rx + rw && y >= ry && y < ry + rh)) hit++
    }
  }
  return hit / (SAMPLES * SAMPLES)
}

const crcTable = Array.from({ length: 256 }, (_, n) => {
  let c = n
  for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1
  return c >>> 0
})
function crc32(buf) {
  let c = 0xffffffff
  for (const b of buf) c = crcTable[(c ^ b) & 0xff] ^ (c >>> 8)
  return (c ^ 0xffffffff) >>> 0
}
function chunk(type, data) {
  const len = Buffer.alloc(4)
  len.writeUInt32BE(data.length)
  const body = Buffer.concat([Buffer.from(type, 'ascii'), data])
  const crc = Buffer.alloc(4)
  crc.writeUInt32BE(crc32(body))
  return Buffer.concat([len, body, crc])
}

function png(size) {
  const scale = size / 512
  const raw = Buffer.alloc(size * (size * 3 + 1))
  let o = 0
  for (let y = 0; y < size; y++) {
    raw[o++] = 0 // filter: none
    for (let x = 0; x < size; x++) {
      const a = coverage(x, y, scale)
      for (let i = 0; i < 3; i++) raw[o++] = Math.round(BG[i] * (1 - a) + FG[i] * a)
    }
  }
  const ihdr = Buffer.alloc(13)
  ihdr.writeUInt32BE(size, 0)
  ihdr.writeUInt32BE(size, 4)
  ihdr[8] = 8 // bit depth
  ihdr[9] = 2 // RGB
  return Buffer.concat([
    Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]),
    chunk('IHDR', ihdr),
    chunk('IDAT', deflateSync(raw, { level: 9 })),
    chunk('IEND', Buffer.alloc(0)),
  ])
}

const outDir = new URL('../public/icons/', import.meta.url)
mkdirSync(outDir, { recursive: true })
for (const size of [180, 192, 512]) {
  writeFileSync(new URL(`icon-${size}.png`, outDir), png(size))
}
