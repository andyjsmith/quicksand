# qs, the command-line client

`qs` talks to a Quicksand server's API as a signed-in user. It's a single
binary with no dependencies; build it for common platforms with
`./scripts/build-cli.sh` (writes `dist/cli/`).

## Signing in

```sh
qs login https://quicksand.example
```

`qs` asks for your username and password and saves the session in
`~/.config/quicksand/cli.json` (`%AppData%\quicksand\cli.json` on Windows),
readable only by you. If the server's certificate isn't trusted by your
system, as with the self-signed one the installer creates, `qs` shows its
SHA-256 fingerprint and asks whether to trust it; the answer is pinned, so a
replaced certificate is refused until you sign in again. Pass
`-fingerprint` to pin a known fingerprint without the prompt.

`qs whoami` shows the account in use; `qs logout` ends the session.

## Commands

VMs are named by their name, Proxmox VM ID or Quicksand ID. Administrators
can name anyone's VM.

| Command | |
| --- | --- |
| `qs ls [-a]` | Your VMs (`-a`: everyone's, for administrators) |
| `qs templates`, `qs networks` | What you can deploy, and onto which networks |
| `qs deploy [options] <template>` | Deploy and wait until ready. Options: `-name`, `-cpus`, `-memory` (MiB), `-network`, `-no-agent` (no agent NIC: the VM is isolated from Quicksand), `-no-wait` |
| `qs start\|shutdown\|stop\|reboot\|reset\|suspend\|resume <vm>` | Power actions |
| `qs destroy [-y] <vm>` | Destroy a VM and its disks |
| `qs ps <vm>` | The guest's processes (needs the agent) |
| `qs kill [-f] <vm> <pid>` | End a guest process; `-f` sends SIGKILL on Linux (Windows always terminates) |
| `qs netstat <vm>` | The guest's TCP and UDP sockets and their processes |
| `qs shell [-t\|-T] [-shell program\|-serial] <vm> [command...]` | A shell in the guest, or run a command there (see below) |
| `qs forward <vm> <forward>...` | Forward local ports into the guest's network (see below) |

## Shells

`qs shell <vm>` opens an interactive shell in the guest: your login shell on
Linux, PowerShell on Windows (cmd.exe on releases before Windows 10 1809,
which lack pseudo-consoles). The shell starts in the guest's files
directory. Exit it as usual to return.

`qs shell <vm> <command...>` runs one command through the guest's shell
(`sh -c`, or `cmd /c` on Windows) and exits with its exit code. Like `ssh`,
it gives the command pipes rather than a terminal, so stdin, stdout and
stderr can be redirected:

```sh
qs shell web1 'tar czf - /etc' > etc.tar.gz
qs shell web1 sort < names.txt
```

`-t` gives a command a terminal anyway (for programs like `top`); `-T`
gives an interactive shell pipes instead. Options go before the VM.

`-shell` picks another shell program, by name (looked up in the guest's
PATH) or path, for both interactive shells and commands:

```sh
qs shell -shell cmd win1                 # cmd.exe instead of PowerShell
qs shell -shell pwsh win1 'Get-Date'     # a command through PowerShell 7
qs shell -shell zsh web1
```

Commands run through it with `/c` for cmd, `-Command` for PowerShell and
`-c` for anything else. The web UI's Shell tab has the same choice.

### Serial console

For templates that enable it (`"serial"` in the server's template config),
`qs shell` falls back to the VM's serial console when the VM has no
connected agent, and `qs shell -serial <vm>` uses it regardless. It's a
terminal on the guest's login prompt, so it can't run single commands.
Press Enter if nothing shows, and **Ctrl-]** to disconnect; the console stays
logged in after you disconnect, so log out first.

## Port forwarding

`qs forward` forwards local TCP ports to addresses as the guest reaches
them, like `ssh -L` (local forwards only). Each forward is
`[bind_address:]port:host:hostport`; give as many as you like:

```sh
qs forward web1 8080:localhost:80             # http://localhost:8080 → the guest's port 80
qs forward web1 5432:db.internal:5432 9999:8.8.8.8:53
qs forward web1 0.0.0.0:3389:localhost:3389   # listen on all interfaces
```

Without a bind address, ports listen on 127.0.0.1 only; `*` means all
interfaces. Put IPv6 addresses in brackets (`[::1]:8080:[fe80::1]:22`).
`host` is resolved in the guest, so `localhost` is the guest itself. It needs
the agent, and runs in the foreground until Ctrl-C.
