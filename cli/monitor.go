package cli

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"
)

// mockMonitorDevice is a fake NPU device used to simulate hailortcli monitor output.
type mockMonitorDevice struct {
	id          string
	arch        string
	temperature float64
	power       float64
	utilization float64
}

// RunMonitor shows live NPU utilization, mirroring the `hailortcli monitor` tool.
// In mock mode it renders simulated stats instead of shelling out to the real binary.
func RunMonitor(ctx context.Context, mockFlag bool) error {
	if mockFlag {
		return runMockMonitor(ctx)
	}
	return runHailortMonitor(ctx)
}

// runHailortMonitor shells out to the HailoRT SDK's own monitoring tool and
// passes its interactive terminal UI straight through to the user.
func runHailortMonitor(ctx context.Context) error {
	binPath, err := exec.LookPath("hailortcli")
	if err != nil {
		return fmt.Errorf("'hailortcli' not found on PATH: install the HailoRT SDK, or run with --mock to preview the UI")
	}

	cmd := exec.CommandContext(ctx, binPath, "monitor")
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// runMockMonitor renders a fake, periodically refreshing utilization table.
func runMockMonitor(ctx context.Context) error {
	devices := []mockMonitorDevice{
		{id: "0000:01:00.0", arch: "HAILO8", temperature: 48.5, power: 2.4, utilization: 0},
		{id: "0000:02:00.0", arch: "HAILO8L", temperature: 42.1, power: 1.6, utilization: 0},
	}

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	fmt.Println("Simulated hailort-monitor (mock mode). Press Ctrl+C to stop.")
	for {
		renderMockMonitorFrame(devices)

		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			for i := range devices {
				devices[i].utilization = clampPercent(devices[i].utilization + (rand.Float64()*30 - 15))
				devices[i].temperature += rand.Float64()*0.6 - 0.3
				devices[i].power += rand.Float64()*0.2 - 0.1
			}
		}
	}
}

func clampPercent(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

// renderMockMonitorFrame clears the screen and prints the current device stats.
func renderMockMonitorFrame(devices []mockMonitorDevice) {
	fmt.Print("\033[H\033[2J") // clear terminal, redraw in place
	fmt.Println("Simulated hailort-monitor (mock mode). Press Ctrl+C to stop.")
	fmt.Println()
	fmt.Printf("%-14s %-9s %8s %8s %8s\n", "DEVICE ID", "ARCH", "TEMP(C)", "POWER(W)", "UTIL(%)")
	for _, d := range devices {
		fmt.Printf("%-14s %-9s %8.1f %8.2f %8.1f\n", d.id, d.arch, d.temperature, d.power, d.utilization)
	}
}
