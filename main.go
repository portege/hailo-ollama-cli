package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"hailo-ollama-cli/cli"
	"hailo-ollama-cli/client"
)

func printGlobalUsage() {
	fmt.Println("Hailo Ollama CLI - Command line interface for the Hailo-Ollama REST API server")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  hailo-ollama [flags] <command> [args]")
	fmt.Println()
	fmt.Println("Flags:")
	fmt.Println("  -H, --host string      API server host (default: http://localhost:8000)")
	fmt.Println("  -m, --mock             Enable built-in mock server (useful for testing without hardware)")
	fmt.Println("  -v, --verbose          Display detailed inference timing statistics")
	fmt.Println("  -f, --metrics          Show performance metrics footer after response")
	fmt.Println("  -h, --help             Show help information")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  run <model> [prompt]  Run interactive chat or query a model")
	fmt.Println("  list / ls             List installed models")
	fmt.Println("  list-remote           List downloadable models from Hailo Model Zoo")
	fmt.Println("  pull <model>          Pull/download a model")
	fmt.Println("  rm / delete <model>   Remove a model")
	fmt.Println("  ps                    List currently running models")
	fmt.Println("  show <model>          Show detailed metadata for a model")
	fmt.Println("  monitor               Live NPU utilization view (wraps hailortcli monitor)")
	fmt.Println("  webui [addr] [metrics.jsonl]")
	fmt.Println("                        Start browser chat UI + live NPU panel (default addr: :8080).")
	fmt.Println("  serve                 Start server service (with --mock, runs a local mock server)")
	fmt.Println()
	fmt.Println("Environment Variables:")
	fmt.Println("  OLLAMA_HOST, HAILO_OLLAMA_HOST  Override the target server host")
	fmt.Println("  HAILO_OLLAMA_MOCK               Enable mock mode if set to 'true'")
	fmt.Println("  HAILO_OLLAMA_DB                 Path of the webui chat SQLite database")
	fmt.Println("                                  (default: <user cache dir>/hailo-ollama/chats.db)")
}

