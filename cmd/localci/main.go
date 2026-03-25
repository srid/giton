// localci — local CI tool that runs commands on Nix platforms and posts
// GitHub commit statuses. Two modes: single-step (-- <cmd>) runs one
// command; multi-step (justfile ci module) orchestrates parallel steps
// via a native DAG executor. MCP mode (--mcp) starts an MCP server
// exposing steps as tools. When --system differs from the current host,
// commands run on a remote machine over SSH.
package main

import (
	"os"

	flag "github.com/spf13/pflag"
)

func main() {
	// Handle subcommands before flag parsing
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "protect":
			os.Args = append(os.Args[:1], os.Args[2:]...)
			if !isInGitRepo() {
				logErr("Not inside a git repository.")
				os.Exit(1)
			}
			os.Exit(runProtect())
		case "serve":
			os.Args = append(os.Args[:1], os.Args[2:]...)
			args := parseServeArgs()
			if !isInGitRepo() {
				logErr("Not inside a git repository.")
				os.Exit(1)
			}
			os.Exit(runMCPHTTPServer(args.port))
		case "run":
			// "localci run" is the same as "localci" — strip "run"
			os.Args = append(os.Args[:1], os.Args[2:]...)
		}
	}

	args := parseArgs()

	if !isInGitRepo() {
		logErr("Not inside a git repository.")
		os.Exit(1)
	}

	// MCP mode: start MCP server immediately — SHA is resolved per tool call.
	if args.mcp {
		os.Exit(runMCPServer())
	}

	// --sha pins to an explicit commit and skips the clean-tree check.
	var sha string
	if args.shaPin != "" {
		resolved, err := resolveRef(args.shaPin)
		if err != nil {
			sha = args.shaPin
		} else {
			sha = resolved
		}
	} else {
		if !isTreeClean() {
			logErr("Working tree is dirty. Commit or stash changes first.")
			os.Exit(1)
		}
		var err error
		sha, err = resolveHEAD()
		if err != nil {
			logErr("Could not resolve HEAD: %v", err)
			os.Exit(1)
		}
	}

	// If no command after --, run multi-step from justfile ci module
	if len(args.cmd) == 0 {
		os.Exit(runMultiStep(args, sha))
	}

	os.Exit(runSingleStep(args, sha))
}

type cliArgs struct {
	system         string
	systemExplicit bool
	name           string
	cmd            []string
	shaPin         string
	mcp            bool
	noSignoff      bool
	workdir        string
}

func parseArgs() cliArgs {
	var a cliArgs

	flag.StringVarP(&a.system, "system", "s", "", "Nix system string (if omitted, runs on current system)")
	flag.StringVarP(&a.name, "name", "n", "", "Check name for GitHub status context (default: command name)")
	flag.StringVar(&a.shaPin, "sha", "", "Pin to a specific commit SHA (skips clean-tree check)")
	flag.BoolVar(&a.mcp, "mcp", false, "Start MCP server exposing CI steps as tools")
	flag.BoolVar(&a.noSignoff, "no-signoff", false, "Skip GitHub status posting (test locally before pushing)")
	flag.StringVar(&a.workdir, "workdir", "", "Pre-extracted working directory (internal, used by multi-step mode)")

	flag.Usage = func() {
		logErr("Usage: localci [run] [options] -- <command...>")
		logErr("       localci [run] [options]                   (multi-step from justfile ci module)")
		logErr("       localci [run] --mcp")
		logErr("       localci serve [-p PORT]")
		logErr("       localci protect")
		logErr("")
		flag.PrintDefaults()
	}

	flag.Parse()

	a.systemExplicit = flag.CommandLine.Changed("system")
	a.cmd = flag.Args()

	return a
}

type serveArgs struct {
	port int
}

func parseServeArgs() serveArgs {
	var a serveArgs
	flag.IntVarP(&a.port, "port", "p", 8417, "HTTP port for MCP server")
	flag.Parse()
	return a
}
