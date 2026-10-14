# Proxmox setup for Quicksand

This is everything Quicksand needs configured in Proxmox VE: an API token
with the right permissions, a resource pool, templates, the networks users
can pick, and the optional, isolated **agent network** Quicksand uses to
reach the [guest agent](agent.md).

Commands are run as root on a Proxmox node. Names used throughout (change
them consistently if you like):

| Thing                        | Name used here             |
| ---------------------------- | -------------------------- |
| Proxmox user and API token   | `quicksand@pve!quicksand`  |
| Resource pool                | `quicksand`                |
| Internal agent bridge        | `vmbr9`                    |
| Agent SDN zone and VNet      | `qsagent` (VLAN tag `10`)  |
| Agent subnet                 | `10.99.0.0/24`             |
| Quicksand's agent address    | `10.99.0.1`                |
| Agent port                   | `7070`                     |

Requires Proxmox VE 8.3 or later (VNet port isolation). Written against 9.2.

## 1. API user and token

```sh
pveum user add quicksand@pve --comment "Quicksand"
pveum user token add quicksand@pve quicksand --privsep 1 --comment "Quicksand server"
```

Save the token secret it prints; it goes in `config.json` as
`pve.tokenSecret` (`pve.tokenId` is `quicksand@pve!quicksand`).

With privilege separation (`--privsep 1`) a token only gets permissions
granted to *both* the token and its user, so every `pveum acl modify` below
grants to both. Define a shorthand:

```sh
grant() { pveum acl modify "$1" --roles "$2" --users quicksand@pve --tokens 'quicksand@pve!quicksand'; }
```

## 2. Resource pool for deployed VMs

Quicksand clones every VM into a pool, and its VM permissions come from the
pool. Permissions on individual VM IDs (`/vms/<id>`) don't work: Proxmox
deletes a VM's ACL entries whenever the VM is destroyed, so they'd vanish
after the first teardown. Pool permissions persist.

```sh
pveum pool add quicksand --comment "VMs deployed by Quicksand"
grant /pool/quicksand PVEVMAdmin
```

`PVEVMAdmin` covers cloning into the pool, configuration (CPU, memory,
network, firewall), power, consoles and deletion.

Set `pve.pool` to `quicksand` and pick a VM ID range no one else uses for
`vmids` in `config.json`.

## 3. Storage

Clones are created on the template's storage. Allow allocating space there:

```sh
grant /storage/<storage> PVEDatastoreUser    # e.g. /storage/local-lvm
```

## 4. Templates

For each template listed in `config.json` (`templates`):

```sh
pveum role add QuicksandTemplate --privs "VM.Audit VM.Clone"   # once
grant /vms/<template-vmid> QuicksandTemplate
```

Recommendations for the template VM itself:

- **Display**: `qxl` or `virtio` for SPICE (Quicksand falls back to VNC for
  other display types). Install the SPICE guest tools (`spice-vdagent`;
  `spice-guest-tools` on Windows) for resizing and clipboard sharing.
- **Audio**: add an audio device with the `spice` driver for guest sound.
- **QEMU guest agent** (optional): Quicksand uses it to show when a VM has
  finished booting.
- **USB tablet**: Proxmox leaves the tablet out of `qxl` VMs unless `tablet`
  is set explicitly, which makes the VNC mouse drift. Its web UI can't set it
  (it deletes the key instead), so run `qm set <vmid> --tablet 1`.
- **Network**: `net0` is replaced by the network the user picks (its bridge is
  changed, its model and options kept). Note that Proxmox checks `SDN.Use` on
  the template's *own* bridge when cloning, so it must be one of the bridges
  granted in section 5.
- **Firewall**: keep the template's VM firewall rules empty; clones inherit
  them.
- **Quicksand agent** (optional): see section 6 and [agent.md](agent.md).
- **Serial console shell** (optional, Linux): set `"serial": "serial0"` on
  the template in `config.json` to offer shells over the serial console, in
  the web UI's Shell tab and with `qs shell`, for VMs without a connected
  agent (or on request). Deploys add the port as a socket if the template
  lacks it. The guest needs a login prompt on it: on Linux, enable
  `serial-getty@ttyS0.service` (or boot with `console=ttyS0`). Windows has
  no serial login, so this doesn't help Windows templates.

## 5. Networks users choose from

Each entry in `config.json` `networks` is a bridge (or SDN VNet), optionally
with a VLAN tag, shown to users by its label. `"bridge": null` is an isolated
network: Quicksand removes the clone's network interfaces entirely (an
interface without a bridge would get QEMU user-mode NAT, i.e. internet
access).

Grant `SDN.Use` on every bridge used, including each template's own bridge:

