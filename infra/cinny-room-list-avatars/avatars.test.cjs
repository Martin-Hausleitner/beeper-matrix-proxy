const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");
const vm = require("node:vm");

function fixture(storage = {}) {
  let mutations = 0;
  const node = (tag = "SPAN") => ({
    tagName: tag, dataset: {}, style: {}, children: [],
    classList: { add() {} },
    append(child) { this.children.push(child); mutations++; },
    replaceChildren() { this.children = []; mutations++; },
    getBoundingClientRect() { return { width: 22, height: 22 }; },
  });
  const source = fs.readFileSync(path.join(__dirname, "openclaw-room-list-avatars.js"), "utf8");
  // Expose the actual functions before lifecycle installation; no browser/network.
  const instrumented = source.replace("  const observerRoot =", "  globalThis.fixtureApi = { getAuth, applyAvatarToIconNode }; return;\n  const observerRoot =");
  const context = {
    URL,
    window: { localStorage: {
      getItem: (key) => storage[key] ?? null,
      key: (index) => Object.keys(storage)[index],
      length: Object.keys(storage).length,
    } },
    document: { createElement: (tag) => node(tag.toUpperCase()), head: { append() {} } },
  };
  vm.runInNewContext(instrumented, context);
  return { ...context.fixtureApi, node, mutationCount: () => mutations };
}

test("a ready avatar is stable across observer-driven repaint", () => {
  const f = fixture();
  const icon = f.node();
  f.applyAvatarToIconNode(icon, "blob:fixture-a", "signal");
  const initial = f.mutationCount();
  for (let frame = 0; frame < 5; frame++) f.applyAvatarToIconNode(icon, "blob:fixture-a", "signal");
  assert.equal(f.mutationCount(), initial);
  f.applyAvatarToIconNode(icon, "blob:fixture-b", "signal");
  assert.ok(f.mutationCount() > initial, "a changed source must still repaint");
});

test("unrelated storage URLs cannot receive a Matrix bearer", () => {
  const f = fixture({ unrelated_url: "https://unrelated.invalid", cinny_access_token: "synthetic-token" });
  assert.equal(f.getAuth().homeserver, "");
});

test("canonical Cinny keys and session auth remain supported", () => {
  const direct = fixture({ cinny_hs_base_url: "https://matrix.invalid/", cinny_access_token: "synthetic-direct" }).getAuth();
  assert.equal(direct.homeserver, "https://matrix.invalid");
  assert.equal(direct.accessToken, "synthetic-direct");
  const session = fixture({ cinny_session: JSON.stringify({ baseUrl: "https://session.invalid", accessToken: "synthetic-session", userId: "@fixture:invalid" }) }).getAuth();
  assert.equal(session.homeserver, "https://session.invalid");
  assert.equal(session.accessToken, "synthetic-session");
});
