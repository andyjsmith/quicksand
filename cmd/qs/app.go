package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/urfave/cli/v3"
)

// Categories group the commands in "qs help".
const (
	catAccount  = "Account"
	catMachines = "Machines"
	catAgent    = "Agent"
)

// vmHelp is how commands name a VM.
const vmHelp = `A VM is named by its name, Proxmox VM ID or Quicksand ID, or by its
template's ID while it's your only VM of that template.`

// stopAfter makes flag parsing stop after n arguments, so what follows
// (such as the command run in the guest) keeps its own flags.
func stopAfter(n int) *int { return &n }

// newApp returns the qs command tree.
func newApp() *cli.Command {
	power := func(name, usage string) *cli.Command {
		return &cli.Command{
			Name: name, Usage: usage, ArgsUsage: "<vm>", Category: catMachines,
			Action:        func(ctx context.Context, cmd *cli.Command) error { return cmdPower(name, cmd) },
			ShellComplete: completeArgs(nil, argVM),
		}
	}
	app := &cli.Command{
		Name:                            "qs",
		Usage:                           "the Quicksand command-line client",
		Description:                     "Sign in with \"qs login\", then list, deploy and manage your VMs.\n\n" + vmHelp,
		EnableShellCompletion:           true,
		ConfigureShellCompletionCommand: configureCompletionCommand,
		ShellComplete:                   completeCommands,
		CommandNotFound:                 commandNotFound,
		Suggest:                         true,
		HideVersion:                     true,
		Commands: []*cli.Command{
			{
				Name:      "login",
				Usage:     "sign in: in a browser with single sign-on, if the server has it, else with a password",
				ArgsUsage: "<server URL>",
				Category:  catAccount,
				Flags: []cli.Flag{
					&cli.BoolFlag{Name: "password", Usage: "sign in with a password even if the server has single sign-on"},
					&cli.BoolFlag{Name: "token", Usage: "sign in with an API token made in the web client (asked for, or read from standard input)"},
					&cli.StringFlag{Name: "fingerprint", Usage: "trust a server certificate with this SHA-256 `fingerprint`"},
				},
				Action:        cmdLogin,
				ShellComplete: completeArgs(nil, argNone),
			},
			{Name: "logout", Usage: "sign out and forget the session", Category: catAccount, Action: cmdLogout},
			{Name: "whoami", Usage: "show who you are signed in as", Category: catAccount, Action: cmdWhoami},
			{Name: "version", Usage: "show qs's version, and the server's when signed in", Action: cmdVersion},

			{
				Name: "ls", Aliases: []string{"list"}, Usage: "list your VMs", Category: catMachines,
				Flags:  []cli.Flag{&cli.BoolFlag{Name: "a", Usage: "list everyone's VMs (administrators only)"}},
				Action: cmdList,
			},
			{Name: "templates", Usage: "list templates you can deploy", Category: catMachines, Action: cmdTemplates},
			{Name: "networks", Usage: "list networks", Category: catMachines, Action: cmdNetworks},
			{
				Name: "deploy", Usage: "deploy a VM and wait until it's ready", ArgsUsage: "<template>", Category: catMachines,
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "name", Usage: "VM `name` (default: generated)"},
					&cli.IntFlag{Name: "cpus", Usage: "number of CPUs (default: the template's)", HideDefault: true},
					&cli.IntFlag{Name: "memory", Usage: "memory in `MiB` (default: the template's)", HideDefault: true},
					&cli.StringFlag{Name: "network", Usage: "network `ID` (default: the default network)"},
					&cli.BoolFlag{Name: "no-agent", Usage: "deploy without the agent network, fully isolated from Quicksand"},
					&cli.BoolFlag{Name: "no-wait", Usage: "return once the deployment has started"},
				},
				Action:        cmdDeploy,
				ShellComplete: completeArgs(map[string]argKind{"network": argNetwork}, argTemplate),
			},
			power("start", "start a VM, or resume a hibernated one"),
			power("shutdown", "shut a VM down cleanly"),
			power("stop", "stop a VM at once, like pulling the plug"),
			power("reboot", "reboot a VM cleanly"),
			power("reset", "reset a VM at once"),
			power("suspend", "pause a VM"),
			power("resume", "resume a paused VM"),
			power("hibernate", "save a VM's memory to disk and stop it; start resumes it"),
			{
				Name: "destroy", Usage: "destroy a VM and its disks", ArgsUsage: "<vm>", Category: catMachines,
				Flags:         []cli.Flag{&cli.BoolFlag{Name: "y", Usage: "don't ask for confirmation"}},
				Action:        cmdDestroy,
				ShellComplete: completeArgs(nil, argVM),
			},
			{
				Name: "extend", Usage: "push back a VM's expiry", ArgsUsage: "<vm>", Category: catMachines,
				Action: cmdExtend, ShellComplete: completeArgs(nil, argVM),
			},
			{
				Name: "snapshot", Usage: "take, revert to or delete a VM's snapshot (each VM has one)", Category: catMachines,
				ShellComplete: completeCommands, CommandNotFound: commandNotFound,
				Commands: []*cli.Command{
					{
						Name: "take", Usage: "snapshot a VM, with its memory unless --disk-only, replacing its snapshot", ArgsUsage: "<vm>",
						Flags: []cli.Flag{
							&cli.BoolFlag{Name: "disk-only", Usage: "skip the memory (reverting then leaves the VM stopped)"},
							&cli.BoolFlag{Name: "force", Usage: "replace an existing snapshot without asking"},
						},
						Action: snapshotTake, ShellComplete: completeArgs(nil, argVM),
					},
					{
						Name: "revert", Usage: "return a VM to its snapshot, which is kept", ArgsUsage: "<vm>",
						Flags:  []cli.Flag{&cli.BoolFlag{Name: "force", Usage: "don't ask for confirmation"}},
						Action: snapshotRevert, ShellComplete: completeArgs(nil, argVM),
					},
					{
						Name: "delete", Usage: "delete a VM's snapshot", ArgsUsage: "<vm>",
						Flags:  []cli.Flag{&cli.BoolFlag{Name: "force", Usage: "don't ask for confirmation"}},
						Action: snapshotDelete, ShellComplete: completeArgs(nil, argVM),
					},
				},
			},
			{
				Name: "link", Usage: "join VMs into a private network", ArgsUsage: "<vm> <vm>...", Category: catMachines,
				Description: "The VMs' link networks are merged if they're in different ones, after asking unless --merge.",
				Flags: []cli.Flag{
					&cli.BoolFlag{Name: "merge", Usage: "merge the VMs' networks without asking, when they're in different ones"},
					&cli.StringFlag{Name: "name", Usage: "name the network"},
				},
				Action: cmdLink, ShellComplete: completeArgs(nil, argVM),
			},
			{
				Name: "unlink", Usage: "take a VM out of its link network", ArgsUsage: "<vm>", Category: catMachines,
				Action: cmdUnlink, ShellComplete: completeArgs(nil, argVM),
			},

			{
				Name: "ps", Usage: "list the guest's processes", ArgsUsage: "<vm>", Category: catAgent,
				Action: cmdPs, ShellComplete: completeArgs(nil, argVM),
			},
			{
				Name: "kill", Usage: "end a guest process", ArgsUsage: "<vm> <pid>", Category: catAgent,
				Flags:  []cli.Flag{&cli.BoolFlag{Name: "f", Usage: "kill immediately (SIGKILL on Linux; Windows always does)"}},
				Action: cmdKill, ShellComplete: completeArgs(nil, argVM, argNone),
			},
			{
				Name: "netstat", Usage: "list the guest's TCP and UDP sockets", ArgsUsage: "<vm>", Category: catAgent,
				Action: cmdNetstat, ShellComplete: completeArgs(nil, argVM),
			},
			{
				Name: "dir", Aliases: []string{"ls-files"}, Usage: "list the guest's files directory, or path",
				ArgsUsage: "<vm> [path]", Category: catAgent, StopOnNthArg: stopAfter(1),
				Action: cmdDir, ShellComplete: completeArgs(nil, argVM, argNone),
			},
			{
				Name: "exec", Usage: "run one command and print its output, exiting with its code",
				ArgsUsage: "<vm> <command...>", Category: catAgent, StopOnNthArg: stopAfter(1),
				Description: "Flags go before the VM; everything after it is the command.",
				Flags: []cli.Flag{
					&cli.IntFlag{Name: "timeout", Value: 60, Usage: "give up after this many `seconds` (at most 1800)"},
					&cli.BoolFlag{Name: "i", Usage: "send standard input to the command (up to 4 MiB)"},
					&cli.BoolFlag{Name: "argv", Usage: "run the program directly with these arguments, not through the guest's shell"},
				},
				Action: cmdExec, ShellComplete: completeArgs(nil, argVM, argNone),
			},
			{
				Name: "shell", Aliases: []string{"sh"}, Usage: "open a shell in the guest, or run a command",
				ArgsUsage: "<vm> [command...]", Category: catAgent, StopOnNthArg: stopAfter(1),
				Description: "The command gets a terminal when interactive, and pipes otherwise. VMs without a\n" +
					"connected agent use the serial console. Flags go before the VM.",
				Flags: []cli.Flag{
					&cli.BoolFlag{Name: "t", Usage: "give the command a terminal"},
					&cli.BoolFlag{Name: "T", Usage: "don't give the shell a terminal"},
					&cli.BoolFlag{Name: "serial", Usage: "use the serial console, even if the VM has an agent"},
					&cli.StringFlag{Name: "shell", Usage: "the shell `program` to use, e.g. cmd or pwsh on Windows, zsh on Linux (default: PowerShell on Windows, the login shell elsewhere)"},
				},
				Action: cmdShell, ShellComplete: completeArgs(nil, argVM, argNone),
			},
			{
				Name: "forward", Usage: "forward local ports to host:hostport as the guest reaches it, like ssh -L",
				ArgsUsage: "<vm> [bind:]port:host:hostport...", Category: catAgent, StopOnNthArg: stopAfter(1),
				Description: "Runs until interrupted with Ctrl-C.",
				Action:      cmdForward, ShellComplete: completeArgs(nil, argVM, argNone),
			},
			{
				Name: "download", Usage: "download a guest file (default: into the current directory)",
				ArgsUsage: "<vm> <remote path> [local path]", Category: catAgent,
				Flags: []cli.Flag{
					&cli.BoolFlag{Name: "r", Usage: "download directories (as a .zip; see -u)"},
					&cli.BoolFlag{Name: "u", Usage: "with -r, extract directories into the local path instead of saving a .zip"},
					&cli.StringFlag{Name: "e", Usage: "download as a zip encrypted (ZipCrypto) with this `password`; - asks for it"},
				},
				Action: cmdDownload, ShellComplete: completeArgs(nil, argVM, argNone, argFile),
			},
			{
				Name: "upload", Usage: "upload files", ArgsUsage: "<vm> <local file>... [remote path]",
				Category: catAgent, StopOnNthArg: stopAfter(1),
				Description: "Files go into the remote path if it's a directory. The default, and \".\", is the\n" +
					"guest's files directory. Several files need a remote path.",
				Action: cmdUpload, ShellComplete: completeArgs(nil, argVM, argFile),
			},
		},
	}
	defaultCompletion(app)
	return app
}

// defaultCompletion gives commands without a completion function one that
// completes only their flags (not urfave's "help" subcommand).
func defaultCompletion(cmd *cli.Command) {
	if cmd.ShellComplete == nil {
		cmd.ShellComplete = completeArgs(nil)
	}
	for _, sub := range cmd.Commands {
		defaultCompletion(sub)
	}
}

// commandNotFound reports an unknown command, suggesting a close one.
func commandNotFound(ctx context.Context, cmd *cli.Command, name string) {
	full := strings.TrimSpace(strings.TrimPrefix(cmd.FullName(), cmd.Root().Name) + " " + name)
	msg := fmt.Sprintf("qs: unknown command %q.", full)
	// SuggestCommand always finds the closest name; only offer close ones.
	if s := cli.SuggestCommand(cmd.Commands, name); len(name) >= 2 && strings.HasPrefix(s, name[:2]) {
		msg += fmt.Sprintf(" Did you mean %q?", s)
	}
	fmt.Fprintln(cmd.Root().ErrWriter, msg+` See "qs help".`)
	exit(2)
}
