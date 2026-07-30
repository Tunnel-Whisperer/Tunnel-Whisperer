package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tunnelwhisperer/tw/internal/config"
	"github.com/tunnelwhisperer/tw/internal/ops"
)

// Completion candidates are "value\tdescription" strings (cobra's
// rich-completion format; zsh renders the description column). The builders
// are pure so they unit-test without a config dir. Everything completion
// reads is local — context index, users dir, server registry, config.yaml —
// never the relay or the daemon.

func contextCandidates(infos []ops.ContextInfo) []string {
	out := make([]string, 0, 2*len(infos))
	for _, c := range infos {
		desc := c.Role
		if c.User != "" {
			desc += " " + c.User
		}
		if c.Relay != "" {
			desc += "@" + c.Relay
		}
		entry := c.Name
		if d := strings.TrimSpace(desc); d != "" {
			entry += "\t" + d
		}
		out = append(out, entry)
		if c.ID != "" {
			out = append(out, c.ID+"\tid of "+c.Name)
		}
	}
	return out
}

func userCandidates(users []ops.UserInfo, exclude []string) []string {
	skip := make(map[string]bool, len(exclude))
	for _, name := range exclude {
		skip[name] = true
	}
	out := make([]string, 0, len(users))
	for _, u := range users {
		if skip[u.Name] {
			continue
		}
		state := "not applied"
		if u.Active {
			state = "applied"
		}
		out = append(out, fmt.Sprintf("%s\t%d tunnel(s), %s", u.Name, len(u.Tunnels), state))
	}
	return out
}

func serverCandidates(servers []ops.RegisteredServer) []string {
	out := make([]string, 0, len(servers))
	for _, s := range servers {
		desc := fmt.Sprintf("port %d", s.RemotePort)
		if s.EnrolledAt != "" {
			desc += ", enrolled " + s.EnrolledAt
		}
		out = append(out, s.ServerID+"\t"+desc)
	}
	return out
}

func appCandidates(apps []config.Application) []string {
	out := make([]string, 0, len(apps))
	for _, a := range apps {
		out = append(out, fmt.Sprintf("%s\t%d mapping(s)", a.Name, len(a.Mappings)))
	}
	return out
}

// noComplete is the silent empty answer: no candidates, no file fallback.
// Tab completion must never surface an error.
func noComplete() ([]string, cobra.ShellCompDirective) {
	return nil, cobra.ShellCompDirectiveNoFileComp
}

func completeContexts(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) != 0 { // only the first positional arg names a context
		return noComplete()
	}
	o, err := ops.New()
	if err != nil {
		return noComplete()
	}
	infos, err := o.ListContexts()
	if err != nil {
		return noComplete()
	}
	return contextCandidates(infos), cobra.ShellCompDirectiveNoFileComp
}

func completeUsers(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) != 0 {
		return noComplete()
	}
	return listUserCandidates(nil)
}

// completeUsersMulti is for `apply [name...]`: every position completes,
// minus the names already typed.
func completeUsersMulti(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	return listUserCandidates(args)
}

func listUserCandidates(exclude []string) ([]string, cobra.ShellCompDirective) {
	o, err := ops.New()
	if err != nil {
		return noComplete()
	}
	users, err := o.ListUsers()
	if err != nil {
		return noComplete()
	}
	return userCandidates(users, exclude), cobra.ShellCompDirectiveNoFileComp
}

func completeServerIDs(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) != 0 {
		return noComplete()
	}
	o, err := ops.New()
	if err != nil {
		return noComplete()
	}
	servers, err := o.ListServers()
	if err != nil {
		return noComplete()
	}
	return serverCandidates(servers), cobra.ShellCompDirectiveNoFileComp
}

func completeApps(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) != 0 {
		return noComplete()
	}
	o, err := ops.New()
	if err != nil {
		return noComplete()
	}
	return appCandidates(o.ListApplications()), cobra.ShellCompDirectiveNoFileComp
}

func init() {
	// Context selectors. rename-context's 2nd arg (the new name) is free
	// text — the len(args) guard in completeContexts leaves it uncompleted.
	configUseContextCmd.ValidArgsFunction = completeContexts
	configDeleteContextCmd.ValidArgsFunction = completeContexts
	configExportCmd.ValidArgsFunction = completeContexts
	configRenameContextCmd.ValidArgsFunction = completeContexts

	// Usernames.
	deleteUserCmd.ValidArgsFunction = completeUsers
	editUserCmd.ValidArgsFunction = completeUsers
	userSingleSessionCmd.ValidArgsFunction = completeUsers
	exportUserCmd.ValidArgsFunction = completeUsers
	unregisterUserCmd.ValidArgsFunction = completeUsers
	applyUsersCmd.ValidArgsFunction = completeUsersMulti

	// Server ids (local registry — no relay dial).
	relayUnenrollServerCmd.ValidArgsFunction = completeServerIDs

	// Application templates.
	appEditCmd.ValidArgsFunction = completeApps
	appDeleteCmd.ValidArgsFunction = completeApps
}
