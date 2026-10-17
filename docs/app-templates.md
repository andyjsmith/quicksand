# App templates

An app template is a Quicksand template whose VMs come up running one app,
such as Chrome or Firefox, instead of a desktop. Several templates can share
one Proxmox template: a single lightweight Linux VM with both browsers
installed can be offered as two templates, **Chrome** and **Firefox**, each
launching its own browser.

App templates can also **open links**: deploy one with a URL and the app
starts on that page. That's what an "Open in Quicksand" browser extension
uses (see [Opening links](#opening-links)).

This page covers the configuration, how launching works, and how the
Openbox browser template (`qs-browser`) was built.

## Configuration

Add `launch` to templates with the agent. Both of these use the same Proxmox
template, VM 227:

```json
"templates": [
  {
    "id": "chrome", "name": "Chrome", "description": "Google Chrome in a lightweight Linux VM.",
    "vmid": 227, "node": "violet", "os": "linux", "agent": true,
    "launch": {
      "command": ["google-chrome", "--start-maximized", "--no-first-run", "--no-default-browser-check", "--password-store=basic"],
      "openURL": ["google-chrome", "--start-maximized", "--no-first-run", "--no-default-browser-check", "--password-store=basic", "{url}"]
    }
  },
  {
    "id": "firefox", "name": "Firefox", "description": "Firefox in a lightweight Linux VM.",
    "vmid": 227, "node": "violet", "os": "linux", "agent": true,
    "launch": { "command": ["firefox"], "openURL": ["firefox", "{url}"] }
  }
]
```

- `command` is the program and its arguments. No shell is involved, so there
  is no quoting to get wrong. Programs are looked up in the desktop user's
  `PATH`.
- `openURL`, if set, lets the template open links. When a VM is deployed with
  a URL, the app is started with this command instead of `command`, with
  `{url}` replaced by the URL. `{url}` can be a whole argument or part of one
  (`"--app={url}"`). Only `http` and `https` URLs are accepted, so a URL can't
  be mistaken for a command-line option.
- `restart` (default `true`) starts the app again whenever it exits, so
  closing the browser brings a fresh one back. With `false`, the app is
  started once each time someone signs in, and **Relaunch** in the VM's
  actions menu (or `qs relaunch <vm>`) starts it again.

Quotas, expiry and suspension apply per template ID, so Chrome and Firefox
are counted separately even though they share a Proxmox template.

## How launching works

Launching is done by the guest agent (Linux guests only, for now):

1. When a VM's agent first answers after it boots, Quicksand sends it the
   template's launch settings (`PUT /v1/launch`), plus the URL the VM was
   deployed with, if any. Until then the URL is kept with the VM in
   Quicksand's database, so a server restart doesn't lose it.
2. The agent saves the settings in `/var/lib/quicksand-agent/launch.json`, so
   the app comes back after a guest reboot without the server.
3. The agent waits for someone to be signed in to the guest's screen (seat0;
   in the browser template, LightDM signs the user in automatically). It
   then starts the app as that user, with the environment of their session
   (`DISPLAY`, `XAUTHORITY`, `WAYLAND_DISPLAY`, session bus and so on).
4. If the app exits and `restart` is on, it's started again after a delay
   that grows with quick failures: 1s, 2s, 4s, up to a minute, and back to
   1s once it has run for a minute. If someone signs in afresh, the app is
   started in the new session.
5. Every 15 seconds Quicksand compares what each agent reports with its
   template, and sends the settings again if they differ. So editing
   `launch` (or removing it) reaches running VMs too.

The VM's page shows the app and whether it's running, or why not ("no one
is at the guest's screen", "google-chrome isn't installed", the last line
the app printed before failing, and so on).

Stopping or restarting the agent stops the app; the new agent starts it
again. Agents from before app launching answer the settings with a 404, and
the VM's page says the agent is too old: update the agent in the template.

## Opening links

"Open in Quicksand" always deploys a new VM; it never opens a link in a VM
that's already running. There are three ways in:

- **The browser extension** adds "Open in Quicksand" to the menu of links.
  See [extension.md](extension.md).
- **The `/open` page**, `https://quicksand.example/open?template=chrome&url=<link>`
  (with the link URL-encoded, and optionally `&network=<ID>` to choose the
  network to start with), is what the extension uses, and works from
  anywhere: bookmarklets, other tools. It runs in the user's own Quicksand
  session (sending them to sign in first if needed), shows the link, the
  template and the network (which they can change), and deploys when they
  click **Open**. It then goes to the new
  VM's console, which shows the deployment and connects when it's up. It
  asks first because any site could send someone there.
- **The API**, with an API token (`Authorization: Bearer qst_...`):
  `GET /api/templates` lists templates, with `"opensURLs": true` on those that
  can open links; `POST /api/vms` with
  `{"templateId": "chrome", "url": "https://example.com/"}` deploys one (with
  a generated name and the template's defaults unless given); and
  `GET /api/vms/{id}` follows it until `state` is `ready` and
  `launch.running` is true. From the command line:
  `qs deploy --url https://example.com/ chrome`.

A template that can't open links, or a URL that isn't `http`/`https`, is
refused with an explanation.

## Building the Openbox browser template

`qs-browser` (VM 227) is Ubuntu 26.04 that signs straight in to a bare
Openbox session instead of GNOME, with Chrome and Firefox installed. It idles
at about 650 MB of memory, and the browser fills the console with no window
decorations. These are the steps used to build it.

### 1. Start from a Linux template with the agent

It was built from `qs-ubuntu26-agent` (VM 229), the Ubuntu 26.04 desktop
template with the Quicksand agent installed. Any Ubuntu or Debian VM with
the agent works. The agent must be recent enough to launch apps: check that
`quicksand-agent -version` is at least the version of the server.

Make a **full** clone to work on. The easiest way is through Quicksand, so the
agent can be used to set it up: add a temporary template with `"fullClone":
true` pointing at the starting template, deploy it, and use `qs exec`,
`qs upload` and the console. (Cloning in Proxmox directly also works, but the
VM then has no agent network and needs setting up from the console.)

### 2. Run the setup script

Copy [`scripts/app-template/`](../scripts/app-template/) into the guest and
run it as root, naming the user to sign in:

```sh
qs upload build ./scripts/app-template/setup-openbox-browsers.sh /root/
qs upload build ./scripts/app-template/quicksand-follow-display /root/
qs exec --timeout 1800 build 'sh /root/setup-openbox-browsers.sh andy'
```

The script:

- **Installs the session:** LightDM with its GTK greeter, Openbox,
  `python3-xdg` (so Openbox runs the XDG autostart entries, including
  spice-vdagent's), Xorg, `x11-xserver-utils`, `xclip` (for clipboard
  sharing through the agent) and `spice-vdagent`, all with
  `--no-install-recommends`.
- **Installs Firefox from Mozilla's APT repository** instead of the snap,
  which is slow to start and awkward to set policies for. Ubuntu's `firefox`
  package is a stub for the snap with a higher version number, so the
  repository is pinned to priority 1000 and installed with
  `--allow-downgrades`.
- **Installs Chrome** from Google's `.deb` (which also adds Google's
  repository for updates).
- **Turns off first-run screens** with browser policies: Chrome's in
  `/etc/opt/chrome/policies/managed/quicksand.json` (no default-browser
  prompt, sign-in, sync, promotions or password manager), Firefox's in
  `/etc/firefox/policies/policies.json` (no default-browser check, telemetry,
  first-run or what's-new pages, onboarding, and `SkipTermsOfUse`, without
  which Firefox covers the first page with a terms-of-use dialog).
- **Signs the user in automatically**: adds them to the `autologin` group and
  writes `/etc/lightdm/lightdm.conf.d/50-quicksand.conf` with
  `autologin-user` and `autologin-session=openbox`. GDM is disabled and
  LightDM becomes the display manager.
- **Makes every window fill the screen**: an Openbox rule in
  `/etc/xdg/openbox/rc.xml` maximizes normal windows and removes their
  decorations.
- **Sets up the session's autostart** (`/etc/xdg/openbox/autostart`):
  `xset s off -dpms` so the screen never blanks, a plain background, and
  `quicksand-follow-display`.
- **Speeds up booting**: no GRUB menu even after an unclean shutdown, and
  no snapd. See [Faster startup](#faster-startup).

`quicksand-follow-display` keeps the display the size of the Quicksand
console. QEMU offers the console's size as the display's preferred mode;
GNOME switches to it by itself, but a bare X session stays at 800×600. The
helper checks once a second and runs `xrandr --output <output> --preferred`
when the current mode isn't the preferred one. (Quicksand's own agent-driven
resizing is only used on Linux as a fallback with SPICE, and never with VNC,
so it can't be relied on here.)

GNOME stays installed but never runs. Purging it would save disk space, not
memory.

### 3. Check it

Reboot and check that the session is Openbox, not GNOME:

```sh
qs exec build 'loginctl show-session $(loginctl show-seat seat0 -p ActiveSession --value) -p Type -p Desktop; pgrep -a gnome-shell'
```

It should say `Type=x11` and `Desktop=openbox`, with no gnome-shell. To try
launching before making the template, give the temporary template a `launch`
section and wait for the app to appear in the console. Remove it again
afterwards and let Quicksand tell the agent to launch nothing (within 15
seconds).

### 4. Clean up and convert

Inside the guest, remove anything a clone shouldn't inherit, then power off:

```sh
qs exec build 'rm -rf /var/lib/quicksand-agent/launch.json \
  /home/andy/.config/google-chrome /home/andy/.cache/google-chrome \
  /home/andy/.mozilla /home/andy/.cache/mozilla \
  /root/setup-openbox-browsers.sh /root/quicksand-follow-display; apt-get clean'
qs exec build 'systemd-run --on-active=2 systemctl poweroff'
```

Make sure no app is still running first (no `launch` on the build template,
and `pgrep chrome` empty). A browser that is closing rewrites its profile,
and a saved `launch.json` would make every clone launch that app until
Quicksand corrects it.

If the VM was deployed through Quicksand, remove what Quicksand added before
converting it. In the Proxmox UI, or with the API:

- delete the agent NIC (`net1` by default, on the agent bridge);
- under the VM's **Firewall**, delete the rule "Quicksand: agent connections
  from the Quicksand server", the IP set `ipfilter-net1` (empty it first),
  and reset the firewall options (`enable`, `ipfilter`, `macfilter`, `dhcp`,
  `ndp`, `radv`, `policy_in`, `policy_out`) to their defaults;
- check `net0` matches the original template (here
  `bridge=vmbr0,firewall=1`).

Then rename the VM (here `qs-browser`), convert it to a template, and forget
the deployment in Quicksand without destroying it. Destroying it through
Quicksand would delete the new template. (With a scratch Quicksand
instance used just for building, deleting its database is enough.)

Finally, point the app templates' `vmid` at it, as in
[Configuration](#configuration).

### Faster startup

Every app VM boots from scratch, so seconds at boot are seconds of waiting
for the link to open. Measured on `qs-browser` (Ubuntu 26.04, 4 CPUs, 3 GiB):
before the changes below, it took about 45 seconds from power-on to Firefox
starting, 35 of them before Linux even started. After them it takes about 13:
6 in the firmware and GRUB, then Linux has the user signed in 4.4 seconds
after it starts and the agent launches the app 2 seconds later. With the 7
seconds Quicksand takes to clone and start a VM, a link opens about 20
seconds after clicking **Open**.

**GRUB's menu.** Ubuntu hides GRUB's menu (`GRUB_TIMEOUT=0`), except after a
boot that didn't finish cleanly: then it shows it for 30 seconds
(`GRUB_RECORDFAIL_TIMEOUT`). A throwaway VM hits that all the time: force
stops, resets, a template whose saved GRUB state still says the last boot
failed. That was the 30 seconds. The setup script turns it off:

```sh
cat > /etc/default/grub.d/90-quicksand.cfg <<'G'
GRUB_TIMEOUT=0
GRUB_TIMEOUT_STYLE=hidden
GRUB_RECORDFAIL_TIMEOUT=0
G
update-grub
grub-editenv /boot/grub/grubenv unset recordfail
```

Tested by setting `recordfail` and force-resetting the VM: Linux started
straight away. (Hold Shift, or Esc on UEFI, during boot to get the menu
when you need it.)

**snapd.** On the Ubuntu desktop, `snapd.seeded.service` sat on the boot's
critical path for about a second, and nothing in this template needs snaps
once Firefox comes from Mozilla's repository. The setup script purges
snapd, which also frees about 2 GB of disk.

**Finding what else is slow.** In the guest:

```sh
systemd-analyze                              # kernel, initrd and userspace time
systemd-analyze critical-chain graphical.target
systemd-analyze blame | head -20
journalctl -b -o short-monotonic | grep -E 'session opened|launched app'
```

The last line shows when LightDM signed the user in and when the agent
launched the app, in seconds since the kernel started. To see time spent
before Linux (firmware and GRUB), compare the VM's uptime in Proxmox with
`/proc/uptime` in the guest right after a cold start.

Other things worth trying, depending on what `blame` shows: disabling
services an app VM doesn't need (`cups`, `ModemManager`, `bluetooth`,
`kerneloops`, `whoopsie`), and the `apt-daily` and `unattended-upgrades`
timers, which compete with the app for CPU early on and are pointless in a
VM that's thrown away. Giving the template more CPUs also helps the browser
itself start faster.

**Outside the guest.** Linked clones (the default, without `fullClone`)
deploy in a few seconds; full clones copy the disk and take much longer.
Keep the disk first in the boot order and detach the installer ISO from the
template (`ide2`), so clones don't depend on it.

### Changing the template later

Proxmox doesn't let a template's disk change once VMs are cloned from it.
To change it, make a full clone, change that, convert it to a new template,
point the configuration at the new VM ID, and delete the old template once
no VMs use it.
