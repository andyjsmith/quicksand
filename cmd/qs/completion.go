package main

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/urfave/cli/v3"
)

// Shell completion comes from urfave/cli: "qs completion <shell>" prints a
// script that, on each Tab, runs qs with the words before the cursor (and
// the one at it, if it's a flag) followed by --generate-shell-completion.
// qs then prints the candidates, one per line as "value:description".
// urfave completes commands and flags; completeArgs adds the VMs,
// templates and networks arguments and flags take. When nothing is
// printed, the shells complete local paths.

// argKind is what a positional argument or flag value is.
type argKind int

const (
	argNone     argKind = iota // free text: nothing to suggest
	argVM                      // one of the user's VMs
	argTemplate                // a template ID
	argNetwork                 // a network ID
	argFile                    // a local path, which the shells complete
)

// candidate is a completion and its description (may be empty).
type candidate struct{ value, desc string }

// completionValues returns the candidates for an argument; tests replace it.
var completionValues = apiValues

// completeArgs completes positional arguments by kind (the last one
// repeats) and the values of flagValues' flags, deferring to urfave for
// flag names.
func completeArgs(flagValues map[string]argKind, kinds ...argKind) cli.ShellCompleteFunc {
	return func(ctx context.Context, cmd *cli.Command) {
		args := cmd.Args().Slice()
		n := len(args)
		// A trailing flag is being completed, or its value is; flags end
		// where StopOnNthArg says.
		if n > 0 && strings.HasPrefix(args[n-1], "-") && (cmd.StopOnNthArg == nil || n-1 < *cmd.StopOnNthArg) {
			if kind, ok := flagValues[strings.TrimLeft(args[n-1], "-")]; ok {
				printCandidates(cmd.Root().Writer, completionValues(kind))
				return
			}
			cli.DefaultCompleteWithFlags(ctx, cmd)
			return
		}
		if len(kinds) > 0 {
			printCandidates(cmd.Root().Writer, completionValues(kinds[min(n, len(kinds)-1)]))
		}
	}
}

// completeCommands completes a command's subcommands (with their usage as
// descriptions, and without "help"), or its flags.
func completeCommands(ctx context.Context, cmd *cli.Command) {
	if args := cmd.Args().Slice(); len(args) > 0 && strings.HasPrefix(args[len(args)-1], "-") {
		cli.DefaultCompleteWithFlags(ctx, cmd)
		return
	}
	var list []candidate
	for _, sub := range cmd.Commands {
		if !sub.Hidden && sub.Name != "help" {
			list = append(list, candidate{sub.Name, sub.Usage})
		}
	}
	printCandidates(cmd.Root().Writer, list)
}

func printCandidates(w io.Writer, list []candidate) {
	for _, c := range list {
		// The scripts split at the first ":".
		if c.desc == "" || strings.Contains(c.value, ":") {
			fmt.Fprintln(w, c.value)
		} else {
			fmt.Fprintf(w, "%s:%s\n", c.value, c.desc)
		}
	}
}

// apiValues fetches candidates from the server as the signed-in user.
// Completion must stay quick and quiet, so it gives up fast and ignores
// errors.
func apiValues(kind argKind) []candidate {
	var path string
	var item func(map[string]any) candidate
	switch kind {
	case argVM:
		path = "/api/vms"
		item = func(m map[string]any) candidate {
			state := str(m, "state")
			if state == "ready" {
				state = str(m, "power")
			}
			return candidate{str(m, "name"), joinNonEmpty(", ", str(m, "templateName"), state)}
		}
	case argTemplate:
		path = "/api/templates"
		item = func(m map[string]any) candidate { return candidate{str(m, "id"), str(m, "name")} }
	case argNetwork:
		path = "/api/networks"
		item = func(m map[string]any) candidate { return candidate{str(m, "id"), str(m, "label")} }
	default:
		return nil
	}
	c, err := signedIn()
	if err != nil {
		return nil
	}
	c.http.Timeout = 3 * time.Second
	var list []map[string]any
	if c.do("GET", path, nil, &list) != nil {
		return nil
	}
	var out []candidate
	for _, m := range list {
		if cand := item(m); cand.value != "" {
			out = append(out, cand)
		}
	}
	slices.SortFunc(out, func(a, b candidate) int { return strings.Compare(a.value, b.value) })
	return out
}

