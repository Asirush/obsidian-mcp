package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/asirush/obsidian-mcp/internal/mcp"
	"github.com/asirush/obsidian-mcp/internal/tools"
	"github.com/asirush/obsidian-mcp/internal/vault"
)

const (
	ServerName    = "obsidian-mcp"
	ServerVersion = "1.0.0"
)

func main() {
	var (
		vaultFlag   string
		httpAddr    string
		serverURL   string
		apiKey      string
		showVersion bool
	)

	flag.StringVar(&vaultFlag, "vault", "", "Path to the Obsidian vault directory (or set OBSIDIAN_VAULT_PATH)")
	flag.StringVar(&httpAddr, "http", "", "Listen address for HTTP mode (Open WebUI OpenAPI + MCP Streamable HTTP), e.g. ':8080' or '127.0.0.1:8080'")
	flag.StringVar(&serverURL, "server-url", "", "Optional public base URL advertised in /openapi.json (e.g. 'http://localhost:8080')")
	flag.StringVar(&apiKey, "api-key", "", "Optional Bearer API key for HTTP mode (or set OBSIDIAN_MCP_API_KEY)")
	flag.BoolVar(&showVersion, "version", false, "Print version and exit")
	flag.Parse()

	if showVersion {
		fmt.Printf("%s v%s\n", ServerName, ServerVersion)
		os.Exit(0)
	}

	vaultPath := strings.TrimSpace(vaultFlag)
	if vaultPath == "" && flag.NArg() > 0 {
		vaultPath = strings.TrimSpace(flag.Arg(0))
	}
	if vaultPath == "" {
		vaultPath = strings.TrimSpace(os.Getenv("OBSIDIAN_VAULT_PATH"))
	}
	if vaultPath == "" {
		vaultPath = strings.TrimSpace(os.Getenv("OBSIDIAN_VAULT"))
	}
	if vaultPath == "" {
		fmt.Fprintln(os.Stderr, "Error: Obsidian vault path is required.")
		fmt.Fprintln(os.Stderr, "Usage (stdio): obsidian-mcp --vault /path/to/vault")
		fmt.Fprintln(os.Stderr, "Usage (HTTP):  obsidian-mcp --vault /path/to/vault --http :8080")
		os.Exit(1)
	}

	if httpAddr == "" {
		httpAddr = strings.TrimSpace(os.Getenv("OBSIDIAN_MCP_HTTP"))
	}
	if apiKey == "" {
		apiKey = strings.TrimSpace(os.Getenv("OBSIDIAN_MCP_API_KEY"))
	}

	v, err := vault.New(vaultPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error initializing vault: %v\n", err)
		os.Exit(1)
	}

	instructions := fmt.Sprintf(
		"Obsidian MCP Server connected to vault at %q. Use the provided tools to read, create, update, append, patch, move, or delete markdown notes, manage YAML frontmatter and tags, analyze [[wikilinks]] and backlinks, and work with daily notes.",
		v.Root(),
	)

	srv := mcp.NewServer(ServerName, ServerVersion, instructions)
	tools.RegisterAll(srv, v)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if httpAddr != "" {
		fmt.Fprintf(os.Stderr, "obsidian-mcp v%s listening on %s (vault: %s)\n", ServerVersion, httpAddr, v.Root())
		fmt.Fprintf(os.Stderr, "  - OpenAPI Tool Server (Open WebUI): http://%s/openapi.json\n", formatDisplayHost(httpAddr))
		fmt.Fprintf(os.Stderr, "  - MCP Streamable HTTP endpoint:     http://%s/mcp\n", formatDisplayHost(httpAddr))
		err := srv.ServeHTTP(ctx, mcp.HTTPConfig{
			Addr:      httpAddr,
			ServerURL: serverURL,
			APIKey:    apiKey,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "HTTP server error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if err := srv.Serve(ctx, os.Stdin, os.Stdout); err != nil && err != context.Canceled {
		fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
		os.Exit(1)
	}
}

func formatDisplayHost(addr string) string {
	if strings.HasPrefix(addr, ":") {
		return "localhost" + addr
	}
	return addr
}

