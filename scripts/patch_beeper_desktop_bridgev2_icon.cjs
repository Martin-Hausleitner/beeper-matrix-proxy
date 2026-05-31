#!/usr/bin/env node

const fs = require("fs");
const os = require("os");
const path = require("path");

const appRoot =
  process.env.BEEPER_DESKTOP_APP_ROOT ||
  "/Applications/Beeper Desktop.app/Contents/Resources/app";

const backupMarker = ".bak-openclaw-bridgev2-icon-";
const customIconName = "openclawCustomPhoto";

const mimeByExt = new Map([
  [".apng", "image/apng"],
  [".avif", "image/avif"],
  [".gif", "image/gif"],
  [".jpg", "image/jpeg"],
  [".jpeg", "image/jpeg"],
  [".png", "image/png"],
  [".svg", "image/svg+xml"],
  [".webp", "image/webp"],
]);

function argValue(name) {
  const index = process.argv.indexOf(name);
  if (index === -1) return undefined;
  return process.argv[index + 1];
}

function listLoadStoreFiles(root = appRoot) {
  const dirs = [
    path.join(root, "build", "renderer"),
    path.join(root, "build-browser"),
  ];
  const files = [];
  for (const dir of dirs) {
    if (!fs.existsSync(dir)) continue;
    for (const name of fs.readdirSync(dir)) {
      if (name.startsWith("load-stores-") && name.endsWith(".js")) {
        const file = path.join(dir, name);
        const text = fs.readFileSync(file, "utf8");
        if (
          text.includes("bridge-generic") &&
          text.includes("hungryserv") &&
          text.includes('displayName:"Generic Matrix Bridge"')
        ) {
          files.push(file);
        }
      }
    }
  }
  return files;
}

function makeBackupPath(file) {
  const stamp = new Date().toISOString().replace(/[-:.TZ]/g, "").slice(0, 14);
  return `${file}${backupMarker}${stamp}`;
}

function toDataUri(imagePath) {
  const absolute = path.resolve(imagePath);
  const ext = path.extname(absolute).toLowerCase();
  const mime = mimeByExt.get(ext);
  if (!mime) {
    throw new Error(`Unsupported image extension "${ext}". Use PNG, JPG, SVG, GIF, AVIF, or WebP.`);
  }
  const bytes = fs.readFileSync(absolute);
  return {
    absolute,
    dataUri: `data:${mime};base64,${bytes.toString("base64")}`,
  };
}

function customPhotoSvg(dataUri) {
  return `<svg width="28" height="28" viewBox="0 0 28 28" fill="none" xmlns="http://www.w3.org/2000/svg"><defs><clipPath id="openclawBridgeV2IconClip"><rect width="28" height="28" rx="10"/></clipPath></defs><rect width="28" height="28" rx="10" fill="#262830"/><image href="${dataUri}" width="28" height="28" preserveAspectRatio="xMidYMid slice" clip-path="url(#openclawBridgeV2IconClip)"/></svg>`;
}

function setBridgeV2ToGenericPlatform(text) {
  const pattern =
    /beeper:([A-Za-z_$][\w$]*),hungryserv:\1,(?:bridgev2:[A-Za-z_$][\w$]*,)?dummybridge:([A-Za-z_$][\w$]*),/;
  if (!pattern.test(text)) {
    throw new Error("Could not find Beeper platform mapping table.");
  }
  return text.replace(pattern, "beeper:$1,hungryserv:$1,bridgev2:$2,dummybridge:$2,");
}

function setGenericBridgeBrand(text, svg) {
  const iconBackground = JSON.stringify(svg);
  const display = text.indexOf('displayName:"Generic Matrix Bridge"');
  if (display === -1) {
    throw new Error("Could not find Generic Matrix Bridge brand definition.");
  }

  const brandStart = text.indexOf("brand:{", display);
  const brandEnd = text.indexOf('},loginMode:"bridgev2"', brandStart);
  if (brandStart === -1 || brandEnd === -1) {
    throw new Error("Could not replace Generic Matrix Bridge brand block.");
  }

  const brand = `brand:{iconBackground:${iconBackground},iconName:"${customIconName}"}`;
  return `${text.slice(0, brandStart)}${brand}${text.slice(brandEnd + 1)}`;
}

