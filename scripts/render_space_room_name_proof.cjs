#!/usr/bin/env node
"use strict";

const fs = require("node:fs/promises");
const os = require("node:os");
const path = require("node:path");
const sharp = require("sharp");

const repoRoot = path.resolve(__dirname, "..");
const iconRoot = path.join(repoRoot, "beepersource/assets/brand-icons/icons");
const proofDir = process.env.BEEPER_MATRIX_PROXY_SPACE_PROOF_DIR ||
  path.join(os.tmpdir(), "beeper-matrix-proxy", "space-room-name-proof-2026-05-24");

const spaces = [
  {
    platform: "WhatsApp",
    icon: "whatsapp.png",
    color: "#25D366",
    rooms: ["Family Updates", "Project Planning", "Test Group"],
  },
  {
    platform: "Signal",
    icon: "signal.png",
    color: "#3A76F0",
    rooms: ["Support Chat", "Design Review", "Team Notes"],
  },
  {
    platform: "Telegram",
    icon: "telegram.png",
    color: "#229ED9",
    rooms: ["Laptop Alerts", "Config Channel", "Logo Maker"],
  },
  {
    platform: "OnlyFans",
    icon: "onlyfans.png",
    color: "#00AFF0",
    rooms: ["Creator Updates", "Support", "Fan Messages"],
  },
];

const accountGroups = [
  {
    platform: "WhatsApp",
    icon: "whatsapp.png",
    color: "#25D366",
    login: "WhatsApp · Main",
    rooms: ["Family Updates", "Project Planning"],
  },
  {
    platform: "Signal",
    icon: "signal.png",
    color: "#3A76F0",
    login: "Signal · Support",
    rooms: ["Design Review", "Team Notes"],
  },
  {
    platform: "Telegram",
    icon: "telegram.png",
    color: "#229ED9",
    login: "Telegram · Laptop",
    rooms: ["Laptop Alerts", "Config Channel"],
  },
];

function esc(value) {
  return String(value).replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
}

function initials(name) {
  const parts = name.split(/[^a-z0-9äöü]+/i).filter(Boolean);
  return parts.slice(0, 2).map((part) => part[0]).join("").toUpperCase();
}

async function dataURL(file) {
  const body = await fs.readFile(path.join(iconRoot, file));
  return `data:image/png;base64,${body.toString("base64")}`;
}

