#!/usr/bin/env python3
"""Map ActivityWatch window/browser events to local software projects.

This script deliberately keeps project rules and reports local/private. It reads
ActivityWatch buckets, applies deterministic matching rules, and writes aggregate
time totals without storing raw window titles unless explicitly requested.
"""

from __future__ import annotations

import argparse
import datetime as dt
import hashlib
import json
import os
import pathlib
import re
import sys
import urllib.error
import urllib.parse
import urllib.request
from collections import defaultdict
from typing import Any


DEFAULT_CONFIG = pathlib.Path.home() / "Library/Application Support/openclaw/project-activity/projects.json"
DEFAULT_REPORT_DIR = pathlib.Path.home() / "Library/Application Support/openclaw/project-activity/reports"
DEFAULT_AW_URL = "http://127.0.0.1:5600"


def main() -> int:
    parser = argparse.ArgumentParser(description="Aggregate ActivityWatch events by project")
    parser.add_argument("--aw-url", default=os.environ.get("ACTIVITYWATCH_URL", DEFAULT_AW_URL))
    sub = parser.add_subparsers(dest="cmd", required=True)

    init = sub.add_parser("init-config", help="create a private project matching config from git repos")
    init.add_argument("--config", type=pathlib.Path, default=DEFAULT_CONFIG)
    init.add_argument("--repo-root", type=pathlib.Path, action="append", default=[pathlib.Path.cwd()])
    init.add_argument("--max-depth", type=int, default=3)
    init.set_defaults(func=cmd_init_config)

    report = sub.add_parser("report", help="write an aggregate project activity report")
    report.add_argument("--config", type=pathlib.Path, default=DEFAULT_CONFIG)
    report.add_argument("--days", type=int, default=1)
    report.add_argument("--start", help="ISO timestamp, default: now - --days")
    report.add_argument("--end", help="ISO timestamp, default: now")
    report.add_argument("--output", type=pathlib.Path)
    report.add_argument("--include-samples", action="store_true", help="include hashed sample titles/URLs")
    report.add_argument("--format", choices=["json", "markdown"], default="json")
    report.set_defaults(func=cmd_report)

    args = parser.parse_args()
    return args.func(args)


def cmd_init_config(args: argparse.Namespace) -> int:
    repos = discover_repos(args.repo_root, args.max_depth)
    config = {
        "version": 1,
        "privacy": {
            "raw_window_titles_in_reports": False,
            "default_report_dir": str(DEFAULT_REPORT_DIR),
        },
        "sources": {
            "window_bucket_prefix": "aw-watcher-window",
            "web_bucket_prefixes": ["aw-watcher-web-"],
            "editor_bucket_prefixes": ["aw-watcher-vscode"],
        },
        "projects": [],
        "ignore": [
            {"app_regex": "(?i)^(loginwindow|screensaverengine)$"},
            {"title_regex": "(?i)\\b(password|token|secret|keychain)\\b"},
        ],
    }
    for repo in repos:
        name = repo.name
        config["projects"].append(
            {
                "name": name,
                "category": "software",
                "confidence": "repo-name-or-path",
                "match": [
                    {"path_contains": [str(repo)]},
                    {"title_regex": rf"(?i)(^|[^A-Za-z0-9_-]){re.escape(name)}([^A-Za-z0-9_-]|$)"},
                ],
            }
        )
    args.config.parent.mkdir(parents=True, exist_ok=True)
    args.config.write_text(json.dumps(config, indent=2, sort_keys=True) + "\n")
    print(f"wrote {args.config} with {len(repos)} project rules")
    return 0


def cmd_report(args: argparse.Namespace) -> int:
    config = load_config(args.config)
    start, end = parse_range(args)
    buckets = aw_get(args.aw_url, "/api/0/buckets/")
    bucket_ids = select_buckets(buckets, config)
    totals: dict[str, float] = defaultdict(float)
    app_totals: dict[str, dict[str, float]] = defaultdict(lambda: defaultdict(float))
    samples: dict[str, list[dict[str, str]]] = defaultdict(list)
    unmatched = 0.0
    events_seen = 0

    for bucket_id in bucket_ids:
        events = aw_events(args.aw_url, bucket_id, start, end)
        for event in events:
            duration = float(event.get("duration") or 0)
            if duration <= 0:
                continue
            data = event.get("data") or {}
            if is_ignored(data, config):
                continue
            project = match_project(data, config)
            app = str(data.get("app") or bucket_id)
            events_seen += 1
            if project is None:
                unmatched += duration
                continue
            totals[project] += duration
            app_totals[project][app] += duration
            if args.include_samples and len(samples[project]) < 8:
                samples[project].append(redacted_sample(data))

    report = {
        "created_at": dt.datetime.now(dt.timezone.utc).isoformat(),
        "range": {"start": start.isoformat(), "end": end.isoformat()},
        "source_buckets": bucket_ids,
        "events_seen": events_seen,
        "projects": [
            {
                "name": name,
                "seconds": round(seconds, 3),
                "hours": round(seconds / 3600, 4),
                "apps": {app: round(sec, 3) for app, sec in sorted(app_totals[name].items())},
                **({"samples": samples[name]} if args.include_samples else {}),
            }
            for name, seconds in sorted(totals.items(), key=lambda item: item[1], reverse=True)
        ],
        "unmatched_seconds": round(unmatched, 3),
        "unmatched_hours": round(unmatched / 3600, 4),
    }
    output = args.output or default_report_path(args.format)
    output.parent.mkdir(parents=True, exist_ok=True)
    if args.format == "markdown":
        output.write_text(render_markdown(report))
    else:
        output.write_text(json.dumps(report, indent=2, sort_keys=True) + "\n")
    print(f"wrote {output}")
    return 0


