#!/usr/bin/env node
"use strict";

const crypto = require("node:crypto");
const fs = require("node:fs/promises");
const os = require("node:os");
const path = require("node:path");
const sharp = require("sharp");

const repoRoot = path.resolve(__dirname, "..");
const iconDir = path.join(repoRoot, "beepersource/assets/brand-icons/icons");
const manifestPath = path.join(repoRoot, "beepersource/assets/brand-icons/manifest.json");
const proofDir = process.env.BEEPER_MATRIX_PROXY_ICON_PROOF_DIR ||
  path.join(os.tmpdir(), "beeper-matrix-proxy", "creator-platform-icons-2026-05-24");

const headers = {
  "user-agent":
    "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_0) AppleWebKit/537.36 OpenClawIconAudit/1.0",
  accept: "text/html,application/xhtml+xml,application/xml;q=0.9,image/*,*/*;q=0.8",
};

const platforms = [
  {
    key: "creatorhero",
    label: "CreatorHero",
    color: "#F2F0EC",
    urls: ["https://www.creatorhero.com/"],
    fallbacks: ["https://www.creatorhero.com/assets/images/preview.jpg"],
  },
  {
    key: "onlyfans",
    label: "OnlyFans",
    color: "#00AFF0",
    simpleIcon: "onlyfans",
    urls: ["https://onlyfans.com/"],
  },
  { key: "fansly", label: "Fansly", color: "#2799F6", urls: ["https://fansly.com/"] },
  {
    key: "fanvue",
    label: "Fanvue",
    color: "#B7FF2A",
    urls: ["https://www.fanvue.com/", "https://try.fanvue.com/fanvue"],
  },
  { key: "mymfans", label: "MYM.fans", color: "#111111", urls: ["https://mym.fans/"] },
  {
    key: "fancentro",
    label: "FanCentro",
    color: "#7D3CFF",
    urls: ["https://www.fancentro.com/"],
    fallbacks: ["https://www.fancentro.com/apple-touch-icon.png"],
  },
  { key: "slushy", label: "Slushy", color: "#FF4F8B", urls: ["https://www.slushy.com/"] },
  { key: "uncove", label: "Uncove", color: "#191919", urls: ["https://uncove.com/"] },
  {
    key: "subscribestar",
    label: "SubscribeStar",
    color: "#F28C28",
    urls: ["https://www.subscribestar.com/"],
  },
  { key: "maloum", label: "Maloum", color: "#E83D6F", urls: ["https://www.maloum.com/en"] },
  {
    key: "dfans",
    label: "dFans",
    color: "#6738FF",
    urls: ["https://dfans.co/", "https://dfans.xyz/"],
  },
  { key: "manyvids", label: "ManyVids", color: "#EF2995", urls: ["https://www.manyvids.com/"] },
  { key: "unlockd", label: "Unlockd", color: "#1A6DFF", urls: ["https://unlockd.com/"] },
  { key: "sospoilt", label: "SoSpoilt", color: "#111111", urls: ["https://www.sospoilt.com/"] },
  { key: "xpanded", label: "Xpanded", color: "#C32026", urls: ["https://xpanded.com/"] },
  {
    key: "revealme",
    label: "RevealMe",
    color: "#FF335F",
    urls: ["https://www.reveal.me/", "https://revealme.com/"],
    fallbacks: ["https://www.reveal.me/apple-touch-icon.png"],
  },
  { key: "admireme", label: "AdmireMe", color: "#E63E81", urls: ["https://admireme.vip/"] },
  {
    key: "camsoda",
    label: "CamSoda",
    color: "#FF6F00",
    urls: ["https://www.camsoda.com/"],
    fallbacks: ["https://www.camsoda.com/apple-touch-icon.png"],
  },
  { key: "stacked", label: "Stacked", color: "#111111", urls: ["https://stacked.com/"] },
  {
    key: "fanview",
    label: "Fanview",
    color: "#0098C7",
    urls: ["https://www.fanview.tech/"],
    fallbacks: ["https://www.fanview.tech/wp-content/uploads/2021/07/cropped-logo-fanview-icon-32x32.png"],
  },
];

