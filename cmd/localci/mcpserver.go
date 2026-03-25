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
// tool and the dependency graph as a resource. Agents read the graph to
// determine which tools can be called in parallel.
func runMCPServer(args cliArgs) int {
	config, err := loadConfig(args.configFile)
	if err != nil {
		logErr("%v", err)
		return 1
	}

	currentSystem := getCurrentSystem()
	cwd, _ := os.Getwd()

	allSystems := collectSystems(config)

	// Resolve remote hosts upfront — tool handlers can't prompt.
	hostMap := map[string]string{currentSystem: mustHostname()}
	for _, sys := range allSystems {
		if sys != currentSystem {
			host, err := getRemoteHost(sys)
			if err != nil {
				logErr("Failed to get host for %s: %v", sys, err)
				return 1
			}
			hostMap[sys] = host
			exec.Command("ssh", host, "echo", "ok").Run()
		}
	}

	self, err := selfPathResolved()
	if err != nil {
		logErr("Could not resolve self path: %v", err)
		return 1
	}

	procs := buildProcessEntries(config)

	s := server.NewMCPServer("localci", "0.1.0")

	// Register a tool for each step×system combination
	for _, p := range procs {
		step := config.Steps[p.step]
		tool := mcp.NewTool(p.key,
			mcp.WithDescription(fmt.Sprintf("Run CI step: %s", step.Command)),
			mcp.WithString("sha",
				mcp.Description("Git ref to test (default: HEAD)"),
			),
		)
		s.AddTool(tool, makeStepHandler(p, step, self, cwd, hostMap, args.noSignoff))
	}

	// Register dependency graph resource
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
	hostMap map[string]string,
	noSignoff bool,
) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		sha := request.GetString("sha", "HEAD")

		// Resolve ref to full SHA
		resolved, err := resolveRef(sha)
		if err == nil {
			sha = resolved
		}

		cmdParts := []string{self, "--sha", sha}
		if noSignoff {
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

		output := buf.String()
		// Truncate large output
		lines := strings.Split(output, "\n")
		const maxLines = 200
		if len(lines) > maxLines {
			output = fmt.Sprintf("... (%d lines truncated)\n", len(lines)-maxLines) +
				strings.Join(lines[len(lines)-maxLines:], "\n")
		}

		if rc != 0 {
			return mcp.NewToolResultText(fmt.Sprintf("FAILED (exit %d)\n\n%s", rc, output)), nil
		}
		return mcp.NewToolResultText(output), nil
	}
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
