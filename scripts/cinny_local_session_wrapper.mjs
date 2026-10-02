import http from "node:http";
import https from "node:https";
import crypto from "node:crypto";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { execFileSync } from "node:child_process";

function readToken() {
  if (process.env.CINNY_ACCESS_TOKEN) return process.env.CINNY_ACCESS_TOKEN;
  const user = process.env.USER || "";
  return execFileSync("security", ["find-generic-password", "-s", "matrix-archive-sync", "-a", user, "-w"], {
    encoding: "utf8",
    stdio: ["ignore", "pipe", "ignore"],
  }).trim();
}

const token = readToken();
if (!token) {
  throw new Error("CINNY_ACCESS_TOKEN or keychain item matrix-archive-sync is required");
}

const matrixTarget = process.env.CINNY_MATRIX_TARGET || "https://vcvm.tail6a40cd.ts.net:3443";
const cinnyTarget = process.env.CINNY_TARGET || "http://127.0.0.1:3180";
const matrixPort = Number(process.env.CINNY_MATRIX_PROXY_PORT || "34430");
const cinnyPort = Number(process.env.CINNY_WRAPPER_PORT || "3182");
const rewriteBeeperProfiles = process.env.CINNY_REWRITE_BEEPER_PROFILES !== "false";
const suppressBridgeNoise = process.env.CINNY_SUPPRESS_BRIDGE_NOISE !== "false";
const agent = new https.Agent({ keepAlive: true, rejectUnauthorized: false });
const profileByRoom = new Map();
const fallbackAvatarByMediaID = new Map();
const profileCachePath =
  process.env.CINNY_PROFILE_CACHE_PATH || path.join(os.homedir(), ".cache", "openclaw", "cinny-profile-cache.json");
const brandIconDir = path.join(path.dirname(new URL(import.meta.url).pathname), "..", "beepersource", "assets", "brand-icons", "icons");
const supportedBrandIcons = new Set([
  "beeper",
  "discord",
  "email",
  "imessage",
  "instagram",
  "linkedin",
  "matrix",
  "messenger",
  "signal",
  "slack",
  "telegram",
  "whatsapp",
  "x",
]);

const session = {
  cinny_hs_base_url: `http://127.0.0.1:${matrixPort}`,
  cinny_user_id: "@openclaw:100.120.120.120",
  cinny_device_id: "PQGFHCMMPG",
  cinny_access_token: token,
  cinny_messageLayout: "modern",
};

function corsHeaders(origin) {
  return {
    "Access-Control-Allow-Origin": origin || "*",
    "Access-Control-Allow-Methods": "GET,POST,PUT,DELETE,OPTIONS",
    "Access-Control-Allow-Headers": "Authorization,Content-Type,Accept,Origin,X-Requested-With",
    "Access-Control-Expose-Headers": "Synapse-Trace-Id,Server",
    "Access-Control-Max-Age": "86400",
    Vary: "Origin",
  };
}

function matrixServerName() {
  const parts = session.cinny_user_id.split(":");
  return parts.length > 1 ? parts.slice(1).join(":") : "localhost";
}

loadProfileCache();

function matrixGhostLocalpart(sourceID, displayname = "") {
  const cleanBase = displayname || sourceID;
  const clean = cleanBase
    .replace(/[^a-zA-Z0-9]/g, "_")
    .replace(/^[\s_]+|[\s_]+$/g, "")
    .toLowerCase()
    .slice(0, 32);
  if (displayname && clean) return clean;
  const hash = crypto.createHash("sha256").update(sourceID).digest().subarray(0, 6).toString("base64url");
  return `beeper_${clean || "sender"}_${hash}`;
}

function matrixGhostID(sourceID, displayname = "") {
  if (sourceID.startsWith("@") && sourceID.includes(":") && sourceID.endsWith(`:${matrixServerName()}`)) {
    return sourceID;
  }
  return `@${matrixGhostLocalpart(sourceID, displayname)}:${matrixServerName()}`;
}

function rememberProfile(roomID, mxid, profile) {
  if (!roomID || !mxid || !profile) return;
  if (!profileByRoom.has(roomID)) profileByRoom.set(roomID, new Map());
  profileByRoom.get(roomID).set(mxid, {
    displayname: profile.displayname || profile.display_name || profile.id || mxid,
    avatar_url: profile.avatar_url || profile.avatarUrl || undefined,
    has_fallback: profile.has_fallback,
  });
  persistProfileCache();
}

function loadProfileCache() {
  try {
    const raw = fs.readFileSync(profileCachePath, "utf8");
    const parsed = JSON.parse(raw);
    for (const [roomID, profiles] of Object.entries(parsed.rooms || {})) {
      const roomProfiles = new Map();
      for (const [mxid, profile] of Object.entries(profiles || {})) {
        roomProfiles.set(mxid, {
          displayname: profile.displayname || mxid,
          avatar_url: profile.avatar_url || undefined,
          has_fallback: profile.has_fallback,
        });
      }
      if (roomProfiles.size) profileByRoom.set(roomID, roomProfiles);
    }
  } catch (_) {}
}

