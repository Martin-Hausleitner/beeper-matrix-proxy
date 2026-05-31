# Project Activity Tracking

This repo uses ActivityWatch as the local signal source for project time
tracking. ActivityWatch already records active windows, browser tabs, AFK state,
and optional editor activity on this machine, so the project layer should not
run a second invasive watcher.

## Architecture

```text
ActivityWatch
  aw-watcher-window      -> active app + window title
  aw-watcher-web-*       -> browser URL/title where available
  aw-watcher-vscode      -> editor activity where available
        |
        v
scripts/project_activity.py
        |
        +-- private project rules:
        |   ~/Library/Application Support/openclaw/project-activity/projects.json
        |
        +-- private aggregate reports:
            ~/Library/Application Support/openclaw/project-activity/reports/
```

The public repo contains the generic script and documentation only. The project
rules and reports stay local because they can reveal private project names,
window titles, URLs, customer names, or research topics.

## Setup

Create a private config from local git repositories:

```bash
scripts/project_activity.py init-config \
  --config "$HOME/Library/Application Support/openclaw/project-activity/projects.json" \
  --repo-root "$HOME/Documents/Playground" \
  --repo-root "$HOME/Documents/GitHub"
```

Generate a daily report:

```bash
scripts/project_activity.py report \
  --config "$HOME/Library/Application Support/openclaw/project-activity/projects.json" \
  --days 1 \
  --format markdown
```

The report aggregates seconds/hours by project. By default it does not store raw
window titles or URLs. Use `--include-samples` only for debugging; even then the
samples are SHA-256 prefixes, not plaintext titles.

## Matching Strategy

Project mapping is deterministic and auditable:

1. Match absolute repo paths in titles, URLs, editor metadata, or terminal
   titles.
2. Match repository basenames in window titles, such as a VS Code project name.
3. Match manually added URL/domain/title rules for web dashboards, Linear,
   GitHub, Notion, Figma, or AI research sessions.
4. Leave unmatched time explicitly visible instead of guessing.

Example private rule:

```json
{
  "name": "beeper-matrix-proxy",
  "category": "software",
  "confidence": "manual",
  "match": [
    {"path_contains": ["$HOME/Documents/Playground/sh-vcvm-matrix-bridgev2-src"]},
    {"title_regex": "(?i)beeper-matrix-proxy|sh-vcvm-matrix-bridgev2-src"},
    {"url_contains": ["github.com/Martin-Hausleitner/beeper-matrix-proxy"]}
  ]
}
```

## Privacy Rules

- Keep `projects.json` and reports outside the public repo.
- Do not commit raw ActivityWatch exports.
- Do not sync raw window titles to GitHub.
- Prefer aggregate hours, project names, and app categories.
- Keep unmatched time visible so private/unclassified activity does not get
  mislabeled.

## Future Improvements

- Use [infra/project-activity/ai.openclaw.project-activity.plist.example](../infra/project-activity/ai.openclaw.project-activity.plist.example)
  as the LaunchAgent template for hourly aggregate reports.
- Add optional ActivityWatch bucket writing, e.g. `aw-openclaw-projects`, for
  project labels as first-class AW events.
- Add Git branch/current workspace detection for terminal sessions.
- Add IDE-specific enrichers for Cursor, VS Code, JetBrains, and Zed.
- Add a dashboard that joins project time with git commits and issue IDs without
  storing raw window titles.
