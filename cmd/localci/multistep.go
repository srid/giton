package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/fatih/color"
	"github.com/rodaine/table"
)

// StepConfig represents a step in the multi-step config file (localci.json).
// Each step has a command and optionally targets specific Nix systems.
type StepConfig struct {
	Command   string   `json:"command"`
	Systems   []string `json:"systems,omitempty"`
	DependsOn []string `json:"depends_on,omitempty"`
}

// MultiStepConfig is the top-level config file structure.
type MultiStepConfig struct {
	Steps map[string]StepConfig `json:"steps"`
}

// loadConfig reads and parses a localci JSON config file.
func loadConfig(path string) (MultiStepConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return MultiStepConfig{}, fmt.Errorf("config file not found: %s", path)
		}
		return MultiStepConfig{}, err
	}
	var config MultiStepConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return MultiStepConfig{}, fmt.Errorf("failed to parse config: %w", err)
	}
	return config, nil
}

// processEntry tracks a step×system combination and its unique key.
type processEntry struct {
	step string
	sys  string // empty if no systems defined
	key  string // "step" or "step (system)"
}

// resolveHosts resolves and warms SSH connections for all remote systems.
// Must happen upfront since subprocesses/tool handlers can't prompt.
func resolveHosts(config MultiStepConfig) (hostMap map[string]string, allSystems []string, err error) {
	currentSystem := getCurrentSystem()
	allSystems = collectSystems(config)
	hostMap = map[string]string{currentSystem: mustHostname()}
	for _, sys := range allSystems {
		if sys != currentSystem {
			host, err := getRemoteHost(sys)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to get host for %s: %w", sys, err)
			}
			hostMap[sys] = host
			logMsg("Warming SSH connection to %s (%s)...", cBold(host), sys)
			exec.Command("ssh", host, "echo", "ok").Run()
		}
	}
	return hostMap, allSystems, nil
}

// runMultiStep reads a JSON config defining steps (with optional systems and
// dependencies), resolves remote hosts, extracts the repo to each target,
// and runs all steps in parallel using a native DAG executor.
// Each step self-invokes localci in single-step mode with --sha pinning.
func runMultiStep(args cliArgs, sha string) int {
	config, err := loadConfig(args.configFile)
	if err != nil {
		logErr("%v", err)
		return 1
	}

	logMsg("Multi-step mode: %s  %s", cBold(args.configFile), cDim("SHA="+shortSHA(sha)))

	currentSystem := getCurrentSystem()
	cwd, _ := os.Getwd()

	hostMap, allSystems, err := resolveHosts(config)
	if err != nil {
		logErr("%v", err)
		return 1
	}

	// Pre-extract repo once per system for efficiency
	workdirBase := fmt.Sprintf("/tmp/localci-%s", shortSHA(sha))
	workdirMap := make(map[string]string)

	localDir := workdirBase + "-local"
	logMsg("Extracting repo (local)...")
	if err := extractRepoLocal(sha, localDir); err != nil {
		logErr("Failed to extract repo locally: %v", err)
		return 1
	}
	workdirMap[currentSystem] = localDir

	for _, sys := range allSystems {
		if sys != currentSystem {
			host := hostMap[sys]
			rdir := fmt.Sprintf("%s-%s", workdirBase, sys)
			logMsg("Extracting repo on %s (%s)...", cBold(host), sys)
			if err := extractRepoRemote(sha, host, rdir); err != nil {
				logErr("Failed to extract repo on %s: %v", host, err)
				return 1
			}
			workdirMap[sys] = rdir
		}
	}

	logDir := fmt.Sprintf("/tmp/localci-%s-logs", shortSHA(sha))
	os.RemoveAll(logDir)
	os.MkdirAll(logDir, 0o755)

	procs := buildProcessEntries(config)
	writeManifest(logDir, procs)

	self, err := selfPathResolved()
	if err != nil {
		logErr("Could not resolve self path: %v", err)
		return 1
	}

	// Run DAG executor
	exitCode := runDAG(procs, config, sha, self, cwd, logDir, hostMap, workdirMap, args.noSignoff)

	// Cleanup
	if exitCode == 0 {
		os.RemoveAll(logDir)
	}
	os.RemoveAll(localDir)
	for _, sys := range allSystems {
		if sys != currentSystem {
			host := hostMap[sys]
			rdir := fmt.Sprintf("%s-%s", workdirBase, sys)
			exec.Command("ssh", host, "rm -rf '"+rdir+"'").Run()
		}
	}

	fmt.Fprintln(os.Stderr)
	if exitCode == 0 {
		logOk("All steps passed")
	} else {
		logWarn("One or more steps failed (exit %d)", exitCode)
		printStepReport(logDir)
		logInfo("Full logs: %s/", logDir)
	}

	return exitCode
}

