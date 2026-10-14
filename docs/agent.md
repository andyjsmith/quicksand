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

Build the binaries and installers (on any machine with Go):

```sh
./scripts/build-agent.sh        # writes dist/agent/
```

Get the server's public key from the Admin page, or on the Quicksand host:

```sh
sudo -u quicksand quicksand agent pubkey -config /etc/quicksand/config.json
```

Copy `dist/agent/` into the template VM, then:

**Linux** (as root; systemd):

```sh
./install-linux.sh 'ed25519:<server public key>' 10.99.0.0/24
```

This installs `/usr/local/bin/quicksand-agent`, `/etc/quicksand-agent/config.json`
and the `quicksand-agent` service. If the distribution uses NetworkManager,
stop it from running DHCP on the agent NIC (the second NIC, usually `ens19`):

```sh
printf '[keyfile]\nunmanaged-devices=interface-name:ens19\n' > /etc/NetworkManager/conf.d/90-quicksand-agent.conf
```

(The agent re-applies its address every 30 seconds regardless.) Host
firewalls (ufw, firewalld) must allow TCP 7070 on that NIC.

**Windows** (elevated PowerShell):

```powershell
.\install-windows.ps1 -ServerKey "ed25519:<server public key>" -Subnet 10.99.0.0/24
```

This installs `C:\Program Files\Quicksand\quicksand-agent.exe`, writes
`C:\ProgramData\Quicksand\agent.json` (readable by SYSTEM and Administrators
only), adds a Windows Firewall rule for the agent port, and registers the
`QuicksandAgent` service (automatic start, LocalSystem, restarted on failure),
logging to `C:\ProgramData\Quicksand\agent.log`. Running it again upgrades
an existing install in place. Manage it like any service, e.g.
`Restart-Service QuicksandAgent`.

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

Quicksand exposes these to signed-in users (for their own VMs) as
`GET /api/vms/{id}/agent` and `POST /api/vms/{id}/exec`, and in the console's
side panel.

### Display resolution (Windows)

`POST /v1/display` with `{"width": 1920, "height": 1080}` changes the guest's
screen resolution. The web console uses it for its "Resize guest" mode when
the protocol can't resize the guest itself: always over VNC, and over SPICE
until the SPICE agent connects.

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

Widths are rounded down to a multiple of 8, and sizes are kept between
640×480 and 8192×8192. The response gives the size set, the `adapter`, and
whether it was `exact`. Linux agents answer 501 for now.

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
`socks5://127.0.0.1:1080` (see [proxmox-setup.md](proxmox-setup.md#7-developing-away-from-the-quicksand-host)).
`quicksand agent info <agent-ip>` then works from the development machine too.
