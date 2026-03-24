package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// StepConfig represents a step in the multi-step config file.
type StepConfig struct {
	Command   string   `json:"command"`
	Systems   []string `json:"systems,omitempty"`
	DependsOn []string `json:"depends_on,omitempty"`
}

// MultiStepConfig is the top-level config file structure.
type MultiStepConfig struct {
	Steps map[string]StepConfig `json:"steps"`
}

// ProcessCompose types for config generation.
type pcConfig struct {
	Version          string               `json:"version"`
	LogConfiguration pcLogConfig          `json:"log_configuration"`
	Processes        map[string]pcProcess `json:"processes"`
}

type pcLogConfig struct {
	FlushEachLine bool `json:"flush_each_line"`
}

type pcProcess struct {
	Command      string                    `json:"command"`
	WorkingDir   string                    `json:"working_dir"`
	LogLocation  string                    `json:"log_location"`
	Namespace    string                    `json:"namespace,omitempty"`
	Availability pcAvailability            `json:"availability"`
	DependsOn    map[string]pcDependency   `json:"depends_on,omitempty"`
}

type pcAvailability struct {
	Restart string `json:"restart"`
}

type pcDependency struct {
	Condition string `json:"condition"`
}

// processEntry tracks a step×system combination and its process-compose key.
type processEntry struct {
	step string
	sys  string // empty if no systems defined
	key  string // process-compose process name
}

func runMultiStep(args cliArgs, sha string) int {
	data, err := os.ReadFile(args.configFile)
	if err != nil {
		if os.IsNotExist(err) {
			logErr("Config file not found: %s", args.configFile)
		} else {
			logErr("Failed to read config: %v", err)
		}
		return 1
	}

	var config MultiStepConfig
	if err := json.Unmarshal(data, &config); err != nil {
		logErr("Failed to parse config: %v", err)
		return 1
	}

	logMsg("Multi-step mode: %s%s%s  %sSHA=%s%s", bold, args.configFile, reset, dim, shortSHA(sha), reset)

	currentSystem := getCurrentSystem()
	cwd, _ := os.Getwd()

	// Collect all unique systems from config
	allSystems := collectSystems(config)

	// Resolve remote hosts upfront
	hostMap := map[string]string{currentSystem: mustHostname()}
	for _, sys := range allSystems {
		if sys != currentSystem {
			host, err := getRemoteHost(sys)
			if err != nil {
				logErr("Failed to get host for %s: %v", sys, err)
				return 1
			}
			hostMap[sys] = host
			// Warm SSH connection
			logMsg("Warming SSH connection to %s%s%s (%s)...", bold, host, reset, sys)
			exec.Command("ssh", host, "echo", "ok").Run()
		}
	}

	// Pre-extract repo once per system
	workdirBase := fmt.Sprintf("/tmp/giton-%s", shortSHA(sha))
	workdirMap := make(map[string]string)

	// Local
	localDir := workdirBase + "-local"
	logMsg("Extracting repo (local)...")
	if err := extractRepoLocal(sha, localDir); err != nil {
		logErr("Failed to extract repo locally: %v", err)
		return 1
	}
	workdirMap[currentSystem] = localDir

	// Remote systems
	for _, sys := range allSystems {
		if sys != currentSystem {
			host := hostMap[sys]
			rdir := fmt.Sprintf("%s-%s", workdirBase, sys)
			logMsg("Extracting repo on %s%s%s (%s)...", bold, host, reset, sys)
			if err := extractRepoRemote(sha, host, rdir); err != nil {
				logErr("Failed to extract repo on %s: %v", host, err)
				return 1
			}
			workdirMap[sys] = rdir
		}
	}

	logDir := fmt.Sprintf("/tmp/giton-%s-logs", shortSHA(sha))
	os.MkdirAll(logDir, 0o755)

	// Build process entries (step × system matrix)
	procs := buildProcessEntries(config)

	// Resolve self path
	self, err := selfPathResolved()
	if err != nil {
		logErr("Could not resolve self path: %v", err)
		return 1
	}

	// Generate process-compose config
	pcCfg := generatePCConfig(config, procs, sha, self, cwd, logDir, hostMap, workdirMap)

	// Write process-compose config to temp file
	pcFile, err := os.CreateTemp("", "giton-pc-*.json")
	if err != nil {
		logErr("Failed to create temp file: %v", err)
		return 1
	}
	defer os.Remove(pcFile.Name())

	enc := json.NewEncoder(pcFile)
	enc.SetIndent("", "  ")
	if err := enc.Encode(pcCfg); err != nil {
		logErr("Failed to write process-compose config: %v", err)
		return 1
	}
	pcFile.Close()

	// Cleanup function
	defer func() {
		os.RemoveAll(localDir)
		for _, sys := range allSystems {
			if sys != currentSystem {
				host := hostMap[sys]
				rdir := fmt.Sprintf("%s-%s", workdirBase, sys)
				exec.Command("ssh", host, "rm -rf '"+rdir+"'").Run()
			}
		}
	}()

	// Run process-compose
	pcCmd := exec.Command("process-compose", "up",
		"--tui="+strconv.FormatBool(args.tui), "--no-server", "--config", pcFile.Name())
	pcCmd.Stdout = os.Stdout
	pcCmd.Stderr = os.Stderr
	pcExit := exitCode(pcCmd.Run())

	// Print summary
	fmt.Fprintln(os.Stderr)
	if pcExit == 0 {
		logOk("All steps passed")
	} else {
		logWarn("One or more steps failed (exit %d)", pcExit)
		logInfo("Logs: %s/", logDir)
		if !args.tui {
			printFailedLogs(logDir)
		}
	}

	return pcExit
}

