package main

import (
	"fmt"
	"os"

)

func main() {
	args := parseArgs(os.Args[1:])

	if !isInGitRepo() {
		logErr("Not inside a git repository.")
		os.Exit(1)
	}

	var sha string
	if args.shaPin != "" {
		sha = args.shaPin
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

	if args.configFile != "" {
		os.Exit(runMultiStep(args, sha))
	}

	if len(args.cmd) == 0 {
		logErr("A command after -- is required (or use -f for multi-step mode).")
		printUsage()
		os.Exit(1)
	}

	os.Exit(runSingleStep(args, sha))
}

type cliArgs struct {
	system         string
	systemExplicit bool
	name           string
	cmd            []string
	shaPin         string
	configFile     string
	tui            bool
	workdir        string
}

func parseArgs(args []string) cliArgs {
	var a cliArgs
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-s", "--system":
			i++
			if i >= len(args) {
				logErr("Missing value for %s", args[i-1])
				os.Exit(1)
			}
			a.system = args[i]
			a.systemExplicit = true
		case "-n", "--name":
			i++
			if i >= len(args) {
				logErr("Missing value for %s", args[i-1])
				os.Exit(1)
			}
			a.name = args[i]
		case "--sha":
			i++
			if i >= len(args) {
				logErr("Missing value for --sha")
				os.Exit(1)
			}
			a.shaPin = args[i]
		case "-f", "--file":
			i++
			if i >= len(args) {
				logErr("Missing value for %s", args[i-1])
				os.Exit(1)
			}
			a.configFile = args[i]
		case "--tui":
			a.tui = true
		case "--workdir":
			i++
			if i >= len(args) {
				logErr("Missing value for --workdir")
				os.Exit(1)
			}
			a.workdir = args[i]
		case "-h", "--help":
			printUsage()
			os.Exit(1)
		case "--":
			a.cmd = args[i+1:]
			return a
		default:
			fmt.Fprintf(os.Stderr, "Error: Unknown option '%s'\n", args[i])
			printUsage()
			os.Exit(1)
		}
	}
	return a
}

func printUsage() {
	fmt.Println(`Usage: giton [options] -- <command...>
       giton -f <config.json>

Run commands on Nix platforms and post GitHub commit statuses.

Single-step mode:
  -s, --system    Nix system string (if omitted, runs on current system)
  -n, --name      Check name for GitHub status context (default: command name)
  --              Separator before the command to run

Multi-step mode:
  -f, --file      JSON config file defining steps, systems, and dependencies

Common options:
  --sha           Pin to a specific commit SHA (skips clean-tree check)
  --tui           Enable process-compose TUI (multi-step mode only)

Status context: giton/<name> (no --system) or giton/<name>/<system> (with --system)`)
}

