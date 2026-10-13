package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// readNewPassword prompts for a password twice. In a terminal, echo is
// turned off with stty; piped input is read as a single line.
func readNewPassword() (string, error) {
	in := bufio.NewReader(os.Stdin)
	if !isTerminal(os.Stdin) {
		line, err := in.ReadString('\n')
		if err != nil && line == "" {
			return "", errors.New("no password on standard input")
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
	first, err := prompt(in, "Password: ")
	if err != nil {
		return "", err
	}
	second, err := prompt(in, "Repeat password: ")
	if err != nil {
		return "", err
	}
	if first != second {
		return "", errors.New("passwords do not match")
	}
	return first, nil
}

func prompt(in *bufio.Reader, label string) (string, error) {
	fmt.Fprint(os.Stderr, label)
	stty("-echo")
	line, err := in.ReadString('\n')
	stty("echo")
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func stty(arg string) {
	cmd := exec.Command("stty", arg)
	cmd.Stdin = os.Stdin
	cmd.Run()
}

func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
