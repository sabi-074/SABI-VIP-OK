# SABI-VIP-OK

**One repo, zero manual steps.** Deploy this on a blank Railway service and
it automatically installs everything, builds everything, and gives you
one link with every ready-to-use config.

## What it runs (all in a single container)

- **SabiTun** — the custom Noise_IK-over-WebSocket tunnel protocol
  (from [sabi-074/sabi-vip](https://github.com/sabi-074/sabi-vip))
- **VLESS**, **VMess**, **Trojan**, **Shadowsocks** — via Xray-core,
  each on its own WebSocket path
- **nginx** — routes all five by path on Railway's single public port
- A **`/setup` status page** — lists every config, freshly generated on
  every boot. This is "the one link."

## Deploy (the only steps that require you to click anything)

1. Go to [railway.app/new](https://railway.app/new) → **Empty Project**.
2. In the new project, click **+ New** → **GitHub Repo** → pick
   `sabi-074/SABI-VIP-OK` (authorize Railway's GitHub access if asked).
3. Once it deploys, open the service → **Settings** → **Networking** →
   **Generate Domain**.
4. Open `https://<that-domain>/setup` in a browser.

That's it. No config files to edit, no commands to run, nothing to
install by hand — `entrypoint.sh` does all of that automatically every
time the container starts:
- builds the SabiTun server from source
- generates fresh credentials for VLESS/VMess/Trojan/Shadowsocks (unless
  you set your own via Railway service variables — see below)
- writes every config file
- starts all 3 processes (SabiTun server, Xray, nginx)
- renders `/setup` with everything filled in for your actual domain

## Making credentials persistent (optional)

By default, every restart generates brand-new random credentials (fine
for testing). To keep the same ones across restarts, set these as Railway
**service variables** (Settings → Variables) — the entrypoint will reuse
them instead of generating new ones:

| Variable | What it is |
|---|---|
| `SABITUN_PRIVATE_KEY` | base64, 32 raw bytes — the server's static identity key |
| `VLESS_ID` | any UUID |
| `VMESS_ID` | any UUID |
| `TROJAN_PW` | any string |
| `SS_PW` | any string |

## Why this is safe to leave public

The GitHub repo itself contains no secrets — every credential is
generated at container boot, not committed to source. Anyone who finds
your Railway domain can still only use it if they also see your `/setup`
page or its logs, same as any of these protocols normally.

## Files

- `Dockerfile` — multi-stage build: compiles the SabiTun server, then
  bundles it with nginx + Xray-core in the final image
- `entrypoint.sh` — everything described above, runs on every boot
- `sabitun/` — the SabiTun protocol library (copied from the main
  `sabi-vip` repo; see [PROJECT_STATUS.md](https://github.com/sabi-074/sabi-vip/blob/main/PROJECT_STATUS.md)
  there for full protocol/architecture history)
- `server/main.go` — the SabiTun server binary's entry point
- `xray/config.template.json` — the 4-protocol Xray config, with
  placeholders substituted at boot
- `nginx.conf.template` — the path router, with `$PORT` substituted at boot
