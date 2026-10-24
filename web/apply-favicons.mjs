import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import playwright from './node_modules/.pnpm/playwright-core@1.59.1/node_modules/playwright-core/index.js';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const imagesDir = path.join(__dirname, 'public/images');
const publicDir = path.join(__dirname, 'public');

const Z_PATH = 'M 14 14 H 50 L 14 50 H 50';
const NODE_PATH = 'M 28 28 H 36 V 36 H 28 Z';

export function getSvgZ(mode = 'light') {
  const isDark = mode === 'dark';
  const ink = isDark ? '#F1E9DA' : '#1D2B2E';
  const signal = isDark ? '#FF7655' : '#C44536';

  return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64" fill="none">
  <path d="${Z_PATH}" stroke="${ink}" stroke-width="8" stroke-linecap="square" stroke-linejoin="miter"/>
  <path d="${NODE_PATH}" fill="${signal}"/>
</svg>`;
}

export function getSvgZAppIcon() {
  return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64" fill="none">
  <rect width="64" height="64" fill="#161B1D"/>
  <path d="${Z_PATH}" stroke="#F1E9DA" stroke-width="8" stroke-linecap="square" stroke-linejoin="miter"/>
  <path d="${NODE_PATH}" fill="#FF7655"/>
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