function parseHexColor(hex) {
  const normalized = hex.replace("#", "");
  const full = normalized.length === 3
    ? normalized.split("").map((part) => part + part).join("")
    : normalized.padEnd(6, "0").slice(0, 6);
  return {
    r: Number.parseInt(full.slice(0, 2), 16),
    g: Number.parseInt(full.slice(2, 4), 16),
    b: Number.parseInt(full.slice(4, 6), 16),
  };
}

function mixChannel(value, target, amount) {
  return Math.round(value + (target - value) * amount);
}

function colorToHex(color) {
  return `#${[color.r, color.g, color.b].map((value) => value.toString(16).padStart(2, "0")).join("")}`;
}

function colorMix(hex, target, amount) {
  const color = parseHexColor(hex);
  return colorToHex({
    r: mixChannel(color.r, target, amount),
    g: mixChannel(color.g, target, amount),
    b: mixChannel(color.b, target, amount),
  });
}

function appIconBackground(platform) {
  const top = colorMix(platform.color, 255, 0.18);
  const bottom = colorMix(platform.color, 0, 0.08);
  return `
    <svg xmlns="http://www.w3.org/2000/svg" width="256" height="256" viewBox="0 0 256 256">
      <defs>
        <linearGradient id="g" x1="42" y1="18" x2="214" y2="236">
          <stop offset="0" stop-color="${top}"/>
          <stop offset="1" stop-color="${bottom}"/>
        </linearGradient>
      </defs>
      <rect width="256" height="256" rx="56" fill="url(#g)"/>
    </svg>`;
}

async function composeAppIcon(platform, logoBytes) {
  const logoSize = platform.key === "fanvue" || platform.key === "xpanded" ? 174 : 188;
  return roundIcon(await sharp(Buffer.from(appIconBackground(platform)))
    .composite([
      {
        input: await sharp(logoBytes, { animated: false, density: 512 })
          .resize(logoSize, logoSize, { fit: "inside", withoutEnlargement: false })
          .png()
          .toBuffer(),
        gravity: "center",
      },
    ])
    .png()
    .toBuffer());
}

function roundedMask() {
  return Buffer.from(`
    <svg xmlns="http://www.w3.org/2000/svg" width="256" height="256" viewBox="0 0 256 256">
      <rect width="256" height="256" rx="56" fill="#fff"/>
    </svg>`);
}

async function roundIcon(bytes) {
  return sharp(bytes)
    .ensureAlpha()
    .composite([{ input: roundedMask(), blend: "dest-in" }])
    .png()
    .toBuffer();
}