func str(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

func joinNonEmpty(sep string, parts ...string) string {
	return strings.Join(slices.DeleteFunc(parts, func(s string) bool { return s == "" }), sep)
}

// configureCompletionCommand lists "qs completion" in help and replaces
// urfave's fish script (which never completes paths) and PowerShell script
// (which only works saved as qs.ps1, and passes the partial word as an
// argument) with fixed ones speaking the same protocol.
func configureCompletionCommand(c *cli.Command) {
	c.Hidden = false
	c.ShellComplete = completeCommands
	c.Usage = "print a script that completes qs commands, flags, VMs, templates and networks"
	c.Description = completionDescription
	for _, sub := range c.Commands {
		script, ok := completionScripts[sub.Name]
		if !ok {
			continue
		}
		if sub.Name == "pwsh" {
			sub.Aliases = append(sub.Aliases, "powershell")
		}
		sub.Action = func(ctx context.Context, cmd *cli.Command) error {
			_, err := io.WriteString(cmd.Root().Writer, script)
			return err
		}
	}
}

const completionDescription = `Load the script for your shell:

  bash:        source <(qs completion bash)          in ~/.bashrc (needs bash-completion)
  zsh:         source <(qs completion zsh)           in ~/.zshrc, after compinit
  fish:        qs completion fish > ~/.config/fish/completions/qs.fish
  PowerShell:  qs completion pwsh | Out-String | Invoke-Expression    in $PROFILE

The .deb and .rpm packages set up bash, zsh and fish.`

var completionScripts = map[string]string{
	"fish": `# fish completion for qs. Generated by "qs completion fish"; save it as
# ~/.config/fish/completions/qs.fish, or load it with: qs completion fish | source
function __qs_perform_completion
    set -l args (commandline -opc)
    set -l lastArg (commandline -ct)
    if string match -q -- "-*" $lastArg
        set results ($args[1] $args[2..-1] $lastArg --generate-shell-completion 2>/dev/null)
    else
        set results ($args[1] $args[2..-1] --generate-shell-completion 2>/dev/null)
    end
    set results (string match -v -r '^\s*$' -- $results)
    if test (count $results) -eq 0
        # Nothing to suggest: complete local paths.
        __fish_complete_path $lastArg
        return
    end
    for line in $results
        set -l parts (string split -m 1 ":" -- $line)
        if test (count $parts) -eq 2
            printf "%s\t%s\n" $parts[1] $parts[2]
        else
            printf "%s\n" $line
        end
    end
end
complete -c qs -e
complete -c qs -f -a '(__qs_perform_completion)'
`,
	"pwsh": `# PowerShell completion for qs. Generated by "qs completion pwsh"; load it with
#   qs completion pwsh | Out-String | Invoke-Expression
# for example in your $PROFILE.
Register-ArgumentCompleter -Native -CommandName qs, qs.exe -ScriptBlock {
    param($wordToComplete, $commandAst, $cursorPosition)
    $words = @($commandAst.CommandElements | Where-Object { $_.Extent.StartOffset -lt $cursorPosition } | ForEach-Object { $_.ToString() })
    $exe, $rest = $words
    $rest = @($rest)
    # Like the bash script: the word being completed is passed only if it's a flag.
    if ($wordToComplete -ne '' -and -not $wordToComplete.StartsWith('-')) {
        $rest = @($rest | Select-Object -SkipLast 1)
    }
    $out = @(& $exe @rest --generate-shell-completion 2>$null | Where-Object { $_ -ne '' })
    # Returning nothing lets PowerShell complete paths.
    foreach ($line in $out) {
        $value, $desc = $line.Split(':', 2)
        if (-not $value.StartsWith($wordToComplete)) { continue }
        if (-not $desc) { $desc = $value }
        [System.Management.Automation.CompletionResult]::new($value, $value, 'ParameterValue', $desc)
    }
}
`,
}