```sh
grant /sdn/zones/localnetwork/vmbr0 PVESDNUser       # a plain Linux bridge
grant /sdn/zones/<zone>/<vnet> PVESDNUser            # an SDN VNet
```

## 6. Agent network (optional)

Skip this section unless templates have the Quicksand agent installed
(`"agent": true` on the template). Deployed VMs from such templates get a
second NIC on this network, and Quicksand connects to the agent through it.

### How it is isolated

```
                     vmbr9 (internal, no physical ports, VLAN-aware)
                       │
   Quicksand host ─────┤ tag 10, plain port (not isolated)      10.99.0.1
                       │
                       └── VNet qsagent (VLAN zone, tag 10, "Isolate Ports")
                              ├── VM A net1 (isolated port)       10.99.0.2
                              ├── VM B net1 (isolated port)       10.99.0.3
                              └── ...
```

The layers, from the bottom:

1. **Layer 2 – port isolation.** The VNet has *Isolate Ports* set: the Linux
   bridge never forwards a frame from one isolated port to another — no
   unicast, broadcast, multicast or ARP. Deployed VMs can't see each other at
   all. They can reach non-isolated ports, which is why the Quicksand host is
   attached to the parent bridge `vmbr9` with the same VLAN tag rather than to
   the VNet (every guest port on a VNet is isolated, with no exceptions).
2. **No routing.** The subnet has no gateway; nothing on it routes anywhere.
   Agents get no default route, so a VM's normal traffic keeps using the
   network its user picked. Because no host routes between agent addresses,
   there is no layer-3 path around the isolation either.
3. **Per-VM firewall (managed by Quicksand).** On each clone Quicksand:
   - puts the agent NIC behind the VM firewall and turns the firewall off on
     the VM's other NICs, so they behave exactly as before;
   - sets inbound and outbound policy to `DROP` and adds one rule: inbound TCP
     to the agent port from `serverIP` only. Replies to Quicksand's
     connections are allowed by connection tracking; the guest can't open
     connections on this NIC (not to Quicksand, not to anything);
   - enables `ipfilter` with the VM's assigned address and `macfilter`, so
     the guest can't use any other IP or MAC address on the agent network
     (no spoofing other VMs or Quicksand).
4. **Agent authentication.** Agents only accept TLS clients holding
   Quicksand's private key (see [agent.md](agent.md)).
5. **Quicksand host firewall.** Its own agent NIC drops unsolicited inbound
   traffic, as a second line behind layer 3.

Addresses: there is no DHCP. Quicksand gives each agent NIC a MAC address
that encodes its IP (`02:51:53:00:00:17` → `10.99.0.23`), and the agent
assigns that address to the NIC itself (with no gateway). Quicksand knows the
address before the VM starts, which is also what its `ipfilter` entry pins.

### 6.1 Internal bridge

Add a bridge with no physical ports, so agent traffic never leaves the host.
In `/etc/network/interfaces`:

```
auto vmbr9
iface vmbr9 inet manual
        bridge-ports none
        bridge-stp off
        bridge-fd 0
        bridge-vlan-aware yes
        bridge-vids 2-4094
#Quicksand agent network (internal)
```

Apply with `ifreload -a`. The host itself gets no address on it.

**Clusters:** a bridge without ports exists on one node only. The Quicksand
host and every VM it deploys (`node` of each template) must be on the same
node, unless you connect `vmbr9` between nodes (a dedicated NIC/VLAN, or use
an SDN VXLAN zone instead of the VLAN zone below — port isolation works the
same way).

### 6.2 SDN zone and VNet

Datacenter → SDN → Zones → Add → **VLAN**: ID `qsagent`, bridge `vmbr9`.
Then VNets → Create: name `qsagent`, zone `qsagent`, tag `10`, and tick
**Isolate Ports**. Don't add a subnet. Finally SDN → **Apply**.

Or from the shell:

```sh
pvesh create /cluster/sdn/zones --zone qsagent --type vlan --bridge vmbr9
pvesh create /cluster/sdn/vnets --vnet qsagent --zone qsagent --tag 10 --isolate-ports 1
pvesh set /cluster/sdn
```

Let Quicksand attach VMs to it:

```sh
grant /sdn/zones/qsagent/qsagent PVESDNUser
```

### 6.3 Firewall

The per-VM rules only take effect when the firewall is enabled for the
datacenter: Datacenter → Firewall → Options → **Firewall: Yes**.

Turning it on also applies the host firewall (input policy `DROP` by
default). Proxmox allows its web UI, SSH, VNC/SPICE and cluster traffic from
the local network out of the box, but check before enabling that the
machines that need the host can still reach it:

