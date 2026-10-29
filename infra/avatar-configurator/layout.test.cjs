const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");

test("empty and self-only participant lists leave an empty proof layout", () => {
  const html = fs.readFileSync(path.join(__dirname, "index.html"), "utf8");
  const participants = html.slice(html.indexOf("      function parseParticipantLine("), html.indexOf("      function gradientFor("));
  const layout = html.slice(html.indexOf("      function bubbleLayout("), html.indexOf("      async function drawAvatar("));
  const controls = {
    participants: { value: "" }, selfIds: { value: "Fixture Self" },
    excludeSelf: { checked: true }, maxParticipants: { value: "10" },
  };
  const context = vm.createContext({ controls, clamp: (x, min, max) => Math.max(min, Math.min(max, x)) });
  vm.runInContext(participants + layout, context);
  for (const input of ["", "Fixture Self", "  \n  "]) {
    controls.participants.value = input;
    const result = vm.runInContext("bubbleLayout(participantList(), 0, {})", context);
    assert.equal(result.length, 0);
  }
});
