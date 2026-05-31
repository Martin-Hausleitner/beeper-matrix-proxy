(() => {
  "use strict";

  const VERSION = "openclaw-room-list-avatars-v1";
  const AVATAR_SIZE = 64;
  const BRAND_ICON_BASE = "/assets/openclaw-brand-icons";
  const USER_CONFIG = window.OPENCLAW_CINNY_AVATARS || {};
  const badgeScale = Number.isFinite(Number(USER_CONFIG.badgeScale))
    ? Math.max(0.5, Math.min(0.8, Number(USER_CONFIG.badgeScale)))
    : 0.64;
  const badgeOffsetPx = Number.isFinite(Number(USER_CONFIG.badgeOffsetPx))
    ? Math.max(-5, Math.min(3, Number(USER_CONFIG.badgeOffsetPx)))
    : -3;
  const PLATFORM_KEYS = [
    "whatsapp",
    "signal",
    "telegram",
    "discord",
    "instagram",
    "messenger",
    "slack",
    "x",
    "linkedin",
    "matrix",
    "beeper",
    "email",
    "imessage",
  ];
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
      overflow: visible;
      display: inline-flex;
      align-items: center;
      justify-content: center;
      background: color-mix(in srgb, var(--bg-surface-low, #1f1f22), #fff 6%);
      box-shadow: 0 1px 2px rgb(0 0 0 / 28%);
      transform: translateZ(0);
      position: relative;
    }

    .openclaw-room-list-avatar > img.openclaw-avatar-image {
      inline-size: 100%;
      block-size: 100%;
      object-fit: cover;
      display: block;
      border-radius: inherit;
    }

    .openclaw-room-list-avatar > img.openclaw-avatar-badge {
      position: absolute;
      inline-size: calc(${badgeScale} * 100%);
      block-size: calc(${badgeScale} * 100%);
      right: ${badgeOffsetPx}px;
      bottom: ${badgeOffsetPx}px;
      object-fit: contain;
      border-radius: 30%;
      box-shadow: 0 1px 3px rgb(0 0 0 / 34%);
      pointer-events: none;
      z-index: 2;
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
    const userId =
      session?.userId ||
      session?.user_id ||
      session?.mxid ||
      session?.credentials?.userId ||
      "";

    return {
      homeserver: homeserver.replace(/\/+$/, ""),
      accessToken,
      userId,
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

  function currentRoomIdFromLocation() {
    const segments = window.location.pathname.split("/").filter(Boolean);
    const maybeRoomId = decodeURIComponent(segments[segments.length - 1] || "");
    return maybeRoomId.startsWith("!") ? maybeRoomId : "";
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

  function platformKeyFromText(text) {
    const normalized = String(text || "")
      .toLowerCase()
      .replace(/[._-]+/g, " ");
    if (/\bwhatsapp\b/.test(normalized)) return "whatsapp";
    if (/\bsignal\b/.test(normalized)) return "signal";
    if (/\btelegram\b/.test(normalized)) return "telegram";
    if (/\bdiscord\b/.test(normalized)) return "discord";
    if (/\binstagram\b/.test(normalized)) return "instagram";
    if (/\bmessenger\b/.test(normalized)) return "messenger";
    if (/\bslack\b/.test(normalized)) return "slack";
    if (/\blinkedin\b/.test(normalized)) return "linkedin";
    if (/\bimessage\b|\bbluebubbles\b/.test(normalized)) return "imessage";
    if (/\bmatrix\b|\bbeeper matrix\b/.test(normalized)) return "matrix";
    if (/\bbeeper\b|\bbridgev2\b/.test(normalized)) return "beeper";
    if (/\bemail\b|\bmail\b/.test(normalized)) return "email";
    if (/\bx\b|\btwitter\b/.test(normalized)) return "x";
    return "";
  }

  function visibleTextNear(leftMin, leftMax, topMin, topMax) {
    return Array.from(document.querySelectorAll("button, div, span, p"))
      .filter((node) => {
        const rect = node.getBoundingClientRect();
        return (
          rect.width > 0 &&
          rect.height > 0 &&
          rect.left >= leftMin &&
          rect.left <= leftMax &&
          rect.top >= topMin &&
          rect.top <= topMax
        );
      })
      .map((node) => (node.textContent || "").trim())
      .filter(Boolean)
      .join("\n");
  }

  function currentPlatformKey() {
    const headerPlatform = platformKeyFromText(visibleTextNear(250, 900, 0, 72));
    if (headerPlatform) return headerPlatform;

    const spacePlatform = platformKeyFromText(visibleTextNear(42, 280, 40, 170));
    if (spacePlatform) return spacePlatform;

    const pageText = visibleTextNear(0, 360, 0, window.innerHeight);
    return platformKeyFromText(pageText);
  }

  function platformBadgeUrl(platformKey) {
    const key = PLATFORM_KEYS.includes(platformKey) ? platformKey : "";
    return key ? `${BRAND_ICON_BASE}/${key}.png` : "";
  }

  async function fetchMatrixMediaObjectUrl(homeserver, accessToken, mediaUrl) {
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
    return fetchMatrixMediaObjectUrl(homeserver, accessToken, mediaUrl);
  }

  async function fetchMemberAvatar(roomId, roomName, options = {}) {
    const { homeserver, accessToken, userId } = getAuth();
    if (!homeserver || !accessToken) return null;

    const membersUrl = `${homeserver}/_matrix/client/v3/rooms/${encodeURIComponent(roomId)}/joined_members`;
    const membersResponse = await fetch(membersUrl, {
      headers: { Authorization: `Bearer ${accessToken}` },
      credentials: "omit",
    });
    if (!membersResponse.ok) return null;

    const body = await membersResponse.json();
    const joined = body?.joined && typeof body.joined === "object" ? body.joined : {};
    const normalizedRoomName = roomName.trim().toLowerCase();
    const candidates = Object.entries(joined)
      .filter(([memberId]) => memberId !== userId)
      .map(([memberId, member]) => ({
        memberId,
        displayName: String(member?.display_name || ""),
        avatarUrl: String(member?.avatar_url || ""),
      }))
      .filter((member) => member.avatarUrl.startsWith("mxc://"));

    const exactMatch = candidates.find(
      (member) => member.displayName.trim().toLowerCase() === normalizedRoomName,
    );
    const preferred = exactMatch || (options.exactOnly ? null : candidates[0]);
    if (!preferred) return null;

    return fetchMatrixMediaObjectUrl(homeserver, accessToken, preferred.avatarUrl);
  }

  async function fetchBestAvatar(roomId, roomName) {
    const exactMemberAvatar = await fetchMemberAvatar(roomId, roomName, { exactOnly: true });
    if (exactMemberAvatar) return exactMemberAvatar;

    const roomAvatar = await fetchRoomAvatar(roomId);
    if (roomAvatar) return roomAvatar;

    const memberAvatar = await fetchMemberAvatar(roomId, roomName);
    if (memberAvatar) return memberAvatar;

    return null;
  }

  function findIconNode(anchor) {
    const titleLine = anchor.querySelector("p.t4fedto")?.firstElementChild;
    const firstNode = titleLine?.firstElementChild;
    if (!firstNode || firstNode.dataset.openclawRoomAvatar === "ready") return firstNode || null;
    return firstNode;
  }

  function applyAvatarToIconNode(iconNode, avatarObjectUrl, platformKey = currentPlatformKey()) {
    if (!iconNode || !avatarObjectUrl) return;

    if (iconNode.tagName === "IMG") {
      const wrapper = document.createElement("span");
      wrapper.className = "openclaw-room-list-avatar";
      wrapper.dataset.openclawRoomAvatar = "ready";
      wrapper.dataset.openclawRoomAvatarPlatform = platformKey || "";
      wrapper.style.inlineSize = `${Math.max(22, Math.round(iconNode.getBoundingClientRect().width || 22))}px`;
      wrapper.style.blockSize = `${Math.max(22, Math.round(iconNode.getBoundingClientRect().height || 22))}px`;

      const img = document.createElement("img");
      img.src = avatarObjectUrl;
      img.alt = iconNode.alt || "";
      img.decoding = "async";
      img.loading = "lazy";
      img.className = "openclaw-avatar-image";
      wrapper.append(img);

      const badgeUrl = platformBadgeUrl(platformKey);
      if (badgeUrl) {
        const badge = document.createElement("img");
        badge.src = badgeUrl;
        badge.alt = "";
        badge.decoding = "async";
        badge.loading = "lazy";
        badge.className = "openclaw-avatar-badge";
        wrapper.append(badge);
      }

      iconNode.replaceWith(wrapper);
      return;
    }

    iconNode.classList.add("openclaw-room-list-avatar");
    iconNode.dataset.openclawRoomAvatar = "ready";
    iconNode.dataset.openclawRoomAvatarPlatform = platformKey || "";
    iconNode.replaceChildren();

    const img = document.createElement("img");
    img.src = avatarObjectUrl;
    img.alt = "";
    img.decoding = "async";
    img.loading = "lazy";
    img.className = "openclaw-avatar-image";
    iconNode.append(img);

    const badgeUrl = platformBadgeUrl(platformKey);
    if (badgeUrl) {
      const badge = document.createElement("img");
      badge.src = badgeUrl;
      badge.alt = "";
      badge.decoding = "async";
      badge.loading = "lazy";
      badge.className = "openclaw-avatar-badge";
      iconNode.append(badge);
    }
  }

  function applyAvatar(anchor, avatarObjectUrl) {
    applyAvatarToIconNode(findIconNode(anchor), avatarObjectUrl);
  }

  function isPlatformImage(img) {
    const text = `${img.alt || ""} ${img.closest("button, a")?.textContent || ""}`;
    return Boolean(platformKeyFromText(text));
  }

  function shouldDecorateNativeAvatarImage(img, platformKey) {
    if (!platformKey || !img?.src) return false;
    if (img.classList.contains("openclaw-avatar-image")) return false;
    if (img.classList.contains("openclaw-avatar-badge")) return false;
    if (img.closest(".openclaw-room-list-avatar")) return false;
    if (isPlatformImage(img)) return false;

    const rect = img.getBoundingClientRect();
    if (rect.width < 18 || rect.width > 64 || rect.height < 18 || rect.height > 64) return false;
    if (Math.abs(rect.width - rect.height) > 10) return false;

    // Keep the left app rail untouched; those icons are the spaces/platform selectors themselves.
    if (rect.left < 42) return false;

    return true;
  }

  function decorateNativeAvatarImage(img, platformKey) {
    if (!shouldDecorateNativeAvatarImage(img, platformKey)) return;

    const rect = img.getBoundingClientRect();
    const wrapper = document.createElement("span");
    wrapper.className = "openclaw-room-list-avatar";
    wrapper.dataset.openclawRoomAvatar = "ready";
    wrapper.dataset.openclawRoomAvatarPlatform = platformKey;
    wrapper.style.inlineSize = `${Math.max(22, Math.round(rect.width || 22))}px`;
    wrapper.style.blockSize = `${Math.max(22, Math.round(rect.height || 22))}px`;

    const avatar = img.cloneNode(true);
    avatar.classList.add("openclaw-avatar-image");
    wrapper.append(avatar);

    const badgeUrl = platformBadgeUrl(platformKey);
    if (badgeUrl) {
      const badge = document.createElement("img");
      badge.src = badgeUrl;
      badge.alt = "";
      badge.decoding = "async";
      badge.loading = "lazy";
      badge.className = "openclaw-avatar-badge";
      wrapper.append(badge);
    }

    img.replaceWith(wrapper);
  }

  function decorateVisibleNativeAvatars() {
    const platformKey = currentPlatformKey();
    if (!platformKey) return;
    document.querySelectorAll("img").forEach((img) => decorateNativeAvatarImage(img, platformKey));
  }

  function isHeaderAvatarCandidate(node, roomName) {
    if (node.closest("a.t4fedt2")) return false;

    const rect = node.getBoundingClientRect();
    if (rect.width < 20 || rect.width > 58 || rect.height < 20 || rect.height > 58) return false;
    if (Math.abs(rect.width - rect.height) > 10) return false;

    const roomInitials = initialsFromName(roomName);
    const text = (node.textContent || "").trim();
    return (
      text === "#" ||
      text === roomInitials ||
      /^[\p{L}\p{N}]{1,3}$/u.test(text) ||
      node.tagName === "IMG" ||
      Boolean(node.querySelector("svg, img"))
    );
  }

  function headerAvatarCandidateScore(node, roomName) {
    const roomInitials = initialsFromName(roomName);
    const text = (node.textContent || "").trim();
    if (text === "#") return 0;
    if (text === roomInitials) return 1;
    if (/^[\p{L}\p{N}]{1,3}$/u.test(text)) return 2;
    if (node.tagName !== "IMG" && node.querySelector("svg, img")) return 3;
    if (node.dataset.openclawRoomAvatar === "ready") return 4;
    if (node.tagName === "IMG") return 5;
    return 6;
  }

  function bestHeaderAvatarCandidate(candidates, roomName) {
    return candidates
      .sort((left, right) => {
        const scoreDelta =
          headerAvatarCandidateScore(left, roomName) -
          headerAvatarCandidateScore(right, roomName);
        if (scoreDelta !== 0) return scoreDelta;
        return left.getBoundingClientRect().left - right.getBoundingClientRect().left;
      })[0] || null;
  }

  function findHeaderIconNode(header, roomName) {
    const nodes = Array.from(document.querySelectorAll("span, div, button, img"));
    const headerRect = header?.getBoundingClientRect();

    if (headerRect) {
      const relativeCandidates = nodes.filter((node) => {
        if (!isHeaderAvatarCandidate(node, roomName)) return false;

        const rect = node.getBoundingClientRect();
        if (rect.top < headerRect.top - 12 || rect.top > headerRect.bottom + 12) return false;
        return rect.left >= headerRect.left - 48 && rect.left <= headerRect.left + 96;
      });
      const relativeCandidate = bestHeaderAvatarCandidate(relativeCandidates, roomName);
      if (relativeCandidate) return relativeCandidate;
    }

    const globalCandidates = nodes.filter((node) => {
      if (!isHeaderAvatarCandidate(node, roomName)) return false;
      const rect = node.getBoundingClientRect();
      return rect.top >= 0 && rect.top <= 92 && rect.left >= 258 && rect.left <= 395;
    });
    return bestHeaderAvatarCandidate(globalCandidates, roomName);
  }

  async function patchRoom(anchor, roomId) {
    if (pendingRooms.has(roomId)) return;
    pendingRooms.add(roomId);

    try {
      if (!roomAvatarCache.has(roomId)) {
        const avatar = await fetchBestAvatar(roomId, anchor.innerText.trim());
        roomAvatarCache.set(
          roomId,
          avatar || fallbackAvatarDataUrl(anchor.innerText.trim(), roomId),
        );
      }

      const avatarObjectUrl = roomAvatarCache.get(roomId);
      if (avatarObjectUrl) applyAvatar(anchor, avatarObjectUrl);
    } catch (error) {
      roomAvatarCache.set(roomId, fallbackAvatarDataUrl(anchor.innerText.trim(), roomId));
      const avatarObjectUrl = roomAvatarCache.get(roomId);
      if (avatarObjectUrl) applyAvatar(anchor, avatarObjectUrl);
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
      if (!iconNode) return;
      patchRoom(anchor, roomId);
    });
  }

  async function patchCurrentRoomHeader() {
    const roomId = currentRoomIdFromLocation();
    if (!roomId) return;

    const activeRoomLink = Array.from(document.querySelectorAll("a.t4fedt2[href]")).find(
      (anchor) => roomIdFromHref(anchor.getAttribute("href") || "") === roomId,
    );
    const activeRoomLines = (activeRoomLink?.innerText || "")
      .split("\n")
      .map((line) => line.trim())
      .filter(Boolean);
    const roomName =
      activeRoomLines[activeRoomLines.length - 1] ||
      document.title.replace(/\s+-\s+Cinny$/i, "").trim() ||
      roomId;

    if (!roomAvatarCache.has(roomId) && !pendingRooms.has(roomId)) {
      pendingRooms.add(roomId);
      try {
        const avatar = await fetchBestAvatar(roomId, roomName);
        roomAvatarCache.set(roomId, avatar || fallbackAvatarDataUrl(roomName, roomId));
      } catch (error) {
        roomAvatarCache.set(roomId, fallbackAvatarDataUrl(roomName, roomId));
        console.debug("[OpenClaw Cinny avatars] skipped current room avatar", roomId, error);
      } finally {
        pendingRooms.delete(roomId);
      }
    }

    const avatarObjectUrl = roomAvatarCache.get(roomId);
    if (!avatarObjectUrl) return;

    const candidates = Array.from(document.querySelectorAll("div, header, section")).filter((node) => {
      if (node.closest("a.t4fedt2")) return false;
      const rect = node.getBoundingClientRect();
      if (rect.width <= 0 || rect.height <= 0) return false;
      if (rect.left < 240 || rect.top < -8 || rect.top > 96 || rect.height > 90) return false;
      return (node.innerText || "").includes(roomName);
    });

    const header = candidates
      .sort((left, right) => left.getBoundingClientRect().height - right.getBoundingClientRect().height)[0];
    if (!header) return;

    const iconNode = findHeaderIconNode(header, roomName);

    if (iconNode) applyAvatarToIconNode(iconNode, avatarObjectUrl);
  }

  let scheduled = false;
  function schedulePatch() {
    if (scheduled) return;
    scheduled = true;
    window.requestAnimationFrame(() => {
      scheduled = false;
      patchVisibleRooms();
      patchCurrentRoomHeader();
      decorateVisibleNativeAvatars();
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
