# Hailo Ollama CLI

A command-line interface and web UI for the Hailo-Ollama REST API server. It lets you chat with local models, list and pull models, check running processes, view NPU utilization, and run a browser-based chat interface.

## Features

- **Chat**: Interactive terminal chat or single-shot prompts (`run`)
- **Model management**: list, pull, show metadata, and remove local models
- **Process view**: list currently loaded/running models
- **NPU monitor**: live NPU utilization via `hailortcli monitor`, with a simulated fallback in mock mode
- **Web UI**: ChatGPT-style browser chat with streamed token responses (`webui`)
- **Mock mode**: test the CLI without real Hailo hardware

## Requirements

- Go 1.22+ (the CLI embeds web assets; Go 1.16+ supports `//go:embed`)
- A running Hailo-Ollama REST API server at `http://localhost:8000`
- For `monitor`: the [HailoRT SDK](https://hailo.ai/developer-zone/) with `hailortcli` on your `PATH`

## Build

### Local binary

```bash
make build
```

The binary is placed in `dist/hailo-ollama-cli-<os>-<arch>`.

### Run directly from source

```bash
make run
# or
make run -- <flags>
```

### Cross-compile all targets

```bash
make dist
```

This produces binaries for:

- `linux/arm64`
- `linux/amd64`
- `darwin/arm64`
- `darwin/amd64`

### Install to `/usr/local/bin`

```bash
make install
```

Uninstall later with:

```bash
make uninstall
```

### Build without Make

```bash
go build -o hailo-ollama-cli .
```

## Run

### Global flags

| Flag            | Description                                                   |
| --------------- | ------------------------------------------------------------- |
| `-H, --host`    | API server host (default: `http://localhost:8000`)            |
| `-m, --mock`    | Enable built-in mock server (useful for testing)              |
| `-v, --verbose` | Display detailed inference timing statistics                  |
| `-f, --metrics` | Show performance metrics footer after a response              |
| `-h, --help`    | Show help                                                     |

Environment variables `OLLAMA_HOST` and `HAILO_OLLAMA_HOST` override the default host. Set `HAILO_OLLAMA_MOCK=true` to enable mock mode without the flag.

### Commands

```bash
# List installed models
hailo-ollama list

# List downloadable models
hailo-ollama list-remote

# Pull a model
hailo-ollama pull llama3.2:3b

# Single prompt
hailo-ollama run llama3.2:3b "Explain quantum computing"

# Interactive terminal chat
hailo-ollama run llama3.2:3b

# Show model metadata
hailo-ollama show llama3.2:3b

# Show loaded models
hailo-ollama ps

# Remove a model
hailo-ollama rm llama3.2:3b

# Live NPU monitor (requires hailortcli)
hailo-ollama monitor

# Browser chat UI (default: http://localhost:8080)
hailo-ollama webui
hailo-ollama webui :9000

# Start a local mock API server
hailo-ollama serve --mock
```

### System instructions and reasoning/thinking

Both the interactive terminal chat and the web UI support an optional **system instruction**.

- **Terminal**: use `/set system <prompt>` to set a system instruction (see `/help`).
- **Web UI**: the composer has a "System instruction (optional)" field above the prompt.

Reasoning/thinking output is supported for **thinking-capable models** (the Qwen and DeepSeek families) that report a separate `thinking` stream:

- **Terminal**: toggle with `/set think`, then a dimmed reasoning trace is printed after each answer.
- **Web UI**: enable the **Thinking** toggle in the composer; reasoning is shown in a collapsible "Reasoning" panel under each assistant message.

Llama models support system instructions but do not emit a separate reasoning stream, so the thinking trace/panel only appears when the selected model reports one.

## Testing with mock mode

Run most commands without a real Hailo NPU by adding `--mock`:

```bash
hailo-ollama --mock list
hailo-ollama --mock run llama3.2:3b "Hello!"
hailo-ollama --mock webui
hailo-ollama --mock monitor
```

## Development

```bash
# Run tests
make test

# Run vet
make vet

# Clean build artifacts
make clean
```

## Project structure

```
.
├── cli/              # Terminal UI, web UI, and monitor command
│   ├── cli.go
│   ├── interactive.go
│   ├── monitor.go
│   ├── web.go
│   └── static/       # Embedded web chat assets
├── client/           # HTTP client for the Hailo-Ollama REST API
│   ├── client.go
│   ├── mock.go
│   └── client_test.go
├── main.go           # Entry point and command routing
├── Makefile          # Build automation
└── README.md
```

## License

MIT
