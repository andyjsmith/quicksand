// Command qs is the Quicksand command-line client: sign in, then list,
// deploy and manage your VMs. The commands are in app.go.
package main

import (
	"context"
	"fmt"
	"os"
)

func main() {
	if err := newApp().Run(context.Background(), os.Args); err != nil {
		fmt.Fprintln(os.Stderr, "qs:", err)
		os.Exit(1)
	}
}
