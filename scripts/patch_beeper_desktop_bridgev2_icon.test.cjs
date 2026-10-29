const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const vm = require("node:vm");

test("two patches in one second preserve both backups and restore the newest", (t) => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "icon-backup-test-"));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  const file = path.join(dir, "fixture.js");
  const original = 'const Ft="bridge-generic",Mc={beeper:us,hungryserv:us,dummybridge:Ft,androidsms:Ft};const Lo={displayName:"Generic Matrix Bridge",brand:{iconBackground:"#123456",iconName:"generic"},loginMode:"bridgev2"};';
  fs.writeFileSync(file, original);
  const source = fs.readFileSync(path.join(__dirname, "patch_beeper_desktop_bridgev2_icon.cjs"), "utf8").replace(/main\(\);\s*$/, "");
  const context = vm.createContext({
    require, file, process: { env: {}, argv: [] },
    Date: class extends Date { constructor() { super("2026-10-29T12:00:00Z"); } },
  });
  vm.runInContext(source, context);
  const first = vm.runInContext('patchFile(file, "first-svg")', context);
  const firstPatched = fs.readFileSync(file, "utf8");
  const second = vm.runInContext('patchFile(file, "second-svg")', context);
  assert.notEqual(first.backup, second.backup);
  assert.equal(fs.readFileSync(first.backup, "utf8"), original);
  assert.equal(fs.readFileSync(second.backup, "utf8"), firstPatched);
  assert.equal(fs.readdirSync(dir).filter((name) => name.includes(".bak-")).length, 2);
  const restored = vm.runInContext("restoreFile(file)", context);
  assert.equal(restored.backup, second.backup);
  assert.equal(fs.readFileSync(file, "utf8"), firstPatched);
});
