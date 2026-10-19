# Deploying Quicksand

Quicksand runs as a single binary (the web client is embedded) under
systemd or in Docker, on a Debian-like host that can reach the Proxmox API (8006) and
SPICE proxy (3128). If you use the agent, the host also needs its NIC on the
agent network ([proxmox-setup.md](proxmox-setup.md#64-the-quicksand-host)).

## Install from a package

Releases on the [releases page](https://github.com/andyjsmith/quicksand/releases) include `quicksand` packages for
Debian/Ubuntu (.deb) and Fedora/RHEL (.rpm), for amd64:

```sh
sudo apt install ./quicksand_1.2.0_amd64.deb
sudo dnf install ./quicksand-1.2.0-1.x86_64.rpm
```

The package installs `/usr/bin/quicksand` and `quicksand.service`, and on
first install does what the installer below does: the `quicksand` user,
`/etc/quicksand`, `/var/lib/quicksand` and a self-signed certificate. The
example configuration and schema are in `/usr/share/doc/quicksand/`. Write
`/etc/quicksand/config.json` as described under "Install or upgrade" below,
then:

```sh
sudo systemctl enable --now quicksand
```

Upgrading the package restarts the service. Removing it stops the service;
purging the .deb also deletes `/etc/quicksand`, but the database in
`/var/lib/quicksand` is kept. The `quicksand` command uses
`/etc/quicksand/config.json` unless there is a `config.json` in the current
directory or you pass `-config`.

To build the packages yourself, run `./scripts/build-packages.sh` after
the build scripts; it writes `dist/packages/`.

## Run in Docker

The server is also published as an image, `ghcr.io/andyjsmith/quicksand`:
`latest` and the version (e.g. `1.2.0` or `1.2`) for releases, and `main`
for the newest commit on main. [compose.yml](../compose.yml) runs it:

```sh
mkdir -p quicksand/config && cd quicksand
curl -LO https://raw.githubusercontent.com/andyjsmith/quicksand/main/compose.yml
# write config/config.json (see "Install or upgrade" below), then:
docker compose up -d
docker compose exec quicksand quicksand user add -admin <username>
```

`./config` is mounted at `/etc/quicksand`, read-only: `config.json`, the
template `icons` folder next to it, and any TLS certificate. The server runs
as user ID 10001, which needs to be able to read them. The database is kept
in the `data` volume, at `/var/lib/quicksand` (the default `"database":
"quicksand.db"` puts it there).

Some differences from the packages:

- **Listen address.** The container needs `"listen": ":8484"` rather than
  `127.0.0.1`. Its user can't bind ports below 1024, so it listens on 8484;
  change the published port in `compose.yml` (e.g. `"443:8484"`) to serve
  it elsewhere on the host.
- **Agent network.** The Docker host needs the agent network address,
  `agent.network.serverIP`, as for any Quicksand host
  ([proxmox-setup.md](proxmox-setup.md#64-the-quicksand-host)): connections
  to agents leave the host from it.
- **HTTPS.** No certificate is generated. Add one to `./config` and a `tls`
  section pointing at it (`/etc/quicksand/tls.crt` and `tls.key`), or
  make a self-signed one with:

  ```sh
  openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 -nodes -days 825 \
    -subj "/CN=$(hostname)" -addext "subjectAltName=DNS:$(hostname)" \
    -keyout config/tls.key -out config/tls.crt
  ```

Upgrade with `docker compose pull && docker compose up -d`. Logs are in
`docker compose logs -f`. To build the image yourself, run
`./scripts/build-server.sh && docker build -t quicksand .`: it packages the
binary that builds, as CI does.

## Build

On a machine with Go and Node.js:

```sh
./scripts/build-server.sh     # writes dist/server/: quicksand, quicksand.service, install.sh
```

## Install or upgrade

Copy `dist/server/` to the host and run the installer as root:

```sh
scp -r dist/server host:quicksand-install
ssh host sudo ./quicksand-install/install.sh
```

The installer is idempotent and never overwrites existing configuration,
keys, certificates or data. On first run it:

- creates the `quicksand` system user, `/etc/quicksand` (config, certificate) and
  `/var/lib/quicksand` (database);
- installs `/usr/local/bin/quicksand` and the hardened `quicksand.service`
  (runs as `quicksand`, may bind port 443, read-only filesystem except its
  data directory);
- generates a self-signed certificate `/etc/quicksand/tls.crt`/`tls.key` for
  the host's name and addresses.

Then write `/etc/quicksand/config.json` (see `config.example.json`; on the host
use `"listen": ":443"`, `"database": "/var/lib/quicksand/quicksand.db"`,
`"secureCookies": true`, `"tls": {"cert": "/etc/quicksand/tls.crt", "key": "/etc/quicksand/tls.key"}`
and an `agent` section if you use the agent), make it
readable by the service only:

```sh
chown root:quicksand /etc/quicksand/config.json && chmod 0640 /etc/quicksand/config.json
```

and run the installer again (it starts the service once a config exists).
Upgrades are the same build-copy-install steps; the service restarts on the
new binary.

`config.schema.json` (in the repository) describes every setting: put
`"$schema": "./config.schema.json"` at the top of a config file next to it
for autocompletion and checking in editors such as VS Code.

### Template icons

Templates show an icon for their OS unless they have their own: put images
in an `icons` folder next to the configuration file (for the packages,
`/etc/quicksand/icons/`, readable by the `quicksand` user) and name one in
the template:

```json
"templates": [{ "id": "firefox", "icon": "firefox.png", ... }]
```

PNG, JPEG, GIF, WebP and SVG are accepted, up to 512 KiB; square images look
best. Quicksand reads them when it starts (restart it after adding or
changing one), and refuses to start if one is missing or isn't an image.

### App templates

A template's `launch` has the agent keep one app running in its VMs, such as
a browser, and `openURL` lets it be deployed to open a link. Several
templates can share a Proxmox template. See
[app-templates.md](app-templates.md) for the settings and for building a
lightweight browser template.

### Expiry, suspension and quotas

```json
"expiry": { "timeLimit": "7d", "extendDuration": "24h", "maxExtends": 3 },
"suspend": { "idleTime": "2h" },
"quota": { "user": 5, "global": 50 },
"templates": [{ "id": "win10", "quota": { "user": 1, "global": 4 }, ... }]
```

- `expiry`: VMs are destroyed `timeLimit` after they're deployed (durations
  like `"36h"` or `"7d"`). Owners can push that back by `extendDuration`
  (default 24h), up to `maxExtends` times: unset is unlimited, `0` never.
  Administrators can always extend. Without `expiry` or a `timeLimit`, VMs
  never expire. When expiry is first configured, existing VMs get a full
  time limit from that moment. VMs close to expiring (within a day, or a
  quarter of the time limit if that's shorter) are flagged in red.
- `suspend`: running VMs nobody has used for `idleTime` are hibernated:
  Proxmox saves their memory to disk and stops them, freeing the host's
  memory and CPU, and starting them again resumes them where they left off.
  A VM is in use while someone has a console, shell, port forward or serial
  console open on it, or is working with its files or processes through the
  agent; work the guest does on its own (a long build, say) doesn't count, so
  set `idleTime` with that in mind. The idle clock restarts whenever a VM is
  started or the server restarts. Suspension doesn't pause expiry: a
  hibernated VM is still destroyed when it expires. Without `suspend` or an
  `idleTime`, VMs are never suspended. Users can also hibernate their VMs
  themselves, whether or not this is configured.
- `quota`: the most VMs one user (`user`) or everyone together (`global`) may
  have at once. The top-level quota counts every template; a template's own
  counts only its VMs. Unset limits are unlimited, and quotas apply to
  administrators too.

## First administrator

```sh
sudo -u quicksand quicksand user add -config /etc/quicksand/config.json -admin <username>
```

Further users are managed on the Admin page (or with `quicksand user`).
To sign users in through Keycloak or another OpenID Connect provider,
alongside passwords or instead of them, see
[authentication.md](authentication.md).

## Agent key

The `agent` section needs a private key: run `quicksand agent keygen` and
put the printed `privateKey` in `config.json`. Templates' agent configs list
the matching public key (also shown on the Admin page). Back the config up:
a new key means updating the agent config in every template.

## HTTPS certificate

Serve Quicksand over HTTPS: browsers only allow clipboard access and audio
decoding on secure origins. The generated certificate is self-signed, so
browsers warn until it's accepted. To use one from your own CA, replace
`/etc/quicksand/tls.crt` (with intermediates appended) and `tls.key`
(group `quicksand`, mode 0640) and `systemctl restart quicksand`.

## Behind a reverse proxy

Behind a reverse proxy, every request comes from the proxy, so the audit
log would show its address for everyone. List the proxy in
`trustedProxies` (addresses or CIDR ranges) and the log shows each client's
own address, from the `X-Forwarded-For` header the proxy adds (or
`X-Real-IP`, if that's all it sends):

```json
"trustedProxies": ["127.0.0.1", "::1"]
```

List the address requests reach Quicksand *from*: `127.0.0.1` for a proxy
on the same host, or, in Docker with `ports:` mappings, the Docker
network's gateway (e.g. `172.17.0.1`). Headers from anywhere else are
ignored, as anyone could send them. The proxy must also pass WebSocket
upgrades through, for consoles and shells.

## Operating

```sh
systemctl status quicksand
journalctl -u quicksand -f
quicksand version
```

The version is also in the web client's menu under your name, and
`qs version` shows both its own and the server's.

### Logs

- **The audit log** of what users do is on the Admin page and kept in the
  database for a year by default. Set `"audit": {"retention": "90d"}` to
  change that, or `"0d"` to keep entries forever. See
  [authentication.md](authentication.md#audit-log).
- **The service's own log** goes to the systemd journal, which decides how
  long it's kept: set `SystemMaxUse=` or `MaxRetentionSec=` in
  `/etc/systemd/journald.conf`.

Back up `/etc/quicksand` and `/var/lib/quicksand/quicksand.db` (SQLite; copy
it while the service is stopped, or use `sqlite3 quicksand.db ".backup out.db"`).
