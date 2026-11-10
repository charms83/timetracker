// Erzeugt icons/icon{16,32,48,128}.png aus einem SVG mit Playwright-Chromium.
const { chromium } = require("playwright");
const fs = require("fs"), path = require("path");
const svg = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 128 128" width="128" height="128">
<rect x="50" y="6" width="28" height="14" rx="4" fill="#0F752F"/>
<rect x="60" y="16" width="8" height="14" fill="#0F752F"/>
<rect x="98" y="26" width="14" height="10" rx="3" fill="#0F752F" transform="rotate(45 105 31)"/>
<circle cx="64" cy="76" r="46" fill="#fff" stroke="#0F752F" stroke-width="12"/>
<path d="M64 76 L64 46" stroke="#0F752F" stroke-width="9" stroke-linecap="round"/>
<path d="M64 76 L84 88" stroke="#0F752F" stroke-width="9" stroke-linecap="round"/>
<circle cx="64" cy="76" r="6" fill="#0F752F"/></svg>`;
(async () => {
  const b = await chromium.launch();
  const out = path.join(__dirname, "..", "icons");
  for (const s of [16, 32, 48, 128]) {
    const p = await b.newPage({ viewport: { width: s, height: s } });
    await p.setContent(`<body style="margin:0;background:transparent"><img src="data:image/svg+xml;base64,${Buffer.from(svg).toString("base64")}" width="${s}" height="${s}">`);
    fs.writeFileSync(path.join(out, `icon${s}.png`), await p.screenshot({ omitBackground: true }));
    await p.close();
  }
  await b.close();
})();