type stepState int

const (
	stateWaiting stepState = iota
	stateRunning
	stateDone
	stateFailed
	stateSkipped
)

// runDAG executes steps respecting dependencies. Independent steps run
// concurrently via goroutines. If a step fails, its dependents are skipped.
func runDAG(
	procs []processEntry, config MultiStepConfig,
	sha, self, cwd, logDir string,
	hostMap, workdirMap map[string]string,
	noSignoff bool,
) int {
	// Build dependency map: key → list of dependency keys
	depMap := buildDepMap(procs, config)

	var mu sync.Mutex
	state := make(map[string]stepState)
	for _, p := range procs {
		state[p.key] = stateWaiting
	}

	var wg sync.WaitGroup
	hasFailure := false

	// tryLaunch checks if a step's dependencies are met and launches it.
	// Must be called with mu held.
	var tryLaunch func(p processEntry)
	tryLaunch = func(p processEntry) {
		if state[p.key] != stateWaiting {
			return
		}
		for _, dep := range depMap[p.key] {
			switch state[dep] {
			case stateFailed, stateSkipped:
				state[p.key] = stateSkipped
				logWarn("Skipped %s (dependency failed)", cBold(p.key))
				// Cascade: skip anything that depends on this
				for _, pp := range procs {
					tryLaunch(pp)
				}
				return
			case stateWaiting, stateRunning:
				return // not ready yet
			}
		}

		state[p.key] = stateRunning
		wg.Add(1)
		go func(p processEntry) {
			defer wg.Done()

			step := config.Steps[p.step]
			cmdParts := buildStepCmd(self, sha, p, step, workdirMap, noSignoff)
			logMsg("Running %s...", cBold(p.key))

			cmd := exec.Command(cmdParts[0], cmdParts[1:]...)
			cmd.Dir = cwd
			var buf bytes.Buffer
			cmd.Stdout = &buf
			cmd.Stderr = &buf
			rc := exitCode(cmd.Run())

			output := buf.Bytes()

			// Write log file
			logFile := filepath.Join(logDir, sanitizeLogName(p.key)+".log")
			os.WriteFile(logFile, output, 0o644)

			// Print captured output so callers can see step results
			mu.Lock()
			os.Stderr.Write(output)
			if rc == 0 {
				state[p.key] = stateDone
				logOk("%s passed", cBold(p.key))
			} else {
				state[p.key] = stateFailed
				hasFailure = true
				logWarn("%s failed (exit %d)", cBold(p.key), rc)
			}
			// Try launching dependents
			for _, pp := range procs {
				tryLaunch(pp)
			}
			mu.Unlock()
		}(p)
	}

	mu.Lock()
	for _, p := range procs {
		tryLaunch(p)
	}
	mu.Unlock()

	wg.Wait()

	if hasFailure {
		return 1
	}
	return 0
}

// buildDepMap returns a map from process key to its dependency keys.
func buildDepMap(procs []processEntry, config MultiStepConfig) map[string][]string {
	depMap := make(map[string][]string)
	for _, p := range procs {
		step := config.Steps[p.step]
		var deps []string
		for _, dep := range step.DependsOn {
			for _, dp := range procs {
				if dp.step == dep && dp.sys == p.sys {
					deps = append(deps, dp.key)
					break
				}
			}
		}
		depMap[p.key] = deps
	}
	return depMap
}

