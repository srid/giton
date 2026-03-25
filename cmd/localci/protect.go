package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// runProtect reads the justfile ci module, computes all status contexts
// (expanding the step×system matrix), and sets them as required status
// checks on the repo's default branch via gh api.
func runProtect() int {
	config, err := loadFromJustfile()
	if err != nil {
		logErr("%v", err)
		return 1
	}

	repo, err := getRepo()
	if err != nil || repo == "" {
		logErr("Could not determine GitHub repository. Is 'gh' authenticated?")
		return 1
	}

	branch, err := getDefaultBranch(repo)
	if err != nil {
		logErr("Could not determine default branch: %v", err)
		return 1
	}

	contexts := buildContexts(config)

	logMsg("Setting required status checks on %s (%s)", cBold(repo), branch)
	for _, ctx := range contexts {
		logInfo("%s", ctx)
	}

	payload := map[string]any{
		"required_status_checks": map[string]any{
			"strict":   true,
			"contexts": contexts,
		},
		"enforce_admins":                false,
		"required_pull_request_reviews": nil,
		"restrictions":                  nil,
	}
	payloadJSON, _ := json.Marshal(payload)

	cmd := exec.Command("gh", "api",
		fmt.Sprintf("repos/%s/branches/%s/protection", repo, branch),
		"-X", "PUT",
		"--input", "-")
	cmd.Stdin = strings.NewReader(string(payloadJSON))
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		logErr("Failed to set branch protection: %v", err)
		return 1
	}

	logOk("Branch protection set on %s/%s", repo, branch)
	return 0
}

// buildContexts expands the config into GitHub status context strings.
func buildContexts(config MultiStepConfig) []string {
	var contexts []string
	for name, step := range config.Steps {
		if len(step.Systems) == 0 {
			contexts = append(contexts, "localci/"+name)
		} else {
			for _, sys := range step.Systems {
				contexts = append(contexts, fmt.Sprintf("localci/%s/%s", name, sys))
			}
		}
	}
	return contexts
}

func getDefaultBranch(repo string) (string, error) {
	out, err := exec.Command("gh", "api", "repos/"+repo, "--jq", ".default_branch").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
