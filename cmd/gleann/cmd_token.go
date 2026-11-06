package main

import (
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/tevfik/gleann/internal/auth"
)

func authTokenPath() string { return filepath.Join(auth.DefaultDir(), "admin.token") }

const tokenUsage = `Usage: gleann token <command>

Manage bearer tokens for the gleann API (REST, OpenAI-compatible /v1, A2A).

Commands:
  create <name> [--scope query|admin]   Create a token (shown once). Default scope: query
  list                                  List tokens (hashes are never shown)
  revoke <name|id>                      Revoke a token
  rotate                                Replace the admin token
  show                                  Print the admin token (used by local tools)

Scopes:
  query   search, ask, /v1 chat, A2A, read-only index/graph/memory/conversation routes
  admin   everything else (config, build, delete, plugins, memory writes, ...)

Clients send "Authorization: Bearer <token>" (OpenAI SDK: api_key=<token>).
Local CLI/MCP read ` + "~/.gleann/auth/admin.token" + ` or $GLEANN_API_TOKEN automatically.`

func cmdToken(args []string) {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		fmt.Println(tokenUsage)
		return
	}
	store, err := auth.Open(auth.DefaultDir())
	if err != nil {
		fatalToken(err)
	}
	switch args[0] {
	case "create":
		if len(args) < 2 || args[1] == "" || args[1][0] == '-' {
			fatalToken(fmt.Errorf("usage: gleann token create <name> [--scope query|admin]"))
		}
		scope := getFlag(args, "--scope")
		if scope == "" {
			scope = string(auth.ScopeQuery)
		}
		raw, err := store.Create(args[1], auth.Scope(scope))
		if err != nil {
			fatalToken(err)
		}
		fmt.Println(raw)
		fmt.Fprintf(os.Stderr, "token %q (%s) created — this is the only time it is shown\n", args[1], scope)
	case "list":
		es, err := store.List()
		if err != nil {
			fatalToken(err)
		}
		tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "NAME\tID\tSCOPE\tCREATED")
		for _, e := range es {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", e.Name, e.ID, e.Scope, e.Created.Format("2006-01-02 15:04"))
		}
		tw.Flush()
	case "revoke":
		if len(args) < 2 {
			fatalToken(fmt.Errorf("usage: gleann token revoke <name|id>"))
		}
		if err := store.Revoke(args[1]); err != nil {
			fatalToken(err)
		}
		fmt.Printf("revoked %s\n", args[1])
	case "rotate":
		if _, err := store.Rotate(); err != nil {
			fatalToken(err)
		}
		fmt.Printf("admin token rotated; new value in %s\n(restart is not required; local tools re-read the file)\n", authTokenPath())
	case "show":
		raw, _, err := store.EnsureAdmin()
		if err != nil {
			fatalToken(err)
		}
		fmt.Println(raw)
	default:
		fatalToken(fmt.Errorf("unknown token command %q\n\n%s", args[0], tokenUsage))
	}
}

func fatalToken(err error) {
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	os.Exit(1)
}
