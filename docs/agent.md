# Quicksand guest agent

The agent is an optional, small HTTPS server installed in VM templates. The
Quicksand server connects *to* it (the agent never connects out) over the
isolated agent network described in
[proxmox-setup.md](proxmox-setup.md#6-agent-network-optional), and uses it to
run commands in the guest. It is a single static binary with no
dependencies, for Linux and Windows (amd64 and arm64).

## Authentication

Mutual TLS with Ed25519 keys:

- The Quicksand server has a private key in its config
  (`agent.privateKey`, from `quicksand agent keygen`).
- Each agent's config lists the server's public key(s) in `authorizedKeys`.
  During the TLS handshake the server presents a certificate for its key;
  the agent refuses the connection unless that key is listed. Connections
  without a client certificate, or with any other key, never reach the API.
- The agent generates a fresh TLS certificate every time it starts, so no
  key material lives in the template (clones of one template would otherwise
  share it). The server doesn't verify the agent's certificate: the guest is
  untrusted anyway, and *which* VM answers at an address is guaranteed by the
  network (port isolation and per-VM IP filters), not by agent keys.
- All traffic is encrypted (TLS 1.3).

Treat everything an agent returns as untrusted: malware in the guest can
replace or impersonate the agent within its own VM.

## Installing in a template

Build the agent (on any machine with Go):

```sh
./scripts/build-agent.sh        # writes dist/agent/
```

Get the server's public key from the Admin page, or on the Quicksand host:

```sh
sudo -u quicksand quicksand agent pubkey -config /etc/quicksand/config.json
```

Copy the agent binary for the template's OS and architecture from
`dist/agent/` into the template VM and run its `install` command:

**Linux** (as root; systemd):

```sh
./quicksand-agent-linux-amd64 install -key 'ed25519:<server public key>'
```

This installs `/usr/local/bin/quicksand-agent`, writes
`/etc/quicksand-agent/config.json` (readable by root only) and the
`quicksand-agent` systemd service, and starts it. If the distribution uses
NetworkManager, stop it from running DHCP on the agent NIC (the second NIC
in clones, usually `ens19`); the installer prints how:

```sh
printf '[keyfile]\nunmanaged-devices=interface-name:ens19\n' > /etc/NetworkManager/conf.d/90-quicksand-agent.conf
```

(The agent re-applies its address every 30 seconds regardless.) Host
firewalls (ufw, firewalld) must allow TCP 7070 on that NIC.

**Windows** (in an elevated prompt):

```powershell
.\quicksand-agent-windows-amd64.exe install -key "ed25519:<server public key>"
```

This installs `C:\Program Files\Quicksand\quicksand-agent.exe`, writes
`C:\ProgramData\Quicksand\agent.json` (readable by SYSTEM and Administrators
only), adds a Windows Firewall rule for the agent port, and registers the
`QuicksandAgent` service (automatic start, LocalSystem, restarted on failure),
logging to `C:\ProgramData\Quicksand\agent.log`. Manage it like any service,
e.g. `Restart-Service QuicksandAgent`.

On both, `-subnet` sets the agent network if it isn't the default
`10.99.0.0/24` (it must match Quicksand's config) and `-port` the agent port
(7070). Running `install` again, with a newer binary or another key, upgrades
the installation in place.

Then shut the VM down, convert it to a template (or update the existing
template), and set `"agent": true` on it in Quicksand's `config.json`.

The template itself doesn't need the agent NIC: Quicksand adds it to each
clone. Until a clone has that NIC, the agent just logs that the agent
network isn't ready and keeps waiting.

## Configuration file

```json
{
  "authorizedKeys": ["ed25519:..."],
  "port": 7070,
  "network": { "subnet": "10.99.0.0/24", "macPrefix": "02:51:53" }
}
```

| Field            | Meaning                                                                 |
| ---------------- | ----------------------------------------------------------------------- |
| `authorizedKeys` | Server public keys allowed to connect. List several to rotate keys or to allow a development server. |
| `port`           | TCP port (default 7070); must match Quicksand's `agent.port`.           |
| `network`        | Find the NIC whose MAC starts with `macPrefix` (default `02:51:53`), give it the address encoded in its MAC with no gateway, and listen only there. Must match Quicksand's `agent.network`. |
| `listen`         | Listen on this address instead (e.g. `127.0.0.1:7070`); for testing.    |

Without `network` or `listen`, the agent listens on all interfaces.

Command-line flags: `-config <path>` (default `/etc/quicksand-agent/config.json`,
or `%ProgramData%\Quicksand\agent.json` on Windows), `-log <file>` (as a
Windows service it defaults to `agent.log` next to the config), `-version`.

## API

All endpoints require the authenticated TLS connection described above.

- `GET /v1/info` — hostname, OS, architecture, user, agent version, time and
  network interfaces.
- `POST /v1/exec` — run a command and wait for it:
  ```json
  { "shell": "ipconfig /all", "timeoutSeconds": 60 }
  { "argv": ["/usr/bin/uname", "-a"], "stdin": "<base64>" }
  ```
  `shell` runs through `sh -c` (Linux) or `cmd /c` (Windows); `argv` runs a
  program directly. Returns `exitCode`, `stdout` and `stderr` (base64), and
  `timedOut`, `truncated` (output is capped at 4 MiB per stream) or `error`.
  Timeouts default to 60 seconds, at most 30 minutes.

- `PUT /v1/links` — the addresses of the guest's link-network NICs (see
  [proxmox-setup.md](proxmox-setup.md#7-link-networks-optional)), each found by
  MAC: `{ "links": [{ "mac": "02:51:54:00:00:05", "address": "10.98.0.5/16" }] }`.
  It's the complete set; the agent keeps those addresses on their NICs (static,
  no gateway), retrying every few seconds for NICs that were just hot-plugged,
  and reports per link whether it's `configured`. Quicksand sends it after
  every link change and every 30 seconds, so a restarted agent catches up.

- `GET /v1/clipboard?after=SEQ&wait=SECONDS` — the guest clipboard's text and
  a `seq` that changes with it; when `seq` is still `after`, waits up to `wait`
  seconds (at most 25) for a change first. `PUT /v1/clipboard` with
  `{ "text": "..." }` sets it. Consoles use these when their protocol can't
  share the clipboard (VNC, or SPICE without spice-vdagent): copies in the guest
  reach the user's clipboard, and the user's clipboard reaches the guest only
  when they press Ctrl+V in the console. On Windows the agent reaches the
  console session's clipboard through a helper copy of itself; on Linux it runs
  `xclip`, `xsel` or `wl-clipboard` as the desktop user, so install one of them
  (xclip works on GNOME's Wayland sessions too). Without them, or with no one
  logged in, the endpoints answer 501.

Quicksand exposes these to signed-in users (for their own VMs) as
`GET /api/vms/{id}/agent` and `POST /api/vms/{id}/exec`, and in the console's
side panel.

### Display resolution

`POST /v1/display` with `{"width": 1920, "height": 1080}` changes the guest's
screen resolution. The web console uses it for its "Resize guest" mode when
the protocol can't resize the guest itself. The response gives the size
set, the `adapter` used, and whether it was `exact`; widths are rounded down
to a multiple of 8, and sizes kept between 640×480 and 8192×8192.

**Windows.** The console uses the agent over VNC (QEMU's resize requests
don't reach a Windows guest) and over SPICE until the SPICE agent connects.
The agent runs as a service in session 0, which has no access to the screen,
so for each change it starts a copy of itself (`quicksand-agent set-display
W H`) in the console session, as SYSTEM, on whichever desktop has the input
(the login screen's or the user's). That copy registers the size as the
display driver's custom mode and switches to it, the way the SPICE agent and
virtio-win's `viogpuap` do:

| Display (`vga`) | Driver | Sizes |
| --- | --- | --- |
| `qxl` | QXL WDDM (`qxldod`, from the SPICE guest tools) | Exact, up to the display memory (`vga: qxl,memory=…` for more) |
| `virtio` | VirtIO GPU (`viogpudo`, virtio-win) from mid-2026 on | Exact |
| `virtio` | VirtIO GPU from older virtio-win releases | The closest mode the driver offers |
| other, or no driver installed | e.g. Microsoft Basic Display Adapter | The closest mode the driver offers |

**Linux.** With a `virtio` or `qxl` display, the console protocols resize
Linux desktops exactly on their own: QEMU passes the client's size to the
virtual GPU (from VNC, or from SPICE through spice-vdagent), and the desktop
follows it. The console uses the agent only over SPICE without spice-vdagent.
The agent changes the resolution of the desktop on seat0 (the login screen's
or the user's), as that session's user: through GNOME's display
configuration service (Wayland or X11; applied temporarily, so nothing is
saved to the user's settings), or with `xrandr` on other X11 desktops. Both
choose from the modes the display offers: the requested size if it's there,
otherwise the largest that fits.

## Testing

**Unit and integration tests** (`go test ./...`) run real agents in-process:
authorized and unauthorized keys, clients without certificates, exec exit
codes, timeouts and output limits, and exec through Quicksand's HTTP API with
ownership checks.

**Against a local agent**, without Proxmox:

```sh
quicksand agent keygen                                # put the private key in config.json
go run ./cmd/quicksand-agent -config agent-dev.json   # "listen": "127.0.0.1:7070", authorizedKeys: [public key]
quicksand agent info 127.0.0.1:7070
quicksand agent exec 127.0.0.1:7070 -- uname -a
quicksand agent exec -shell 'echo hi; exit 3' 127.0.0.1:7070
```

**Interface self-configuration**, without root, in a throwaway network
namespace with a dummy NIC carrying an agent MAC:

```sh
unshare -rn sh -c '
  ip link set lo up
  ip link add qs0 type dummy && ip link set qs0 address 02:51:53:00:00:17
  quicksand-agent -config agent-net.json &     # with "network": {"subnet": "10.99.0.0/24"}
  sleep 2; ip -4 addr show qs0                 # 10.99.0.23/24, no default route
  quicksand agent info 10.99.0.23'
```

**Real VMs from a development machine**: tunnel through the Quicksand host
with `ssh -N -D 1080 root@<quicksand-host>` and set `agent.proxy` to
`socks5://127.0.0.1:1080` (see [proxmox-setup.md](proxmox-setup.md#9-developing-away-from-the-quicksand-host)).
`quicksand agent info <agent-ip>` then works from the development machine too.