func main() {
	var hostFlag string
	var mockFlag bool
	var verboseFlag bool
	var metricsFlag bool
	var helpFlag bool
	var filteredArgs []string

	// Custom command line parsing to support flags in any order
	for i := 1; i < len(os.Args); i++ {
		arg := os.Args[i]
		if arg == "--mock" || arg == "-m" {
			mockFlag = true
		} else if arg == "--verbose" || arg == "-v" {
			verboseFlag = true
		} else if arg == "--metrics" || arg == "-f" {
			metricsFlag = true
		} else if arg == "--help" || arg == "-h" {
			helpFlag = true
		} else if arg == "--host" || arg == "-H" {
			if i+1 < len(os.Args) {
				hostFlag = os.Args[i+1]
				i++ // skip next arg
			} else {
				fmt.Println("Error: --host flag requires a value")
				os.Exit(1)
			}
		} else if strings.HasPrefix(arg, "--host=") {
			hostFlag = strings.TrimPrefix(arg, "--host=")
		} else if strings.HasPrefix(arg, "-H=") {
			hostFlag = strings.TrimPrefix(arg, "-H=")
		} else {
			filteredArgs = append(filteredArgs, arg)
		}
	}

	if helpFlag {
		printGlobalUsage()
		return
	}

	// Resolve host address
	host := hostFlag
	if host == "" {
		if h := os.Getenv("OLLAMA_HOST"); h != "" {
			host = h
		} else if h := os.Getenv("HAILO_OLLAMA_HOST"); h != "" {
			host = h
		}
	}
	if host == "" {
		host = "http://localhost:8000"
	}
	// Add http:// prefix if missing
	if !strings.HasPrefix(host, "http://") && !strings.HasPrefix(host, "https://") {
		host = "http://" + host
	}

	// Resolve mock mode
	if !mockFlag {
		if os.Getenv("HAILO_OLLAMA_MOCK") == "true" {
			mockFlag = true
		}
	}

	if len(filteredArgs) == 0 {
		printGlobalUsage()
		return
	}

	cmd := filteredArgs[0]
	cmdArgs := filteredArgs[1:]

	ctx := context.Background()

	// Handle "serve" subcommand separately if mock mode is on
	if cmd == "serve" {
		if mockFlag {
			// Extract port from host if present, e.g. "localhost:8000" -> "8000"
			port := "8000"
			if idx := strings.LastIndex(host, ":"); idx != -1 {
				port = host[idx+1:]
			}
			addr, cleanup, err := client.StartMockServer(port)
			if err != nil {
				fmt.Printf("Error starting mock server: %v\n", err)
				os.Exit(1)
			}
			defer cleanup()
			fmt.Printf("Mock Hailo-Ollama API Server is running at: %s\n", addr)
			fmt.Println("Press Ctrl+C to stop.")
			select {} // block forever
		} else {
			fmt.Println("hailo-ollama serve:")
			fmt.Println("  The Hailo NPU service is a compiled C++ API daemon.")
			fmt.Println("  On Raspberry Pi OS, it is managed via systemd.")
			fmt.Println("  To start it, use:")
			fmt.Println("    sudo systemctl start hailo-ollama")
			fmt.Println()
			fmt.Println("  To run a local simulated mock server for testing, run:")
			fmt.Println("    hailo-ollama serve --mock")
			return
		}
	}

	// If mock mode is enabled for a standard command, start ephemeral mock server
	var cleanup func()
	if mockFlag {
		mockAddr, cl, err := client.StartMockServer("")
		if err != nil {
			fmt.Printf("Failed to launch built-in mock server: %v\n", err)
			os.Exit(1)
		}
		cleanup = cl
		host = mockAddr
	}
	if cleanup != nil {
		defer cleanup()
	}

	apiCli := client.NewClient(host)

	// Route subcommands
	var err error
	switch cmd {
	case "list", "ls":
		err = cli.ListModels(ctx, apiCli)

	case "list-remote":
		err = cli.ListRemoteModels(ctx, apiCli)

	case "ps":
		err = cli.ListRunningModels(ctx, apiCli)

	case "show":
		if len(cmdArgs) < 1 {
			fmt.Println("Error: 'show' command requires a model name. Example: 'hailo-ollama show llama3.2'")
			os.Exit(1)
		}
		err = cli.ShowModelInfo(ctx, apiCli, cmdArgs[0])

	case "monitor":
		err = cli.RunMonitor(ctx, mockFlag)

	case "webui":
		addr := ":8080"
		if len(cmdArgs) > 0 {
			addr = cmdArgs[0]
		}
		metricsPath := ""
		if len(cmdArgs) > 1 {
			metricsPath = cmdArgs[1]
		}
		err = cli.RunWebUI(ctx, apiCli, addr, metricsPath)

	case "rm", "delete":
		if len(cmdArgs) < 1 {
			fmt.Println("Error: delete command requires a model name. Example: 'hailo-ollama rm llama3.2'")
			os.Exit(1)
		}
		err = cli.DeleteModel(ctx, apiCli, cmdArgs[0])

	case "pull":
		if len(cmdArgs) < 1 {
			fmt.Println("Error: 'pull' command requires a model name. Example: 'hailo-ollama pull llama3.2'")
			os.Exit(1)
		}
		err = cli.PullModel(ctx, apiCli, cmdArgs[0])

	case "run":
		if len(cmdArgs) < 1 {
			fmt.Println("Error: 'run' command requires a model name. Example: 'hailo-ollama run llama3.2'")
			os.Exit(1)
		}
		modelName := cmdArgs[0]

		if len(cmdArgs) > 1 {
			// Single query mode: hailo-ollama run model "prompt text"
			prompt := strings.Join(cmdArgs[1:], " ")
			req := client.GenerateRequest{
				Model:  modelName,
				Prompt: prompt,
			}
			var lastChunk client.GenerateResponseChunk
			var firstByteTime time.Duration
			startTime := time.Now()
			hasReceivedFirstByte := false
			var responseLength int

			err = apiCli.GenerateStream(ctx, req, func(chunk client.GenerateResponseChunk) {
				if chunk.Response != "" {
					if !hasReceivedFirstByte {
						firstByteTime = time.Since(startTime)
						hasReceivedFirstByte = true
					}
					fmt.Print(chunk.Response)
					responseLength += len(chunk.Response)
					os.Stdout.Sync()
				}
				if chunk.Done {
					lastChunk = chunk
				}
			})
			if err == nil {
				fmt.Println() // final newline
				totalTime := time.Since(startTime)

				// Print Footer
				if metricsFlag {
					fmt.Println("\n---")
					fmt.Printf("Token response size:   %d tokens (%d bytes)\n", lastChunk.EvalCount, responseLength)
					if hasReceivedFirstByte {
						fmt.Printf("First byte response:   %s\n", firstByteTime.Round(time.Millisecond))
					} else {
						fmt.Printf("First byte response:   -\n")
					}
					fmt.Printf("Completion time:       %s\n", totalTime.Round(time.Millisecond))
				}

				if verboseFlag && lastChunk.Done {
					promptEvalSec := float64(lastChunk.PromptEvalDuration) / float64(time.Second)
					evalSec := float64(lastChunk.EvalDuration) / float64(time.Second)

					var promptRate, evalRate float64
					if promptEvalSec > 0 {
						promptRate = float64(lastChunk.PromptEvalCount) / promptEvalSec
					}
					if evalSec > 0 {
						evalRate = float64(lastChunk.EvalCount) / evalSec
					}

					fmt.Println()
					fmt.Printf("total duration:       %s\n", time.Duration(lastChunk.TotalDuration))
					fmt.Printf("load duration:        %s\n", time.Duration(lastChunk.LoadDuration))
					if lastChunk.PromptEvalCount > 0 {
						fmt.Printf("prompt eval count:    %d token(s)\n", lastChunk.PromptEvalCount)
						fmt.Printf("prompt eval duration: %s\n", time.Duration(lastChunk.PromptEvalDuration))
						fmt.Printf("prompt eval rate:     %.2f tokens/s\n", promptRate)
					}
					if lastChunk.EvalCount > 0 {
						fmt.Printf("eval count:           %d token(s)\n", lastChunk.EvalCount)
						fmt.Printf("eval duration:        %s\n", time.Duration(lastChunk.EvalDuration))
						fmt.Printf("eval rate:            %.2f tokens/s\n", evalRate)
					}
				}
			}
		} else {
			// Interactive mode
			err = cli.RunInteractive(ctx, apiCli, modelName, verboseFlag, metricsFlag)
		}

	default:
		fmt.Printf("Error: Unknown command '%s'\n", cmd)
		fmt.Println("Run 'hailo-ollama --help' for usage details.")
		os.Exit(1)
	}

	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
}
