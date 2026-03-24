package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func isInGitRepo() bool {
	cmd := exec.Command("git", "rev-parse", "--is-inside-work-tree")
	cmd.Stderr = nil
	cmd.Stdout = nil
	return cmd.Run() == nil
}

func isTreeClean() bool {
	// Check unstaged changes
	if err := exec.Command("git", "diff", "--quiet").Run(); err != nil {
		return false
	}
	// Check staged changes
	if err := exec.Command("git", "diff", "--cached", "--quiet").Run(); err != nil {
		return false
	}
	// Check untracked files
	out, err := exec.Command("git", "ls-files", "--others", "--exclude-standard").Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == ""
}

func resolveHEAD() (string, error) {
	out, err := exec.Command("git", "rev-parse", "HEAD").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// extractRepoLocal extracts the repo at the given SHA to a local directory.
func extractRepoLocal(sha, dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	archive := exec.Command("git", "archive", "--format=tar", sha)
	untar := exec.Command("tar", "-C", dir, "-x")
	pipe, err := archive.StdoutPipe()
	if err != nil {
		return err
	}
	untar.Stdin = pipe
	if err := archive.Start(); err != nil {
		return err
	}
	if err := untar.Run(); err != nil {
		return fmt.Errorf("tar extract: %w", err)
	}
	if err := archive.Wait(); err != nil {
		return fmt.Errorf("git archive: %w", err)
	}
	// Ensure writable
	return exec.Command("chmod", "-R", "u+w", dir).Run()
}

// extractRepoRemote extracts the repo at the given SHA to a remote host via SSH.
func extractRepoRemote(sha, host, dir string) error {
	archive := exec.Command("git", "archive", "--format=tar", sha)
	sshCmd := exec.Command("ssh", host,
		fmt.Sprintf("mkdir -p '%s' && tar -C '%s' -x && chmod -R u+w '%s'", dir, dir, dir))
	pipe, err := archive.StdoutPipe()
	if err != nil {
		return err
	}
	sshCmd.Stdin = pipe
	sshCmd.Stdout = os.Stdout
	sshCmd.Stderr = os.Stderr
	if err := archive.Start(); err != nil {
		return err
	}
	if err := sshCmd.Run(); err != nil {
		return fmt.Errorf("ssh extract: %w", err)
	}
	if err := archive.Wait(); err != nil {
		return fmt.Errorf("git archive: %w", err)
	}
	return nil
}
