package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"hailo-ollama-cli/client"
	"github.com/peterh/liner"
)

// RunInteractive starts the interactive chat loop with the model.
func RunInteractive(ctx context.Context, apiCli *client.Client, model string, initialVerbose bool, initialMetrics bool) error {
	line := liner.NewLiner()
	defer line.Close()

	line.SetCtrlCAborts(false) // Handle Ctrl+C manually to clear line or cancel stream

	// Set autocompletion for slash commands
	line.SetCompleter(func(inputLine string) []string {
		if strings.HasPrefix(inputLine, "/") {
			cmds := []string{"/bye", "/exit", "/clear", "/help", "/set system", "/set parameter", "/show"}
			var matched []string
			for _, c := range cmds {
				if strings.HasPrefix(c, inputLine) {
					matched = append(matched, c)
				}
			}
			return matched
		}
		return nil
	})

	// Load command history
	homeDir, err := os.UserHomeDir()
	historyFile := ""
	if err == nil {
		historyFile = filepath.Join(homeDir, ".hailo_ollama_history")
		if f, err := os.Open(historyFile); err == nil {
			line.ReadHistory(f)
			f.Close()
		}
	}

	// State variables
	var chatHistory []client.ChatMessage
	var systemPrompt string
	options := make(map[string]any)
	verbose := initialVerbose
	metrics := initialMetrics

	fmt.Printf(">>> Send a message to %s (type /help for commands)\n", model)

	multiline := false
	var multilineBuffer []string

	// Channel for interrupting streams
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGINT)
	defer signal.Stop(sigChan)

	for {
		promptText := ">>> "
		if multiline {
			promptText = "... "
		}

		input, err := line.Prompt(promptText)
		if err != nil {
			if errors.Is(err, liner.ErrPromptAborted) {
				// Ctrl+C during prompt: clear the line and print a newline
				fmt.Println()
				multiline = false
				multilineBuffer = nil
				continue
			}
			if errors.Is(err, io.EOF) {
				// Ctrl+D: exit gracefully
				fmt.Println("\nBye!")
				break
			}
			return fmt.Errorf("prompt error: %w", err)
		}

		// Handle history persistence
		trimmedInput := strings.TrimSpace(input)
		if trimmedInput != "" && !multiline {
			line.AppendHistory(input)
			if historyFile != "" {
				if f, err := os.Create(historyFile); err == nil {
					line.WriteHistory(f)
					f.Close()
				}
			}
		}

		// Check for multiline delimiters
		if multiline {
			if strings.HasSuffix(trimmedInput, `"""`) {
				multiline = false
				cleaned := strings.TrimSuffix(trimmedInput, `"""`)
				multilineBuffer = append(multilineBuffer, cleaned)
				fullPrompt := strings.Join(multilineBuffer, "\n")
				multilineBuffer = nil
				if strings.TrimSpace(fullPrompt) != "" {
					chatHistory, err = processPrompt(ctx, apiCli, model, fullPrompt, chatHistory, systemPrompt, options, verbose, sigChan)
					if err != nil && !errors.Is(err, context.Canceled) {
						fmt.Printf("Error: %v\n", err)
					}
				}
			} else {
				multilineBuffer = append(multilineBuffer, input)
			}
			continue
		} else {
			if strings.HasPrefix(trimmedInput, `"""`) {
				multiline = true
				cleaned := strings.TrimPrefix(trimmedInput, `"""`)
				multilineBuffer = []string{cleaned}
				continue
			}
		}

		// Skip empty inputs
		if trimmedInput == "" {
			continue
		}

		// Handle slash commands
		if strings.HasPrefix(trimmedInput, "/") {
			parts := strings.Fields(trimmedInput)
			cmd := parts[0]

			switch cmd {
			case "/bye", "/exit":
				fmt.Println("Bye!")
				return nil
			case "/clear":
				chatHistory = nil
				fmt.Println("Cleared chat history.")
			case "/show":
				if len(parts) < 2 {
					fmt.Println("Showing current model info:")
					info, err := apiCli.ShowModel(ctx, model)
					if err != nil {
						fmt.Printf("Error showing model: %v\n", err)
					} else {
						if info.License != "" {
							fmt.Printf("License: %s\n", info.License)
						}
						if info.Template != "" {
							fmt.Printf("Template: %s\n", info.Template)
						}
						if info.Modelfile != "" {
							fmt.Printf("Modelfile:\n%s\n", info.Modelfile)
						}
					}
				} else {
					target := parts[1]
					info, err := apiCli.ShowModel(ctx, target)
					if err != nil {
						fmt.Printf("Error: %v\n", err)
					} else {
						fmt.Printf("Model Info for %s:\n%+v\n", target, info)
					}
				}
			case "/set":
				if len(parts) < 3 {
					fmt.Println("Usage: \n  /set system <prompt>\n  /set parameter <key> <value>\n  /set verbose\n  /set metrics")
					continue
				}
				subCmd := parts[1]
				switch subCmd {
				case "system":
					systemPrompt = strings.Join(parts[2:], " ")
					fmt.Println("System prompt updated.")
				case "verbose":
					verbose = !verbose
					fmt.Printf("Verbose stats: %t\n", verbose)
				case "metrics":
					metrics = !metrics
					fmt.Printf("Metrics footer: %t\n", metrics)
				case "parameter":
					if len(parts) < 4 {
						fmt.Println("Usage: /set parameter <key> <value>")
						continue
					}
					key := parts[2]
					valStr := parts[3]
					// parse float or int or bool
					if b, err := strconv.ParseBool(valStr); err == nil {
						options[key] = b
					} else if i, err := strconv.ParseInt(valStr, 10, 64); err == nil {
						options[key] = i
					} else if f, err := strconv.ParseFloat(valStr, 64); err == nil {
						options[key] = f
					} else {
						options[key] = valStr
					}
					fmt.Printf("Parameter %s set to %v\n", key, options[key])
				default:
					fmt.Printf("Unknown setting: %s\n", subCmd)
				}
			case "/help", "/?":
				printHelp()
			default:
				fmt.Printf("Unknown command: %s. Type /help for assistance.\n", cmd)
			}
			continue
		}

		// Regular prompt
		var processErr error
		chatHistory, processErr = processPrompt(ctx, apiCli, model, trimmedInput, chatHistory, systemPrompt, options, verbose, metrics, sigChan)
		if processErr != nil {
			if errors.Is(processErr, context.Canceled) {
				fmt.Println("\n[Cancelled]")
			} else {
				fmt.Printf("Error: %v\n", processErr)
			}
		}
	}

	return nil
}

