#!/usr/bin/env node
"use strict";

const crypto = require("node:crypto");
const fs = require("node:fs/promises");
const fsSync = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const sharp = require("sharp");

const repoRoot = path.resolve(__dirname, "..");
const iconRoot = path.join(repoRoot, "beepersource/assets/brand-icons");
const proofDir = process.env.BEEPER_MATRIX_PROXY_AVATAR_PROOF_DIR ||
  path.join(os.tmpdir(), "beeper-matrix-proxy", "avatar-badge-customization-2026-05-24");

const platforms = [
  { key: "whatsapp", label: "WhatsApp", initials: "AN", gradient: ["#2DD36F", "#0A8F4A"] },
  { key: "signal", label: "Signal", initials: "FP", gradient: ["#4F8CFF", "#2648D8"] },
  { key: "telegram", label: "Telegram", initials: "HM", gradient: ["#30B9FF", "#0C6DD8"] },
  { key: "onlyfans", label: "OnlyFans", initials: "OF", gradient: ["#24C6FF", "#007CCF"] },
  { key: "creatorhero", label: "CreatorHero", initials: "CH", gradient: ["#F5F1EA", "#D9D3CA"] },
  { key: "fansly", label: "Fansly", initials: "FL", gradient: ["#55B9FF", "#1B59D8"] },
];

const layouts = [
  { key: "photo-br-edge", title: "Foto + Badge unten rechts", mode: "photo", position: "bottom-right", layout: "edge" },
  { key: "initials-br-edge", title: "Initialen + Badge unten rechts", mode: "initials", position: "bottom-right", layout: "edge" },
  { key: "photo-bl-edge", title: "Foto + Badge unten links", mode: "photo", position: "bottom-left", layout: "edge" },
  { key: "photo-br-circle-safe", title: "Foto + Badge kreis-sicher", mode: "photo", position: "bottom-right", layout: "circle-safe" },
];

function svgText(value) {
  return String(value).replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
}

function badgeRect(position, layout, sizePercent = 34, insetPercent = 0) {
  const avatarSize = 256;
  const size = Math.round((avatarSize * sizePercent) / 100);
  let x;
  let y;
  if (layout === "circle-safe") {
    const radius = avatarSize / 2;
    const badgeRadius = size / 2;
    const centerOffset = Math.round((radius - badgeRadius) * 0.707);
    x = Math.round(radius + centerOffset - badgeRadius);
    y = Math.round(radius + centerOffset - badgeRadius);
  } else {
    const inset = Math.round((avatarSize * insetPercent) / 100);
    x = avatarSize - size - inset;
    y = avatarSize - size - inset;
  }
  if (position.endsWith("left")) x = avatarSize - size - x;
  if (position.startsWith("top")) y = avatarSize - size - y;
  return { x, y, size, radius: Math.round(size / 4) };
}

function publicPhotoSVG(platform) {
  const [a, b] = platform.gradient;
  return `
    <svg xmlns="http://www.w3.org/2000/svg" width="256" height="256" viewBox="0 0 256 256">
      <defs>
        <linearGradient id="bg" x1="38" y1="18" x2="224" y2="242">
          <stop offset="0" stop-color="${a}"/>
          <stop offset="1" stop-color="${b}"/>
        </linearGradient>
        <radialGradient id="glow" cx="38%" cy="30%" r="62%">
          <stop offset="0" stop-color="#fff" stop-opacity=".42"/>
          <stop offset=".58" stop-color="#fff" stop-opacity=".08"/>
          <stop offset="1" stop-color="#000" stop-opacity=".16"/>
        </radialGradient>
      </defs>
      <rect width="256" height="256" rx="58" fill="url(#bg)"/>
      <circle cx="128" cy="92" r="42" fill="#fff" opacity=".92"/>
      <path d="M49 231c8-57 40-92 79-92s71 35 79 92" fill="#fff" opacity=".88"/>
      <rect width="256" height="256" rx="58" fill="url(#glow)"/>
    </svg>`;
}