function patchText(text, svg) {
  return setGenericBridgeBrand(setBridgeV2ToGenericPlatform(text), svg);
}

function patchFile(file, svg) {
  const original = fs.readFileSync(file, "utf8");
  const patched = patchText(original, svg);
  if (patched === original) return { file, status: "unchanged" };

  const backup = makeBackupPath(file);
  fs.copyFileSync(file, backup);
  fs.writeFileSync(file, patched);
  return { file, status: "patched", backup };
}

function checkFile(file) {
  const text = fs.readFileSync(file, "utf8");
  const bridgev2UsesGeneric =
    /beeper:([A-Za-z_$][\w$]*),hungryserv:\1,bridgev2:([A-Za-z_$][\w$]*),dummybridge:\2,/.test(text);
  return {
    file,
    bridgev2UsesGeneric,
    customPhotoIcon: text.includes(`iconName:"${customIconName}"`),
  };
}

function restoreFile(file) {
  const dir = path.dirname(file);
  const base = path.basename(file);
  const backups = fs
    .readdirSync(dir)
    .filter((name) => name.startsWith(`${base}${backupMarker}`))
    .sort();

  if (backups.length === 0) return { file, status: "no-backup" };

  const latest = path.join(dir, backups[backups.length - 1]);
  fs.copyFileSync(latest, file);
  return { file, status: "restored", backup: latest };
}

function runSelfTest() {
  const fixture = `const Ft="bridge-generic",Mc={beeper:us,hungryserv:us,dummybridge:Ft,androidsms:Ft};const Lo={name:Ft,version:"0.0.1",displayName:"Generic Matrix Bridge",network:"generic",brand:{iconBackground:"#549b57",iconName:"generic"},loginMode:"bridgev2",bridgeProvider:"cloud"};`;
  const svg = customPhotoSvg("data:image/png;base64,dGVzdA==");
  const patched = patchText(fixture, svg);
  const checks = {
    bridgev2MapsToGeneric: patched.includes("beeper:us,hungryserv:us,bridgev2:Ft,dummybridge:Ft"),
    customIconName: patched.includes(`iconName:"${customIconName}"`),
    embeddedImage: patched.includes("data:image/png;base64,dGVzdA=="),
  };
  const ok = Object.values(checks).every(Boolean);

  const imagePath = path.join(os.tmpdir(), "openclaw-bridgev2-icon-test.svg");
  fs.writeFileSync(
    imagePath,
    `<svg xmlns="http://www.w3.org/2000/svg" width="64" height="64"><rect width="64" height="64" fill="#3563ff"/><text x="32" y="39" text-anchor="middle" fill="white" font-size="22" font-family="Arial">MP</text></svg>`,
  );
  const data = toDataUri(imagePath);

  return {
    ok,
    checks,
    sampleImageAccepted: data.dataUri.startsWith("data:image/svg+xml;base64,"),
  };
}

function main() {
  const mode = process.argv.includes("--check")
    ? "check"
    : process.argv.includes("--restore")
      ? "restore"
      : process.argv.includes("--self-test")
        ? "self-test"
        : "patch";

  if (mode === "self-test") {
    const result = runSelfTest();
    console.log(JSON.stringify(result, null, 2));
    process.exit(result.ok && result.sampleImageAccepted ? 0 : 1);
  }

  const files = listLoadStoreFiles();
  if (files.length === 0) {
    console.error(`No Beeper Desktop load-store files found below ${appRoot}`);
    process.exit(1);
  }

  if (mode === "check") {
    console.log(JSON.stringify({ appRoot, mode, results: files.map(checkFile) }, null, 2));
    return;
  }

  if (mode === "restore") {
    console.log(JSON.stringify({ appRoot, mode, results: files.map(restoreFile) }, null, 2));
    return;
  }

  const image = argValue("--image");
  if (!image) {
    console.error("Missing --image <path>. Pass any local PNG, JPG, SVG, GIF, AVIF, or WebP image.");
    process.exit(2);
  }

  const { absolute, dataUri } = toDataUri(image);
  const svg = customPhotoSvg(dataUri);
  const results = files.map((file) => patchFile(file, svg));
  console.log(JSON.stringify({ appRoot, mode, image: absolute, results }, null, 2));
}

main();
