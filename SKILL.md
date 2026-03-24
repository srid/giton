---
name: localci
description: Run local CI steps and post GitHub commit statuses. Use when you need to build, test, or verify code changes against CI.
---

# localci

Run CI from the terminal. Each step posts a GitHub commit status (pending → success/failure).

## Quick usage

```bash
# Run a single command as a CI check
localci -- nix build

# Run multi-step CI from config
localci -f localci.json
```

## MCP mode (agent integration)

Start localci as an MCP server so you can invoke CI steps individually:

```json
{
  "mcpServers": {
    "localci": {
      "command": "nix",
      "args": ["run", "github:srid/localci", "--", "--mcp", "-f", "localci.json"]
    }
  }
}
```

In MCP mode, each step from the config file is exposed as an MCP tool. Invoke them individually to run specific CI checks. Dependencies are respected — invoking "test" auto-runs "build" first if configured with `depends_on`.

## When to use

- After making code changes, run CI to verify they pass
- When a step fails, read the output, fix the code, and re-invoke the step
- Use `--sha <sha>` to pin to a specific commit (skips clean-tree check)

## Config file format (localci.json)

```json
{
  "steps": {
    "build": { "command": "nix build" },
    "test": { "command": "nix run .#test", "depends_on": ["build"] }
  }
}
```

Steps can optionally target specific Nix systems with `"systems": ["x86_64-linux", "aarch64-darwin"]`.