function persistProfileCache() {
  try {
    const rooms = {};
    for (const [roomID, roomProfiles] of profileByRoom.entries()) {
      rooms[roomID] = {};
      for (const [mxid, profile] of roomProfiles.entries()) {
        rooms[roomID][mxid] = {
          displayname: profile.displayname || mxid,
          avatar_url: profile.avatar_url || undefined,
          has_fallback: profile.has_fallback,
        };
      }
    }
    fs.mkdirSync(path.dirname(profileCachePath), { recursive: true, mode: 0o700 });
    fs.writeFileSync(profileCachePath, JSON.stringify({ version: 1, rooms }, null, 2), { mode: 0o600 });
  } catch (error) {
    console.error("profile cache write failed", error.message);
  }
}

function initialsForLabel(label = "") {
  const words = String(label)
    .normalize("NFKD")
    .replace(/[^\p{L}\p{N}\s._-]/gu, " ")
    .split(/[\s._-]+/)
    .map((part) => part.trim())
    .filter(Boolean);
  if (!words.length) return "?";
  if (words.length === 1) return Array.from(words[0]).slice(0, 2).join("").toUpperCase();
  return `${Array.from(words[0])[0] || ""}${Array.from(words[words.length - 1])[0] || ""}`.toUpperCase();
}

function fallbackAvatarMediaID(mxid, displayname = "") {
  const digest = crypto.createHash("sha256").update(`${mxid}\0${displayname}`).digest("base64url").slice(0, 16);
  return `openclaw-avatar-${digest}.svg`;
}

function fallbackAvatarURL(mxid, displayname = "") {
  const mediaID = fallbackAvatarMediaID(mxid, displayname);
  if (!fallbackAvatarByMediaID.has(mediaID)) {
    const hue = crypto.createHash("sha256").update(mxid || displayname).digest()[0] % 360;
    const hue2 = (hue + 34) % 360;
    const initials = initialsForLabel(displayname || mxid)
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;");
    fallbackAvatarByMediaID.set(
      mediaID,
      `<svg xmlns="http://www.w3.org/2000/svg" width="256" height="256" viewBox="0 0 256 256"><defs><linearGradient id="g" x1="32" y1="28" x2="224" y2="228" gradientUnits="userSpaceOnUse"><stop stop-color="hsl(${hue} 78% 66%)"/><stop offset="1" stop-color="hsl(${hue2} 74% 54%)"/></linearGradient></defs><rect width="256" height="256" rx="72" fill="url(#g)"/><text x="128" y="142" text-anchor="middle" dominant-baseline="middle" font-family="Poppins, Inter, -apple-system, BlinkMacSystemFont, Segoe UI, sans-serif" font-size="${initials.length > 1 ? 88 : 104}" font-weight="700" fill="white">${initials}</text></svg>`,
    );
  }
  return `mxc://${matrixServerName()}/${mediaID}`;
}

function profileAvatarURL(mxid, profile) {
  return profile.avatar_url || fallbackAvatarURL(mxid, profile.displayname || mxid);
}

function memberEvent(roomID, mxid, profile) {
  const fingerprint = crypto
    .createHash("sha256")
    .update(`${roomID}\0${mxid}\0${profile.displayname || ""}\0${profile.avatar_url || ""}`)
    .digest("base64url")
    .slice(0, 16);
  const content = {
    membership: "join",
    displayname: profile.displayname || mxid,
  };
  content.avatar_url = profileAvatarURL(mxid, profile);
  return {
    type: "m.room.member",
    sender: mxid,
    state_key: mxid,
    room_id: roomID,
    event_id: `$openclaw_profile_${fingerprint}:${matrixServerName()}`,
    origin_server_ts: Date.now(),
    content,
  };
}

function roomStateEvent(roomID, type, content, stateKey = "") {
  const fingerprint = crypto
    .createHash("sha256")
    .update(`${roomID}\0${type}\0${stateKey}\0${JSON.stringify(content)}`)
    .digest("base64url")
    .slice(0, 16);
  return {
    type,
    sender: session.cinny_user_id,
    state_key: stateKey,
    room_id: roomID,
    event_id: `$openclaw_state_${fingerprint}:${matrixServerName()}`,
    origin_server_ts: Date.now(),
    content,
  };
}

function syntheticRoomName(roomID, roomProfiles) {
  const names = Array.from(roomProfiles?.values?.() || [])
    .map((profile) => profile.displayname)
    .filter(Boolean);
  if (names.length === 1) return names[0];
  if (names.length > 1) return names.slice(0, 3).join(", ") + (names.length > 3 ? ` +${names.length - 3}` : "");
  return roomID;
}

