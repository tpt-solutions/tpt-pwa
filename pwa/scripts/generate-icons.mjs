// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.
// Generates every raster icon in the repo from one drawing routine, using a
// minimal pure-Node PNG encoder (no image dependencies):
//   pwa/public/icons/           -> PWA manifest icons (+ maskable variant)
//   cortex-shell/src-tauri/icons/ -> Tauri bundle icons (PNG set + icon.ico)
import { deflateSync } from 'node:zlib'
import { mkdirSync, writeFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

const repoRoot = join(fileURLToPath(new URL('.', import.meta.url)), '..', '..')
const pwaIconsDir = join(repoRoot, 'pwa', 'public', 'icons')
const tauriIconsDir = join(repoRoot, 'cortex-shell', 'src-tauri', 'icons')

// ---- Minimal PNG encoder -------------------------------------------------

const CRC_TABLE = (() => {
  const table = new Uint32Array(256)
  for (let n = 0; n < 256; n++) {
    let c = n
    for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1
    table[n] = c >>> 0
  }
  return table
})()

function crc32(bytes) {
  let c = 0xffffffff
  for (const byte of bytes) c = CRC_TABLE[(c ^ byte) & 0xff] ^ (c >>> 8)
  return (c ^ 0xffffffff) >>> 0
}

function chunk(type, data) {
  const length = Buffer.alloc(4)
  length.writeUInt32BE(data.length)
  const body = Buffer.concat([Buffer.from(type, 'ascii'), data])
  const crc = Buffer.alloc(4)
  crc.writeUInt32BE(crc32(body))
  return Buffer.concat([length, body, crc])
}

function encodePng(size, rgba) {
  const signature = Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a])
  const header = Buffer.alloc(13)
  header.writeUInt32BE(size, 0)
  header.writeUInt32BE(size, 4)
  header[8] = 8 // bit depth
  header[9] = 6 // color type: RGBA
  // scanlines: one filter byte (0) per row
  const stride = size * 4
  const raw = Buffer.alloc((stride + 1) * size)
  for (let y = 0; y < size; y++) {
    raw[y * (stride + 1)] = 0
    Buffer.from(rgba.buffer, rgba.byteOffset + y * stride, stride).copy(raw, y * (stride + 1) + 1)
  }
  return Buffer.concat([signature, chunk('IHDR', header), chunk('IDAT', deflateSync(raw)), chunk('IEND', Buffer.alloc(0))])
}

// ---- Icon drawing ---------------------------------------------------------

function mix(a, b, t) {
  return a + (b - a) * t
}

/**
 * Rounded-square tile on the brand background with a white "T" letterform,
 * vertically-gradient tile fill, 2x2 supersampled edges. `inset` grows for
 * maskable rendering so the mark stays inside the 80% safe circle.
 */
function drawIcon(size, { inset = 0.08, background = [15, 17, 21] } = {}) {
  const rgba = new Uint8Array(size * size * 4)
  const radius = size * 0.2
  const tile = size * inset
  const tileSpan = size - 2 * tile
  const sample = (x, y) => {
    // tile coverage (rounded rect)
    const lx = Math.min(Math.max(x, tile), tile + tileSpan) - x
    const cornerX = tile + radius
    const cornerY = tile + radius
    let inside = 0
    const inX = x >= tile && x <= tile + tileSpan
    const inY = y >= tile && y <= tile + tileSpan
    if (inX && inY) {
      const cx = Math.min(Math.max(x, cornerX), tile + tileSpan - radius)
      const cy = Math.min(Math.max(y, cornerY), tile + tileSpan - radius)
      const dx = x - cx
      const dy = y - cy
      inside = dx * dx + dy * dy <= radius * radius ? 1 : 0
    }
    // "T" letterform in unit coords relative to the tile
    const u = (x - tile) / tileSpan
    const v = (y - tile) / tileSpan
    const barY = v >= 0.18 && v <= 0.32
    const stemX = u >= 0.41 && u <= 0.59
    const stemY = v >= 0.32 && v <= 0.76
    const glyph = (barY && u >= 0.18 && u <= 0.82) || (stemX && stemY) ? 1 : 0
    if (glyph) return [245, 248, 255, 255]
    if (inside) {
      const t = (y - tile) / tileSpan
      // gradient #7c5cff -> #4f8cff
      return [
        Math.round(mix(0x7c, 0x4f, t)),
        Math.round(mix(0x5c, 0x8c, t)),
        Math.round(mix(0xff, 0xff, t)),
        255,
      ]
    }
    return [...background, 255]
  }
  const offsets = [0.25, 0.75]
  for (let y = 0; y < size; y++) {
    for (let x = 0; x < size; x++) {
      let r = 0
      let g = 0
      let b = 0
      for (const dy of offsets) {
        for (const dx of offsets) {
          const [sr, sg, sb] = sample(x + dx, y + dy)
          r += sr
          g += sg
          b += sb
        }
      }
      const index = (y * size + x) * 4
      rgba[index] = Math.round(r / 4)
      rgba[index + 1] = Math.round(g / 4)
      rgba[index + 2] = Math.round(b / 4)
      rgba[index + 3] = 255
    }
  }
  return rgba
}

function writePng(path, size, rgba) {
  writeFileSync(path, encodePng(size, rgba))
  console.log(`generate-icons: ${path} (${size}x${size})`)
}

/** ICO container wrapping a single PNG image (valid for Windows Vista+). */
function writeIco(path, size, png) {
  const header = Buffer.alloc(6)
  header.writeUInt16LE(0, 0)
  header.writeUInt16LE(1, 2) // icon
  header.writeUInt16LE(1, 4) // 1 image
  const entry = Buffer.alloc(16)
  entry[0] = size === 256 ? 0 : size
  entry[1] = size === 256 ? 0 : size
  entry.writeUInt16LE(1, 4) // planes
  entry.writeUInt16LE(32, 6) // bpp
  entry.writeUInt32LE(png.length, 8)
  entry.writeUInt32LE(22, 12) // data offset (6 + 16)
  writeFileSync(path, Buffer.concat([header, entry, png]))
  console.log(`generate-icons: ${path} (${size}x${size} png-in-ico)`)
}

mkdirSync(pwaIconsDir, { recursive: true })
writePng(join(pwaIconsDir, 'icon-192.png'), 192, drawIcon(192))
writePng(join(pwaIconsDir, 'icon-512.png'), 512, drawIcon(512))
writePng(join(pwaIconsDir, 'maskable-512.png'), 512, drawIcon(512, { inset: 0.2 }))

mkdirSync(tauriIconsDir, { recursive: true })
writePng(join(tauriIconsDir, '32x32.png'), 32, drawIcon(32, { inset: 0.1 }))
writePng(join(tauriIconsDir, '128x128.png'), 128, drawIcon(128))
writePng(join(tauriIconsDir, '128x128@2x.png'), 256, drawIcon(256))
writeIco(join(tauriIconsDir, 'icon.ico'), 256, encodePng(256, drawIcon(256)))
