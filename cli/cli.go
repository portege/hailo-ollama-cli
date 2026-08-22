package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"hailo-ollama-cli/client"
)

// Helper to format bytes to human readable string (KB, MB, GB, etc.)
func formatSize(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

// Helper to print a relative time string (e.g. "2 hours ago" or "5 minutes from now")
func formatRelativeTime(t time.Time) string {
	now := time.Now()
	diff := now.Sub(t)

	isFuture := diff < 0
	if isFuture {
		diff = -diff
	}

	var res string
	switch {
	case diff < time.Minute:
		res = "just now"
	case diff < time.Hour:
		res = fmt.Sprintf("%d minutes", int(diff.Minutes()))
	case diff < 24*time.Hour:
		res = fmt.Sprintf("%d hours", int(diff.Hours()))
	default:
		res = fmt.Sprintf("%d days", int(diff.Hours()/24))
	}

	if isFuture {
		return res + " from now"
	}
	if res == "just now" {
		return res
	}
	return res + " ago"
}

// Helper to extract short ID (digest) from model digest
func formatDigest(digest string) string {
	if strings.HasPrefix(digest, "sha256:") {
		digest = strings.TrimPrefix(digest, "sha256:")
	}
	if len(digest) > 12 {
		return digest[:12]
	}
	return digest
}

// ListModels displays all models currently installed locally.
func ListModels(ctx context.Context, apiCli *client.Client) error {
	models, err := apiCli.ListLocalModels(ctx)
	if err != nil {
		return err
	}

	if len(models) == 0 {
		fmt.Println("No models installed. Pull a model using 'hailo-ollama pull <model>'.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 4, ' ', 0)
	fmt.Fprintln(w, "NAME\tID\tSIZE\tMODIFIED")

	for _, m := range models {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
			m.Name,
			formatDigest(m.Digest),
			formatSize(m.Size),
			formatRelativeTime(m.ModifiedAt),
		)
	}
	w.Flush()
	return nil
}

// ListRemoteModels displays models available in Hailo's Model Zoo.
func ListRemoteModels(ctx context.Context, apiCli *client.Client) error {
	models, err := apiCli.ListRemoteModels(ctx)
	if err != nil {
		return err
	}

	if len(models) == 0 {
		fmt.Println("No remote models reported by Hailo Model Zoo.")
		return nil
	}

	fmt.Println("Remote models available in Hailo Model Zoo (use 'hailo-ollama pull <model>' to download):")
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 4, ' ', 0)
	fmt.Fprintln(w, "MODEL NAME")
	for _, name := range models {
		fmt.Fprintf(w, "%s\n", name)
	}
	w.Flush()
	return nil
}

// ListRunningModels shows models currently loaded into memory.
func ListRunningModels(ctx context.Context, apiCli *client.Client) error {
	models, err := apiCli.ListRunningModels(ctx)
	if err != nil {
		return err
	}

	if len(models) == 0 {
		fmt.Println("No models currently loaded in memory.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 4, ' ', 0)
	fmt.Fprintln(w, "NAME\tID\tSIZE\tPROCESSOR\tEXPIRES AT")

	for _, m := range models {
		expiresStr := formatRelativeTime(m.ExpiresAt)
		// Hailo NPU is assumed because it runs on the Hailo Ollama server
		fmt.Fprintf(w, "%s\t%s\t%s\tHailo NPU\t%s\n",
			m.Name,
			formatDigest(m.Digest),
			formatSize(m.Size),
			expiresStr,
		)
	}
	w.Flush()
	return nil
}

// ShowModelInfo displays metadata of a local model.
func ShowModelInfo(ctx context.Context, apiCli *client.Client, name string) error {
	info, err := apiCli.ShowModel(ctx, name)
	if err != nil {
		return err
	}

	if info.License != "" {
		fmt.Printf("License:\n%s\n\n", info.License)
	}
	if info.Template != "" {
		fmt.Printf("Template:\n%s\n\n", info.Template)
	}
	if info.Parameters != "" {
		fmt.Printf("Parameters:\n%s\n\n", info.Parameters)
	}
	if info.Modelfile != "" {
		fmt.Printf("Modelfile:\n%s\n\n", info.Modelfile)
	}

	if len(info.Details.Families) > 0 {
		fmt.Println("Details:")
		fmt.Printf("  Family:             %s\n", info.Details.Family)
		fmt.Printf("  Families:           %s\n", strings.Join(info.Details.Families, ", "))
		fmt.Printf("  Parameter Size:     %s\n", info.Details.ParameterSize)
		fmt.Printf("  Quantization Level: %s\n", info.Details.QuantizationLevel)
	}
	return nil
}

// DeleteModel removes a model from the Hailo-Ollama local store.
func DeleteModel(ctx context.Context, apiCli *client.Client, name string) error {
	err := apiCli.DeleteModel(ctx, name)
	if err != nil {
		return err
	}
	fmt.Printf("Deleted model '%s'\n", name)
	return nil
}

// PullModel pulls a new model and displays a progress bar.
func PullModel(ctx context.Context, apiCli *client.Client, name string) error {
	var lastLineLength int

	fmt.Printf("Pulling model %s...\n", name)

	err := apiCli.PullModel(ctx, name, func(chunk client.PullResponseChunk) {
		// Clear previous line
		if lastLineLength > 0 {
			fmt.Print("\r" + strings.Repeat(" ", lastLineLength) + "\r")
		}

		lineStr := ""
		if chunk.Total > 0 {
			percent := float64(chunk.Completed) * 100 / float64(chunk.Total)
			compStr := formatSize(chunk.Completed)
			totalStr := formatSize(chunk.Total)

			// Progress bar calculation
			barLen := 20
			filledLen := int(percent / 100.0 * float64(barLen))
			if filledLen > barLen {
				filledLen = barLen
			}
			barStr := strings.Repeat("=", filledLen)
			if filledLen < barLen {
				barStr += ">" + strings.Repeat(" ", barLen-filledLen-1)
			}

			lineStr = fmt.Sprintf("%s [%s] %.1f%% (%s/%s)", chunk.Status, barStr, percent, compStr, totalStr)
		} else {
			lineStr = chunk.Status
		}

		fmt.Print(lineStr)
		os.Stdout.Sync()
		lastLineLength = len(lineStr)
	})

	// Erase final progress line and print success
	if lastLineLength > 0 {
		fmt.Print("\r" + strings.Repeat(" ", lastLineLength) + "\r")
	}

	if err != nil {
		return err
	}

	fmt.Printf("Successfully pulled model '%s'\n", name)
	return nil
}
