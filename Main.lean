/-
  giton — Local CI tool: run commands on Nix platforms with GitHub status reporting.
  Lean 4 implementation.
-/
import Lean.Data.Json
import Lean.Data.Json.Printer

open Lean (Json toJson FromJson)
open System (FilePath)

-- ─── Colors ──────────────────────────────────────────────────────────────────

structure Colors where
  bold : String
  dim : String
  red : String
  green : String
  yellow : String
  cyan : String
  reset : String

def noColors : Colors := ⟨"", "", "", "", "", "", ""⟩

def ansiColors : Colors :=
  ⟨"\x1b[1m", "\x1b[2m", "\x1b[31m", "\x1b[32m", "\x1b[33m", "\x1b[36m", "\x1b[0m"⟩

def detectColors : IO Colors := do
  let res ← IO.Process.output { cmd := "test", args := #["-t", "2"] }
  if res.exitCode == 0 then return ansiColors else return noColors

-- ─── Logging ─────────────────────────────────────────────────────────────────

def logMsg (c : Colors) (msg : String) : IO Unit :=
  IO.eprintln s!"{c.cyan}{c.bold}==>{c.reset} {msg}"

def logInfo (c : Colors) (msg : String) : IO Unit :=
  IO.eprintln s!"    {c.dim}{msg}{c.reset}"

def logErr (c : Colors) (msg : String) : IO Unit :=
  IO.eprintln s!"{c.red}{c.bold}Error:{c.reset} {msg}"

def logOk (c : Colors) (msg : String) : IO Unit :=
  IO.eprintln s!"{c.green}{c.bold}==>{c.reset} {msg}"

def logWarn (c : Colors) (msg : String) : IO Unit :=
  IO.eprintln s!"{c.yellow}{c.bold}==>{c.reset} {msg}"

-- ─── Time formatting ─────────────────────────────────────────────────────────

private def zeroPad (n : Nat) : String :=
  if n < 10 then s!"0{n}" else s!"{n}"

def fmtTime (seconds : Nat) : String :=
  if seconds >= 3600 then
    let h := seconds / 3600
    let m := (seconds % 3600) / 60
    let s := seconds % 60
    s!"{h}h{zeroPad m}m{zeroPad s}s"
  else if seconds >= 60 then
    let m := seconds / 60
    let s := seconds % 60
    s!"{m}m{zeroPad s}s"
  else
    s!"{seconds}s"

-- ─── Helpers ─────────────────────────────────────────────────────────────────

def shortSHA (sha : String) : String :=
  sha.take 12

def truncDesc (s : String) (n : Nat) : String :=
  s.take n

/-- Run a command and return (stdout.trim, exitCode). -/
def runCmd (cmd : String) (args : Array String) : IO (String × UInt32) := do
  let res ← IO.Process.output { cmd := cmd, args := args }
  return (res.stdout.trim, res.exitCode)

/-- Run a command, inheriting stdout/stderr, return exit code. -/
def runCmdInherit (cmd : String) (args : Array String) : IO UInt32 := do
  let child ← IO.Process.spawn {
    cmd := cmd
    args := args
    stdout := .inherit
    stderr := .inherit
    stdin := .inherit
  }
  child.wait

/-- Run a shell command via bash -c, inheriting stdout/stderr. -/
def runShell (command : String) : IO UInt32 :=
  runCmdInherit "bash" #["-c", command]

-- ─── Git operations ──────────────────────────────────────────────────────────

def isInGitRepo : IO Bool := do
  let (_, rc) ← runCmd "git" #["rev-parse", "--is-inside-work-tree"]
  return rc == 0

def isTreeClean : IO Bool := do
  let (out, rc) ← runCmd "git" #["status", "--porcelain"]
  return rc == 0 && out.isEmpty

def resolveHEAD : IO String := do
  let (out, rc) ← runCmd "git" #["rev-parse", "HEAD"]
  if rc != 0 then throw (IO.userError "Could not resolve HEAD")
  return out

def extractRepoLocal (sha dir : String) : IO Unit := do
  let rc ← runShell s!"mkdir -p '{dir}' && git archive --format=tar '{sha}' | tar -C '{dir}' -x && chmod -R u+w '{dir}'"
  if rc != 0 then throw (IO.userError s!"Failed to extract repo to {dir}")

def extractRepoRemote (sha host dir : String) : IO Unit := do
  let rc ← runShell s!"git archive --format=tar '{sha}' | ssh '{host}' \"mkdir -p '{dir}' && tar -C '{dir}' -x && chmod -R u+w '{dir}'\""
  if rc != 0 then throw (IO.userError s!"Failed to extract repo to {host}:{dir}")

-- ─── GitHub operations ───────────────────────────────────────────────────────

def getRepo : IO String := do
  let (out, rc) ← runCmd "gh" #["repo", "view", "--json", "nameWithOwner", "--jq", ".nameWithOwner"]
  if rc != 0 || out.isEmpty then throw (IO.userError "Could not determine GitHub repository. Is 'gh' authenticated?")
  return out

def postStatus (repo sha state context description : String) : IO Unit := do
  let desc := truncDesc description 140
  let _ ← runCmd "gh" #["api", s!"repos/{repo}/statuses/{sha}",
    "-f", s!"state={state}", "-f", s!"context={context}",
    "-f", s!"description={desc}", "--silent"]
  pure ()

