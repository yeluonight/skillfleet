package main

import (
	"fmt"
	"os"

	"github.com/yeluonight/skillfleet/internal/daemon"
)

func runDaemonStart(_ []string) error {
	bin, err := daemon.OwnBinaryPath()
	if err != nil {
		return err
	}
	// Empty config path → agent uses its default agent.json.
	if err := daemon.Start(daemon.AgentSpec(bin, "")); err != nil {
		return err
	}
	fmt.Println("skillfleet-agent started (background service).")
	fmt.Println("Hint: if it doesn't survive logout, run: loginctl enable-linger", os.Getenv("USER"))
	return nil
}

func runDaemonStop(_ []string) error {
	if err := daemon.Stop("skillfleet-agent"); err != nil {
		return err
	}
	fmt.Println("skillfleet-agent stopped.")
	return nil
}

func runDaemonRestart(_ []string) error {
	bin, err := daemon.OwnBinaryPath()
	if err != nil {
		return err
	}
	if err := daemon.Restart(daemon.AgentSpec(bin, "")); err != nil {
		return err
	}
	fmt.Println("skillfleet-agent restarted.")
	return nil
}

func runDaemonStatus(_ []string) error {
	st, err := daemon.StatusOf("skillfleet-agent")
	if err != nil {
		return err
	}
	if !st.Installed {
		fmt.Println("skillfleet-agent is not installed as a service. Run `skillfleet-agent start`.")
		return nil
	}
	fmt.Printf("Unit:    %s\n", st.UnitPath)
	if st.ActiveState == "" {
		fmt.Println("State:   installed but not loaded")
		return nil
	}
	fmt.Printf("State:   %s (%s)\n", st.ActiveState, st.SubState)
	if st.PID != "" && st.PID != "0" {
		fmt.Printf("PID:     %s\n", st.PID)
	}
	return nil
}