function initialsSVG(platform) {
  const [a, b] = platform.gradient;
  return `
    <svg xmlns="http://www.w3.org/2000/svg" width="256" height="256" viewBox="0 0 256 256">
      <defs>
        <linearGradient id="bg" x1="35" y1="25" x2="226" y2="232">
          <stop offset="0" stop-color="${a}"/>
          <stop offset="1" stop-color="${b}"/>
        </linearGradient>
      </defs>
      <rect width="256" height="256" rx="58" fill="url(#bg)"/>
      <text x="128" y="151" text-anchor="middle" font-family="Poppins, Inter, Arial, Helvetica, sans-serif" font-size="84" font-weight="800" fill="#fff">${svgText(platform.initials)}</text>
    </svg>`;
}

async function composeAvatar(platform, layout) {
  const icon = await sharp(path.join(iconRoot, "icons", `${platform.key}.png`))
    .resize(79, 79, { fit: "contain", background: { r: 0, g: 0, b: 0, alpha: 0 } })
    .png()
    .toBuffer();
  const baseSVG = layout.mode === "initials" ? initialsSVG(platform) : publicPhotoSVG(platform);
  const { x, y, size, radius } = badgeRect(layout.position, layout.layout);
  const shadow = Buffer.from(`
    <svg xmlns="http://www.w3.org/2000/svg" width="256" height="256" viewBox="0 0 256 256">
      <rect x="${x - 1}" y="${y + 1}" width="${size + 2}" height="${size + 2}" rx="${radius}" fill="#000" opacity=".12"/>
    </svg>`);
  return sharp(Buffer.from(baseSVG))
    .composite([
      { input: shadow },
      { input: icon, left: x, top: y },
    ])
    .png()
    .toBuffer();
}

async function gallery() {
  await fs.mkdir(proofDir, { recursive: true });
  const cells = [];
  for (const layout of layouts) {
    for (const platform of platforms) {
      const png = await composeAvatar(platform, layout);
      cells.push({ platform, layout, png });
    }
  }
  const cellW = 146;
  const cellH = 186;
  const cols = platforms.length;
  const rows = layouts.length;
  const width = cols * cellW + 42;
  const height = rows * cellH + 74;
  const images = [];
  const labels = [];
  for (let index = 0; index < cells.length; index += 1) {
    const row = Math.floor(index / cols);
    const col = index % cols;
    const x = 22 + col * cellW;
    const y = 52 + row * cellH;
    images.push({
      input: await sharp(cells[index].png).resize(98, 98).png().toBuffer(),
      left: x + 16,
      top: y,
    });
    labels.push(`
      <text x="${x + 65}" y="${y + 125}" text-anchor="middle" font-family="Inter, Arial, sans-serif" font-size="13" font-weight="700" fill="#202124">${svgText(cells[index].platform.label)}</text>`);
    if (col === 0) {
      labels.push(`<text x="22" y="${y - 14}" font-family="Inter, Arial, sans-serif" font-size="15" font-weight="800" fill="#111827">${svgText(cells[index].layout.title)}</text>`);
    }
  }
  const svg = `
    <svg xmlns="http://www.w3.org/2000/svg" width="${width}" height="${height}" viewBox="0 0 ${width} ${height}">
      <rect width="100%" height="100%" fill="#f7f8fa"/>
      <text x="22" y="28" font-family="Inter, Arial, sans-serif" font-size="18" font-weight="850" fill="#111827">Public avatar badge proof gallery</text>
      ${labels.join("\n")}
    </svg>`;
  const output = await sharp(Buffer.from(svg)).composite(images).png().toBuffer();
  const outPath = path.join(proofDir, "avatar-badge-customization-gallery.png");
  await fs.writeFile(outPath, output);
  const proof = {
    generated_at: new Date().toISOString(),
    private_data: false,
    output: outPath,
    sha256: crypto.createHash("sha256").update(output).digest("hex"),
    platforms: platforms.map((platform) => platform.key),
    layouts: layouts.map(({ key, title, position, layout, mode }) => ({ key, title, position, layout, mode })),
    icon_sources: JSON.parse(await fs.readFile(path.join(iconRoot, "manifest.json"), "utf8")).icons
      .filter((entry) => platforms.some((platform) => platform.key === entry.key))
      .map(({ key, label, source, sha256, fallback }) => ({ key, label, source, sha256, fallback })),
  };
  await fs.writeFile(path.join(proofDir, "avatar-badge-customization-proof.json"), `${JSON.stringify(proof, null, 2)}\n`);
  console.log(outPath);
}

gallery().catch((error) => {
  console.error(error);
  process.exit(1);
});