- your admin workstation: TCP 8006 (and 22);
- the Quicksand host (its management address): TCP 8006 (API) and 3128
  (SPICE proxy).

If they aren't on the node's local network, add host rules for them first
(Datacenter → Firewall → Add), for example:

```sh
pvesh create /cluster/firewall/rules --type in --action ACCEPT --source <quicksand-mgmt-ip> \
  --proto tcp --dport 8006,3128 --enable 1 --comment "Quicksand API and SPICE proxy"
```

Either firewall backend works (the default iptables one, or nftables).
Nothing else needs configuring: Quicksand writes each clone's firewall itself.

### 6.4 The Quicksand host

Quicksand itself runs in a VM or container with two NICs:

- `net0`: the management network (reaches the Proxmox API and SPICE proxy,
  and serves the web UI);
- `net1`: **bridge `vmbr9`, VLAN tag `10`**, static address `10.99.0.1/24`,
  no gateway, firewall on.

For a container (`<ctid>` is its ID):

```sh
pct set <ctid> --net1 name=eth1,bridge=vmbr9,tag=10,ip=10.99.0.1/24,firewall=1
pvesh set /nodes/<node>/lxc/<ctid>/firewall/options --enable 1 --policy_in DROP
```

`policy_in DROP` drops anything agents try to send it unsolicited; Quicksand's
own outbound connections and their replies are unaffected. If the container
already has inbound rules for `net0` (e.g. allowing the web UI), keep them, or
add `--iface net0` accept rules for the ports you serve on.

For a VM, add the NIC with `qm set <vmid> --net1 virtio,bridge=vmbr9,tag=10,firewall=1`
and configure `10.99.0.1/24` (no gateway) inside it.

### 6.5 Quicksand configuration

Generate Quicksand's agent key:

```sh
quicksand agent keygen
```

It prints a private key for `config.json` and the matching public key for
agent configs. Then in `config.json`:

```json
"agent": {
  "privateKey": "ed25519-private:...",
  "port": 7070,
  "network": {
    "bridge": "qsagent",
    "subnet": "10.99.0.0/24",
    "serverIP": "10.99.0.1"
  }
}
```

Optional `network` fields: `nic` (default `net1`), `model` (default
`e1000`, which needs no guest drivers), `macPrefix` (default `02:51:53`;
the agents' config must match). Mark templates that have the agent with
`"agent": true`.

Keep the private key: templates trust its public key, so a new key means
updating every template. The public key is shown on the Admin page and by
`quicksand agent pubkey`.

### 6.6 Checking it

1. Deploy a VM from an agent template. Its card shows "agent" once the agent
   is up (green when connected); the console's side panel can run commands.
2. On the Quicksand host: `quicksand agent info 10.99.0.2` (the VM's agent
   address is shown in the console panel and on the Admin page).
3. Isolation, from inside one deployed VM (substitute another VM's agent
   address): `ping 10.99.0.3` and `arp -a` must show nothing from the other
   VM, and connecting to `10.99.0.1` on any port must fail.
4. In Proxmox, the clone's Firewall tab shows Quicksand's rule, the
   `ipfilter-net1` IP set, and the options (`DROP` policies, IP filter on).

## 7. Developing away from the Quicksand host

The agent network is only reachable from the Quicksand host. When running
Quicksand on another machine (e.g. a laptop), tunnel agent connections
through the Quicksand host with an SSH SOCKS proxy:

```sh
ssh -N -D 1080 root@<quicksand-host>
```

and set `"proxy": "socks5://127.0.0.1:1080"` in the `agent` section of the
development `config.json`. Connections then originate from the Quicksand
host's `10.99.0.1`, which is what the VM firewall allows.

Give the development setup its own key (`quicksand agent keygen`) and list
both public keys in the agent configs (`authorizedKeys` takes several), so
the production key never has to leave the Quicksand host.

## Summary of permissions

| Path                               | Role                 | For                               |
| ---------------------------------- | -------------------- | --------------------------------- |
| `/pool/quicksand`                  | `PVEVMAdmin`         | deployed VMs                      |
| `/vms/<template>`                  | `QuicksandTemplate`  | cloning each template             |
| `/storage/<storage>`               | `PVEDatastoreUser`   | disks of clones                   |
| `/sdn/zones/localnetwork/<bridge>` | `PVESDNUser`         | each plain bridge used            |
| `/sdn/zones/<zone>/<vnet>`         | `PVESDNUser`         | each SDN VNet used                |
| `/sdn/zones/qsagent/qsagent`       | `PVESDNUser`         | the agent network (if used)       |

Grant each to both the user and its token.
