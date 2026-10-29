#!/usr/bin/env node
"use strict";

const crypto = require("node:crypto");
const fs = require("node:fs/promises");
const path = require("node:path");
const sharp = require("sharp");

const repoRoot = path.resolve(__dirname, "..");
const iconDir = path.join(repoRoot, "beepersource/assets/brand-icons/icons");
const manifestPath = path.join(repoRoot, "beepersource/assets/brand-icons/manifest.json");

const apps = [
  { key: "whatsapp", bundleId: "net.whatsapp.WhatsApp" },
  { key: "signal", bundleId: "org.whispersystems.signal" },
  { key: "telegram", bundleId: "ph.telegra.Telegraph" },
  { key: "messenger", bundleId: "com.facebook.Messenger" },
  { key: "instagram", bundleId: "com.burbn.instagram" },
  { key: "discord", bundleId: "com.hammerandchisel.discord" },
  { key: "slack", bundleId: "com.tinyspeck.chatlyio" },
  { key: "x", bundleId: "com.atebits.Tweetie2" },
  { key: "linkedin", bundleId: "com.linkedin.LinkedIn" },
  { key: "beeper", bundleId: "com.automattic.beeper" },
];

const headers = {
  "user-agent":
    "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_0) AppleWebKit/537.36 OpenClawIconAudit/1.0",
  accept: "application/json,image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8",
};

async function fetchJSON(url) {
  const response = await fetch(url, { redirect: "follow", headers });
  if (!response.ok) {
    throw new Error(`${response.status} ${response.statusText}: ${url}`);
  }
  return response.json();
}

async function fetchBytes(url) {
  const response = await fetch(url, { redirect: "follow", headers });
  if (!response.ok) {
    throw new Error(`${response.status} ${response.statusText}: ${url}`);
  }
  return Buffer.from(await response.arrayBuffer());
}

function preferLargestArtwork(result) {
  return result.artworkUrl512 || result.artworkUrl100 || result.artworkUrl60;
}

async function lookupApp(bundleId) {
  const url = new URL("https://itunes.apple.com/lookup");
  url.searchParams.set("bundleId", bundleId);
  url.searchParams.set("country", "us");
  const payload = await fetchJSON(url.href);
  if (!payload.results || payload.results.length !== 1) {
    throw new Error(`expected one App Store result for ${bundleId}, got ${payload.resultCount || 0}`);
  }
  const result = payload.results[0];
  const artworkURL = preferLargestArtwork(result);
  if (!artworkURL) {
    throw new Error(`missing artwork URL for ${bundleId}`);
  }
  return {
    bundleId,
    trackName: result.trackName,
    sellerName: result.sellerName,
    trackViewUrl: result.trackViewUrl,
    artworkURL,
  };
}

async function normalizeAppIcon(bytes) {
  const square = await sharp(bytes, { animated: false })
    .resize(256, 256, { fit: "cover", position: "center" })
    .png()
    .toBuffer();
  return roundIcon(square);
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

async function updateManifest(updates) {
  const manifest = JSON.parse(await fs.readFile(manifestPath, "utf8"));
  const byKey = new Map(updates.map((entry) => [entry.key, entry]));
  manifest.icons = manifest.icons.map((entry) => byKey.get(entry.key) || entry);
  manifest.version = "brand-icons-v5";
  manifest.generated_at = "2026-05-25";
  await fs.writeFile(manifestPath, `${JSON.stringify(manifest, null, 2)}\n`);
}

async function main() {
  await fs.mkdir(iconDir, { recursive: true });
  const manifest = JSON.parse(await fs.readFile(manifestPath, "utf8"));
  const current = new Map(manifest.icons.map((entry) => [entry.key, entry]));
  const updates = [];

  for (const app of apps) {
    const existing = current.get(app.key);
    if (!existing) {
      throw new Error(`manifest has no entry for ${app.key}`);
    }
    const meta = await lookupApp(app.bundleId);
    const normalized = await normalizeAppIcon(await fetchBytes(meta.artworkURL));
    const fileName = `${app.key}.png`;
    const outPath = path.join(iconDir, fileName);
    await fs.writeFile(outPath, normalized);
    const sha256 = crypto.createHash("sha256").update(normalized).digest("hex");
    updates.push({
      ...existing,
      png: `icons/${fileName}`,
      sha256,
      source: `${meta.artworkURL} (App Store: ${meta.trackName}; ${meta.sellerName}; ${meta.bundleId})`,
      source_kind: "apple-app-store-icon",
      source_priority: 9,
      license_note:
        "Official App Store artwork fetched via Apple iTunes Lookup API for app-style Matrix badges. Use as nominative/internal UI reference; verify Apple and vendor trademark terms before public redistribution.",
      fallback: false,
    });
    console.log(`${app.key}: ${meta.trackName} ${meta.artworkURL}`);
  }

  await updateManifest(updates);
}

main().catch((error) => {
  console.error(error);
  process.exit(1);
});
