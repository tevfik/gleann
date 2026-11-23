package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/tevfik/gleann/internal/service"
)

// Seams for tests: these change the system (service unit, background process).
var (
	serviceInstall   = service.Install
	serviceUninstall = service.Uninstall
	serviceStart     = service.Start
)

func cmdService(args []string) {
	if len(args) < 1 {
		printServiceUsage()
		exitFunc(1)
	}

	sub := args[0]
	if sub == "help" || wantsHelpIn(args, serviceFlagSets[sub]) {
		printServiceUsage()
		return
	}
	if fs, ok := serviceFlagSets[sub]; ok {
		rejectUnknownFlags("service "+sub, args[1:], fs)
	}
	switch sub {
	case "install":
		addr := resolveServiceAddr(args[1:])
		bin := getFlag(args[1:], "--bin")
		if err := serviceInstall(bin, addr); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			exitFunc(1)
		}
		fmt.Println("✅ gleann service installed (auto-start on login)")
		fmt.Println("   Start now with: gleann service start")

	case "uninstall":
		if err := serviceUninstall(); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			exitFunc(1)
		}
		fmt.Println("✅ gleann service uninstalled")

	case "start":
		addr := resolveServiceAddr(args[1:])
		bin := getFlag(args[1:], "--bin")
		if err := serviceStart(bin, addr); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			exitFunc(1)
		}
		st := service.GetStatus()
		fmt.Printf("✅ gleann server started (PID %d, %s)\n", st.PID, st.Addr)

	case "stop":
		if err := service.Stop(); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			exitFunc(1)
		}
		fmt.Println("✅ gleann server stopped")

	case "restart":
		service.Stop() // ignore error if not running
		addr := resolveServiceAddr(args[1:])
		bin := getFlag(args[1:], "--bin")
		if err := serviceStart(bin, addr); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			exitFunc(1)
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
			exitFunc(1)
		}
		fmt.Print(logs)

	default:
		fmt.Fprintf(os.Stderr, "unknown service command: %s\n", sub)
		printServiceUsage()
		exitFunc(1)
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
  --port <n>           Server port (install/start/restart; shorthand for --addr :<n>)
  --bin  <path>        Path to gleann binary (default: auto-detect)
  --lines <n>          Number of log lines to show (default: 50)

Platforms:
  Linux    systemd user service (~/.config/systemd/user/)
  macOS    launchd agent (~/Library/LaunchAgents/)
  Windows  Task Scheduler (schtasks)`)
}

func resolveServiceAddr(args []string) string {
	addr := getFlag(args, "--addr")
	if addr == "" {
		portFlag := getFlag(args, "--port")
		if portFlag == "" {
			portFlag = getFlag(args, "-p")
		}
		if portFlag != "" {
			if !strings.HasPrefix(portFlag, ":") {
				addr = ":" + portFlag
			} else {
				addr = portFlag
			}
		}
	}
	return addr
}