// buildStepCmd constructs the self-invocation command for a single step.
func buildStepCmd(self, sha string, p processEntry, step StepConfig, workdirMap map[string]string, noSignoff bool) []string {
	cmdParts := []string{self, "--sha", sha}
	if noSignoff {
		cmdParts = append(cmdParts, "--no-signoff")
	}
	if p.sys != "" {
		cmdParts = append(cmdParts, "-s", p.sys)
		if dir, ok := workdirMap[p.sys]; ok {
			cmdParts = append(cmdParts, "--workdir", dir)
		}
	}
	cmdParts = append(cmdParts, "-n", p.step, "--", step.Command)
	return cmdParts
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

// buildProcessEntries expands the step×system matrix into individual
// process entries. Each entry gets a unique key: just the step name when
// there's one system, "step (system)" when multiple.
func buildProcessEntries(config MultiStepConfig) []processEntry {
	var procs []processEntry
	for stepName, step := range config.Steps {
		systems := step.Systems
		if len(systems) == 0 {
			systems = []string{""} // no-system sentinel
		}
		for _, sys := range systems {
			key := stepName
			if sys != "" {
				key = fmt.Sprintf("%s (%s)", stepName, sys)
			}
			procs = append(procs, processEntry{step: stepName, sys: sys, key: key})
		}
	}
	return procs
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

var logNameReplacer = strings.NewReplacer("/", "-", " ", "-", "(", "-", ")", "-")
var multiDash = regexp.MustCompile(`-{2,}`)

func sanitizeLogName(name string) string {
	s := logNameReplacer.Replace(name)
	s = multiDash.ReplaceAllString(s, "-")
	return strings.TrimRight(s, "-")
}

type manifestEntry struct {
	Key     string `json:"key"`
	Step    string `json:"step"`
	System  string `json:"system,omitempty"`
	LogFile string `json:"log_file"`
}

func writeManifest(logDir string, procs []processEntry) {
	var entries []manifestEntry
	for _, p := range procs {
		entries = append(entries, manifestEntry{
			Key:     p.key,
			Step:    p.step,
			System:  p.sys,
			LogFile: filepath.Join(logDir, sanitizeLogName(p.key)+".log"),
		})
	}
	data, _ := json.MarshalIndent(entries, "", "  ")
	os.WriteFile(filepath.Join(logDir, "manifest.json"), data, 0o644)
}

type stepResult struct {
	step     string
	system   string
	failed   bool
	messages []string
}

func loadStepResults(logDir string) []stepResult {
	data, err := os.ReadFile(filepath.Join(logDir, "manifest.json"))
	if err != nil {
		return nil
	}
	var entries []manifestEntry
	if json.Unmarshal(data, &entries) != nil {
		return nil
	}

	var results []stepResult
	for _, e := range entries {
		sr := stepResult{step: e.Step, system: e.System}
		logData, err := os.ReadFile(e.LogFile)
		if err != nil || len(logData) == 0 {
			results = append(results, sr)
			continue
		}
		for _, line := range strings.Split(strings.TrimSpace(string(logData)), "\n") {
			sr.messages = append(sr.messages, line)
			if strings.Contains(line, "failed") {
				sr.failed = true
			}
		}
		results = append(results, sr)
	}
	return results
}

func printStepReport(logDir string) {
	results := loadStepResults(logDir)
	if len(results) == 0 {
		return
	}

	headerFmt := color.New(color.FgWhite, color.Bold).SprintfFunc()
	passFmt := color.New(color.FgGreen).SprintfFunc()
	failFmt := color.New(color.FgRed, color.Bold).SprintfFunc()
	skipFmt := color.New(color.FgYellow).SprintfFunc()

	statusStr := func(r stepResult) string {
		if r.failed {
			return failFmt("FAIL")
		}
		if len(r.messages) == 0 {
			return skipFmt("skip")
		}
		return passFmt("pass")
	}

	hasSystems := false
	for _, r := range results {
		if r.system != "" {
			hasSystems = true
			break
		}
	}

	fmt.Fprintln(os.Stderr)
	if hasSystems {
		tbl := table.New("Step", "System", "Status")
		tbl.WithHeaderFormatter(headerFmt).WithWriter(os.Stderr)
		for _, r := range results {
			tbl.AddRow(r.step, r.system, statusStr(r))
		}
		tbl.Print()
	} else {
		tbl := table.New("Step", "Status")
		tbl.WithHeaderFormatter(headerFmt).WithWriter(os.Stderr)
		for _, r := range results {
			tbl.AddRow(r.step, statusStr(r))
		}
		tbl.Print()
	}

	const tailLines = 20
	for _, r := range results {
		if !r.failed {
			continue
		}
		label := r.step
		if r.system != "" {
			label += " (" + r.system + ")"
		}
		fmt.Fprintln(os.Stderr)
		logWarn("%s:", cBold(label))
		start := 0
		if len(r.messages) > tailLines {
			start = len(r.messages) - tailLines
			logInfo("... (%d lines omitted)", start)
		}
		for _, msg := range r.messages[start:] {
			fmt.Fprintf(os.Stderr, "    %s\n", msg)
		}
	}
}

// buildDependencyGraph returns a JSON-serializable dependency graph
// for the given config. Used by the MCP server's graph resource.
func buildDependencyGraph(procs []processEntry, config MultiStepConfig) map[string]any {
	depMap := buildDepMap(procs, config)
	steps := make(map[string]any)
	for _, p := range procs {
		deps := depMap[p.key]
		if deps == nil {
			deps = []string{}
		}
		steps[p.key] = map[string]any{
			"depends_on": deps,
		}
	}
	return map[string]any{
		"steps": steps,
	}
}
