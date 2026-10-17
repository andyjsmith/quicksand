package main

import (
	"bytes"
	"context"
	"io"
	"slices"
	"strings"
	"testing"
)

func fakeValues(kind argKind) []candidate {
	switch kind {
	case argVM:
		return []candidate{{"scratch", "Debian, running"}, {"web1", "Debian, stopped"}, {"web2", ""}}
	case argTemplate:
		return []candidate{{"debian", "Debian 13"}, {"win10", "Windows 10"}}
	case argNetwork:
		return []candidate{{"default", "Default"}, {"isolated", ""}}
	}
	return nil
}

// completions runs qs as a completion script does: with the words before
// the cursor (and the one at it, if it's a flag), then the completion flag.
// It returns the values, without descriptions.
func completions(t *testing.T, words ...string) []string {
	t.Helper()
	completionValues = fakeValues
	t.Cleanup(func() { completionValues = apiValues })
	app := newApp()
	var out bytes.Buffer
	app.Writer, app.ErrWriter = &out, io.Discard
	args := append(append([]string{"qs"}, words...), "--generate-shell-completion")
	if err := app.Run(context.Background(), args); err != nil {
		t.Fatalf("qs %q: %v", words, err)
	}
	var values []string
	for line := range strings.Lines(out.String()) {
		value, _, _ := strings.Cut(strings.TrimSpace(line), ":")
		if value != "" {
			values = append(values, value)
		}
	}
	return values
}

func TestCompletion(t *testing.T) {
	for _, tc := range []struct {
		words []string
		want  []string
	}{
		{[]string{"deploy"}, []string{"debian", "win10"}},
		{[]string{"deploy", "--network"}, []string{"default", "isolated"}},
		{[]string{"deploy", "-network"}, []string{"default", "isolated"}},
		{[]string{"deploy", "--name", "x", "--network"}, []string{"default", "isolated"}},
		{[]string{"deploy", "--no-wait"}, []string{"debian", "win10"}},
		{[]string{"deploy", "debian"}, []string{"debian", "win10"}},
		{[]string{"deploy", "--name"}, nil}, // free text
		{[]string{"shell"}, []string{"scratch", "web1", "web2"}},
		{[]string{"sh", "--serial"}, []string{"scratch", "web1", "web2"}}, // alias
		{[]string{"shell", "web1"}, nil},                                  // the command to run
		{[]string{"shell", "web1", "-x"}, nil},                            // flags end at the VM
		{[]string{"snapshot", "revert"}, []string{"scratch", "web1", "web2"}},
		{[]string{"link", "web1", "web2"}, []string{"scratch", "web1", "web2"}}, // repeats
		{[]string{"upload", "web1"}, nil},                                       // local files: the shell's
		{[]string{"download", "web1", "/etc/hosts"}, nil},
		{[]string{"download", "-e"}, nil},
		{[]string{"download", "-e", "secret"}, []string{"scratch", "web1", "web2"}},
		{[]string{"kill", "web1"}, nil},
		{[]string{"whoami"}, nil},
	} {
		if got := completions(t, tc.words...); !slices.Equal(got, tc.want) {
			t.Errorf("qs %s <Tab> = %q, want %q", strings.Join(tc.words, " "), got, tc.want)
		}
	}
}

func TestCompletionOfCommandsAndFlags(t *testing.T) {
	top := completions(t)
	for _, want := range []string{"deploy", "snapshot", "shell", "completion", "login"} {
		if !slices.Contains(top, want) {
			t.Errorf("qs <Tab> = %q, missing %q", top, want)
		}
	}
	if slices.Contains(top, "sh") || slices.Contains(top, "list") {
		t.Errorf("qs <Tab> = %q, offers aliases", top)
	}
	if got := completions(t, "snapshot"); !slices.Equal(got, []string{"take", "revert", "delete"}) {
		t.Errorf("qs snapshot <Tab> = %q", got)
	}
	flags := completions(t, "deploy", "--no")
	if !slices.Contains(flags, "--no-agent") || !slices.Contains(flags, "--no-wait") || slices.Contains(flags, "--name") {
		t.Errorf("qs deploy --no<Tab> = %q", flags)
	}
	if got := completions(t, "completion"); !slices.Equal(got, []string{"bash", "zsh", "fish", "pwsh"}) {
		t.Errorf("qs completion <Tab> = %q", got)
	}
}

func TestCompletionScripts(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish", "pwsh", "powershell"} {
		app := newApp()
		var out bytes.Buffer
		app.Writer = &out
		if err := app.Run(context.Background(), []string{"qs", "completion", shell}); err != nil {
			t.Fatalf("qs completion %s: %v", shell, err)
		}
		if !strings.Contains(out.String(), "--generate-shell-completion") {
			t.Errorf("qs completion %s doesn't call back into qs", shell)
		}
	}
}
