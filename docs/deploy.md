# Deploying Quicksand

Quicksand runs as a single binary (the web client is embedded) under
systemd, on a Debian-like host that can reach the Proxmox API (8006) and
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
