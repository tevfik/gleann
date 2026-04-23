package main

import (
	"fmt"
	"os"

	"github.com/tevfik/gleann/internal/service"
)

func cmdService(args []string) {
	if len(args) < 1 {
		printServiceUsage()
		os.Exit(1)
	}

	sub := args[0]
	switch sub {
	case "install":
		addr := getFlag(args[1:], "--addr")
		bin := getFlag(args[1:], "--bin")
		if err := service.Install(bin, addr); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("✅ gleann service installed (auto-start on login)")
		fmt.Println("   Start now with: gleann service start")

	case "uninstall":
		if err := service.Uninstall(); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("✅ gleann service uninstalled")

	case "start":
		addr := getFlag(args[1:], "--addr")
		bin := getFlag(args[1:], "--bin")
		if err := service.Start(bin, addr); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		st := service.GetStatus()
		fmt.Printf("✅ gleann server started (PID %d, %s)\n", st.PID, st.Addr)

	case "stop":
		if err := service.Stop(); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("✅ gleann server stopped")

	case "restart":
		service.Stop() // ignore error if not running
		addr := getFlag(args[1:], "--addr")
		bin := getFlag(args[1:], "--bin")
		if err := service.Start(bin, addr); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		st := service.GetStatus()
		fmt.Printf("✅ gleann server restarted (PID %d, %s)\n", st.PID, st.Addr)

	case "status":
		st := service.GetStatus()
		fmt.Print(service.FormatStatus(st))

	case "logs":
		n := 50
		if v := getFlag(args[1:], "--lines"); v != "" {
			fmt.Sscanf(v, "%d", &n)
		}
		logs, err := service.Logs(n)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		fmt.Print(logs)

	default:
		fmt.Fprintf(os.Stderr, "unknown service command: %s\n", sub)
		printServiceUsage()
		os.Exit(1)
	}
}

func printServiceUsage() {
	fmt.Println(`Usage: gleann service <command> [options]

Commands:
  install    Install as OS service (auto-start on login)
  uninstall  Remove OS service
  start      Start gleann server in background
  stop       Stop running server
  restart    Restart server
  status     Show server status
  logs       Show server logs

Options:
  --addr <host:port>   Server address (default: :8080)
  --bin  <path>        Path to gleann binary (default: auto-detect)
  --lines <n>          Number of log lines to show (default: 50)

Platforms:
  Linux    systemd user service (~/.config/systemd/user/)
  macOS    launchd agent (~/Library/LaunchAgents/)
  Windows  Task Scheduler (schtasks)`)
}