func collectSystems(config MultiStepConfig) []string {
	seen := make(map[string]bool)
	var systems []string
	for _, step := range config.Steps {
		for _, sys := range step.Systems {
			if !seen[sys] {
				seen[sys] = true
				systems = append(systems, sys)
			}
		}
	}
	return systems
}

func buildProcessEntries(config MultiStepConfig) []processEntry {
	var procs []processEntry
	for stepName, step := range config.Steps {
		systems := step.Systems
		if len(systems) == 0 {
			procs = append(procs, processEntry{
				step: stepName,
				sys:  "",
				key:  stepName,
			})
		} else {
			for _, sys := range systems {
				var key string
				if len(systems) == 1 {
					key = stepName
				} else {
					key = fmt.Sprintf("%s (%s)", stepName, sys)
				}
				procs = append(procs, processEntry{
					step: stepName,
					sys:  sys,
					key:  key,
				})
			}
		}
	}
	return procs
}

func generatePCConfig(
	config MultiStepConfig,
	procs []processEntry,
	sha, self, cwd, logDir string,
	hostMap, workdirMap map[string]string,
) pcConfig {
	processes := make(map[string]pcProcess)

	for _, p := range procs {
		step := config.Steps[p.step]

		// Build command: self --sha SHA [-s sys] [--workdir dir] -n step -- command
		cmdParts := []string{self, "--sha", sha}
		if p.sys != "" {
			cmdParts = append(cmdParts, "-s", p.sys)
			if dir, ok := workdirMap[p.sys]; ok {
				cmdParts = append(cmdParts, "--workdir", dir)
			}
		}
		cmdParts = append(cmdParts, "-n", p.step, "--", step.Command)

		// Resolve dependencies for this system
		depends := make(map[string]pcDependency)
		for _, dep := range step.DependsOn {
			// Find the matching process entry for this dependency + same system
			for _, dp := range procs {
				if dp.step == dep && dp.sys == p.sys {
					depends[dp.key] = pcDependency{Condition: "process_completed_successfully"}
					break
				}
			}
		}

		// Build log file path
		logFile := filepath.Join(logDir, sanitizeLogName(p.key)+".log")

		proc := pcProcess{
			Command:      strings.Join(cmdParts, " "),
			WorkingDir:   cwd,
			LogLocation:  logFile,
			Availability: pcAvailability{Restart: "exit_on_failure"},
		}
		if p.sys != "" {
			hostname := hostMap[p.sys]
			if hostname == "" {
				hostname = "local"
			}
			proc.Namespace = fmt.Sprintf("%s (%s)", p.sys, hostname)
		}
		if len(depends) > 0 {
			proc.DependsOn = depends
		}

		processes[p.key] = proc
	}

	return pcConfig{
		Version:          "0.5",
		LogConfiguration: pcLogConfig{FlushEachLine: true},
		Processes:        processes,
	}
}

var logNameRe = regexp.MustCompile(`[/ ()]`)
var multiDash = regexp.MustCompile(`-{2,}`)

func sanitizeLogName(name string) string {
	s := logNameRe.ReplaceAllString(name, "-")
	s = multiDash.ReplaceAllString(s, "-")
	s = strings.TrimRight(s, "-")
	return s
}

func selfPathResolved() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

func mustHostname() string {
	h, err := os.Hostname()
	if err != nil {
		return "local"
	}
	return h
}

func printFailedLogs(logDir string) {
	entries, err := os.ReadDir(logDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".log") {
			continue
		}
		path := filepath.Join(logDir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil || len(data) == 0 {
			continue
		}
		content := string(data)
		if !strings.Contains(content, "failed") {
			continue
		}
		stepName := strings.TrimSuffix(entry.Name(), ".log")
		fmt.Fprintln(os.Stderr)
		logWarn("%s%s%s:", bold, stepName, reset)

		// Parse JSON log lines and extract messages
		for _, line := range strings.Split(content, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var logEntry struct {
				Message string `json:"message"`
			}
			if json.Unmarshal([]byte(line), &logEntry) == nil && logEntry.Message != "" {
				fmt.Fprintln(os.Stderr, logEntry.Message)
			}
		}
	}
}
