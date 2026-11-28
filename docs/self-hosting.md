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

Set `WADA_WORKTREE_DIR` in the daemon's environment or pass `--worktree-dir` to
store new worktrees under an absolute directory on the daemon host. Choose a
directory outside your source repositories. For example, a repository at
`/srv/src/app` can use `wadad --worktree-dir /srv/worktrees`. Wada groups checkouts
by repository under that directory. Archives and restores use the path already
recorded for each workspace.

Set `WADA_BRANCH_PREFIX` on the daemon or pass `--branch-prefix team/agents` to
choose the namespace for new worktree branches and local pull request copies.
Wada uses `wada/` by default and adds the trailing slash when needed. Set
`--branch-prefix=` or an empty `WADA_BRANCH_PREFIX` for names without a prefix.
Git validates the prefix when the daemon starts.

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
   - Open `http://my-mac.tailnet.ts.net:6767` on your phone or laptop and install it
     as a PWA, or run `wada daemon pair --app-url http://my-mac.tailnet.ts.net:6767`
     to generate an `#offer=` link.
   - Browsers install and keep a PWA only from a secure origin: HTTPS, or
     `localhost`. Behind Caddy or `tailscale serve` it works offline once
     loaded: its service worker keeps this build's shell, scripts and icons, and
     a new build waits until you choose **Reload** in the app's banner, so a
     running chat is never swapped out under you.

---

In the web client, **Add project** in the sidebar registers a directory on the
connected daemon. The field suggests matching folders on that host. The
project's **New workspace** action creates a worktree or opens an existing checkout. **Restore workspace**
reopens an archived workspace from an active project.
**Files** opens the workspace editor, with an option to keep chat beside it.
**Schedules** can edit the prompt, cadence, and agent settings of existing
schedules.

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
   Clients and daemons reach the relay through Caddy on port 443. The relay's
   own plaintext port 8787 is published on `127.0.0.1` only; set
   `WADA_RELAY_BIND` only when another TLS proxy of yours fronts it.
   The relay tells `wadad` each client's address, taken from
   `X-Forwarded-For` only when the connection comes from a network in
   `WADA_RELAY_TRUSTED_PROXIES` (`wada-relay --trusted-proxies`). Compose
   trusts the private ranges Caddy connects from; behind another proxy, list
   its network instead.
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
   parameter, because browsers cannot set WebSocket headers. In Desktop, enter it
   as the relay token under **Settings → Daemon → Add remote host**.
   A browser cannot see why a relay closed its WebSocket, so a wrong relay token
   shows in the Web/PWA client as a connection that keeps retrying; pair again
   with the right token.
   From the CLI, save the host once and use it with `--host`:
   ```sh
   wada host add mac 'https://app.wada.dev/#offer=...' --relay-token "$WADA_RELAY_TOKEN"
   wada host add tailnet my-mac.tailnet.ts.net --credential "$WADA_PASSWORD"
   wada --host mac run --dir /Users/me/src/app "fix the failing test"
   ```
   A target without a scheme or port gets port 6767. With `--host`, `--offer` or
   `--endpoint`, `--dir` names a folder on the daemon's machine and is sent as
   typed; `wada run` without it uses the daemon's inbox.
   Everything inside `#offer=...` remains in the browser URL hash fragment and is
   never sent in HTTP requests.

   A machine you can SSH to needs no pairing, open port or password: install
   `wadad` there and add it as an `ssh://` host.
   ```sh
   wada host add devbox ssh://me@devbox.local
   wada --host devbox workspace ls
   ```
   The client runs the system's `ssh -T -o BatchMode=yes` (set `WADA_SSH` for
   another binary), so the host must take your keys or agent; nothing prompts.
   On the host it runs `sh -lc 'exec wadad --stdio-bridge'`, or the host's
   `--ssh-command`. The bridge finds the host's daemon, starting it detached
   when none runs, gives the client its local credential and carries the
   WebSocket connection over ssh's stdio. Desktop takes the same `ssh://` target
   under **Settings → Daemon → Add remote host**.

4. *(Optional)* Run `wadad` + `wadad-term` + Web UI inside Docker too:
   ```sh
   docker compose --env-file .env --profile daemon --profile tls up -d --build
   ```
   The `daemon` profile has no default password: without `WADA_PASSWORD` it
   accepts only its random pairing token. It publishes port `6767` on
   `127.0.0.1` only; set `WADA_DAEMON_BIND` to expose it on another interface.

---

## 3. Security Model & Forge/PR Guard

- **Zero-Knowledge Relay:** Static X25519 keys live in `<DataDir>/daemon-keypair.json`
  (`0600`). They are the daemon's identity: `wadad` refuses to start when the file
  cannot be read or its keys do not match, rather than make new keys that every
  paired client would have to pair again with. Only `<DataDir>/hosts.json` (on Desktop CLI/app) or browser
  `localStorage` stores pairing profiles. Nonce counters strictly increment per
  message; tampered or replayed ciphertext closes the tunnel. A client that falls
  more than 4 MiB behind is disconnected rather than losing frames.
- **Liveness:** `wadad` pings the relay every 15 s and Go clients ping `wadad`
  every 10 s; a connection that receives nothing, not even a pong, for three
  intervals is closed and reconnected, so a half-open connection after sleep or
  a network change does not linger. The web client pings every 10 s and reports
  the connection lost when a ping goes unanswered until the next one.
- **Daemon Registration:** Before routing any client to a server hash, `wada-relay`
  challenges the connecting daemon to prove it holds the static private key behind
  that hash (an HMAC keyed by X25519 with a relay ephemeral key). Knowing the hash
  from a pairing link is not enough to register as, or evict, that daemon; a daemon
  that passes the challenge replaces its previous connection.
- **Password vs Pairing Token:** Setting a daemon password (`wadad --password`,
  `wada daemon set-password`, or Settings → Daemon) removes the cleartext token
  from `#offer=` links and requires bcrypt verification inside the encrypted
  Noise `hello` handshake. Setting or clearing the password also issues a new pairing
  token and disconnects every remote client except the one that made the change,
  so earlier links and sessions must authenticate again. After five failed
  sign-ins in a row, each further attempt from that address must wait a doubling
  delay (1 s up to 5 min). A relay client's address is the one the relay
  reports for it; clients of a relay that reports none share one. `wada daemon set-password` asks twice and never clears the
  password on an empty entry; clearing takes `--clear`.
- **GitHub/Forge Integration (`gh`):** Worktree PR badges and `wada pr status|create`
  parse `git remote get-url origin` and verify `gh auth status --hostname <host>`
  with `GH_PROMPT_DISABLED=1`. Hosts without active local `gh` authentication are
  never probed. Creating a PR first pushes the worktree's branch to `origin`.
- **Secrets in child processes:** `wadad` removes `WADA_PASSWORD` and
  `WADA_RELAY_TOKEN` from its environment after reading them, so terminals,
  harness workers and `git`/`gh` never inherit them.
