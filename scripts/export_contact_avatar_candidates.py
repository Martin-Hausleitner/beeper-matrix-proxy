#!/usr/bin/env python3
"""Export private contact/photo avatar candidates for manual Matrix avatar merging.

The script never writes into the public repository by default. It copies Apple
Contacts avatar blobs into a private application-support directory and records
optional Google Photos/Takeout paths as review candidates.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import re
import sqlite3
from pathlib import Path
from typing import Any


DEFAULT_ADDRESSBOOK = (
    Path.home()
    / "Library"
    / "Application Support"
    / "AddressBook"
    / "AddressBook-v22.abcddb"
)
DEFAULT_OUTPUT_DIR = (
    Path.home()
    / "Library"
    / "Application Support"
    / "matrix-archive-sync"
    / "contact-avatar-candidates"
)
PHOTO_EXTENSIONS = {".jpg", ".jpeg", ".png", ".heic", ".heif", ".webp", ".tif", ".tiff"}


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--addressbook-db", type=Path, default=DEFAULT_ADDRESSBOOK)
    parser.add_argument("--output-dir", type=Path, default=DEFAULT_OUTPUT_DIR)
    parser.add_argument(
        "--google-photos-dir",
        type=Path,
        default=env_path("BEEPER_MATRIX_PROXY_GOOGLE_PHOTOS_DIR")
        or env_path("GOOGLE_PHOTOS_TAKEOUT_DIR"),
        help="Optional local Google Photos/Takeout export folder. No cloud API is called.",
    )
    parser.add_argument("--max-google-photo-candidates", type=int, default=500)
    args = parser.parse_args()

    output_dir = args.output_dir.expanduser()
    apple_dir = output_dir / "apple-contacts"
    output_dir.mkdir(parents=True, exist_ok=True)
    apple_dir.mkdir(parents=True, exist_ok=True)
    chmod_private(output_dir)
    chmod_private(apple_dir)

    candidates: list[dict[str, Any]] = []
    candidates.extend(export_apple_contacts(args.addressbook_db.expanduser(), apple_dir))
    candidates.extend(
        collect_google_photo_candidates(
            args.google_photos_dir,
            args.max_google_photo_candidates,
        )
    )

    report = {
        "schema": "matrix-archive-sync.contact-avatar-candidates.v1",
        "notes": [
            "Private local review file. Do not commit.",
            "Apple Contacts avatars can be used when a contact is explicitly matched.",
            "Google Photos candidates are path-only review hints; no automatic face matching is performed.",
        ],
        "addressbook_db": str(args.addressbook_db.expanduser()),
        "google_photos_dir": str(args.google_photos_dir.expanduser()) if args.google_photos_dir else None,
        "counts": {
            "total": len(candidates),
            "apple_contacts": sum(1 for item in candidates if item["source"] == "apple_contacts"),
            "google_photos_local": sum(1 for item in candidates if item["source"] == "google_photos_local"),
        },
        "candidates": candidates,
    }

    report_path = output_dir / "contact-avatar-candidates.json"
    write_private_json(report_path, report)
    write_override_template(output_dir / "contact-avatar-overrides.template.yaml", candidates)
    print(json.dumps({"report": str(report_path), "counts": report["counts"]}, indent=2))
    return 0


def export_apple_contacts(addressbook_db: Path, apple_dir: Path) -> list[dict[str, Any]]:
    if not addressbook_db.exists():
        return []

    query = """
        SELECT
            Z_PK,
            ZUNIQUEID,
            ZFIRSTNAME,
            ZMIDDLENAME,
            ZLASTNAME,
            ZNICKNAME,
            ZORGANIZATION,
            ZIMAGETYPE,
            ZIMAGEREFERENCE,
            ZIMAGEDATA,
            ZTHUMBNAILIMAGEDATA
        FROM ZABCDRECORD
        WHERE ZIMAGEDATA IS NOT NULL OR ZTHUMBNAILIMAGEDATA IS NOT NULL
        ORDER BY ZLASTNAME, ZFIRSTNAME, Z_PK
    """
    candidates: list[dict[str, Any]] = []
    with sqlite3.connect(f"file:{addressbook_db}?mode=ro", uri=True) as db:
        db.row_factory = sqlite3.Row
        for row in db.execute(query):
            blob = row["ZIMAGEDATA"] or row["ZTHUMBNAILIMAGEDATA"]
            if not blob:
                continue
            body = bytes(blob)
            digest = hashlib.sha256(body).hexdigest()
            ext = image_extension(body)
            display_name = contact_display_name(row)
            file_name = f"{slug(display_name)}-{row['Z_PK']}-{digest[:12]}{ext}"
            avatar_path = apple_dir / file_name
            if not avatar_path.exists():
                avatar_path.write_bytes(body)
                avatar_path.chmod(0o600)
            candidates.append(
                {
                    "source": "apple_contacts",
                    "confidence": "manual-review",
                    "display_name": display_name,
                    "apple_contact_pk": row["Z_PK"],
                    "apple_contact_id": row["ZUNIQUEID"],
                    "avatar_file": str(avatar_path),
                    "sha256": digest,
                    "image_type": row["ZIMAGETYPE"],
                    "image_reference": row["ZIMAGEREFERENCE"],
                }
            )
    return candidates


def collect_google_photo_candidates(root: Path | None, limit: int) -> list[dict[str, Any]]:
    if root is None:
        return []
    root = root.expanduser()
    if not root.exists():
        return []
    candidates: list[dict[str, Any]] = []
    for path in root.rglob("*"):
        if len(candidates) >= limit:
            break
        if not path.is_file() or path.suffix.lower() not in PHOTO_EXTENSIONS:
            continue
        try:
            stat = path.stat()
            digest = file_hash(path)
        except OSError:
            continue
        candidates.append(
            {
                "source": "google_photos_local",
                "confidence": "manual-review",
                "display_name": path.stem,
                "photo_file": str(path),
                "sha256": digest,
                "bytes": stat.st_size,
            }
        )
    return candidates


def write_override_template(path: Path, candidates: list[dict[str, Any]]) -> None:
    lines = [
        "# Private template for BEEPER_MATRIX_PROXY_CONTACT_AVATAR_OVERRIDES.",
        "# Move confirmed candidates into contacts and add exact IDs/aliases.",
        "contacts:",
    ]
    for item in candidates[:25]:
        avatar_file = item.get("avatar_file")
        if not avatar_file:
            continue
        lines.extend(
            [
                f"  - display_name: {yaml_quote(item['display_name'])}",
                "    aliases: []",
                "    beeper_chat_ids: []",
                "    matrix_room_ids: []",
                "    sender_ids: []",
                f"    avatar_file: {yaml_quote(avatar_file)}",
                f"    apple_contact_id: {yaml_quote(str(item.get('apple_contact_id') or ''))}",
                "    confidence: manual-confirmed",
            ]
        )
    body = "\n".join(lines) + "\n"
    path.write_text(body)
    path.chmod(0o600)


def write_private_json(path: Path, value: dict[str, Any]) -> None:
    path.write_text(json.dumps(value, indent=2, ensure_ascii=False) + "\n")
    path.chmod(0o600)


def image_extension(body: bytes) -> str:
    if body.startswith(b"\x89PNG\r\n\x1a\n"):
        return ".png"
    if body.startswith(b"\xff\xd8\xff"):
        return ".jpg"
    if body.startswith(b"II*\x00") or body.startswith(b"MM\x00*"):
        return ".tiff"
    if body.startswith(b"RIFF") and body[8:12] == b"WEBP":
        return ".webp"
    return ".img"


def contact_display_name(row: sqlite3.Row) -> str:
    parts = [row["ZFIRSTNAME"], row["ZMIDDLENAME"], row["ZLASTNAME"]]
    name = " ".join(str(part).strip() for part in parts if part and str(part).strip())
    if name:
        return name
    for key in ("ZNICKNAME", "ZORGANIZATION", "ZUNIQUEID"):
        value = row[key]
        if value and str(value).strip():
            return str(value).strip()
    return f"Apple Contact {row['Z_PK']}"


def file_hash(path: Path) -> str:
    h = hashlib.sha256()
    with path.open("rb") as f:
        for chunk in iter(lambda: f.read(1024 * 1024), b""):
            h.update(chunk)
    return h.hexdigest()


def slug(value: str) -> str:
    cleaned = re.sub(r"[^A-Za-z0-9._-]+", "-", value.strip()).strip("-")
    return cleaned[:80] or "contact"


def yaml_quote(value: str) -> str:
    return json.dumps(value, ensure_ascii=False)


def env_path(name: str) -> Path | None:
    value = os.environ.get(name, "").strip()
    return Path(value).expanduser() if value else None


def chmod_private(path: Path) -> None:
    try:
        path.chmod(0o700)
    except OSError:
        pass


if __name__ == "__main__":
    raise SystemExit(main())
