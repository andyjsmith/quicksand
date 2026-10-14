// Command qs is the Quicksand command-line client: sign in, then list,
// deploy and manage your VMs.
package main

import (
	"fmt"
	"os"
)

const usage = `Usage: qs <command> [options]

Account:
  login [-fingerprint SHA256] <server URL>   sign in (asks for username and password)
  logout                                     sign out and forget the session
  whoami                                     show who you are signed in as

Machines (a VM is named by its name, Proxmox VM ID or Quicksand ID):
  ls [-a]                                    list your VMs (-a: everyone's, admins only)
  templates                                  list templates you can deploy
  networks                                   list networks
  deploy [options] <template>                deploy a VM and wait until it's ready
      -name NAME  -cpus N  -memory MiB  -network ID  -no-agent  -no-wait
  start | shutdown | stop | reboot | reset | suspend | resume <vm>
  destroy [-y] <vm>                          destroy a VM and its disks
  extend <vm>                                push back a VM's expiry

Agent:
  ps <vm>                                    list the guest's processes
  kill [-f] <vm> <pid>                       end a guest process (-f: SIGKILL on Linux)
  netstat <vm>                               list the guest's TCP and UDP sockets
  dir <vm> [path]                            list the guest's files directory, or path
  exec [-timeout seconds] [-i] [-argv] <vm> <command...>
                                             run one command and print its output (exits with its
                                             code); -i: send stdin; -argv: no shell
  shell [-t | -T] [-shell program | -serial] <vm> [command...]
                                             open a shell in the guest, or run a command
      (a terminal when interactive; -t: always, -T: never; -shell: e.g. cmd or zsh
      instead of PowerShell or the login shell; -serial: use the serial console,
      the default for VMs without a connected agent)
  forward <vm> [bind:]port:host:hostport...  forward local ports to host:hostport as the
                                             guest reaches it, like ssh -L; Ctrl-C stops
  download [-r [-u]] [-e password] <vm> <remote path> [local path]
                                             download a guest file (default: into the current
                                             directory); -r: directories, as a .zip; -u: extract
                                             them instead; -e: as a zip encrypted with password
                                             ("-e -" asks for it)
  upload <vm> <local file>... [remote path]  upload files (into <remote path> if it's a
                                             directory; default and ".": the guest's files
                                             directory; several files need a remote path)
`

func main() {
	if len(os.Args) < 2 || os.Args[1] == "-h" || os.Args[1] == "--help" || os.Args[1] == "help" {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	var err error
	switch cmd {
	case "login":
		err = cmdLogin(args)
	case "logout":
		err = cmdLogout()
	case "whoami":
		err = cmdWhoami()
	case "ls", "list":
		err = cmdList(args)
	case "templates":
		err = cmdTemplates()
	case "networks":
		err = cmdNetworks()
	case "deploy":
		err = cmdDeploy(args)
	case "start", "shutdown", "stop", "reboot", "reset", "suspend", "resume":
		err = cmdPower(cmd, args)
	case "destroy":
		err = cmdDestroy(args)
	case "extend":
		err = cmdExtend(args)
	case "ps":
		err = cmdPs(args)
	case "kill":
		err = cmdKill(args)
	case "netstat":
		err = cmdNetstat(args)
	case "shell", "sh":
		err = cmdShell(args)
	case "forward":
		err = cmdForward(args)
	case "dir", "ls-files":
		err = cmdDir(args)
	case "exec":
		err = cmdExec(args)
	case "download":
		err = cmdDownload(args)
	case "upload":
		err = cmdUpload(args)
	default:
		fmt.Fprintf(os.Stderr, "qs: unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "qs:", err)
		os.Exit(1)
	}
}
