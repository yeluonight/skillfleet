package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/yeluonight/skillfleet/internal/config"
	"github.com/yeluonight/skillfleet/internal/daemon"
	"github.com/yeluonight/skillfleet/internal/db"
	"github.com/yeluonight/skillfleet/internal/setup"
)

func runDaemonStart(_ []string) error {
	bin, err := daemon.OwnBinaryPath()
	if err != nil {
		return err
	}
	if err := daemon.Start(daemon.ServerSpec(bin, "")); err != nil {
		return err
	}
	fmt.Println("skillfleet-server started (systemd user service).")
	fmt.Println("Hint: if it doesn't survive logout, run: loginctl enable-linger", os.Getenv("USER"))
	return nil
}

func runDaemonStop(_ []string) error {
	if err := daemon.Stop("skillfleet-server"); err != nil {
		return err
	}
	fmt.Println("skillfleet-server stopped.")
	return nil
}

func runDaemonRestart(_ []string) error {
	bin, err := daemon.OwnBinaryPath()
	if err != nil {
		return err
	}
	if err := daemon.Restart(daemon.ServerSpec(bin, "")); err != nil {
		return err
	}
	fmt.Println("skillfleet-server restarted.")
	return nil
}

// runDaemonStatus prints the systemd service status (if installed) plus
// runtime info read from the resolved config: bind address, data dir, DB
// file size, and a setup-completion hint. Per REVIEW#3, the setup code is
// NOT shown — the DB stores only its sha256 hash, so it cannot be read
// back; status only reports whether setup is still required.
func runDaemonStatus(_ []string) error {
	st, err := daemon.StatusOf("skillfleet-server")
	if err != nil {
		return err
	}
	if st.Installed {
		fmt.Printf("Unit:    %s\n", st.UnitPath)
		if st.ActiveState != "" {
			fmt.Printf("State:   %s (%s)\n", st.ActiveState, st.SubState)
			if st.PID != "" && st.PID != "0" {
				fmt.Printf("PID:     %s\n", st.PID)
			}
		}
	} else {
		fmt.Println("Service: NOT installed (run `skillfleet-server start`).")
	}

	cfg, err := loadOrSeedConfig(config.DefaultPath)
	if err != nil {
		fmt.Println("(could not read config:", err, ")")
		return nil
	}
	fmt.Printf("Bind:    %s\n", cfg.Server.Bind)
	fmt.Printf("DataDir: %s\n", cfg.Server.DataDir)

	dbPath := filepath.Join(cfg.Server.DataDir, db.DefaultFileName)
	if info, statErr := os.Stat(dbPath); statErr == nil {
		fmt.Printf("DB size: %s (%d bytes)\n", humanBytes(info.Size()), info.Size())
	}

	// Setup status (read-only, no plaintext code — REVIEW#3).
	if database, openErr := db.Open(context.Background(), dbPath); openErr == nil {
		defer func() { _ = database.Close() }()
		if status, statusErr := setup.CurrentStatus(context.Background(), database); statusErr == nil {
			if status.Required {
				fmt.Println("Setup:   INCOMPLETE — finish WebUI bootstrap with the code")
				fmt.Println("         printed to stderr on first start, or restart the")
				fmt.Println("         server to regenerate a fresh code.")
			} else {
				fmt.Println("Setup:   complete")
			}
		}
	}
	return nil
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

func printUsage(w *os.File) {
	_, _ = fmt.Fprintln(w, "Usage:")
	_, _ = fmt.Fprintln(w, "  skillfleet-server                       # run server (foreground)")
	_, _ = fmt.Fprintln(w, "  skillfleet-server -config <path>        # run with a specific config")
	_, _ = fmt.Fprintln(w, "  skillfleet-server start                 # install + start systemd user service")
	_, _ = fmt.Fprintln(w, "  skillfleet-server stop                  # stop the systemd user service")
	_, _ = fmt.Fprintln(w, "  skillfleet-server restart               # restart the systemd user service")
	_, _ = fmt.Fprintln(w, "  skillfleet-server status                # show service + runtime status")
	_, _ = fmt.Fprintln(w, "  skillfleet-server -version              # print version")
}
