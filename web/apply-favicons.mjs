import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import playwright from './node_modules/.pnpm/playwright-core@1.59.1/node_modules/playwright-core/index.js';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const imagesDir = path.join(__dirname, 'public/images');
const publicDir = path.join(__dirname, 'public');

const RING_PATH = 'M 27 10 C 41 8 54 18 54 32 C 54 44.5 44 54 32 54 C 19.5 54 10 44 10 32 C 10 24 14.5 17 21 13';
const HORN_PATH = 'M 27 10 C 23 10 19 6.5 17 4 C 18 8.5 16 12 13 14 C 17.5 14 21.5 12.5 24 10.5 Z';

export function getSvgZ(mode = 'light') {
  const isDark = mode === 'dark';
  const ink = isDark ? '#F8FAFC' : '#0F172A';
  const signal = isDark ? '#FF2A54' : '#E11D48';
  const voidBg = isDark ? '#090D16' : '#FFFFFF';
  const voidBorder = isDark ? 'rgba(255,255,255,0.25)' : '#CBD5E1';

  return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64" fill="none">
  <path d="${RING_PATH}" stroke="${ink}" stroke-width="4.5" stroke-linecap="round"/>
  <path d="${HORN_PATH}" fill="${signal}"/>
  <circle cx="21" cy="13" r="2.25" fill="${ink}"/>
  <circle cx="32" cy="32" r="8.5" fill="${voidBg}" stroke="${voidBorder}" stroke-width="1.5"/>
  <circle cx="32" cy="32" r="5" fill="${signal}"/>
  <circle cx="33.8" cy="30.2" r="1.3" fill="#FFFFFF"/>
</svg>`;
}

export function getSvgZAppIcon() {
  return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64" fill="none">
  <rect width="64" height="64" fill="#090D16"/>
  <path d="${RING_PATH}" stroke="#FFFFFF" stroke-width="4.5" stroke-linecap="round"/>
  <path d="${HORN_PATH}" fill="#FF2A54"/>
  <circle cx="21" cy="13" r="2.25" fill="#FFFFFF"/>
  <circle cx="32" cy="32" r="8.5" fill="#090D16" stroke="rgba(255,255,255,0.25)" stroke-width="1.5"/>
  <circle cx="32" cy="32" r="5" fill="#FF2A54"/>
  <circle cx="33.8" cy="30.2" r="1.3" fill="#FFFFFF"/>
</svg>`;
}

// Packs multiple PNG buffers into standard ICO format
function createIco(pngBuffers) {
  const count = pngBuffers.length;
  const headerSize = 6 + count * 16;
  let offset = headerSize;
  const entries = [];
  
  for (const item of pngBuffers) {
    const entry = Buffer.alloc(16);
    entry.writeUInt8(item.width >= 256 ? 0 : item.width, 0);
    entry.writeUInt8(item.height >= 256 ? 0 : item.height, 1);
    entry.writeUInt8(0, 2); // color count
    entry.writeUInt8(0, 3); // reserved
    entry.writeUInt16LE(1, 4); // color planes
    entry.writeUInt16LE(32, 6); // bits per pixel
    entry.writeUInt32LE(item.buffer.length, 8); // image size
    entry.writeUInt32LE(offset, 12); // image offset
    entries.push(entry);
    offset += item.buffer.length;
  }
  
  const header = Buffer.alloc(6);
  header.writeUInt16LE(0, 0); // reserved
  header.writeUInt16LE(1, 2); // ICO type
  header.writeUInt16LE(count, 4); // image count
  
  return Buffer.concat([header, ...entries, ...pngBuffers.map(p => p.buffer)]);
}

export async function applyFavicons() {
  console.log('Generating and applying Zhulong industrial Z-mark favicon set...');
  
  const browser = await playwright.chromium.launch({
    headless: true,
    args: ['--no-sandbox', '--disable-setuid-sandbox']
  });
  const page = await browser.newPage();

  const svgLight = getSvgZ('light');
  const svgDark = getSvgZ('dark');
  const svgApp = getSvgZAppIcon();

  // 1. Write SVGs
  fs.writeFileSync(path.join(imagesDir, 'favicon.svg'), svgLight.trim() + '\n', 'utf8');
  fs.writeFileSync(path.join(imagesDir, 'favicon_light.svg'), svgDark.trim() + '\n', 'utf8');
  console.log('✔ Wrote favicon.svg and favicon_light.svg');

  // Helper for rendering PNG
  async function renderPng(svg, size, omitBg = true) {
    await page.setViewportSize({ width: size, height: size });
    await page.setContent(`<!DOCTYPE html><html><body style="margin:0;padding:0;overflow:hidden;"><div style="width:${size}px;height:${size}px;display:flex;">${svg}</div></body></html>`);
    return await page.screenshot({ omitBackground: omitBg });
  }

  // 2. Render standard 48x48 PNGs
  const png48Light = await renderPng(svgLight, 48);
  const png48Dark = await renderPng(svgDark, 48);
  fs.writeFileSync(path.join(imagesDir, 'favicon.png'), png48Light);
  fs.writeFileSync(path.join(imagesDir, 'favicon_light.png'), png48Dark);
  console.log('✔ Wrote favicon.png and favicon_light.png (48x48)');

  // 3. Render 16x16 and 32x32 for tab / ICO
  const png16 = await renderPng(svgApp, 16, false);
  const png32 = await renderPng(svgApp, 32, false);
  const png48 = await renderPng(svgApp, 48, false);
  fs.writeFileSync(path.join(imagesDir, 'favicon-16x16.png'), png16);
  fs.writeFileSync(path.join(imagesDir, 'favicon-32x32.png'), png32);
  console.log('✔ Wrote favicon-16x16.png and favicon-32x32.png');

  // 4. Render Apple Touch Icon (180x180); iOS applies its own icon mask.
  const png180 = await renderPng(svgApp, 180, false);
  fs.writeFileSync(path.join(imagesDir, 'apple-touch-icon.png'), png180);
  fs.writeFileSync(path.join(publicDir, 'apple-touch-icon.png'), png180);
  console.log('✔ Wrote apple-touch-icon.png (180x180)');

  // 5. Generate favicon.ico (16, 32, 48)
  const icoBuffer = createIco([
    { width: 16, height: 16, buffer: png16 },
    { width: 32, height: 32, buffer: png32 },
    { width: 48, height: 48, buffer: png48 },
  ]);
  fs.writeFileSync(path.join(publicDir, 'favicon.ico'), icoBuffer);
  fs.writeFileSync(path.join(imagesDir, 'favicon.ico'), icoBuffer);
  console.log('✔ Wrote favicon.ico to public/ and public/images/');

  await browser.close();
  console.log('All favicon assets applied successfully!');
}

applyFavicons().catch(console.error);
