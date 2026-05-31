# Cinny Room List Avatars

Local Cinny enhancer that replaces generic room-type icons in the room list
with the Matrix `m.room.avatar` image when a room has one.

Cinny does not currently expose an official plugin API for this UI slot, so the
enhancer is installed as a static script into a self-hosted Cinny container. It
does not patch Matrix state, does not send messages, and does not persist tokens.

## Install Into A Running Container

```sh
./infra/cinny-room-list-avatars/install_into_container.sh openclaw-cinny-e2e
```

The script copies `openclaw-room-list-avatars.js` into `/app/` and injects it
into `/app/index.html` if the script tag is not already present.

## How It Works

- Reads Cinny's homeserver URL and access token from browser `localStorage`.
- Finds visible room-list anchors in the Cinny DOM.
- Fetches `m.room.avatar` via Matrix Client-Server API.
- Fetches the thumbnail with Authorization and uses a local object URL for the
  image, so authenticated media works.
- Leaves Cinny's original `#`/room-type icon in place when no avatar exists.
- Re-runs on DOM changes so virtualized room-list rows are handled as they
  appear.

This is deliberately a small client-side enhancement. The durable source of
truth remains Matrix room state, so Element Web and Cinny keep using the same
avatars.
