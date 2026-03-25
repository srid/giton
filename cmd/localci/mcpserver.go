package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// runMCPServer starts an MCP server over stdio, exposing each step as a
// tool and the dependency graph as a resource. Tool descriptions include
// dependency info so agents can parallelize independent steps.
func runMCPServer(args cliArgs) int {
	config, err := loadConfig(args.configFile)
	if err != nil {
		logErr("%v", err)
		return 1
	}

	cwd, _ := os.Getwd()

	// Resolve and warm SSH connections upfront — tool handlers can't prompt.
	if _, _, err := resolveHosts(config); err != nil {
		logErr("%v", err)
		return 1
	}

	self, err := selfPathResolved()
	if err != nil {
		logErr("Could not resolve self path: %v", err)
		return 1
	}

	procs := buildProcessEntries(config)
	depMap := buildDepMap(procs, config)

	s := server.NewMCPServer("localci", "0.1.0")

	// Register a tool per step×system with dependency info in description.
	// Agents see which tools can run in parallel without reading a resource.
	for _, p := range procs {
		step := config.Steps[p.step]
		desc := fmt.Sprintf("Run CI step: %s", step.Command)
		deps := depMap[p.key]
		if len(deps) == 0 {
			desc += " (no dependencies — can run immediately)"
		} else {
			desc += fmt.Sprintf(" (depends on: %s — run those first)", strings.Join(deps, ", "))
		}

		tool := mcp.NewTool(p.key,
			mcp.WithDescription(desc),
			mcp.WithString("sha",
				mcp.Description("Git ref to test (default: HEAD)"),
			),
		)
		s.AddTool(tool, makeStepHandler(p, step, self, cwd))
	}

	// Dependency graph resource (structured JSON for programmatic access)
	graphResource := mcp.NewResource(
		"localci://graph",
		"Dependency Graph",
		mcp.WithResourceDescription("Step dependency graph — shows which steps can run in parallel"),
		mcp.WithMIMEType("application/json"),
	)
	s.AddResource(graphResource, makeGraphHandler(procs, config))

	if err := server.ServeStdio(s); err != nil {
		logErr("MCP server error: %v", err)
		return 1
	}
	return 0
}

func makeStepHandler(
	p processEntry, step StepConfig,
	self, cwd string,
) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		sha := request.GetString("sha", "HEAD")

		resolved, err := resolveRef(sha)
		if err == nil {
			sha = resolved
		}

		cmdParts := []string{self, "--sha", sha}
		if !isCommitPushed(sha) {
			cmdParts = append(cmdParts, "--no-signoff")
		}
		if p.sys != "" {
			cmdParts = append(cmdParts, "-s", p.sys)
		}
		cmdParts = append(cmdParts, "-n", p.step, "--", step.Command)

		cmd := exec.CommandContext(ctx, cmdParts[0], cmdParts[1:]...)
		cmd.Dir = cwd
		var buf bytes.Buffer
		cmd.Stdout = &buf
		cmd.Stderr = &buf
		rc := exitCode(cmd.Run())

		output := truncateOutput(buf.String(), 200)

		if rc != 0 {
			return mcp.NewToolResultText(fmt.Sprintf("FAILED (exit %d)\n\n%s", rc, output)), nil
		}
		return mcp.NewToolResultText(output), nil
	}
}

// truncateOutput keeps the last maxLines of output, prepending a truncation notice.
func truncateOutput(output string, maxLines int) string {
	lines := strings.Split(output, "\n")
	if len(lines) <= maxLines {
		return output
	}
	return fmt.Sprintf("... (%d lines truncated)\n", len(lines)-maxLines) +
		strings.Join(lines[len(lines)-maxLines:], "\n")
}

func makeGraphHandler(procs []processEntry, config MultiStepConfig) server.ResourceHandlerFunc {
	return func(ctx context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		graph := buildDependencyGraph(procs, config)
		data, _ := json.MarshalIndent(graph, "", "  ")
		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      "localci://graph",
				MIMEType: "application/json",
				Text:     string(data),
			},
		}, nil
	}
}
