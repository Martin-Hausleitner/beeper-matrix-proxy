(() => {
  "use strict";

  const VERSION = "openclaw-room-list-avatars-v1";
  const AVATAR_SIZE = 64;
  const roomAvatarCache = new Map();
  const pendingRooms = new Set();

  const style = document.createElement("style");
  style.dataset.openclawRoomListAvatars = VERSION;
  style.textContent = `
    .openclaw-room-list-avatar {
      inline-size: 1.375rem;
      block-size: 1.375rem;
      min-inline-size: 1.375rem;
      border-radius: 0.42rem;
      overflow: hidden;
      display: inline-flex;
      align-items: center;
      justify-content: center;
      background: color-mix(in srgb, var(--bg-surface-low, #1f1f22), #fff 6%);
      box-shadow: 0 1px 2px rgb(0 0 0 / 28%);
      transform: translateZ(0);
    }

    .openclaw-room-list-avatar > img {
      inline-size: 100%;
      block-size: 100%;
      object-fit: cover;
      display: block;
    }
  `;
  document.head.append(style);

  function readLocalStorageJson(key) {
    const value = window.localStorage.getItem(key);
    if (!value) return null;
    try {
      return JSON.parse(value);
    } catch {
      return null;
    }
  }

  function findLocalStorageValue(candidateKeys, fallbackPredicate) {
    for (const key of candidateKeys) {
      const value = window.localStorage.getItem(key);
      if (typeof value === "string" && value.length > 0) return value;
    }

    for (let index = 0; index < window.localStorage.length; index += 1) {
      const key = window.localStorage.key(index);
      if (!key) continue;
      const value = window.localStorage.getItem(key);
      if (typeof value === "string" && fallbackPredicate(key, value)) return value;
    }

    return "";
  }

  function getAuth() {
    const directHomeserver = findLocalStorageValue(
      ["cinny_hs_base_url", "mx_hs_url", "homeserver"],
      (_key, value) => /^https?:\/\//.test(value) && value.includes("_matrix") === false,
    );
    const directToken = findLocalStorageValue(
      ["cinny_access_token", "mx_access_token", "access_token"],
      (key, value) => key.toLowerCase().includes("token") && value.startsWith("syt_"),
    );

    const session = readLocalStorageJson("cinny_session") || readLocalStorageJson("mx_session");
    const homeserver =
      directHomeserver ||
      session?.baseUrl ||
      session?.homeserverUrl ||
      session?.homeserver?.baseUrl ||
      "";
    const accessToken =
      directToken ||
      session?.accessToken ||
      session?.access_token ||
      session?.credentials?.accessToken ||
      "";

    return {
      homeserver: homeserver.replace(/\/+$/, ""),
      accessToken,
    };
  }

  function roomIdFromHref(href) {
    try {
      const url = new URL(href, window.location.origin);
      const segments = url.pathname.split("/").filter(Boolean);
      const maybeRoomId = decodeURIComponent(segments[segments.length - 1] || "");
      return maybeRoomId.startsWith("!") ? maybeRoomId : "";
    } catch {
      return "";
    }
  }

  function mediaEndpoints(homeserver, mxcUrl) {
    if (!mxcUrl.startsWith("mxc://")) return "";
    const media = mxcUrl.slice("mxc://".length);
    const slash = media.indexOf("/");
    if (slash === -1) return "";
    const serverName = encodeURIComponent(media.slice(0, slash));
    const mediaId = encodeURIComponent(media.slice(slash + 1));
    return [
      `${homeserver}/_matrix/client/v1/media/thumbnail/${serverName}/${mediaId}` +
        `?width=${AVATAR_SIZE}&height=${AVATAR_SIZE}&method=crop&allow_redirect=true`,
      `${homeserver}/_matrix/client/v1/media/download/${serverName}/${mediaId}` +
        "?allow_redirect=true",
    ];
  }

  function hashHue(seed) {
    let hash = 2166136261;
    for (let index = 0; index < seed.length; index += 1) {
      hash ^= seed.charCodeAt(index);
      hash = Math.imul(hash, 16777619);
    }
    return Math.abs(hash) % 360;
  }

  function initialsFromName(name) {
    const cleaned = name
      .replace(/^Beeper(?: BotE2E| Test)?:\[[^\]]+\]\s*/i, "")
      .replace(/^Beeper:\[[^\]]+\]\s*/i, "")
      .replace(/[^\p{L}\p{N}\s-]/gu, " ")
      .trim();
    const words = cleaned.split(/\s+/).filter(Boolean);
    const initials = words.slice(0, 2).map((word) => Array.from(word)[0]).join("");
    return (initials || "#").toUpperCase();
  }

  function fallbackAvatarDataUrl(name, roomId) {
    const initials = initialsFromName(name);
    const hue = hashHue(`${roomId}:${name}`);
    const hue2 = (hue + 34) % 360;
    const svg = `
      <svg xmlns="http://www.w3.org/2000/svg" width="64" height="64" viewBox="0 0 64 64">
        <defs>
          <linearGradient id="g" x1="10" y1="8" x2="56" y2="60">
            <stop offset="0" stop-color="hsl(${hue} 86% 57%)"/>
            <stop offset="1" stop-color="hsl(${hue2} 80% 45%)"/>
          </linearGradient>
        </defs>
        <rect width="64" height="64" rx="14" fill="url(#g)"/>
        <text
          x="32"
          y="38"
          text-anchor="middle"
          font-family="Inter, Arial, Helvetica, sans-serif"
          font-size="${initials.length > 1 ? 20 : 24}"
          font-weight="700"
          fill="white"
        >${initials.replace(/[<&>"]/g, "")}</text>
      </svg>`;
    return `data:image/svg+xml;charset=utf-8,${encodeURIComponent(svg)}`;
  }

  async function fetchRoomAvatar(roomId) {
    const { homeserver, accessToken } = getAuth();
    if (!homeserver || !accessToken) return null;

    const stateUrl =
      `${homeserver}/_matrix/client/v3/rooms/${encodeURIComponent(roomId)}` +
      "/state/m.room.avatar/";

    const stateResponse = await fetch(stateUrl, {
      headers: { Authorization: `Bearer ${accessToken}` },
      credentials: "omit",
    });

    if (stateResponse.status === 404) return null;
    if (!stateResponse.ok) throw new Error(`avatar state ${stateResponse.status}`);

    const state = await stateResponse.json();
    const mediaUrl = typeof state.url === "string" ? state.url : "";
    const endpoints = mediaEndpoints(homeserver, mediaUrl);
    if (!endpoints) return null;

    let lastStatus = 0;
    for (const endpoint of endpoints) {
      const mediaResponse = await fetch(endpoint, {
        headers: { Authorization: `Bearer ${accessToken}` },
        credentials: "omit",
      });
      lastStatus = mediaResponse.status;
      if (!mediaResponse.ok) continue;

      const blob = await mediaResponse.blob();
      return URL.createObjectURL(blob);
    }

    throw new Error(`avatar media ${lastStatus}`);
  }

  function findIconNode(anchor) {
    const titleLine = anchor.querySelector("p.t4fedto")?.firstElementChild;
    const firstNode = titleLine?.firstElementChild;
    if (!firstNode || firstNode.dataset.openclawRoomAvatar === "ready") return firstNode || null;
    if (firstNode.querySelector("svg")) return firstNode;
    return null;
  }

  function applyAvatar(anchor, avatarObjectUrl) {
    const iconNode = findIconNode(anchor);
    if (!iconNode || !avatarObjectUrl) return;

    iconNode.classList.add("openclaw-room-list-avatar");
    iconNode.dataset.openclawRoomAvatar = "ready";
    iconNode.replaceChildren();

    const img = document.createElement("img");
    img.src = avatarObjectUrl;
    img.alt = "";
    img.decoding = "async";
    img.loading = "lazy";
    iconNode.append(img);
  }

  async function patchRoom(anchor, roomId) {
    if (pendingRooms.has(roomId)) return;
    pendingRooms.add(roomId);

    try {
      if (!roomAvatarCache.has(roomId)) {
        const avatar = await fetchRoomAvatar(roomId);
        roomAvatarCache.set(
          roomId,
          avatar || fallbackAvatarDataUrl(anchor.innerText.trim(), roomId),
        );
      }

      const avatarObjectUrl = roomAvatarCache.get(roomId);
      if (avatarObjectUrl) applyAvatar(anchor, avatarObjectUrl);
    } catch (error) {
      roomAvatarCache.set(roomId, null);
      console.debug("[OpenClaw Cinny avatars] skipped room avatar", roomId, error);
    } finally {
      pendingRooms.delete(roomId);
    }
  }

  function patchVisibleRooms() {
    const anchors = document.querySelectorAll("a.t4fedt2[href]");
    anchors.forEach((anchor) => {
      const roomId = roomIdFromHref(anchor.getAttribute("href") || "");
      if (!roomId) return;
      anchor.dataset.openclawRoomAvatarRoom = roomId;

      const cachedAvatar = roomAvatarCache.get(roomId);
      if (cachedAvatar) {
        applyAvatar(anchor, cachedAvatar);
        return;
      }

      const iconNode = findIconNode(anchor);
      if (!iconNode || !iconNode.querySelector("svg")) return;
      patchRoom(anchor, roomId);
    });
  }

  let scheduled = false;
  function schedulePatch() {
    if (scheduled) return;
    scheduled = true;
    window.requestAnimationFrame(() => {
      scheduled = false;
      patchVisibleRooms();
    });
  }

  const observerRoot = document.getElementById("root") || document.body;
  const observer = new MutationObserver(schedulePatch);
  observer.observe(observerRoot, { childList: true, subtree: true });

  window.addEventListener("focus", schedulePatch);
  window.addEventListener("hashchange", schedulePatch);
  window.addEventListener("popstate", schedulePatch);
  window.setInterval(schedulePatch, 2000);
  schedulePatch();
})();