function stripSenderPrefix(body, displayname) {
  if (typeof body !== "string" || !displayname) return body;
  const prefix = `${displayname}: `;
  return body.startsWith(prefix) ? body.slice(prefix.length) : body;
}

function speakerPrefixMatch(line) {
  if (typeof line !== "string") return null;
  const match = line.match(/^([\p{L}\p{N}][\p{L}\p{N} ._'’+-]{0,78}):\s*(.*)$/u);
  if (!match) return null;
  const name = match[1].trim();
  if (!name || /^https?$/i.test(name)) return null;
  return { name, text: match[2] || "" };
}

function bodyWithoutSpeakerPrefix(body = "") {
  const line = String(body).trim();
  const prefixed = speakerPrefixMatch(line);
  return (prefixed ? prefixed.text : line).trim();
}

function isBoilerplateText(text = "") {
  const raw = String(text).trim();
  if (
    /^Unsupported Beeper message$/i.test(raw) ||
    /^Beeper media could not be mirrored to Matrix:/i.test(raw) ||
    /^Failed to bridge voice message\b/i.test(raw) ||
    /^Sent an album\b/i.test(raw) ||
    /^Message deleted$/i.test(raw)
  ) {
    return true;
  }
  const normalized = bodyWithoutSpeakerPrefix(raw);
  return (
    /^Unsupported Beeper message$/i.test(normalized) ||
    /^Beeper media could not be mirrored to Matrix:/i.test(normalized) ||
    /^Failed to bridge voice message\b/i.test(normalized) ||
    /^Sent an album\b/i.test(normalized) ||
    /^Message deleted$/i.test(normalized)
  );
}

function bridgeTranscriptSpeakerLines(body = "") {
  const lines = String(body).replace(/\r\n/g, "\n").split("\n");
  const messages = [];
  let current = null;

  for (const rawLine of lines) {
    const line = rawLine.trimEnd();
    if (!line.trim()) {
      if (current) current.lines.push("");
      continue;
    }

    const prefixed = speakerPrefixMatch(line);
    if (prefixed) {
      if (current && current.lines.some((item) => item.trim())) messages.push(current);
      current = { speaker: prefixed.name, lines: [prefixed.text] };
      continue;
    }

    if (current) {
      current.lines.push(line);
    }
  }

  if (current && current.lines.some((item) => item.trim())) messages.push(current);
  return messages
    .map((message) => ({
      speaker: message.speaker,
      body: message.lines.join("\n").trim(),
    }))
    .filter((message) => message.speaker && message.body && !isBoilerplateText(`${message.speaker}: ${message.body}`));
}

function cloneEvent(event) {
  return JSON.parse(JSON.stringify(event));
}

function syntheticEventID(originalEvent, speaker, index) {
  const seed = `${originalEvent?.event_id || ""}\0${originalEvent?.room_id || ""}\0${speaker}\0${index}`;
  const digest = crypto.createHash("sha256").update(seed).digest("base64url").slice(0, 28);
  return `$openclaw_speaker_${digest}:${matrixServerName()}`;
}

function splitSpeakerTranscriptEvent(roomID, event) {
  if (event?.type !== "m.room.message") return null;
  const body = String(event?.content?.body || "");
  if (!body.includes(":")) return null;
  const speakerMessages = bridgeTranscriptSpeakerLines(body);
  if (speakerMessages.length < 1) return null;
  if (speakerMessages.length === 1 && event.sender !== session.cinny_user_id) return null;

  return speakerMessages.map((message, index) => {
    const mxid = matrixGhostID(`openclaw-speaker:${message.speaker}`, message.speaker);
    const profile = {
      id: `openclaw-speaker:${message.speaker}`,
      displayname: message.speaker,
      has_fallback: true,
    };
    rememberProfile(roomID || event.room_id, mxid, profile);

    const next = cloneEvent(event);
    next.sender = mxid;
    next.event_id = syntheticEventID(event, message.speaker, index);
    next.origin_server_ts = Number(event.origin_server_ts || Date.now()) + index;
    next.unsigned = {
      ...(next.unsigned || {}),
      openclaw_original_event_id: event.event_id,
      openclaw_split_index: index,
    };
    next.content = {
      ...(next.content || {}),
      msgtype: next.content?.msgtype || "m.text",
      body: message.body,
    };
    delete next.content.formatted_body;
    delete next.content.format;
    delete next.content["com.beeper.per_message_profile"];
    return next;
  });
}

function rewriteEvent(roomID, event) {
  const profile = event?.content?.["com.beeper.per_message_profile"];
  if (event?.type !== "m.room.message" || !profile?.id) return null;
  const mxid = matrixGhostID(String(profile.id), profile.displayname || profile.display_name || "");
  event.sender = mxid;
  if (event.content) {
    event.content.body = stripSenderPrefix(event.content.body, profile.displayname);
    event.content.formatted_body = stripSenderPrefix(event.content.formatted_body, profile.displayname);
  }
  rememberProfile(roomID || event.room_id, mxid, profile);
  return mxid;
}

function isBridgeStateEvent(event) {
  return (
    event?.type === "uk.half-shot.bridge" ||
    event?.type === "com.beeper.room_features" ||
    event?.type === "com.beeper.room_features_config" ||
    (event?.type === "m.room.avatar" && event?.sender === session.cinny_user_id)
  );
}

function isBoilerplateMessage(event) {
  const body = String(event?.content?.body || "").trim();
  return event?.type === "m.room.message" && isBoilerplateText(body);
}

function eventLooksBridged(event) {
  if (!event || typeof event !== "object") return false;
  if (isBridgeStateEvent(event)) return true;
  if (event.content?.["com.beeper.per_message_profile"]) return true;
  const sender = String(event.sender || "");
  return /^@(telegram|whatsapp|signal|discord|instagram|facebook|imessage|slack|linkedin|twitter|x)_/i.test(sender);
}

function suppressRoomNoise(room) {
  if (!suppressBridgeNoise || !room) return;
  const timelineEvents = room.timeline?.events || [];
  const stateEvents = room.state?.events || [];
  const bridgedRoom = [...timelineEvents, ...stateEvents].some(eventLooksBridged);

  if (Array.isArray(room.timeline?.events)) {
    room.timeline.events = room.timeline.events.filter((event) => !isBridgeStateEvent(event) && !isBoilerplateMessage(event));
  }

  if (bridgedRoom) {
    room.unread_notifications = {
      ...(room.unread_notifications || {}),
      notification_count: 0,
      highlight_count: 0,
    };
  }
}

function rewriteTimelineEvents(roomID, events = [], seen = new Set()) {
  const rewritten = [];
  for (const event of events) {
    const mxid = rewriteEvent(roomID, event);
    if (mxid) seen.add(mxid);

    const splitEvents = splitSpeakerTranscriptEvent(roomID, event);
    if (splitEvents) {
      for (const splitEvent of splitEvents) {
        if (splitEvent.sender) seen.add(splitEvent.sender);
        if (!isBridgeStateEvent(splitEvent) && !isBoilerplateMessage(splitEvent)) {
          rewritten.push(splitEvent);
        }
      }
      continue;
    }

    if (!isBridgeStateEvent(event) && !isBoilerplateMessage(event)) {
      rewritten.push(event);
    }
  }
  return rewritten;
}

function roomIDFromPath(pathname) {
  const match = pathname.match(/\/rooms\/([^/]+)\//);
  return match ? decodeURIComponent(match[1]) : "";
}

function injectMemberEvents(roomID, stateEvents = [], seen = new Set()) {
  const profiles = profileByRoom.get(roomID);
  if (!profiles?.size) return stateEvents;
  const events = Array.isArray(stateEvents) ? stateEvents : [];
  const existingMembers = new Set(
    events
      .filter((event) => event?.type === "m.room.member")
      .map((event) => event.state_key)
      .filter(Boolean),
  );
  for (const [mxid, profile] of profiles.entries()) {
    if (!existingMembers.has(mxid) || seen.has(mxid)) {
      events.push(memberEvent(roomID, mxid, profile));
      existingMembers.add(mxid);
    }
  }
  return events;
}

function transformMatrixResponse(pathname, payload) {
  if (!rewriteBeeperProfiles || !payload || typeof payload !== "object") return payload;

  if (payload.rooms?.join) {
    for (const [roomID, room] of Object.entries(payload.rooms.join)) {
      const seen = new Set();
      if (Array.isArray(room.timeline?.events)) {
        room.timeline.events = rewriteTimelineEvents(roomID, room.timeline.events, seen);
      }
      for (const event of room.state?.events || []) {
        const mxid = rewriteEvent(roomID, event);
        if (mxid) seen.add(mxid);
      }
      if (profileByRoom.get(roomID)?.size) {
        room.state ||= {};
        room.state.events = injectMemberEvents(roomID, room.state.events || [], seen);
      }
      suppressRoomNoise(room);
    }
  }

  if (Array.isArray(payload.chunk)) {
    const roomID = roomIDFromPath(pathname);
    const seen = new Set();
    payload.chunk = rewriteTimelineEvents(roomID, payload.chunk, seen);
    const profiles = profileByRoom.get(roomID);
    if (profiles?.size) {
      for (const [mxid, profile] of profiles.entries()) {
        if (seen.has(mxid)) payload.chunk.push(memberEvent(roomID, mxid, profile));
      }
    }
  }

  if (Array.isArray(payload.events_before) || Array.isArray(payload.events_after) || payload.event) {
    const roomID = roomIDFromPath(pathname) || payload.event?.room_id || "";
    const seen = new Set();
    if (Array.isArray(payload.events_before)) {
      payload.events_before = rewriteTimelineEvents(roomID, payload.events_before, seen);
    }
    if (payload.event) {
      const mxid = rewriteEvent(roomID, payload.event);
      if (mxid) seen.add(mxid);
      const splitEvents = splitSpeakerTranscriptEvent(roomID, payload.event);
      if (splitEvents) {
        payload.event = splitEvents[0];
        for (const splitEvent of splitEvents) {
          if (splitEvent.sender) seen.add(splitEvent.sender);
        }
        payload.events_after = [...splitEvents.slice(1), ...(payload.events_after || [])];
      }
      if (isBridgeStateEvent(payload.event) || isBoilerplateMessage(payload.event)) {
        delete payload.event;
      }
    }
    if (Array.isArray(payload.events_after)) {
      payload.events_after = rewriteTimelineEvents(roomID, payload.events_after, seen);
    }
    if (Array.isArray(payload.state)) {
      for (const event of payload.state) {
        const mxid = rewriteEvent(roomID, event);
        if (mxid) seen.add(mxid);
      }
      payload.state = injectMemberEvents(roomID, payload.state, seen);
    }
  }

  if (Array.isArray(payload.notifications)) {
    payload.notifications = payload.notifications.filter((notification) => {
      const event = notification?.event;
      return !isBridgeStateEvent(event) && !isBoilerplateMessage(event) && !eventLooksBridged(event);
    });
  }

  if (payload.joined && pathname.includes("/joined_members")) {
    const roomID = roomIDFromPath(pathname);
    const profiles = profileByRoom.get(roomID);
    if (profiles?.size) {
      for (const [mxid, profile] of profiles.entries()) {
        payload.joined[mxid] = {
          display_name: profile.displayname || mxid,
          avatar_url: profileAvatarURL(mxid, profile),
        };
      }
    }
  }

  return payload;
}

function syntheticDirectAccountData() {
  const direct = {};
  for (const [roomID, roomProfiles] of profileByRoom.entries()) {
    if (!roomProfiles || roomProfiles.size !== 1) continue;
    const [mxid] = roomProfiles.keys();
    if (!direct[mxid]) direct[mxid] = [];
    if (!direct[mxid].includes(roomID)) direct[mxid].push(roomID);
  }
  return direct;
}

function syntheticInitialSync() {
  const join = {};
  for (const [roomID, roomProfiles] of profileByRoom.entries()) {
    const roomName = syntheticRoomName(roomID, roomProfiles);
    const stateEvents = [
      roomStateEvent(roomID, "m.room.create", { creator: session.cinny_user_id, room_version: "10" }),
      roomStateEvent(roomID, "m.room.name", { name: roomName }),
      memberEvent(roomID, session.cinny_user_id, { displayname: "martin" }),
    ];
    for (const [mxid, profile] of roomProfiles.entries()) {
      stateEvents.push(memberEvent(roomID, mxid, profile));
    }
    join[roomID] = {
      state: { events: stateEvents },
      timeline: { events: [], limited: false, prev_batch: `openclaw_${Date.now()}` },
      ephemeral: { events: [] },
      account_data: { events: [] },
      unread_notifications: { notification_count: 0, highlight_count: 0 },
      summary: {},
    };
  }
  return {
    next_batch: `openclaw_fast_sync_${Date.now()}`,
    account_data: {
      events: [{ type: "m.direct", content: syntheticDirectAccountData() }],
    },
    presence: { events: [] },
    rooms: { join, invite: {}, leave: {} },
  };
}

function syntheticIncrementalSync() {
  return {
    next_batch: `openclaw_fast_sync_${Date.now()}`,
    account_data: { events: [] },
    presence: { events: [] },
    rooms: { join: {}, invite: {}, leave: {} },
  };
}

function publicProfiles() {
  const profiles = {};
  for (const roomProfiles of profileByRoom.values()) {
    for (const [mxid, profile] of roomProfiles.entries()) {
      const localpart = mxid.startsWith("@") ? mxid.slice(1).split(":")[0] : mxid;
      const publicProfile = {
        ...profile,
        avatar_url: profileAvatarURL(mxid, profile),
      };
      profiles[mxid] = publicProfile;
      profiles[localpart] = publicProfile;
    }
  }
  return profiles;
}

const profileEnhancer = `
<script>
(() => {
  const endpoint = "/__openclaw/profiles";
  let profiles = {};
  const avatarStyleID = "openclaw-cinny-avatar-style";
  function ensureAvatarStyle() {
    if (document.getElementById(avatarStyleID)) return;
    const style = document.createElement("style");
    style.id = avatarStyleID;
    style.textContent = [
      ".openclaw-hidden-bridge-noise{display:none!important}",
    ].join("\\n");
    document.head.appendChild(style);
  }
  function mxcToMediaURL(mxc) {
    if (typeof mxc !== "string" || !mxc.startsWith("mxc://")) return "";
    const rest = mxc.slice("mxc://".length);
    const slash = rest.indexOf("/");
    if (slash < 1) return "";
    const server = encodeURIComponent(rest.slice(0, slash));
    const media = encodeURIComponent(rest.slice(slash + 1));
    const matrixBase = localStorage.getItem("cinny_hs_base_url") || location.origin;
    return matrixBase.replace(/\\/$/, "") + "/_matrix/client/v1/media/thumbnail/" + server + "/" + media + "?width=96&height=96&method=crop&allow_redirect=true";
  }
  async function refreshProfiles() {
    try {
      profiles = await fetch(endpoint, { cache: "no-store" }).then((response) => response.json());
    } catch (_) {}
  }
  function detectPageNetwork() {
    const visibleMainBlocks = Array.from(document.querySelectorAll("div"))
      .map((element) => {
        const rect = element.getBoundingClientRect();
        return {
          rect,
          text: element.innerText || element.textContent || "",
        };
      })
      .filter(({ rect, text }) => rect.x > 300 && rect.width > 380 && rect.height > 180 && text.trim().length > 20)
      .sort((a, b) => b.rect.width * b.rect.height - a.rect.width * a.rect.height);
    const text = visibleMainBlocks[0]?.text || document.body?.innerText || "";
    const patterns = [
      ["whatsapp", /\\b(?:WhatsApp|from\\s+WhatsApp\\s*\\(whatsapp\\)|WHATSAPP)\\b/i],
      ["signal", /\\b(?:Signal|from\\s+Signal\\s*\\(signal\\)|SIGNAL)\\b/i],
      ["telegram", /\\b(?:Telegram|from\\s+Telegram\\s*\\(telegram\\)|TELEGRAM)\\b/i],
      ["instagram", /\\b(?:Instagram|from\\s+Instagram\\s*\\(instagram\\)|INSTAGRAM)\\b/i],
      ["messenger", /\\b(?:Messenger|from\\s+Messenger\\s*\\(messenger\\)|MESSENGER)\\b/i],
      ["discord", /\\b(?:Discord|from\\s+Discord\\s*\\(discord\\)|DISCORD)\\b/i],
      ["imessage", /\\b(?:iMessage|from\\s+iMessage\\s*\\(imessage\\)|IMESSAGE)\\b/i],
      ["slack", /\\b(?:Slack|from\\s+Slack\\s*\\(slack\\)|SLACK)\\b/i],
    ];
    return patterns.find(([, pattern]) => pattern.test(text))?.[0] || "";
  }
  function hideBoilerplateMessages() {
    if (!document.body) return;
    const badPattern = /(?:^|\\n)(?:[\\p{L}\\p{N}][\\p{L}\\p{N} ._'’+-]{0,78}:\\s*)?(?:Unsupported Beeper message|Beeper media could not be mirrored to Matrix:|Failed to bridge voice message\\b|Sent an album\\b|Message deleted\\b)|changed room avatar/iu;
    const speakerLinePattern = /[\\p{L}\\p{N}][\\p{L}\\p{N} ._'’+-]{0,78}:\\s+\\S/gu;
    const countSpeakerLines = (text) => {
      speakerLinePattern.lastIndex = 0;
      return Array.from(String(text || "").matchAll(speakerLinePattern)).length;
    };
    const isOpenClawTranscriptBlock = (text) => {
      const value = String(text || "");
      return /Hermes OpenClaw/i.test(value) && countSpeakerLines(value) >= 2;
    };
    const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
    const nodes = [];
    while (walker.nextNode()) nodes.push(walker.currentNode);
    for (const node of nodes) {
      const key = node.textContent.trim();
      if (!badPattern.test(key)) continue;
      let target = node.parentElement;
      if (target) {
        target.style.display = "none";
        target.setAttribute("aria-hidden", "true");
      }
      node.textContent = "";
    }
    const transcriptBlocks = Array.from(document.body.querySelectorAll("div")).filter((element) => {
      const text = (element.innerText || element.textContent || "").trim();
      if (!isOpenClawTranscriptBlock(text)) return false;
      if (text.length > 4200) return false;
      const rect = element.getBoundingClientRect();
      if (rect.height > 520 || rect.width > 1200) return false;
      if (element.querySelector("textarea,input,[contenteditable='true']")) return false;
      const childTranscript = Array.from(element.children || []).some((child) =>
        isOpenClawTranscriptBlock(child.innerText || child.textContent || ""),
      );
      return !childTranscript;
    });
    for (const element of transcriptBlocks) {
      element.style.display = "none";
      element.setAttribute("aria-hidden", "true");
    }
    for (const label of Array.from(document.body.querySelectorAll("button,span,div")).filter((element) => {
      return (element.innerText || element.textContent || "").trim() === "Hermes OpenClaw";
    })) {
      let target = label;
      const candidates = [];
      for (let depth = 0; target?.parentElement && depth < 8; depth += 1) {
        const text = (target.innerText || target.textContent || "").trim();
        const rect = target.getBoundingClientRect();
        if (
          isOpenClawTranscriptBlock(text) &&
          text.length < 4200 &&
          rect.height <= 900 &&
          rect.width <= 1400 &&
          !/Send a message|Message Search|Lobby\\b/i.test(text)
        ) {
          candidates.push(target);
        }
        target = target.parentElement;
      }
      const best = candidates.sort((a, b) => {
        const aText = (a.innerText || a.textContent || "").trim();
        const bText = (b.innerText || b.textContent || "").trim();
        return aText.length - bText.length;
      })[0];
      if (best) {
        best.style.display = "none";
        best.setAttribute("aria-hidden", "true");
      }
    }
    const compactBlocks = Array.from(document.body.querySelectorAll("div,span,p")).filter((element) => {
      const text = (element.innerText || element.textContent || "").trim();
      if (!badPattern.test(text)) return false;
      return text.length < 220 && !element.querySelector("img,audio,video,canvas,button,a");
    });
    for (const element of compactBlocks) {
      element.style.display = "none";
      element.setAttribute("aria-hidden", "true");
    }
    for (const node of nodes) {
      const key = node.textContent.trim();
      if (!badPattern.test(key)) continue;
      node.textContent = "";
      let target = node.parentElement;
      let best = target;
      for (let depth = 0; target?.parentElement && depth < 7; depth += 1) {
        const parent = target.parentElement;
        const parentText = parent.innerText || parent.textContent || "";
        if (
          parentText.length > 1800 ||
          /Send a message|Create Room|Message Search|codex cinny e2e smoke/i.test(parentText)
        ) {
          break;
        }
        best = parent;
        target = parent;
      }
      target = best;
      if (target) {
        target.style.display = "none";
        target.setAttribute("aria-hidden", "true");
      }
    }
  }
  function patchTextNodes() {
    if (!document.body) return;
    hideBoilerplateMessages();
    const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
    const nodes = [];
    while (walker.nextNode()) nodes.push(walker.currentNode);
    for (const node of nodes) {
      const key = node.textContent.trim();
      if (/(?:^|\\n)(?:[\\p{L}\\p{N}][\\p{L}\\p{N} ._'’+-]{0,78}:\\s*)?(?:Unsupported Beeper message|Beeper media could not be mirrored to Matrix:|Failed to bridge voice message\\b|Sent an album\\b|Message deleted\\b)|changed room avatar/iu.test(key)) continue;
      const profile = profiles[key];
      if (profile?.displayname && key !== profile.displayname) {
        node.textContent = node.textContent.replace(key, profile.displayname);
      }
    }
  }
  async function tick() {
    hideBoilerplateMessages();
    await refreshProfiles();
    patchTextNodes();
  }
  new MutationObserver(() => {
    hideBoilerplateMessages();
    patchTextNodes();
  }).observe(document.documentElement, { childList: true, subtree: true, characterData: true });
  setInterval(hideBoilerplateMessages, 250);
  setInterval(tick, 1000);
  tick();
})();
</script>`;

const matrixProxy = http.createServer((req, res) => {
  const origin = req.headers.origin || "*";
  if (req.method === "OPTIONS") {
    res.writeHead(204, corsHeaders(origin));
    res.end();
    return;
  }

  const url = new URL(req.url, matrixTarget);
  const syntheticSince = url.searchParams.get("since") || "";
  if (req.method === "GET" && url.pathname === "/_matrix/client/v3/sync" && syntheticSince.startsWith("openclaw_")) {
    const body = Buffer.from(JSON.stringify(syntheticIncrementalSync()));
    res.writeHead(200, {
      "Content-Type": "application/json",
      "Cache-Control": "no-store",
      "Content-Length": String(body.length),
      ...corsHeaders(origin),
    });
    res.end(body);
    return;
  }
  if (
    req.method === "GET" &&
    url.pathname === "/_matrix/client/v3/sync" &&
    !url.searchParams.has("since") &&
    process.env.CINNY_FAST_INITIAL_SYNC !== "false"
  ) {
    const body = Buffer.from(JSON.stringify(syntheticInitialSync()));
    res.writeHead(200, {
      "Content-Type": "application/json",
      "Cache-Control": "no-store",
      "Content-Length": String(body.length),
      ...corsHeaders(origin),
    });
    res.end(body);
    return;
  }
  const mediaMatch = url.pathname.match(/^\/_matrix\/(?:media\/(?:r0|v3)|client\/v1\/media)\/(?:download|thumbnail)\/[^/]+\/([^/?#]+)$/);
  if (req.method === "GET" && mediaMatch) {
    const mediaID = decodeURIComponent(mediaMatch[1]);
    const svg = fallbackAvatarByMediaID.get(mediaID);
    if (svg) {
      const body = Buffer.from(svg);
      res.writeHead(200, {
        "Content-Type": "image/svg+xml; charset=utf-8",
        "Cache-Control": "public, max-age=31536000, immutable",
        "Content-Length": String(body.length),
        ...corsHeaders(origin),
      });
      res.end(body);
      return;
    }
  }
  const headers = { ...req.headers, host: url.host };
  delete headers.origin;
  delete headers["accept-encoding"];

  const upstream = https.request(url, { method: req.method, headers, agent }, (upstreamRes) => {
    const chunks = [];
    upstreamRes.on("data", (chunk) => chunks.push(chunk));
    upstreamRes.on("end", () => {
      let body = Buffer.concat(chunks);
      const headersOut = {};
      const contentType = String(upstreamRes.headers["content-type"] || "");
      let statusCode = upstreamRes.statusCode || 502;
      if (contentType.includes("application/json")) {
        try {
          let parsed = JSON.parse(body.toString("utf8"));
          if (url.pathname.endsWith("/account_data/m.direct") && (statusCode === 404 || parsed?.errcode === "M_NOT_FOUND")) {
            parsed = syntheticDirectAccountData();
            statusCode = 200;
          }
          const transformed = transformMatrixResponse(url.pathname, parsed);
          body = Buffer.from(JSON.stringify(transformed));
        } catch (error) {
          console.error("matrix response rewrite failed", error);
        }
      }
      for (const [key, value] of Object.entries(upstreamRes.headers)) {
        const lower = key.toLowerCase();
        if (!lower.startsWith("access-control-") && lower !== "content-length" && lower !== "content-encoding") {
          headersOut[key] = value;
        }
      }
      headersOut["content-length"] = String(body.length);
      res.writeHead(statusCode, { ...headersOut, ...corsHeaders(origin) });
      res.end(body);
    });
  });

  upstream.on("error", (error) => {
    res.writeHead(502, { "Content-Type": "text/plain", ...corsHeaders(origin) });
    res.end(error.message);
  });
  req.pipe(upstream);
});

const cinnyWrapper = http.createServer((req, res) => {
  if (req.url.startsWith("/__openclaw/profiles")) {
    const body = Buffer.from(JSON.stringify(publicProfiles()));
    res.writeHead(200, {
      "Content-Type": "application/json",
      "Cache-Control": "no-store",
      "Content-Length": String(body.length),
    });
    res.end(body);
    return;
  }
  const brandIconMatch = req.url.match(/^\/__openclaw\/brand-icons\/([a-z0-9_-]+)\.png(?:[?#].*)?$/i);
  if (brandIconMatch) {
    const icon = brandIconMatch[1].toLowerCase();
    if (!supportedBrandIcons.has(icon)) {
      res.writeHead(404, { "Content-Type": "text/plain" });
      res.end("unknown brand icon");
      return;
    }
    try {
      const body = fs.readFileSync(path.join(brandIconDir, `${icon}.png`));
      res.writeHead(200, {
        "Content-Type": "image/png",
        "Cache-Control": "public, max-age=31536000, immutable",
        "Content-Length": String(body.length),
      });
      res.end(body);
    } catch (_) {
      res.writeHead(404, { "Content-Type": "text/plain" });
      res.end("brand icon not found");
    }
    return;
  }

  const url = new URL(req.url, cinnyTarget);
  if ((req.method === "GET" || req.method === "HEAD") && /^\/(?:home|login|register|settings)(?:\/|$)/.test(url.pathname)) {
    url.pathname = "/";
    url.search = "";
  }
  const upstream = http.request(
    url,
    { method: req.method, headers: { ...req.headers, host: new URL(cinnyTarget).host } },
    (upstreamRes) => {
      const chunks = [];
      upstreamRes.on("data", (chunk) => chunks.push(chunk));
      upstreamRes.on("end", () => {
        let body = Buffer.concat(chunks);
        const headers = { ...upstreamRes.headers };
        const contentType = String(headers["content-type"] || "");
        if (contentType.includes("text/html")) {
          const injection = `<script>try{Object.entries(${JSON.stringify(session)}).forEach(([k,v])=>localStorage.setItem(k,v));}catch(e){console.error(e);}</script>${profileEnhancer}`;
          body = Buffer.from(body.toString("utf8").replace("</head>", `${injection}</head>`));
          headers["content-length"] = String(body.length);
        }
        res.writeHead(upstreamRes.statusCode || 200, headers);
        res.end(body);
      });
    },
  );
  upstream.on("error", (error) => {
    res.writeHead(502, { "Content-Type": "text/plain" });
    res.end(error.message);
  });
  req.pipe(upstream);
});

matrixProxy.listen(matrixPort, "127.0.0.1", () => {
  console.log(`matrix proxy listening on http://127.0.0.1:${matrixPort}`);
});
cinnyWrapper.listen(cinnyPort, "127.0.0.1", () => {
  console.log(`cinny wrapper listening on http://127.0.0.1:${cinnyPort}`);
});