func printHelp() {
	fmt.Println("Available commands:")
	fmt.Println("  /bye, /exit          Exit the program")
	fmt.Println("  /clear               Reset the chat history")
	fmt.Println("  /show                Show details for the current model")
	fmt.Println("  /set system <p>      Set the system instruction")
	fmt.Println("  /set parameter <k> <v> Set a model parameter (e.g. temperature 0.7)")
	fmt.Println("  /set verbose         Toggle detailed timing statistics")
	fmt.Println("  /set metrics         Toggle performance metrics footer")
	fmt.Println("  /help, /?            Display this help menu")
	fmt.Println("  \"\"\"                  Start/end a multi-line input block")
}

func processPrompt(
	ctx context.Context,
	apiCli *client.Client,
	model string,
	prompt string,
	history []client.ChatMessage,
	system string,
	options map[string]any,
	verbose bool,
	metrics bool,
	sigChan chan os.Signal,
) ([]client.ChatMessage, error) {
	// Construct the chat request messages list
	var reqMessages []client.ChatMessage
	if system != "" {
		reqMessages = append(reqMessages, client.ChatMessage{Role: "system", Content: system})
	}
	reqMessages = append(reqMessages, history...)
	reqMessages = append(reqMessages, client.ChatMessage{Role: "user", Content: prompt})

	req := client.ChatRequest{
		Model:    model,
		Messages: reqMessages,
		Options:  options,
	}

	// Prepare cancellable context for streaming
	streamCtx, cancelStream := context.WithCancel(ctx)
	defer cancelStream()

	// Intercept SIGINT signals during generation to abort the request
	go func() {
		select {
		case <-sigChan:
			cancelStream()
		case <-streamCtx.Done():
			// normal return
		}
	}()

	var responseBuilder strings.Builder
	var lastChunk client.ChatResponseChunk
	var firstByteTime time.Duration
	startTime := time.Now()
	hasReceivedFirstByte := false

	// Start streaming
	err := apiCli.ChatStream(streamCtx, req, func(chunk client.ChatResponseChunk) {
		if chunk.Message.Content != "" {
			if !hasReceivedFirstByte {
				firstByteTime = time.Since(startTime)
				hasReceivedFirstByte = true
			}
			fmt.Print(chunk.Message.Content)
			responseBuilder.WriteString(chunk.Message.Content)
			// Flush standard output immediately for smooth typing effect
			os.Stdout.Sync()
		}
		if chunk.Done {
			lastChunk = chunk
		}
	})

	if err != nil {
		if errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "context canceled") {
			return history, context.Canceled
		}
		return history, err
	}

	totalTime := time.Since(startTime)
	fmt.Println() // trailing newline after stream finishes

	// Print Footer
	if metrics {
		fmt.Println("\n---")
		fmt.Printf("Token response size:   %d tokens (%d bytes)\n", lastChunk.EvalCount, responseBuilder.Len())
		if hasReceivedFirstByte {
			fmt.Printf("First byte response:   %s\n", firstByteTime.Round(time.Millisecond))
		} else {
			fmt.Printf("First byte response:   -\n")
		}
		fmt.Printf("Completion time:       %s\n", totalTime.Round(time.Millisecond))
	}

	// Print stats if requested
	if verbose && lastChunk.Done {
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
		fmt.Println()
	}

	// Update local chat history
	history = append(history, client.ChatMessage{Role: "user", Content: prompt})
	history = append(history, client.ChatMessage{Role: "assistant", Content: responseBuilder.String()})

	return history, nil
}