-- ─── Nix operations ──────────────────────────────────────────────────────────

def getCurrentSystem : IO String := do
  let (out, rc) ← runCmd "nix" #["eval", "--raw", "--impure", "--expr", "builtins.currentSystem"]
  if rc != 0 then throw (IO.userError "Could not determine current system")
  return out

-- ─── Host configuration ─────────────────────────────────────────────────────

def hostsFilePath : IO FilePath := do
  let xdg ← IO.getEnv "XDG_CONFIG_HOME"
  let configDir ← match xdg with
    | some d => pure d
    | none => do
      let home ← IO.getEnv "HOME"
      match home with
      | some h => pure s!"{h}/.config"
      | none => throw (IO.userError "Cannot determine config directory")
  return s!"{configDir}/giton/hosts.json"

def saveHost (system host : String) : IO Unit := do
  let path ← hostsFilePath
  let dir := path.parent.getD "."
  let _ ← runCmd "mkdir" #["-p", dir.toString]
  let pathExists ← path.pathExists
  let hostsJson ← if pathExists then do
    let content ← IO.FS.readFile path
    match Json.parse content with
    | .ok (.obj kvs) => pure (Json.obj (kvs.insert compare system (.str host)))
    | _ => pure (Json.obj (Lean.RBNode.leaf.insert compare system (.str host)))
  else
    pure (Json.obj (Lean.RBNode.leaf.insert compare system (.str host)))
  IO.FS.writeFile path hostsJson.pretty

def getRemoteHost (c : Colors) (system : String) : IO String := do
  let path ← hostsFilePath
  let pathExists ← path.pathExists
  if pathExists then do
    let content ← IO.FS.readFile path
    match Json.parse content with
    | .ok (.obj kvs) =>
      match kvs.find compare system with
      | some (.str host) =>
        logMsg c s!"Using saved host for {system}: {c.bold}{host}{c.reset}"
        return host
      | _ => pure ()
    | _ => pure ()
  IO.eprint s!"==> Enter hostname for {system}: "
  let host ← do
    let rc ← runShell "test -e /dev/tty"
    if rc == 0 then do
      let (h, _) ← runCmd "bash" #["-c", "read -r host </dev/tty && echo \"$host\""]
      pure h
    else do
      let line ← (← IO.getStdin).getLine
      pure line.trim
  let sanitized := String.mk (host.toList.filter fun ch =>
    ch.isAlphanum || ch == '.' || ch == '_' || ch == '-')
  if sanitized.isEmpty then do
    logErr c "No hostname provided."
    IO.Process.exit 1
  saveHost system sanitized
  return sanitized

-- ─── SSH helpers ─────────────────────────────────────────────────────────────

def ensureSSHControlDir (host : String) : IO Unit := do
  let (out, rc) ← runCmd "ssh" #["-G", host]
  if rc != 0 then return
  for line in out.splitOn "\n" do
    if line.startsWith "controlpath " then
      let path := (line.drop 12).trim
      if !path.isEmpty then
        let dir := (FilePath.mk path).parent.getD "."
        let _ ← runCmd "mkdir" #["-p", dir.toString]
        return