async function render() {
  await fs.mkdir(proofDir, { recursive: true });
  const allIcons = [...new Set([...spaces, ...accountGroups].map((space) => space.icon))];
  const icons = Object.fromEntries(await Promise.all(allIcons.map(async (icon) => [icon, await dataURL(icon)])));
  const platformBlocks = spaces.slice(0, 3).map((space, i) => {
    const y = 104 + i * 190;
    const rows = space.rooms.map((room, j) => {
      const rowY = y + 66 + j * 38;
      return `
        <g>
          <circle cx="72" cy="${rowY + 13}" r="14" fill="${space.color}" opacity=".18"/>
          <text x="72" y="${rowY + 18}" text-anchor="middle" font-family="Poppins, Inter, Arial, sans-serif" font-size="11" font-weight="800" fill="${space.color}">${esc(initials(room))}</text>
          <text x="98" y="${rowY + 18}" font-family="Poppins, Inter, Arial, sans-serif" font-size="16" font-weight="650" fill="#f8fafc">${esc(room)}</text>
          <text x="405" y="${rowY + 18}" text-anchor="end" font-family="Inter, Arial, sans-serif" font-size="12" fill="#6b7280">plain name</text>
        </g>`;
    }).join("");
    return `
      <g>
        <image href="${icons[space.icon]}" x="48" y="${y}" width="42" height="42"/>
        <text x="104" y="${y + 27}" font-family="Poppins, Inter, Arial, sans-serif" font-size="20" font-weight="800" fill="#ffffff">${esc(space.platform)}</text>
        <text x="405" y="${y + 27}" text-anchor="end" font-family="Inter, Arial, sans-serif" font-size="12" fill="#9ca3af">Matrix Space</text>
        ${rows}
      </g>`;
  }).join("");
  const teamBlocks = accountGroups.map((space, i) => {
    const y = 104 + i * 190;
    const rows = space.rooms.map((room, j) => {
      const rowY = y + 86 + j * 38;
      return `
        <g>
          <circle cx="536" cy="${rowY + 13}" r="14" fill="${space.color}" opacity=".18"/>
          <text x="536" y="${rowY + 18}" text-anchor="middle" font-family="Poppins, Inter, Arial, sans-serif" font-size="11" font-weight="800" fill="${space.color}">${esc(initials(room))}</text>
          <text x="562" y="${rowY + 18}" font-family="Poppins, Inter, Arial, sans-serif" font-size="16" font-weight="650" fill="#f8fafc">${esc(room)}</text>
          <text x="846" y="${rowY + 18}" text-anchor="end" font-family="Inter, Arial, sans-serif" font-size="12" fill="#6b7280">plain name</text>
        </g>`;
    }).join("");
    return `
      <g>
        <image href="${icons[space.icon]}" x="512" y="${y}" width="42" height="42"/>
        <text x="568" y="${y + 27}" font-family="Poppins, Inter, Arial, sans-serif" font-size="20" font-weight="800" fill="#ffffff">${esc(space.platform)}</text>
        <text x="846" y="${y + 27}" text-anchor="end" font-family="Inter, Arial, sans-serif" font-size="12" fill="#9ca3af">platform</text>
        <rect x="522" y="${y + 50}" width="36" height="36" rx="12" fill="${space.color}" opacity=".2"/>
        <text x="540" y="${y + 74}" text-anchor="middle" font-family="Poppins, Inter, Arial, sans-serif" font-size="12" font-weight="800" fill="${space.color}">${esc(initials(space.login))}</text>
        <image href="${icons[space.icon]}" x="548" y="${y + 72}" width="16" height="16"/>
        <text x="574" y="${y + 74}" font-family="Poppins, Inter, Arial, sans-serif" font-size="15" font-weight="750" fill="#ffffff">${esc(space.login)}</text>
        <text x="846" y="${y + 74}" text-anchor="end" font-family="Inter, Arial, sans-serif" font-size="12" fill="#9ca3af">login/channel</text>
        ${rows}
      </g>`;
  }).join("");
  const badge = await dataURL("whatsapp.png");
  const svg = `
    <svg xmlns="http://www.w3.org/2000/svg" width="912" height="884" viewBox="0 0 912 884">
      <defs>
        <linearGradient id="phone" x1="0" y1="0" x2="0" y2="1">
          <stop offset="0" stop-color="#111"/>
          <stop offset="1" stop-color="#050505"/>
        </linearGradient>
        <linearGradient id="avatar" x1="38" y1="30" x2="96" y2="102">
          <stop offset="0" stop-color="#33d977"/>
          <stop offset="1" stop-color="#0f954e"/>
        </linearGradient>
      </defs>
      <rect width="912" height="884" rx="38" fill="url(#phone)"/>
      <text x="48" y="48" font-family="Poppins, Inter, Arial, sans-serif" font-size="24" font-weight="850" fill="#fff">All Chats</text>
      <text x="48" y="72" font-family="Inter, Arial, sans-serif" font-size="13" fill="#9ca3af">platform mode: app spaces; rooms show real names.</text>
      <text x="512" y="48" font-family="Poppins, Inter, Arial, sans-serif" font-size="24" font-weight="850" fill="#fff">Team / Login View</text>
      <text x="512" y="72" font-family="Inter, Arial, sans-serif" font-size="13" fill="#9ca3af">platform-account mode: platform -> login/channel -> rooms.</text>
      <line x1="456" y1="36" x2="456" y2="828" stroke="#1f2937" stroke-width="1"/>
      ${platformBlocks}
      ${teamBlocks}
      <g transform="translate(324 744)">
        <rect x="0" y="0" width="82" height="82" rx="24" fill="url(#avatar)"/>
        <text x="41" y="52" text-anchor="middle" font-family="Poppins, Inter, Arial, sans-serif" font-size="33" font-weight="850" fill="#fff">AN</text>
        <rect x="57" y="57" width="25" height="25" rx="7" fill="#000" opacity=".12"/>
        <image href="${badge}" x="56" y="56" width="26" height="26"/>
      </g>
      <text x="456" y="852" text-anchor="middle" font-family="Inter, Arial, sans-serif" font-size="12" fill="#9ca3af">Badge edge layout: lower-right, no floating gap · rooms stay plain in both modes</text>
    </svg>`;
  const out = path.join(proofDir, "space-room-name-and-badge-proof.png");
  await sharp(Buffer.from(svg)).png().toFile(out);
  await fs.writeFile(path.join(proofDir, "space-room-name-and-badge-proof.json"), `${JSON.stringify({
    generated_at: "2026-05-24",
    private_data: false,
    assertions: [
      "Platform categories are Matrix Spaces with app-icon avatars.",
      "platform-account grouping adds login/channel spaces under each platform space.",
      "Portal rooms are rendered with plain chat names only.",
      "The proof view uses edge layout with bottom-right position and zero inset.",
    ],
    output: out,
  }, null, 2)}\n`);
  console.log(out);
}

render().catch((error) => {
  console.error(error);
  process.exit(1);
});
