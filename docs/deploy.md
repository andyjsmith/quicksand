# Deploying Quicksand

Quicksand runs as a single binary (the web client is embedded) under
systemd, on a Debian-like host that can reach the Proxmox API (8006) and
SPICE proxy (3128). If you use the agent, the host also needs its NIC on the
agent network ([proxmox-setup.md](proxmox-setup.md#64-the-quicksand-host)).

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

## First administrator

```sh
sudo -u quicksand quicksand user add -config /etc/quicksand/config.json -admin <username>
```

Further users are managed on the Admin page (or with `quicksand user`).

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
```

Back up `/etc/quicksand` and `/var/lib/quicksand/quicksand.db` (SQLite; copy
it while the service is stopped, or use `sqlite3 quicksand.db ".backup out.db"`).
