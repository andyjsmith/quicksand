# qs, the command-line client

`qs` talks to a Quicksand server's API as a signed-in user. It's a single
binary with no dependencies. Download it from the [releases page](https://github.com/andyjsmith/quicksand/releases)
(binaries for Linux, macOS and Windows, and `quicksand-cli` .deb and .rpm
packages for Linux), or build it for common platforms with
`./scripts/build-cli.sh` (writes `dist/cli/`).

## Signing in

```sh
qs login https://quicksand.example
```

If the server has single sign-on, `qs` opens your browser to sign in there
and receives the result on a temporary `127.0.0.1` port; the browser must
run on the same machine as `qs`. Otherwise, or with `--password`, `qs` asks
for your username and password. See
[authentication.md](authentication.md#the-command-line-1).

Either way, `qs` gets an API token named after your computer ("qs on
laptop") and saves it in `~/.config/quicksand/cli.json`
(`%AppData%\quicksand\cli.json` on Windows), readable only by you. The
token lasts until it goes unused for 90 days, so `qs` stays signed in while
you use it. You see and revoke tokens under **API tokens**, in the menu
under your name in the web client; changing your password revokes them all.

On a computer without a browser, or for scripts, make a token on that page
and sign in with it:

```sh
qs login --token https://quicksand.example
```

`qs` asks for the token, or reads it from standard input
(`echo "$TOKEN" | qs login --token ...`), so it stays out of your shell's
history. If the server's certificate isn't trusted by your
system, as with the self-signed one the installer creates, `qs` shows its
SHA-256 fingerprint and asks whether to trust it; the answer is pinned, so a
replaced certificate is refused until you sign in again. Pass
`--fingerprint` to pin a known fingerprint without the prompt.

`qs whoami` shows the account in use, and `qs version` the versions of `qs`
and the server. `qs logout` signs out and revokes the token (a single
sign-on session at the provider stays signed in).

## Help

`qs help` lists the commands, and `qs help <command>` (or
`qs <command> --help`) shows a command's options. Long options take two
dashes (`--name`); one dash (`-name`) works too.

## Shell completion

`qs` completes its commands and options, and your VM names, template IDs
and network IDs (asked from the server, as you), in bash, zsh, fish and
PowerShell. The .deb and .rpm packages set this up for bash, zsh and fish.
Otherwise, load the script `qs completion <shell>` prints:

| Shell | Add to |
| --- | --- |
| bash | `~/.bashrc`: `source <(qs completion bash)` (needs the bash-completion package) |
| zsh | `~/.zshrc`, after `compinit`: `source <(qs completion zsh)` |
| fish | `qs completion fish > ~/.config/fish/completions/qs.fish` (once) |
| PowerShell | `$PROFILE`: `qs completion pwsh \| Out-String \| Invoke-Expression` |

Completing a VM or template asks the server, so it needs you to be signed
in; when the server can't be reached, `qs` just suggests nothing, and where
nothing is suggested the shell completes local paths.

## Commands

VMs are named by their name, Proxmox VM ID or Quicksand ID. Administrators
can name anyone's VM. While you have just one VM of a template, the
template's ID names it too: with only `win10-2e1782` deployed from `win10`,
`qs shell win10` opens a shell in it.

| Command | |
| --- | --- |
| `qs ls [-a]` | Your VMs (`-a`: everyone's, for administrators) |
| `qs templates`, `qs networks` | What you can deploy, and onto which networks |
| `qs deploy [options] <template>` | Deploy and wait until ready. Options: `--name`, `--cpus`, `--memory` (MiB), `--network`, `--no-agent` (no agent NIC: the VM is isolated from Quicksand), `--no-wait` |
| `qs start\|shutdown\|stop\|reboot\|reset\|suspend\|resume <vm>` | Power actions |
| `qs destroy [-y] <vm>` | Destroy a VM and its disks |
| `qs extend <vm>` | Push back a VM's expiry, where the server's `expiry` settings allow it |
| `qs snapshot take [--disk-only] [--force] <vm>` | Snapshot a VM, with its memory unless `--disk-only`. Each VM has one snapshot; taking another replaces it (asks first, unless `--force`) |
| `qs snapshot revert [--force] <vm>` | Return a VM to its snapshot, which is kept so you can revert again (asks first, unless `--force`) |
| `qs snapshot delete [--force] <vm>` | Delete a VM's snapshot (asks first, unless `--force`) |
| `qs link [--merge] [--name NAME] <vm> <vm>...` | Join VMs into a private link network, where they reach each other and nothing else. VMs already in different networks merge them (asks first, unless `--merge`); `--name` names the network |
| `qs unlink <vm>` | Take a VM out of its link network (a network left with one VM is removed) |
| `qs ps <vm>` | The guest's processes (needs the agent) |
| `qs kill [-f] <vm> <pid>` | End a guest process; `-f` sends SIGKILL on Linux (Windows always terminates) |
| `qs netstat <vm>` | The guest's TCP and UDP sockets and their processes |
| `qs exec [--timeout s] [-i] [--argv] <vm> <command...>` | Run one command and print its output (see below) |
| `qs shell [-t\|-T] [--shell program\|--serial] <vm> [command...]` | A shell in the guest, or run a command there (see below) |
| `qs forward <vm> <forward>...` | Forward local ports into the guest's network (see below) |
| `qs dir <vm> [path]` | List the guest's files directory, or `path` (`/` lists the drives on Windows) |
| `qs download [-r [-u]] [-e password] <vm> <remote> [local]` | Download files and folders (see below) |
| `qs upload <vm> <local>... [remote]` | Upload files (see below) |

## Running a command

`qs exec <vm> <command...>` runs one command through the guest's shell
(`sh -c`, or `cmd /c` on Windows) and prints its output when it finishes,
like the web UI's "Run a command". It exits with the command's exit code.

```sh
qs exec web1 df -h
qs exec win1 'ipconfig /all'
qs exec -i web1 'sort | uniq -c' < words.txt
qs exec --argv web1 ls -l '/path with spaces'
```

- `--timeout seconds` (default 60, at most 1800) kills the command after
  that long; `qs` then exits with 124, like `timeout`.
- `-i` sends standard input to the command (up to 4 MiB).
- `--argv` runs the program directly with the given arguments, without a
  shell to interpret them.

Output is capped at 4 MiB per stream. For long-running or interactive
commands, or more output, use `qs shell` instead, which streams.

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

`--shell` picks another shell program, by name (looked up in the guest's
PATH) or path, for both interactive shells and commands:

```sh
qs shell --shell cmd win1                 # cmd.exe instead of PowerShell
qs shell --shell pwsh win1 'Get-Date'     # a command through PowerShell 7
qs shell --shell zsh web1
```

Commands run through it with `/c` for cmd, `-Command` for PowerShell and
`-c` for anything else. The web UI's Shell tab has the same choice.

### Serial console

For templates that enable it (`"serial"` in the server's template config),
`qs shell` falls back to the VM's serial console when the VM has no
connected agent, and `qs shell --serial <vm>` uses it regardless. It's a
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

## File transfer

```sh
qs download web1 /var/log/syslog              # into the current directory
qs download web1 notes.txt ~/notes-copy.txt   # to a new name
qs download -r web1 /etc                      # a folder, as etc.zip
qs download -r -u web1 /etc ./etc-copy        # a folder, extracted (like scp -r)
qs download -e hunter2 win1 secrets.txt       # as secrets.txt.zip, encrypted
qs upload web1 report.pdf                     # into the guest's files directory
qs upload web1 a.txt b.txt /tmp               # several files into a folder
qs upload win1 setup.exe C:\Temp\installer.exe # to a new name
```

Remote paths are absolute, or relative to the guest's files directory (`.`
is that directory): the template's `filesDir` setting, or the root of the
guest's filesystem (`/`, or the system drive on Windows) without one. A single upload without a remote path goes to the files
directory; with several files, the last argument is always the remote path,
so end with `.` to upload them there. Like `scp`, uploading into a directory keeps the file's
name, and existing files are overwritten. Only files can be uploaded.

- `-r` downloads folders, as a `.zip`. With `-u`, the folder is extracted as
  it streams in (into `<local>/<folder>` if `<local>` is an existing
  directory, otherwise as `<local>`), so it never needs the space for both the
  archive and its contents.
- `-e password` downloads a file or (with `-r`) a folder as a zip encrypted
  with that password; there is no default. `-e -` asks for it (twice, not
  shown), or reads it from standard input when that isn't a terminal. It's ZipCrypto, which any unzip
  tool (Windows Explorer included) opens, but which is weak; it can't be
  combined with `-u`. A password given on the command line is visible to other
  users of your machine and stays in your shell history; `-e -` avoids that.

Transfers stream end to end, so files of any size work without filling
memory or disk on the server. `-` as the local path writes a download to
standard output. A progress bar shows on the terminal. Downloads are written
to `<name>.qs-part` and renamed when complete, so a failed one leaves
nothing behind.