-- ─── CLI parsing ─────────────────────────────────────────────────────────────

structure CliArgs where
  system : String := ""
  systemExplicit : Bool := false
  name : String := ""
  cmd : Array String := #[]
  shaPin : String := ""
  configFile : String := ""
  tui : Bool := false
  workdir : String := ""

def usage : IO UInt32 := do
  IO.println "Usage: giton [options] -- <command...>"
  IO.println "       giton -f <config.json>"
  IO.println ""
  IO.println "Run commands on Nix platforms and post GitHub commit statuses."
  IO.println ""
  IO.println "Single-step mode:"
  IO.println "  -s, --system    Nix system string (if omitted, runs on current system)"
  IO.println "  -n, --name      Check name for GitHub status context (default: command name)"
  IO.println "  --              Separator before the command to run"
  IO.println ""
  IO.println "Multi-step mode:"
  IO.println "  -f, --file      JSON config file defining steps, systems, and dependencies"
  IO.println ""
  IO.println "Common options:"
  IO.println "  --sha           Pin to a specific commit SHA (skips clean-tree check)"
  IO.println "  --tui           Enable process-compose TUI (multi-step mode only)"
  IO.println ""
  IO.println "Status context: giton/<name> (no --system) or giton/<name>/<system> (with --system)"
  return 1

partial def parseArgs (args : List String) : IO CliArgs := do
  let rec go (args : List String) (acc : CliArgs) : IO CliArgs :=
    match args with
    | [] => pure acc
    | "-s" :: val :: rest => go rest { acc with system := val, systemExplicit := true }
    | "--system" :: val :: rest => go rest { acc with system := val, systemExplicit := true }
    | "-n" :: val :: rest => go rest { acc with name := val }
    | "--name" :: val :: rest => go rest { acc with name := val }
    | "--sha" :: val :: rest => go rest { acc with shaPin := val }
    | "-f" :: val :: rest => go rest { acc with configFile := val }
    | "--file" :: val :: rest => go rest { acc with configFile := val }
    | "--tui" :: rest => go rest { acc with tui := true }
    | "--workdir" :: val :: rest => go rest { acc with workdir := val }
    | "--" :: rest => pure { acc with cmd := rest.toArray }
    | "-h" :: _ => do let _ ← usage; IO.Process.exit 1
    | "--help" :: _ => do let _ ← usage; IO.Process.exit 1
    | other :: _ => do
      IO.eprintln s!"Error: Unknown option '{other}'"
      let _ ← usage
      IO.Process.exit 1
  go args {}

-- ─── JSON helpers ────────────────────────────────────────────────────────────

/-- Sanitize a process name for log filenames. -/
def sanitizeLogName (name : String) : String :=
  let replaced := name.toList.map fun ch =>
    if ch == '/' || ch == ' ' || ch == '(' || ch == ')' then '-' else ch
  let collapsed := replaced.foldl (init := ([] : List Char)) fun acc ch =>
    match acc with
    | prev :: _ => if prev == '-' && ch == '-' then acc else ch :: acc
    | [] => [ch]
  let result := collapsed.reverse
  let trimmed := match result.reverse with
    | '-' :: rest => rest.reverse
    | _ => result
  String.mk trimmed

-- ─── Step config ─────────────────────────────────────────────────────────────

structure StepConfig where
  command : String
  systems : Array String
  dependsOn : Array String

def parseStepConfig (json : Json) : Option StepConfig := do
  let command ← match json.getObjValAs? String "command" with
    | .ok c => some c
    | .error _ => none
  let systems := match json.getObjValAs? (Array String) "systems" with
    | .ok s => s
    | .error _ => #[]
  let dependsOn := match json.getObjValAs? (Array String) "depends_on" with
    | .ok d => d
    | .error _ => #[]
  return { command, systems, dependsOn }

structure MultiConfig where
  steps : Array (String × StepConfig)

