import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import playwright from './node_modules/.pnpm/playwright-core@1.59.1/node_modules/playwright-core/index.js';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const imagesDir = path.join(__dirname, 'public/images');
const publicDir = path.join(__dirname, 'public');

// Symmetrical Kinetic Monogram "Z" (Clean Modern Infra Aesthetic)
export function getSvgZ(mode = 'dark') {
  // mode: 'dark' for dark tab chrome, 'light' for light tab chrome
  const isLight = mode === 'light';
  // In light tab mode, slightly punchier crimson base to maximize contrast against white/gray
  const stop1 = isLight ? '#f43f5e' : '#ff4757';
  const stop2 = isLight ? '#ea580c' : '#ff793f';
  const stop3 = isLight ? '#e11d48' : '#ff3366';

  return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64" fill="none">
  <defs>
    <linearGradient id="zhulong-z-${mode}" x1="10" y1="8" x2="54" y2="56" gradientUnits="userSpaceOnUse">
      <stop offset="0%" stop-color="${stop1}"/>
      <stop offset="42%" stop-color="${stop2}"/>
      <stop offset="100%" stop-color="${stop3}"/>
    </linearGradient>
  </defs>
  <!-- 工业动力学 Z 标 (Zhulong Kinetic Monogram) -->
  <path d="M 14 8 H 48 C 51.3 8 54 10.7 54 14 V 18 C 54 19.8 53.2 21.5 51.8 22.6 L 27.2 38 H 48 C 51.3 38 54 40.7 54 44 V 50 C 54 53.3 51.3 56 48 56 H 16 C 12.7 56 10 53.3 10 50 V 46 C 10 44.2 10.8 42.5 12.2 41.4 L 36.8 26 H 16 C 12.7 26 10 23.3 10 20 V 14 C 10 10.7 12.7 8 16 8 Z" 
        fill="url(#zhulong-z-${mode})"/>
</svg>`;
}

// App / Apple Touch Icon version: with sleek obsidian background container
export function getSvgZAppIcon() {
  return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64" fill="none">
  <defs>
    <linearGradient id="z-app-bg" x1="4" y1="4" x2="60" y2="60" gradientUnits="userSpaceOnUse">
      <stop offset="0%" stop-color="#1c1622"/>
      <stop offset="100%" stop-color="#0a0c14"/>
    </linearGradient>
    <linearGradient id="z-app-grad" x1="14" y1="12" x2="50" y2="52" gradientUnits="userSpaceOnUse">
      <stop offset="0%" stop-color="#ff4757"/>
      <stop offset="40%" stop-color="#ff793f"/>
      <stop offset="100%" stop-color="#ff3366"/>
    </linearGradient>
  </defs>
  <rect width="64" height="64" rx="14" fill="url(#z-app-bg)" stroke="#2d354b" stroke-width="1.8"/>
  <path d="M 16 12 H 46 C 48.8 12 51 14.2 51 17 V 20.2 C 51 21.8 50.3 23.2 49.1 24.2 L 28.5 37 H 46 C 48.8 37 51 39.2 51 42 V 47 C 51 49.8 48.8 52 46 52 H 18 C 15.2 52 13 49.8 13 47 V 43.8 C 13 42.2 13.7 40.8 14.9 39.8 L 35.5 27 H 18 C 15.2 27 13 24.8 13 22 V 17 C 13 14.2 15.2 12 18 12 Z" 
        fill="url(#z-app-grad)"/>
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
  console.log('Generating and applying Zhulong Z Monogram Favicon set...');
  
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
  const png16 = await renderPng(svgDark, 16);
  const png32 = await renderPng(svgDark, 32);
  const png48 = await renderPng(svgDark, 48);
  fs.writeFileSync(path.join(imagesDir, 'favicon-16x16.png'), png16);
  fs.writeFileSync(path.join(imagesDir, 'favicon-32x32.png'), png32);
  console.log('✔ Wrote favicon-16x16.png and favicon-32x32.png');

  // 4. Render Apple Touch Icon (180x180) with elegant squircle base
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
