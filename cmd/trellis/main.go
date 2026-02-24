// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/wingedpig/trellis/internal/app"
	"github.com/wingedpig/trellis/internal/config"
)

var (
	version = "0.95"
)

func main() {
	// Check for subcommands before flag parsing
	if len(os.Args) > 1 && os.Args[1] == "init" {
		if err := runInit(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		os.Exit(0)
	}

	// Parse flags
	var (
		configPath  string
		host        string
		port        int
		worktree    string
		showVersion bool
		debug       bool
	)

	flag.StringVar(&configPath, "config", "", "Path to config file (default: auto-detect)")
	flag.StringVar(&configPath, "c", "", "Path to config file (short)")
	flag.StringVar(&host, "host", "", "HTTP server host (overrides config)")
	flag.IntVar(&port, "port", 0, "HTTP server port (overrides config)")
	flag.StringVar(&worktree, "worktree", "", "Worktree to activate (name or branch)")
	flag.StringVar(&worktree, "w", "", "Worktree to activate (short)")
	flag.BoolVar(&showVersion, "version", false, "Show version")
	flag.BoolVar(&showVersion, "v", false, "Show version (short)")
	flag.BoolVar(&debug, "debug", false, "Enable debug mode")
	flag.Parse()

	if showVersion {
		fmt.Printf("trellis %s\n", version)
		os.Exit(0)
	}

	// Find config file if not specified
	if configPath == "" {
		loader := config.NewLoader()
		found, err := loader.FindConfig()
		if err != nil {
			log.Fatalf("Error: %v", err)
		}
		configPath = found
	}

	log.Printf("Using config: %s", configPath)

	// Create and run app
	application, err := app.New(app.Options{
		ConfigPath: configPath,
		Host:       host,
		Port:       port,
		Worktree:   worktree,
		Debug:      debug,
		Version:    version,
	})
	if err != nil {
		log.Fatalf("Failed to create app: %v", err)
	}

	ctx := context.Background()
	if err := application.Run(ctx); err != nil {
		log.Fatalf("App error: %v", err)
	}
}

// runInit handles the "trellis init" command
func runInit() error {
	// Parse init-specific flags
	initFlags := flag.NewFlagSet("init", flag.ExitOnError)
	showHelp := initFlags.Bool("help", false, "Show help for init command")
	initFlags.BoolVar(showHelp, "h", false, "Show help for init command")
	initFlags.Parse(os.Args[2:])

	if *showHelp {
		fmt.Println(`Usage: trellis init [options]

Create a new trellis.hjson configuration file in the current directory.

This command walks you through setting up a Trellis configuration with
interactive prompts. The generated file is fully commented to help you
understand and customize all available options.

Options:
  -h, -help    Show this help message

The command will ask about:
  - Project name (defaults to current directory name)
  - Server port (defaults to 1234)
  - Services to manage (name, command, watch binary)
  - Build workflow command
  - Log format (JSON or plain text)

Examples:
  trellis init              Create config with interactive prompts
  cd myproject && trellis init

After running init:
  1. Review and edit trellis.hjson as needed
  2. Run: ./trellis
  3. Open: http://localhost:1234`)
		return nil
	}

	configFile := "trellis.hjson"

	// Check if config file already exists
	if _, err := os.Stat(configFile); err == nil {
		return fmt.Errorf("%s already exists; remove it first or use a different directory", configFile)
	}

	reader := bufio.NewReader(os.Stdin)

	fmt.Println("Trellis Configuration Setup")
	fmt.Println("============================")
	fmt.Println()
	fmt.Println("This will create a trellis.hjson configuration file in the current directory.")
	fmt.Println("Press Enter to accept defaults shown in [brackets].")
	fmt.Println()

	// Get current directory name as default project name
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}
	defaultName := filepath.Base(cwd)

	// Question 1: Project name
	projectName := prompt(reader, "Project name", defaultName)

	// Question 2: Port
	portStr := prompt(reader, "Server port", "1234")
	port, err := strconv.Atoi(portStr)
	if err != nil {
		port = 1234
	}

	// Question 3: Services
	fmt.Println()
	fmt.Println("Services are long-running processes that Trellis manages (e.g., your backend server).")
	var services []serviceConfig
	for {
		addService := prompt(reader, "Add a service? (y/n)", "n")
		if strings.ToLower(addService) != "y" {
			break
		}
		svc := serviceConfig{}
		svc.Name = prompt(reader, "  Service name", "backend")
		svc.Command = prompt(reader, "  Command to run", "./bin/"+svc.Name)
		watchBinary := prompt(reader, "  Binary to watch for auto-restart (or empty to skip)", svc.Command)
		if watchBinary != "" {
			svc.WatchBinary = watchBinary
		}
		services = append(services, svc)
		fmt.Println()
	}

	// Question 4: Build workflow
	fmt.Println()
	fmt.Println("Workflows are commands you run frequently (e.g., build, test).")
	buildCommand := prompt(reader, "Build command (or empty to skip)", "make build")

	// Question 5: Log format
	fmt.Println()
	jsonLogs := prompt(reader, "Do your services output JSON logs? (y/n)", "y")
	useJSONLogs := strings.ToLower(jsonLogs) == "y"

	// Generate the config file
	configContent := generateConfig(projectName, port, services, buildCommand, useJSONLogs)

	// Write the file
	if err := os.WriteFile(configFile, []byte(configContent), 0644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	fmt.Println()
	fmt.Printf("Created %s\n", configFile)
	fmt.Println()
	fmt.Println("Next steps:")
	fmt.Println("  1. Review and edit trellis.hjson as needed")
	fmt.Println("  2. Run: ./trellis")
	fmt.Println("  3. Open: http://localhost:" + strconv.Itoa(port))
	fmt.Println()

	return nil
}

type serviceConfig struct {
	Name        string
	Command     string
	WatchBinary string
}

func prompt(reader *bufio.Reader, question, defaultVal string) string {
	if defaultVal != "" {
		fmt.Printf("%s [%s]: ", question, defaultVal)
	} else {
		fmt.Printf("%s: ", question)
	}
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(input)
	if input == "" {
		return defaultVal
	}
	return input
}

// escapeHJSONValue escapes a string for safe inclusion in an HJSON double-quoted value.
func escapeHJSONValue(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return s
}

func generateConfig(projectName string, port int, services []serviceConfig, buildCommand string, jsonLogs bool) string {
	var sb strings.Builder

	sb.WriteString(`{
  // =============================================================================
  // Trellis Configuration
  // =============================================================================
  //
  // This is an HJSON file (JSON with comments and relaxed syntax).
  // See https://trellis.dev/docs/reference/config/ for full documentation.
  //
  // Template variables available:
  //   {{.Worktree.Root}}     - Worktree root directory
  //   {{.Worktree.Branch}}   - Current branch name
  //   {{.Worktree.Binaries}} - Configured binary path
  //   {{.Worktree.Name}}     - Worktree directory name
  //   {{.Service.Name}}      - Current service name (in service context)

  // ---------------------------------------------------------------------------
  // Project Metadata
  // ---------------------------------------------------------------------------
  project: {
    // Display name for this project (shown in UI)
    name: "`)
	sb.WriteString(escapeHJSONValue(projectName))
	sb.WriteString(`"
  }

  // ---------------------------------------------------------------------------
  // Server Settings
  // ---------------------------------------------------------------------------
  server: {
    // Host to bind to (use "0.0.0.0" to allow remote access)
    host: "127.0.0.1"

    // Port for the web UI and API
    port: `)
	sb.WriteString(strconv.Itoa(port))
	sb.WriteString(`

    // For HTTPS, uncomment and set paths to your certificates:
    // tls_cert: "~/.trellis/cert.pem"
    // tls_key: "~/.trellis/key.pem"
  }

  // ---------------------------------------------------------------------------
  // Reverse Proxy
  // ---------------------------------------------------------------------------
  //
  // Mirror your production reverse proxy (Caddy, nginx) routing in development.
  // Each listener binds to an address and routes requests to backends by path.
  // WebSocket upgrades are handled automatically.
  //
  // proxy: [
  //   {
  //     listen: ":443"
  //
  //     // TLS via Tailscale (automatic certs from local daemon):
  //     // tls_tailscale: true
  //
  //     // Or TLS via certificate files:
  //     // tls_cert: "~/.trellis/cert.pem"
  //     // tls_key: "~/.trellis/key.pem"
  //
  //     routes: [
  //       { path_regexp: "^/api/.+", upstream: "localhost:3001" }
  //       { upstream: "localhost:3000" }  // Catch-all (no path_regexp)
  //     ]
  //   }
  // ]

  // ---------------------------------------------------------------------------
  // Worktree Configuration
  // ---------------------------------------------------------------------------
  //
  // Trellis treats each git worktree as an isolated environment with its own
  // services, terminals, and logs. This section configures worktree behavior.
  worktree: {
    // How to discover worktrees ("git" uses git worktree list)
    discovery: {
      mode: "git"
    }

    // Where compiled binaries are located (used by {{.Worktree.Binaries}})
    binaries: {
      path: "{{.Worktree.Root}}/bin"
    }

    // Lifecycle hooks run during worktree operations
    // lifecycle: {
    //   // Run once when a new worktree is created
    //   on_create: [
    //     { name: "setup", command: ["make", "setup"], timeout: "5m" }
    //   ]
    //   // Run before each worktree activation
    //   pre_activate: [
    //     { name: "build", command: ["make", "build"], timeout: "2m" }
    //   ]
    // }
  }

  // ---------------------------------------------------------------------------
  // Services
  // ---------------------------------------------------------------------------
  //
  // Services are long-running processes that Trellis manages. When a watched
  // binary changes, Trellis automatically restarts the service.
  services: [
`)

	if len(services) == 0 {
		sb.WriteString(`    // Example service configuration:
    // {
    //   name: "backend"              // Unique service name
    //   command: "./bin/backend"     // Command to run (string or array)
    //   // command: ["./bin/backend", "-port", "8080"]  // Alternative array form
    //
    //   // Working directory (default: worktree root)
    //   // work_dir: "{{.Worktree.Root}}/services/backend"
    //
    //   // Environment variables
    //   // env: {
    //   //   DB_HOST: "localhost"
    //   //   DEBUG: "true"
    //   // }
    //
    //   // Binary to watch - service restarts when this file changes
    //   watch_binary: "{{.Worktree.Binaries}}/backend"
    //
    //   // Additional files to watch (configs, etc.)
    //   // watch_files: ["config/backend.yaml"]
    //
    //   // Restart policy: "always", "on-failure", or "never"
    //   // restart_policy: "on-failure"
    //   // max_restarts: 3
    //   // restart_delay: "1s"
    //
    //   // Graceful shutdown settings
    //   // stop_signal: "SIGTERM"
    //   // stop_timeout: "10s"
    // }
`)
	} else {
		for i, svc := range services {
			sb.WriteString(`    {
      name: "`)
			sb.WriteString(escapeHJSONValue(svc.Name))
			sb.WriteString(`"
      command: "`)
			sb.WriteString(escapeHJSONValue(svc.Command))
			sb.WriteString(`"
`)
			if svc.WatchBinary != "" {
				sb.WriteString(`      watch_binary: "`)
				sb.WriteString(escapeHJSONValue(svc.WatchBinary))
				sb.WriteString(`"
`)
			}
			sb.WriteString(`
      // Uncomment to add environment variables:
      // env: {
      //   DEBUG: "true"
      // }

      // Uncomment to set restart policy:
      // restart_policy: "on-failure"
      // max_restarts: 3
    }`)
			if i < len(services)-1 {
				sb.WriteString(`
`)
			}
		}
		sb.WriteString(`
`)
	}

	sb.WriteString(`  ]

  // ---------------------------------------------------------------------------
  // Workflows
  // ---------------------------------------------------------------------------
  //
  // Workflows are commands you run frequently. They can be triggered from the
  // UI (Cmd/Ctrl + /) or via trellis-ctl.
  //
  // Workflows can have input parameters that are validated before execution.
  // Use 'trellis-ctl workflow describe <id>' to see a workflow's inputs.
  workflows: [
`)

	if buildCommand == "" {
		sb.WriteString(`    // Example workflow configuration:
    // {
    //   id: "build"                   // Unique identifier (used in CLI)
    //   name: "Build"                 // Display name
    //   description: "Build the project"  // Shown in trellis-ctl workflow list
    //   command: ["make", "build"]    // Command to run
    //
    //   // For multiple sequential commands:
    //   // commands: [
    //   //   ["make", "clean"],
    //   //   ["make", "build"]
    //   // ]
    //
    //   // timeout: "10m"             // Maximum run time
    //   // output_parser: "go"        // Parse output: "go", "go_test_json", "generic", "html", "none"
    //   // restart_services: true     // Restart watched services after completion
    // }
    //
    // Example workflow with validated inputs (for CLI/automation):
    // {
    //   id: "db-fetch"
    //   name: "Fetch DB Record"
    //   description: "Fetch a database record by table and ID"
    //   command: ["ssh", "prod-server", "dbfetch", "{{ .Inputs.table }}", "{{ .Inputs.id }}"]
    //   inputs: [
    //     {
    //       name: "table"
    //       type: "text"
    //       description: "Database table name"
    //       allowed_values: ["users", "groups", "messages"]  // Only these tables allowed
    //       required: true
    //     }
    //     {
    //       name: "id"
    //       type: "text"
    //       description: "Record ID"
    //       pattern: "^[0-9]+$"        // Only numeric IDs allowed
    //       required: true
    //     }
    //   ]
    // }
`)
	} else {
		// Parse the build command into array form
		cmdParts := strings.Fields(buildCommand)
		escapedParts := make([]string, len(cmdParts))
		for i, p := range cmdParts {
			escapedParts[i] = escapeHJSONValue(p)
		}
		cmdJSON := `["` + strings.Join(escapedParts, `", "`) + `"]`

		sb.WriteString(`    {
      id: "build"
      name: "Build"
      description: "Build the project"
      command: `)
		sb.WriteString(cmdJSON)
		sb.WriteString(`

      // Uncomment to customize:
      // timeout: "10m"
      // output_parser: "go"
      // restart_services: true
    }

    // Add more workflows as needed:
    // {
    //   id: "test"
    //   name: "Test"
    //   description: "Run the test suite"
    //   command: ["make", "test"]
    //   output_parser: "go_test_json"
    // }
    //
    // Example workflow with validated inputs (for CLI/automation):
    // {
    //   id: "db-fetch"
    //   name: "Fetch DB Record"
    //   description: "Fetch a database record by table and ID"
    //   command: ["ssh", "prod-server", "dbfetch", "{{ .Inputs.table }}", "{{ .Inputs.id }}"]
    //   inputs: [
    //     {
    //       name: "table"
    //       type: "text"
    //       description: "Database table name"
    //       allowed_values: ["users", "groups", "messages"]  // Only these tables allowed
    //       required: true
    //     }
    //     {
    //       name: "id"
    //       type: "text"
    //       description: "Record ID"
    //       pattern: "^[0-9]+$"        // Only numeric IDs allowed
    //       required: true
    //     }
    //   ]
    // }
`)
	}

	sb.WriteString(`  ]

  // ---------------------------------------------------------------------------
  // Terminal Settings
  // ---------------------------------------------------------------------------
  //
  // Trellis uses tmux for terminal management. Terminals are created on demand
  // from the worktree home page.
  terminal: {
    // tmux configuration
    tmux: {
      history_limit: 50000
      // shell: "/bin/zsh"  // Override default shell
    }

    // Remote terminal connections (SSH to production, etc.)
    // remote_windows: [
    //   {
    //     name: "prod"
    //     ssh_host: "prod.example.com"
    //     tmux_session: "main"
    //   }
    // ]

    // Keyboard shortcuts to jump to specific terminals
    // shortcuts: [
    //   { key: "cmd+1", window: "#backend" }     // Service terminal
    //   { key: "cmd+2", window: "~nginx-logs" }  // Log viewer
    //   { key: "cmd+3", window: "!prod" }        // Remote terminal
    // ]
  }

  // ---------------------------------------------------------------------------
  // Log Viewing
  // ---------------------------------------------------------------------------
  //
  // Service logs are captured automatically. Use log_viewers to view external
  // log sources (SSH, Docker, Kubernetes).
  //
  // log_viewers: [
  //   {
  //     name: "nginx"
  //     source: {
  //       type: "ssh"
  //       host: "web.example.com"
  //       path: "/var/log/nginx"
  //       current: "access.log"
  //     }
  //     parser: {
  //       type: "json"
  //       timestamp: "time"
  //       level: "status"
  //       message: "request"
  //     }
  //   }
  // ]

`)

	if jsonLogs {
		sb.WriteString(`  // ---------------------------------------------------------------------------
  // Default Log Parsing
  // ---------------------------------------------------------------------------
  //
  // These defaults apply to all services and log viewers that don't specify
  // their own parser configuration.
  logging_defaults: {
    parser: {
      type: "json"

      // Field names in your JSON logs (adjust to match your log format)
      timestamp: "ts"       // or "time", "timestamp", "@timestamp"
      level: "level"        // or "severity", "lvl"
      message: "msg"        // or "message", "log"
      id: "request_id"      // for distributed tracing
      stack: "stack"        // for stack traces in crash reports
      // file: "source"    // source file path (enables "Open in Editor")
      // line: "lineno"    // source line number
    }

    // Derived fields computed from parsed fields
    // derive: {
    //   short_time: { from: "timestamp", op: "timefmt", args: { format: "15:04:05" } }
    // }

    // Column layout for log display
    // layout: [
    //   { field: "short_time", min_width: 8 }
    //   { field: "level", min_width: 5 }
    //   { field: "message", max_width: 0 }
    // ]
  }

`)
	} else {
		sb.WriteString(`  // ---------------------------------------------------------------------------
  // Default Log Parsing
  // ---------------------------------------------------------------------------
  //
  // Uncomment and configure if your services output structured logs.
  //
  // logging_defaults: {
  //   parser: {
  //     type: "json"         // or "logfmt", "regex", "syslog", "none"
  //     timestamp: "ts"
  //     level: "level"
  //     message: "msg"
  //     id: "request_id"
  //     stack: "stack"
  //     // file: "source"    // source file path (enables "Open in Editor")
  //     // line: "lineno"    // source line number
  //   }
  // }

`)
	}

	sb.WriteString(`  // ---------------------------------------------------------------------------
  // Crash Reports
  // ---------------------------------------------------------------------------
  //
  // When services crash, Trellis captures logs, exit codes, and stack traces.
  crashes: {
    // Where to store crash reports
    reports_dir: ".trellis/crashes"

    // How long to keep crash reports
    max_age: "7d"
    max_count: 100
  }

  // ---------------------------------------------------------------------------
  // Binary Watching
  // ---------------------------------------------------------------------------
  //
  // Controls how Trellis detects binary changes for auto-restart.
  watch: {
    // Wait for rapid changes to settle before restarting
    debounce: "200ms"
  }

  // ---------------------------------------------------------------------------
  // UI Settings
  // ---------------------------------------------------------------------------
  //
  // ui: {
  //   theme: "auto"  // "light", "dark", or "auto"
  //
  //   terminal: {
  //     font_family: "Monaco, monospace"
  //     font_size: 14
  //   }
  //
  //   notifications: {
  //     enabled: true
  //     events: ["service.crashed", "workflow.finished"]
  //   }
  // }

  // ---------------------------------------------------------------------------
  // Distributed Tracing
  // ---------------------------------------------------------------------------
  //
  // Group log viewers together to search across multiple sources with a
  // single trace ID.
  //
  // trace_groups: [
  //   {
  //     name: "api-flow"
  //     log_viewers: ["nginx", "api", "database"]
  //   }
  // ]
}
`)

	return sb.String()
}