def parseMultiConfig (json : Json) : Option MultiConfig := do
  let stepsJson ← match json.getObjVal? "steps" with
    | .ok s => some s
    | .error _ => none
  match stepsJson with
  | .obj kvs =>
    let steps := kvs.fold (init := #[]) fun acc k v =>
      match parseStepConfig v with
      | some cfg => acc.push (k, cfg)
      | none => acc
    some { steps }
  | _ => none

-- ─── Process entries for multi-step ──────────────────────────────────────────

structure ProcessEntry where
  step : String
  sys : String
  key : String

def buildProcessEntries (config : MultiConfig) : Array ProcessEntry :=
  config.steps.foldl (init := #[]) fun acc (stepName, step) =>
    let systems := if step.systems.isEmpty then #[""] else step.systems
    systems.foldl (init := acc) fun acc2 sys =>
      let key := if sys.isEmpty then stepName
        else if step.systems.size == 1 then stepName
        else s!"{stepName} ({sys})"
      acc2.push { step := stepName, sys, key }

-- ─── Build process-compose JSON config ───────────────────────────────────────

def mkJsonObj (fields : Array (String × Json)) : Json :=
  Json.obj (fields.foldl (init := .leaf) fun acc (k, v) => acc.insert compare k v)

def buildPCConfig (entries : Array ProcessEntry) (config : MultiConfig)
    (sha selfBin cwd logDir : String)
    (hostMap workdirMap : Array (String × String))
    : Json :=
  let findMap (m : Array (String × String)) (k : String) : Option String :=
    (m.find? fun (k', _) => k' == k).map Prod.snd
  let processes := entries.foldl (init := #[]) fun acc entry =>
    let stepCfg := (config.steps.find? fun (name, _) => name == entry.step).map Prod.snd
    match stepCfg with
    | none => acc
    | some cfg =>
      -- Build command parts
      let cmdBase := #[selfBin, "--sha", sha]
      let cmdSys := if entry.sys.isEmpty then #[]
        else
          let wdPart := match findMap workdirMap entry.sys with
            | some dir => #["--workdir", dir]
            | none => #[]
          #["-s", entry.sys] ++ wdPart
      let cmdTail := #["-n", entry.step, "--", cfg.command]
      let command := " ".intercalate (cmdBase ++ cmdSys ++ cmdTail).toList

      -- Resolve dependencies
      let depFields := cfg.dependsOn.foldl (init := #[]) fun depAcc dep =>
        let matched := entries.find? fun e => e.step == dep && e.sys == entry.sys
        match matched with
        | some m => depAcc.push (m.key, mkJsonObj #[("condition", .str "process_completed_successfully")])
        | none => depAcc

      let logFile := s!"{logDir}/{sanitizeLogName entry.key}.log"

      -- Build process fields
      let baseFields : Array (String × Json) := #[
        ("command", .str command),
        ("working_dir", .str cwd),
        ("log_location", .str logFile)
      ]
      let nsFields := if !entry.sys.isEmpty then
        let hostname := (findMap hostMap entry.sys).getD "local"
        #[("namespace", .str s!"{entry.sys} ({hostname})")]
      else #[]
      let availFields := #[("availability", mkJsonObj #[("restart", .str "exit_on_failure")])]
      let depFieldsArr := if !depFields.isEmpty then
        #[("depends_on", mkJsonObj depFields)]
      else #[]
      let allFields := baseFields ++ nsFields ++ availFields ++ depFieldsArr
      acc.push (entry.key, mkJsonObj allFields)

  mkJsonObj #[
    ("version", .str "0.5"),
    ("log_configuration", mkJsonObj #[("flush_each_line", .bool true)]),
    ("processes", mkJsonObj processes)
  ]

-- ─── Get hostname ────────────────────────────────────────────────────────────

def getHostname : IO String := do
  let (out, rc) ← runCmd "hostname" #[]
  if rc != 0 then return "local"
  return out

-- ─── Multi-step mode ─────────────────────────────────────────────────────────

def runMultiStep (c : Colors) (args : CliArgs) (sha : String) : IO UInt32 := do
  let configPath := args.configFile
  let configExists ← FilePath.pathExists configPath
  if !configExists then do
    logErr c s!"Config file not found: {configPath}"
    return 1

  let content ← IO.FS.readFile configPath
  let json ← match Json.parse content with
    | .ok j => pure j
    | .error e => do
      logErr c s!"Failed to parse config: {e}"
      return 1

  let config ← match parseMultiConfig json with
    | some cfg => pure cfg
    | none => do
      logErr c "Failed to parse multi-step config"
      return 1

  logMsg c s!"Multi-step mode: {c.bold}{configPath}{c.reset}  {c.dim}SHA={shortSHA sha}{c.reset}"

  let currentSystem ← getCurrentSystem
  let hostname ← getHostname
  let cwd ← IO.currentDir

  -- Collect all unique systems
  let allSystems := config.steps.foldl (init := #[]) fun acc (_, step) =>
    step.systems.foldl (init := acc) fun acc2 sys =>
      if acc2.contains sys then acc2 else acc2.push sys

  -- Resolve remote hosts upfront
  let mut hostMap : Array (String × String) := #[(currentSystem, hostname)]
  for sys in allSystems do
    if sys != currentSystem then do
      let host ← getRemoteHost c sys
      hostMap := hostMap.push (sys, host)
      logMsg c s!"Warming SSH connection to {c.bold}{host}{c.reset} ({sys})..."
      let _ ← runCmdInherit "ssh" #[host, "echo", "ok"]

  -- Pre-extract repo per system
  let workdirBase := s!"/tmp/giton-{shortSHA sha}"
  let localDir := s!"{workdirBase}-local"
  logMsg c "Extracting repo (local)..."
  extractRepoLocal sha localDir
  let mut workdirMap : Array (String × String) := #[(currentSystem, localDir)]

  for sys in allSystems do
    if sys != currentSystem then do
      let host := ((hostMap.find? fun (k, _) => k == sys).map Prod.snd).getD "local"
      let rdir := s!"{workdirBase}-{sys}"
      logMsg c s!"Extracting repo on {c.bold}{host}{c.reset} ({sys})..."
      extractRepoRemote sha host rdir
      workdirMap := workdirMap.push (sys, rdir)

  let logDir := s!"/tmp/giton-{shortSHA sha}-logs"
  let _ ← runCmd "mkdir" #["-p", logDir]

  let entries := buildProcessEntries config

  -- Resolve self path
  let selfBin ← do
    let p ← IO.appPath
    pure p.toString

  let pcConfig := buildPCConfig entries config sha selfBin cwd.toString logDir hostMap workdirMap

  let pcFile := s!"/tmp/giton-pc-{shortSHA sha}.json"
  IO.FS.writeFile pcFile pcConfig.pretty

  let tuiFlag := if args.tui then "true" else "false"
  let pcExit ← runCmdInherit "process-compose" #["up", s!"--tui={tuiFlag}", "--no-server", "--config", pcFile]

  -- Cleanup
  let _ ← runCmd "rm" #["-f", pcFile]
  let _ ← runCmd "rm" #["-rf", localDir]
  for sys in allSystems do
    if sys != currentSystem then
      let host := ((hostMap.find? fun (k, _) => k == sys).map Prod.snd).getD ""
      if !host.isEmpty then do
        let rdir := s!"{workdirBase}-{sys}"
        let _ ← runCmd "ssh" #[host, s!"rm -rf '{rdir}'"]
        pure ()

  -- Summary
  IO.eprintln ""
  if pcExit == 0 then
    logOk c "All steps passed"
  else do
    logWarn c s!"One or more steps failed (exit {pcExit})"
    logInfo c s!"Logs: {logDir}/"
    if !args.tui then do
      let (lsOut, _) ← runCmd "bash" #["-c", s!"ls {logDir}/*.log 2>/dev/null || true"]
      for logFile in lsOut.splitOn "\n" do
        if logFile.isEmpty then continue
        let logExists ← FilePath.pathExists logFile
        if !logExists then continue
        let logContent ← IO.FS.readFile logFile
        if logContent.isEmpty then continue
        if (logContent.splitOn "failed").length <= 1 then continue
        let baseName := (FilePath.mk logFile).fileName.getD "unknown"
        let stepName := baseName.dropRight 4
        IO.eprintln ""
        logWarn c s!"{c.bold}{stepName}{c.reset}:"
        for line in logContent.splitOn "\n" do
          if line.isEmpty then continue
          match Json.parse line with
          | .ok lineJson =>
            match lineJson.getObjValAs? String "message" with
            | .ok msg => IO.eprintln msg
            | .error _ => pure ()
          | .error _ => pure ()
  return pcExit

-- ─── Single-step mode ────────────────────────────────────────────────────────

def runSingleStep (c : Colors) (args : CliArgs) (sha : String) : IO UInt32 := do
  if args.cmd.isEmpty then do
    logErr c "A command after -- is required (or use -f for multi-step mode)."
    let _ ← usage
    return 1

  let name := if args.name.isEmpty then
    let first := args.cmd[0]!
    (FilePath.mk first).fileName.getD first
  else args.name

  let context := if args.systemExplicit then s!"giton/{name}/{args.system}" else s!"giton/{name}"

  let repo ← getRepo
  let cmdStr := " ".intercalate args.cmd.toList

  logMsg c s!"{c.bold}{context}{c.reset}  {c.dim}{repo}@{shortSHA sha}{c.reset}"
  logInfo c cmdStr

  postStatus repo sha "pending" context s!"Running: {cmdStr}"

  let remote ← if args.systemExplicit then do
    let currentSystem ← getCurrentSystem
    pure (currentSystem != args.system)
  else pure false

  let startTime ← IO.monoMsNow

  let exitCode ← if !args.workdir.isEmpty then do
    if remote then do
      let host ← getRemoteHost c args.system
      runShell s!"ssh '{host}' \"cd '{args.workdir}' && {cmdStr}\""
    else
      runShell s!"cd '{args.workdir}' && {cmdStr}"
  else if remote then do
    let host ← getRemoteHost c args.system
    let remoteDir := s!"/tmp/giton-{shortSHA sha}"
    ensureSSHControlDir host
    logMsg c s!"Copying repo to {c.bold}{host}{c.reset}..."
    extractRepoRemote sha host remoteDir
    let rc ← runShell s!"ssh '{host}' \"cd '{remoteDir}' && {cmdStr}\""
    logMsg c "Cleaning up remote temp dir..."
    let _ ← runCmd "ssh" #[host, s!"rm -rf '{remoteDir}'"]
    pure rc
  else do
    -- Local: use mktemp for unique dir
    let (tmpSuffix, _) ← runCmd "mktemp" #["-u", "XXXXXX"]
    let tmpdir := s!"/tmp/giton-{shortSHA sha}-{tmpSuffix}"
    logMsg c "Extracting repo..."
    extractRepoLocal sha tmpdir
    let rc ← runShell s!"cd '{tmpdir}' && {cmdStr}"
    let _ ← runCmd "rm" #["-rf", tmpdir]
    pure rc

  let endTime ← IO.monoMsNow
  let elapsedMs := endTime - startTime
  let elapsed := fmtTime (elapsedMs / 1000)

  if exitCode == 0 then do
    logOk c s!"{c.bold}{context}{c.reset} passed in {c.green}{elapsed}{c.reset}"
    postStatus repo sha "success" context s!"Passed in {elapsed}: {cmdStr}"
  else do
    logWarn c s!"{c.bold}{context}{c.reset} failed (exit {exitCode}) in {c.yellow}{elapsed}{c.reset}"
    postStatus repo sha "failure" context s!"Failed (exit {exitCode}) in {elapsed}: {cmdStr}"

  return exitCode

-- ─── Main ────────────────────────────────────────────────────────────────────

def main (rawArgs : List String) : IO UInt32 := do
  let c ← detectColors
  let args ← parseArgs rawArgs

  let inRepo ← isInGitRepo
  if !inRepo then do
    logErr c "Not inside a git repository."
    return 1

  let sha ← if !args.shaPin.isEmpty then pure args.shaPin
  else do
    let clean ← isTreeClean
    if !clean then do
      logErr c "Working tree is dirty. Commit or stash changes first."
      IO.Process.exit 1
    resolveHEAD

  if !args.configFile.isEmpty then
    runMultiStep c args sha
  else
    runSingleStep c args sha
