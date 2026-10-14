package main

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"text/tabwriter"

	"quicksand/internal/agentproto"
)

// cmdDir lists a guest directory: the files directory by default, or an
// absolute path or one relative to it. On Windows, "/" lists the drives.
func cmdDir(args []string) error {
	if len(args) < 1 || len(args) > 2 {
		return errors.New("usage: qs dir <vm> [path]")
	}
	c, err := signedIn()
	if err != nil {
		return err
	}
	v, err := findVM(c, args[0])
	if err != nil {
		return err
	}
	path := ""
	if len(args) == 2 {
		path = args[1]
	}
	var list agentproto.FileList
	if err := c.do("GET", "/api/vms/"+url.PathEscape(v.ID)+"/files?"+url.Values{"path": {path}}.Encode(), nil, &list); err != nil {
		return err
	}
	fmt.Printf("%s\n\n", list.Path)
	unix := v.Agent == nil || v.Agent.OS != "windows"
	type row struct{ mode, modified, size, name string }
	rows := make([]row, 0, len(list.Entries))
	var files, dirs, sizeWidth int
	var bytes uint64
	for _, e := range list.Entries {
		r := row{mode: e.Mode, size: "<DIR>", name: e.Name}
		if !e.ModTime.IsZero() && e.ModTime.Year() >= 1980 {
			r.modified = e.ModTime.Local().Format("2006-01-02 15:04")
		}
		if e.Dir {
			dirs++
		} else {
			r.size = formatBytes(uint64(e.Size))
			files++
			bytes += uint64(e.Size)
		}
		if e.Symlink {
			r.name += "@"
		}
		sizeWidth = max(sizeWidth, len(r.size))
		rows = append(rows, r)
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	for _, r := range rows {
		// Sizes right-aligned, names after them.
		line := fmt.Sprintf("%s\t%*s\t%s", r.modified, sizeWidth, r.size, r.name)
		if unix {
			line = r.mode + "\t" + line
		}
		fmt.Fprintln(w, line)
	}
	w.Flush()
	total := formatBytes(bytes)
	if bytes < 1<<10 {
		total += " bytes"
	}
	fmt.Printf("\n%s, %s, %s\n", plural(dirs, "folder"), plural(files, "file"), total)
	return nil
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