function isUsableAssetURL(raw) {
  return /\.(png|jpe?g|webp|svg|ico)([?#].*)?$/i.test(raw);
}

function sourcePriority(kind) {
  switch (kind) {
    case "apple-touch-icon":
      return 6;
    case "manifest-icon":
      return 5;
    case "common-path":
      return 4;
    case "favicon":
      return 3;
    case "explicit-fallback":
      return 2;
    case "simple-icons":
      return 1;
    default:
      return 0;
  }
}

function normalizeURL(raw, baseURL) {
  if (!raw || /^data:/i.test(raw)) return "";
  if (!isUsableAssetURL(raw) && !/manifest/i.test(raw)) return "";
  try {
    return new URL(raw, baseURL).href;
  } catch {
    return "";
  }
}

function decodeHTMLAttribute(value) {
  return String(value || "")
    .replace(/&amp;/g, "&")
    .replace(/&quot;/g, '"')
    .replace(/&#39;/g, "'")
    .replace(/&lt;/g, "<")
    .replace(/&gt;/g, ">");
}

function parseAttributes(tag) {
  const attrs = {};
  const attrPattern = /([a-zA-Z_:.-]+)\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'=<>`]+))/g;
  let match;
  while ((match = attrPattern.exec(tag))) {
    attrs[match[1].toLowerCase()] = decodeHTMLAttribute(match[2] ?? match[3] ?? match[4] ?? "");
  }
  return attrs;
}

function eachHeadAsset(html, callback) {
  const tagPattern = /<(link|meta)\b[^>]*>/gi;
  let match;
  while ((match = tagPattern.exec(html))) {
    callback(parseAttributes(match[0]));
  }
}

async function fetchBytes(url) {
  const response = await fetch(url, { redirect: "follow", headers });
  if (!response.ok) {
    throw new Error(`${response.status} ${response.statusText}`);
  }
  return {
    url: response.url,
    contentType: response.headers.get("content-type") || "",
    bytes: Buffer.from(await response.arrayBuffer()),
  };
}

async function fetchText(url) {
  const response = await fetch(url, { redirect: "follow", headers });
  if (!response.ok) throw new Error(`${response.status} ${response.statusText}`);
  return {
    url: response.url,
    text: await response.text(),
  };
}

async function collectCandidates(platform) {
  const candidates = [];
  if (platform.simpleIcon) {
    candidates.push({
      url: `https://cdn.jsdelivr.net/npm/simple-icons@16/icons/${platform.simpleIcon}.svg`,
      kind: "simple-icons",
      // Simple Icons is useful as a public fallback, but the product UI should
      // prefer the icon a website exposes for iOS/PWA home-screen installs.
      score: 420,
    });
  }

  for (const url of platform.urls || []) {
    let page;
    try {
      page = await fetchText(url);
    } catch {
      continue;
    }
    eachHeadAsset(page.text, (attrs) => {
      const rel = (attrs.rel || "").toLowerCase();
      const property = (attrs.property || attrs.name || "").toLowerCase();
      const href = attrs.href || attrs.content || "";
      const assetURL = normalizeURL(href, page.url);
      if (!assetURL) return;

      const sizes = (attrs.sizes || "").split(/\s+/).at(-1) || "";
      const width = Number(sizes.split("x")[0]) || 0;

      if (rel.includes("manifest")) {
        candidates.push({ url: assetURL, kind: "manifest", score: 650 });
      } else if (rel.includes("apple-touch-icon")) {
        candidates.push({ url: assetURL, kind: "apple-touch-icon", score: 1200 + Math.min(width, 512) });
      } else if (rel.includes("icon")) {
        candidates.push({ url: assetURL, kind: "favicon", score: 520 + Math.min(width, 256) });
      } else if (property.includes("image")) {
        candidates.push({ url: assetURL, kind: "social-preview", score: 120 });
      }
    });

    for (const suffix of ["/apple-touch-icon.png", "/favicon-192x192.png", "/favicon.png"]) {
      candidates.push({ url: new URL(suffix, page.url).href, kind: "common-path", score: 520 });
    }
  }

  for (const url of platform.fallbacks || []) {
    candidates.push({ url, kind: "explicit-fallback", score: 760 });
  }

  const expanded = [];
  const seen = new Set();
  for (const candidate of candidates) {
    if (seen.has(candidate.url)) continue;
    seen.add(candidate.url);
    if (candidate.kind === "manifest") {
      try {
        const manifest = JSON.parse((await fetchText(candidate.url)).text);
        for (const icon of manifest.icons || []) {
          const iconURL = normalizeURL(icon.src, candidate.url);
          if (!iconURL) continue;
          const size = String(icon.sizes || "").split(/\s+/).at(-1) || "";
          const width = Number(size.split("x")[0]) || 0;
          expanded.push({
            url: iconURL,
            kind: "manifest-icon",
            score: 1100 + Math.min(width, 512),
          });
        }
      } catch {
        // Ignore malformed or blocked manifests.
      }
    } else {
      expanded.push(candidate);
    }
  }
  return expanded;
}

function whiteSimpleIcon(svg) {
  if (!svg.includes("<svg")) return svg;
  return svg.replace("<svg ", '<svg fill="#fff" ');
}

async function iconFromCandidate(platform, candidate) {
  const fetched = await fetchBytes(candidate.url);
  let input = fetched.bytes;
  if (candidate.kind === "simple-icons") {
    input = Buffer.from(whiteSimpleIcon(input.toString("utf8")));
  }
  const image = sharp(input, { animated: false, density: 512 });
  const meta = await image.metadata();
  if (!meta.width || !meta.height) throw new Error("no dimensions");
  if (meta.width < 24 || meta.height < 24) throw new Error("too small");
  const ratio = Math.max(meta.width, meta.height) / Math.max(1, Math.min(meta.width, meta.height));
  if (candidate.kind === "social-preview" && ratio > 1.4) throw new Error("social preview is not icon-like");

  const sizeBonus = Math.min(Math.max(meta.width, meta.height), 512);
  const ratioBonus = ratio < 1.18 ? 220 : ratio < 1.5 ? 80 : -120;
  const score = candidate.score + sizeBonus + ratioBonus;

  const sourceLooksLikeAppIcon =
    candidate.kind !== "simple-icons" &&
    candidate.kind !== "social-preview" &&
    meta.width >= 128 &&
    meta.height >= 128 &&
    ratio < 1.18;

  if (sourceLooksLikeAppIcon) {
    return {
      score,
      bytes: await roundIcon(await sharp(input, { animated: false, density: 512 })
        .resize(256, 256, { fit: "contain", background: { r: 0, g: 0, b: 0, alpha: 0 } })
        .png()
        .toBuffer()),
      source: fetched.url,
      kind: candidate.kind,
      source_priority: sourcePriority(candidate.kind),
      meta: { width: meta.width, height: meta.height, format: meta.format },
    };
  }

  const transparentLogo = await sharp({
    create: {
      width: 256,
      height: 256,
      channels: 4,
      background: { r: 0, g: 0, b: 0, alpha: 0 },
    },
  })
    .composite([
      {
        input: await sharp(input, { animated: false, density: 512 })
          .resize(214, 214, { fit: "inside", withoutEnlargement: false })
          .png()
          .toBuffer(),
        gravity: "center",
      },
    ])
    .png()
    .toBuffer();

  return {
    score,
    bytes: await composeAppIcon(platform, transparentLogo),
    source: fetched.url,
    kind: candidate.kind,
    source_priority: sourcePriority(candidate.kind),
    meta: { width: meta.width, height: meta.height, format: meta.format },
  };
}

function initials(label) {
  const words = label.replace(/\.[a-z]+$/i, "").split(/[^a-z0-9]+/i).filter(Boolean);
  if (words.length > 1) return (words[0][0] + words[1][0]).toUpperCase();
  return label.replace(/[^a-z0-9]/gi, "").slice(0, 2).toUpperCase() || "?";
}

async function fallbackIcon(platform) {
  const text = initials(platform.label);
  const svg = `
    <svg xmlns="http://www.w3.org/2000/svg" width="256" height="256" viewBox="0 0 256 256">
      <rect width="256" height="256" rx="56" fill="${platform.color}"/>
      <text x="128" y="151" text-anchor="middle" font-family="Inter, Arial, Helvetica, sans-serif" font-size="${text.length > 1 ? 78 : 96}" font-weight="800" fill="#fff">${text}</text>
    </svg>`;
  return {
    score: 0,
    bytes: await composeAppIcon(platform, await sharp(Buffer.from(svg)).png().toBuffer()),
    source: "local generated fallback after official icon lookup failed",
    kind: "local-fallback",
    source_priority: sourcePriority("local-fallback"),
    fallback: true,
  };
}

async function bestIcon(platform) {
  const candidates = await collectCandidates(platform);
  const results = [];
  for (const candidate of candidates) {
    try {
      results.push(await iconFromCandidate(platform, candidate));
    } catch {
      // Keep trying lower-ranked sources.
    }
  }
  results.sort((a, b) => b.score - a.score);
  return results[0] || fallbackIcon(platform);
}

async function updateManifest(generated) {
  const manifest = JSON.parse(await fs.readFile(manifestPath, "utf8"));
  const byKey = new Map(manifest.icons.map((entry) => [entry.key, entry]));
  for (const entry of generated) {
    byKey.set(entry.key, entry);
  }
  manifest.icons = [...manifest.icons.filter((entry) => !generated.some((next) => next.key === entry.key)), ...generated];
  manifest.version = "brand-icons-v5";
  manifest.generated_at = "2026-05-25";
  await fs.writeFile(manifestPath, `${JSON.stringify(manifest, null, 2)}\n`);
}

function gallerySVG(entries) {
  const cell = 150;
  const labelH = 44;
  const cols = 6;
  const rows = Math.ceil(entries.length / cols);
  const width = cols * cell;
  const height = rows * (cell + labelH) + 32;
  const items = entries
    .map((entry, index) => {
      const x = (index % cols) * cell + 26;
      const y = Math.floor(index / cols) * (cell + labelH) + 24;
      const label = entry.label.replace(/&/g, "&amp;").replace(/</g, "&lt;");
      return `
        <image href="data:image/png;base64,${entry.pngBase64}" x="${x}" y="${y}" width="98" height="98"/>
        <text x="${x + 49}" y="${y + 125}" text-anchor="middle" font-family="Inter, Arial, Helvetica, sans-serif" font-size="14" font-weight="650" fill="#202124">${label}</text>`;
    })
    .join("\n");
  return `
    <svg xmlns="http://www.w3.org/2000/svg" width="${width}" height="${height}" viewBox="0 0 ${width} ${height}">
      <rect width="100%" height="100%" fill="#f7f8fa"/>
      ${items}
    </svg>`;
}

async function main() {
  await fs.mkdir(iconDir, { recursive: true });
  await fs.mkdir(proofDir, { recursive: true });
  const generated = [];
  const proof = [];
  for (const platform of platforms) {
    const icon = await bestIcon(platform);
    const fileName = `${platform.key}.png`;
    const outPath = path.join(iconDir, fileName);
    await fs.writeFile(outPath, icon.bytes);
    const sha256 = crypto.createHash("sha256").update(icon.bytes).digest("hex");
    const fallback = Boolean(icon.fallback || icon.kind === "local-fallback");
    generated.push({
      key: platform.key,
      label: platform.label,
      brand_color: platform.color,
      png: `icons/${fileName}`,
      sha256,
      source: icon.source,
      source_kind: icon.kind,
      source_priority: icon.source_priority || sourcePriority(icon.kind),
      license_note: fallback
        ? "Local fallback glyph; replace with an approved official asset when available"
        : "Prefer first-party apple-touch-icon or web-app manifest icon; Simple Icons only as fallback. Verify brand terms before public redistribution",
      fallback,
    });
    proof.push({ ...platform, source: icon.source, kind: icon.kind, source_priority: icon.source_priority || sourcePriority(icon.kind), fallback, sha256 });
    console.log(`${platform.key}: ${icon.kind} ${icon.source}`);
  }
  await updateManifest(generated);

  const manifest = JSON.parse(await fs.readFile(manifestPath, "utf8"));
  const galleryEntries = manifest.icons.map((entry) => ({
    label: entry.label,
    brand_color: entry.brand_color,
    pngBase64: require("node:fs").readFileSync(path.join(repoRoot, "beepersource/assets/brand-icons", entry.png)).toString("base64"),
  }));
  await fs.writeFile(path.join(proofDir, "creator-platform-icon-sources.json"), `${JSON.stringify(proof, null, 2)}\n`);
  await sharp(Buffer.from(gallerySVG(galleryEntries))).png().toFile(path.join(proofDir, "all-brand-icons-gallery.png"));
  await sharp(Buffer.from(gallerySVG(generated.map((entry) => ({
    label: entry.label,
    brand_color: entry.brand_color,
    pngBase64: require("node:fs").readFileSync(path.join(repoRoot, "beepersource/assets/brand-icons", entry.png)).toString("base64"),
  }))))).png().toFile(path.join(proofDir, "creator-platform-icons-gallery.png"));
}

main().catch((error) => {
  console.error(error);
  process.exit(1);
});
