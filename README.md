# Quicksand

Quicksand is a self-service front end for throwaway sandbox VMs on Proxmox VE. You
pick a template, a network and how big you want it, and a minute later you
have a machine you can use from your browser. When you're done (or when
its time runs out) it's destroyed. That's the whole idea: VMs you don't get
attached to.

It's meant for labs, testing, training, and anything else where
people need a fresh Windows or Linux box often, and shouldn't need a
Proxmox account (or your attention) to get one.

## What you get

**In the browser**

- Deploy from the templates you've configured, choosing CPUs, memory and network.
- SPICE and VNC consoles, with audio, clipboard, fullscreen that captures the
  keyboard (so Alt+Tab and friends go to the guest), and the guest's display
  resized to fit your window.
- Several consoles at once, in tabs or side by side.
- Power controls, plus a single snapshot per VM. Take it, break things,
  revert, and repeat as often as you like. Memory is included by default, so
  reverting puts you right back where you were.
- Link networks: link a few of your VMs and they share a private network,
  reaching each other and nothing else. They show up grouped on the dashboard.
- A firewall per VM, in plain terms: allow or block traffic on its network
  and its link network, with presets like "Internet only" for VMs that
  shouldn't touch the local network.
- Expiry with optional extensions, and quotas per user, per template and overall.
- An admin page for users and everyone's machines.

**With the guest agent** (optional, but most of the good stuff needs it)

- A terminal in the browser (PowerShell or bash by default, or whatever you ask for).
- A file browser with uploads, downloads, and zipped (optionally encrypted) folders.
  You can also drop files straight onto the console.
- Process list or tree (with the services inside each svchost on Windows),
  and the guest's open sockets.
- Exact display resizing on Windows, where SPICE alone can't manage it.
- Clipboard sharing on consoles that can't do it themselves (VNC, or SPICE
  without spice-vdagent). The guest only gets your clipboard when you paste.

Templates without the agent can still get a shell over a serial console.

**From the command line**

`qs` does most of the above from a terminal: deploy, power, snapshots, shells,
`exec`, file transfers and port forwarding into the guest. For example:

```sh
qs deploy -name scratch debian
qs shell scratch
qs upload scratch ./sample.zip
qs snapshot take scratch
qs forward scratch 8080:localhost:80
```

See [docs/cli.md](docs/cli.md) for the rest.

## How it fits together

```
browser / qs ──HTTPS──> quicksand ──API──> Proxmox VE
                            │
                            └──agent network──> quicksand-agent (in each VM)
```

- **`quicksand`** is the server: one Go binary with the Vue web client
  embedded, and a SQLite database. It talks to Proxmox with an API token
  that is limited to one resource pool, and proxies the consoles, so users
  never touch Proxmox directly.
- **`quicksand-agent`** runs inside templates (Linux and Windows). Quicksand
  connects to it over a separate, isolated network; the agent never connects
  out. Both sides authenticate with Ed25519 keys. Anything coming back from a
  guest is treated as untrusted.
- **`qs`** is the command-line client. It's a single binary for Linux,
  macOS and Windows.

## Getting it running

You'll need Proxmox VE, a Linux host for the server, and Go and Node.js to
build. In short:

1. **Set up Proxmox:** an API token, a resource pool, your templates, the
   networks users can pick and, if you want the agent, its isolated network.
   [docs/proxmox-setup.md](docs/proxmox-setup.md) walks through all of it.
2. **Build and install the server:** `./scripts/build-server.sh` produces
   `dist/server/`, with an installer that sets up a systemd service, a
   locked-down user and a self-signed certificate.
   [docs/deploy.md](docs/deploy.md) has the details.
3. **Configure it:** start from [config.example.json](config.example.json).
   [config.schema.json](config.schema.json) documents every setting, and editors
   like VS Code autocomplete from it if you keep the `$schema` line.
4. **Create the first admin:**

   ```sh
   sudo -u quicksand quicksand user add -config /etc/quicksand/config.json -admin yourname
   ```

   After that, users are managed from the Admin page.
5. **Install the agent in your templates** (optional): `./scripts/build-agent.sh`,
   then run `quicksand-agent install -key '<server public key>'` inside the
   template. See [docs/agent.md](docs/agent.md).

## Hacking on it

The server serves only the API unless it's built with the `embedweb` tag, so
during development run it next to the Vite dev server:

```sh
go run ./cmd/quicksand serve -config config.json
```

```sh
cd web && npm install && npm run dev
```

Vite proxies API and console traffic to `127.0.0.1:8484`; point it elsewhere
with `QUICKSAND_BACKEND`. For tests and type checks:

```sh
go test ./...
```

```sh
cd web && npm run typecheck
```

GitHub Actions (`.github/workflows/ci.yml`) runs the tests and builds the
server, agent and CLI on every push to any branch. To release, tag the
commit and push the tag:

```sh
git tag -a v1.2.0 -m "Short summary of the release"
git push origin v1.2.0
```

The tag's workflow creates a GitHub release with the binaries and
`SHA256SUMS`. The release notes are the tag's message followed by the
commits since the previous tag. Tags with a suffix (`v1.2.0-rc.1`) become
pre-releases. The newest release's files are always at
`/releases/latest/download/<file>`. Create releases by pushing tags, not from
GitHub's "Draft a new release" page: the workflow creates the release itself.

Layout:

| Path | What's there |
| --- | --- |
| `cmd/quicksand` | The server and its admin commands (`serve`, `user`, `agent keygen`) |
| `cmd/quicksand-agent` | The guest agent |
| `cmd/qs` | The CLI |
| `internal/` | Proxmox client, VM lifecycle, HTTP API, auth, agent protocol, storage |
| `web/` | Vue 3 + shadcn-vue client |
| `docs/` | Setup, deployment, agent and CLI docs |
| `scripts/`, `deploy/` | Build scripts and the server's install files |