def discover_repos(roots: list[pathlib.Path], max_depth: int) -> list[pathlib.Path]:
    repos: set[pathlib.Path] = set()
    for root in roots:
        root = root.expanduser().resolve()
        if not root.exists():
            continue
        for current, dirs, _files in os.walk(root):
            cur = pathlib.Path(current)
            rel_depth = len(cur.relative_to(root).parts)
            if rel_depth > max_depth:
                dirs[:] = []
                continue
            if ".git" in dirs:
                repos.add(cur)
                dirs[:] = [d for d in dirs if d != ".git"]
    return sorted(repos)


def load_config(path: pathlib.Path) -> dict[str, Any]:
    if not path.exists():
        raise SystemExit(f"Config missing: {path}. Run init-config first.")
    return json.loads(path.read_text())


def parse_range(args: argparse.Namespace) -> tuple[dt.datetime, dt.datetime]:
    end = parse_iso(args.end) if args.end else dt.datetime.now(dt.timezone.utc)
    start = parse_iso(args.start) if args.start else end - dt.timedelta(days=args.days)
    return start, end


def parse_iso(raw: str) -> dt.datetime:
    value = raw.replace("Z", "+00:00")
    parsed = dt.datetime.fromisoformat(value)
    if parsed.tzinfo is None:
        parsed = parsed.replace(tzinfo=dt.timezone.utc)
    return parsed.astimezone(dt.timezone.utc)


def aw_get(base: str, path: str, query: dict[str, str] | None = None) -> Any:
    url = base.rstrip("/") + path
    if query:
        url += "?" + urllib.parse.urlencode(query)
    req = urllib.request.Request(url, headers={"Accept": "application/json"})
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            return json.loads(resp.read().decode("utf-8"))
    except urllib.error.URLError as err:
        raise SystemExit(f"ActivityWatch request failed for {url}: {err}") from err


def aw_events(base: str, bucket_id: str, start: dt.datetime, end: dt.datetime) -> list[dict[str, Any]]:
    return aw_get(
        base,
        f"/api/0/buckets/{urllib.parse.quote(bucket_id, safe='')}/events",
        {"start": start.isoformat(), "end": end.isoformat()},
    )


def select_buckets(buckets: dict[str, Any], config: dict[str, Any]) -> list[str]:
    sources = config.get("sources", {})
    prefixes = [sources.get("window_bucket_prefix", "aw-watcher-window")]
    prefixes.extend(sources.get("web_bucket_prefixes", []))
    prefixes.extend(sources.get("editor_bucket_prefixes", []))
    out = []
    for bucket_id in sorted(buckets):
        if any(bucket_id.startswith(prefix) for prefix in prefixes):
            out.append(bucket_id)
    return out


def is_ignored(data: dict[str, Any], config: dict[str, Any]) -> bool:
    for rule in config.get("ignore", []):
        if rule_matches(data, rule):
            return True
    return False


def match_project(data: dict[str, Any], config: dict[str, Any]) -> str | None:
    for project in config.get("projects", []):
        for rule in project.get("match", []):
            if rule_matches(data, rule):
                return str(project["name"])
    return None


def rule_matches(data: dict[str, Any], rule: dict[str, Any]) -> bool:
    text = " ".join(str(data.get(key) or "") for key in ("title", "url", "app", "file", "project", "path"))
    checks = {
        "title_regex": str(data.get("title") or ""),
        "url_regex": str(data.get("url") or ""),
        "app_regex": str(data.get("app") or ""),
    }
    for key, value in checks.items():
        pattern = rule.get(key)
        if pattern and re.search(pattern, value):
            return True
    for key in ("title_contains", "url_contains", "app_contains", "path_contains"):
        needles = rule.get(key) or []
        haystack_key = key.removesuffix("_contains")
        haystack = str(data.get(haystack_key) or text).lower()
        if any(str(needle).lower() in haystack for needle in needles):
            return True
    return False


def redacted_sample(data: dict[str, Any]) -> dict[str, str]:
    sample = {}
    for key in ("app", "title", "url"):
        value = str(data.get(key) or "")
        if value:
            sample[key + "_sha256_12"] = hashlib.sha256(value.encode()).hexdigest()[:12]
    return sample


def default_report_path(fmt: str) -> pathlib.Path:
    stamp = dt.datetime.now(dt.timezone.utc).strftime("%Y%m%dT%H%M%SZ")
    suffix = "md" if fmt == "markdown" else "json"
    return DEFAULT_REPORT_DIR / f"project-activity-{stamp}.{suffix}"


def render_markdown(report: dict[str, Any]) -> str:
    lines = [
        "# Project Activity Report",
        "",
        f"- Range: `{report['range']['start']}` to `{report['range']['end']}`",
        f"- Events seen: `{report['events_seen']}`",
        f"- Unmatched: `{report['unmatched_hours']}` h",
        "",
        "| Project | Hours | Top apps |",
        "|---|---:|---|",
    ]
    for project in report["projects"]:
        apps = ", ".join(f"{app}: {round(sec / 3600, 2)}h" for app, sec in sorted(project["apps"].items(), key=lambda item: item[1], reverse=True)[:3])
        lines.append(f"| {project['name']} | {project['hours']} | {apps} |")
    lines.append("")
    return "\n".join(lines)


if __name__ == "__main__":
    raise SystemExit(main())
