package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func runSingleStep(args cliArgs, sha string) int {
	// Default --name to first word of command
	name := args.name
	if name == "" {
		name = filepath.Base(args.cmd[0])
	}

	// Build context
	var context string
	if args.systemExplicit {
		context = fmt.Sprintf("giton/%s/%s", name, args.system)
	} else {
		context = fmt.Sprintf("giton/%s", name)
	}

	repo, err := getRepo()
	if err != nil || repo == "" {
		logErr("Could not determine GitHub repository. Is 'gh' authenticated?")
		return 1
	}

	logMsg("%s%s%s  %s%s@%s%s", bold, context, reset, dim, repo, sha[:min(12, len(sha))], reset)
	logInfo("%s", strings.Join(args.cmd, " "))

	// Post pending status
	postStatus(repo, sha, "pending", context, "Running: "+strings.Join(args.cmd, " "))

	// Determine if we need remote execution
	remote := false
	if args.systemExplicit {
		currentSystem := getCurrentSystem()
		if currentSystem != args.system {
			remote = true
		}
	}

	start := time.Now()
	var exitCode int

	if args.workdir != "" {
		// Pre-extracted workdir provided (multi-step mode)
		if remote {
			host, err := getRemoteHost(args.system)
			if err != nil {
				logErr("%v", err)
				return 1
			}
			exitCode = runSSH(host, args.workdir, args.cmd)
		} else {
			exitCode = runLocal(args.workdir, args.cmd)
		}
	} else if remote {
		// Remote execution
		host, err := getRemoteHost(args.system)
		if err != nil {
			logErr("%v", err)
			return 1
		}
		remoteDir := fmt.Sprintf("/tmp/giton-%s", sha[:min(12, len(sha))])
		defer cleanupRemote(host, remoteDir)

		// Ensure SSH ControlMaster socket directory exists
		ensureSSHControlDir(host)

		logMsg("Copying repo to %s%s%s...", bold, host, reset)
		if err := extractRepoRemote(sha, host, remoteDir); err != nil {
			logErr("Failed to extract repo remotely: %v", err)
			return 1
		}

		exitCode = runSSH(host, remoteDir, args.cmd)
	} else {
		// Local execution
		tmpdir, err := os.MkdirTemp("", fmt.Sprintf("giton-%s-", sha[:min(12, len(sha))]))
		if err != nil {
			logErr("Failed to create temp dir: %v", err)
			return 1
		}
		defer os.RemoveAll(tmpdir)

		logMsg("Extracting repo...")
		if err := extractRepoLocal(sha, tmpdir); err != nil {
			logErr("Failed to extract repo: %v", err)
			return 1
		}

		exitCode = runLocal(tmpdir, args.cmd)
	}

	elapsed := fmtDuration(int(time.Since(start).Seconds()))
	cmdStr := strings.Join(args.cmd, " ")

	if exitCode == 0 {
		logOk("%s%s%s passed in %s%s%s", bold, context, reset, green, elapsed, reset)
		postStatus(repo, sha, "success", context, fmt.Sprintf("Passed in %s: %s", elapsed, cmdStr))
	} else {
		logWarn("%s%s%s failed (exit %d) in %s%s%s", bold, context, reset, exitCode, yellow, elapsed, reset)
		postStatus(repo, sha, "failure", context, fmt.Sprintf("Failed (exit %d) in %s: %s", exitCode, elapsed, cmdStr))
	}

	return exitCode
}

// runLocal executes a command locally in the given directory.
func runLocal(dir string, cmdArgs []string) int {
	cmd := exec.Command("bash", "-c", "cd '"+dir+"' && "+shellJoin(cmdArgs))
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode()
		}
		return 1
	}
	return 0
}

// runSSH executes a command on a remote host via SSH.
func runSSH(host, dir string, cmdArgs []string) int {
	cmd := exec.Command("ssh", host, fmt.Sprintf("cd '%s' && %s", dir, strings.Join(cmdArgs, " ")))
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode()
		}
		return 1
	}
	return 0
}

func cleanupRemote(host, dir string) {
	logMsg("Cleaning up remote temp dir...")
	exec.Command("ssh", host, "rm -rf '"+dir+"'").Run()
}

func ensureSSHControlDir(host string) {
	out, err := exec.Command("ssh", "-G", host).Output()
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "controlpath ") {
			path := strings.TrimPrefix(line, "controlpath ")
			os.MkdirAll(filepath.Dir(path), 0o700)
			break
		}
	}
}

func getCurrentSystem() string {
	out, err := exec.Command("nix", "eval", "--raw", "--impure", "--expr", "builtins.currentSystem").Output()
	if err != nil {
		return ""
	}
	return string(out)
}

// shellJoin quotes arguments for shell execution.
func shellJoin(args []string) string {
	return strings.Join(args, " ")
}
