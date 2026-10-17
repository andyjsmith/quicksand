package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/urfave/cli/v3"
)

const linkUsage = "qs link [--merge] [--name NAME] <vm> <vm>..."

// cmdLink joins VMs into one link network, merging the networks they're
// in (after asking, unless --merge).
func cmdLink(ctx context.Context, cmd *cli.Command) error {
	merge, name := cmd.Bool("merge"), cmd.String("name")
	if cmd.NArg() < 2 {
		return errors.New("usage: " + linkUsage)
	}
	c, err := signedIn()
	if err != nil {
		return err
	}
	var vms []vm
	for _, ref := range cmd.Args().Slice() {
		v, err := findVM(c, ref)
		if err != nil {
			return err
		}
		vms = append(vms, v)
	}
	var with []string
	for _, v := range vms[1:] {
		with = append(with, v.ID)
	}
	var resp struct {
		Network struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"network"`
		Warning string `json:"warning"`
	}
	path := "/api/vms/" + url.PathEscape(vms[0].ID) + "/link"
	err = c.do("POST", path, map[string]any{"with": with, "merge": merge}, &resp)
	var ae *apiError
	if errors.As(err, &ae) && ae.Status == http.StatusConflict && strings.Contains(ae.Message, "merge") {
		var names []string
		for _, v := range vms {
			if v.Link != nil && !slices.Contains(names, v.Link.Name) {
				names = append(names, v.Link.Name)
			}
		}
		question := "These VMs are in different networks. Merge them into one?"
		if len(names) > 1 {
			question = fmt.Sprintf("These VMs are in different networks. Merge %s and %s into one?",
				strings.Join(names[:len(names)-1], ", "), names[len(names)-1])
		}
		if err := confirm(question); err != nil {
			return err
		}
		err = c.do("POST", path, map[string]any{"with": with, "merge": true}, &resp)
	}
	if err != nil {
		return err
	}
	if name != "" {
		if err := c.do("PATCH", "/api/links/"+strconv.FormatInt(resp.Network.ID, 10), map[string]string{"name": name}, nil); err != nil {
			return err
		}
		resp.Network.Name = name
	}
	if resp.Warning != "" {
		fmt.Fprintln(os.Stderr, "Warning:", resp.Warning)
	}
	fmt.Fprintf(os.Stderr, "Linked into %s:\n", resp.Network.Name)
	return printLinkMembers(c, resp.Network.ID)
}

// printLinkMembers lists the VMs of a link network and their addresses.
func printLinkMembers(c *client, network int64) error {
	var list []vm
	if err := c.do("GET", "/api/vms", nil, &list); err != nil {
		return err
	}
	for _, v := range list {
		if v.Link == nil || v.Link.Network != network {
			continue
		}
		note := ""
		if v.Link.Status == "manual" {
			note = fmt.Sprintf("  (set it yourself on the NIC with MAC %s: no agent)", v.Link.MAC)
		}
		fmt.Fprintf(os.Stderr, "  %-20s %s%s\n", v.Name, v.Link.Address, note)
	}
	return nil
}

// cmdUnlink takes a VM out of its link network.
func cmdUnlink(ctx context.Context, cmd *cli.Command) error {
	c, v, err := vmArg(cmd.Args().Slice(), "qs unlink <vm>")
	if err != nil {
		return err
	}
	if v.Link == nil {
		return fmt.Errorf("%s isn't linked", v.Name)
	}
	if err := c.do("POST", "/api/vms/"+url.PathEscape(v.ID)+"/unlink", nil, nil); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Unlinked %s from %s.\n", v.Name, v.Link.Name)
	return nil
}
