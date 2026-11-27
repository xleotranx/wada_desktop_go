# Self-Hosting `wadad` & `wada-relay`

Wada supports three remote topologies while keeping API keys, source files,
worktrees, and PTY terminals on your own workstation or server:

1. **Direct LAN / Tailscale** — connect straight to `wadad` on port `6767` with
   `--web-dir` serving the built Web/PWA client and password protection enabled.
2. **Hosted or Self-Hosted `wada-relay` (Zero-Knowledge E2EE)** — `wadad` dials
   out over TLS to `wada-relay`; desktop and mobile clients complete a
   `Noise_NK_25519_ChaChaPoly_SHA256` handshake directly with `wadad`. The relay
   forwards opaque ciphertext frames and never sees prompts, source code, or
   credentials.
3. **Full Docker Compose Deployment** — run `wada-relay` + Caddy TLS (and
   optionally `wadad` itself) on your own server or VPS.

---

## 1. Direct Tailscale / LAN Access

1. Build the static Web/PWA bundle once:
   ```sh
   pnpm --dir web install --frozen-lockfile
   pnpm --dir web build
   ```
2. Start `wadad` with password auth, your Tailscale/LAN hostnames, and `--web-dir`:
   ```sh
   wadad \
     --listen 0.0.0.0:6767 \
     --password "$WADA_PASSWORD" \
     --hostnames "localhost,my-mac.tailnet.ts.net,100.64.0.10" \
     --public-url "ws://my-mac.tailnet.ts.net:6767" \
     --web-dir ./web/dist
   ```
   - `Host` / `Origin` checks block DNS-rebinding attacks against unlisted
     hostnames.
   - Open `http://my-mac.tailnet.ts:6767` on your phone or laptop and install it
     as a PWA, or run `wada daemon pair --app-url http://my-mac.tailnet.ts:6767`
     to generate an `#offer=` link.

---

## 2. Self-Hosted `wada-relay` with Docker Compose & Caddy TLS

1. Configure `docker/.env`:
   ```sh
   cp docker/.env.example docker/.env
   # Set WADA_RELAY_DOMAIN (e.g. relay.yourdomain.com) and optional WADA_RELAY_TOKEN
   ```
2. Start the relay with automatic HTTPS via Caddy:
   ```sh
   cd docker
   docker compose --env-file .env --profile tls up -d --build
   ```
3. Point your workstation's `wadad` at your relay:
   ```sh
   wadad \
     --relay-url "wss://relay.yourdomain.com" \
     --relay-token "$WADA_RELAY_TOKEN" \
     --password "$WADA_PASSWORD"
   ```
   Generate a pairing URL with:
   ```sh
   wada daemon pair --app-url https://app.wada.dev
   ```
   Or copy the link from Desktop under **Settings → Daemon → Copy Pairing Link**.
   If the relay has a `WADA_RELAY_TOKEN`, enter it as the relay token when
   pairing in the Web/PWA client; the browser sends it as the `token` query
   parameter, because browsers cannot set WebSocket headers.
   Everything inside `#offer=...` remains in the browser URL hash fragment and is
   never sent in HTTP requests.

4. *(Optional)* Run `wadad` + `wadad-term` + Web UI inside Docker too:
   ```sh
   docker compose --env-file .env --profile daemon --profile tls up -d --build
   ```

---

## 3. Security Model & Forge/PR Guard

- **Zero-Knowledge Relay:** Static X25519 keys live in `<DataDir>/daemon-keypair.json`
  (`0600`). Only `<DataDir>/hosts.json` (on Desktop CLI/app) or browser
  `localStorage` stores pairing profiles. Nonce counters strictly increment per
  message; tampered or replayed ciphertext closes the tunnel. A client that falls
  more than 4 MiB behind is disconnected rather than losing frames.
- **Daemon Registration:** Before routing any client to a server hash, `wada-relay`
  challenges the connecting daemon to prove it holds the static private key behind
  that hash (an HMAC keyed by X25519 with a relay ephemeral key). Knowing the hash
  from a pairing link is not enough to register as, or evict, that daemon; a daemon
  that passes the challenge replaces its previous connection.
- **Password vs Pairing Token:** Setting a daemon password (`wadad --password`,
  `wada daemon set-password`, or Settings → Daemon) removes the cleartext token
  from `#offer=` links and requires bcrypt verification inside the encrypted
  Noise `hello` handshake.
- **GitHub/Forge Integration (`gh`):** Worktree PR badges and `wada pr status|create`
  parse `git remote get-url origin` and verify `gh auth status --hostname <host>`
  with `GH_PROMPT_DISABLED=1`. Hosts without active local `gh` authentication are
  never probed.
