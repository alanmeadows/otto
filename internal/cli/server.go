package cli

import (
	"fmt"
	"os"

	"github.com/alanmeadows/otto/internal/server"
	"github.com/spf13/cobra"
)

var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Manage the otto daemon",
	Long: `Start, stop, and manage the otto background daemon.

The daemon runs an HTTP API and periodically polls tracked PRs for
new review comments. It can be run in the foreground for debugging
or installed as a systemd user service for persistent operation.`,
	Example: `  otto server start
  otto server start --foreground --port 9090
  otto server status
  otto server stop`,
}

var foregroundFlag bool
var portFlag int
var dashboardFlag bool
var dashboardOnlyFlag bool
var dashboardPortFlag int
var tunnelFlag bool

func init() {
	serverCmd.AddCommand(serverStartCmd)
	serverCmd.AddCommand(serverStopCmd)
	serverCmd.AddCommand(serverStatusCmd)
	serverCmd.AddCommand(serverInstallCmd)
	serverCmd.AddCommand(serverLogsCmd)

	serverStartCmd.Flags().BoolVar(&foregroundFlag, "foreground", false, "Run in foreground (don't daemonize)")
	serverStartCmd.Flags().IntVar(&portFlag, "port", 0, "Server port (default from config or 4097)")
	serverStartCmd.Flags().BoolVar(&dashboardFlag, "dashboard", false, "Enable the Copilot session dashboard")
	serverStartCmd.Flags().BoolVar(&dashboardOnlyFlag, "dashboard-only", false, "Run only the dashboard (skip PR monitoring)")
	serverStartCmd.Flags().IntVar(&dashboardPortFlag, "dashboard-port", 0, "Dashboard port (default from config or 4098)")
	serverStartCmd.Flags().BoolVar(&tunnelFlag, "tunnel", false, "Auto-start Azure DevTunnel for dashboard")
}

var serverStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the otto daemon",
	Long: `Start the otto daemon process.

By default the daemon forks into the background. Use --foreground
to run in the current terminal (useful for debugging). The port
defaults to the config value or 4097.

Use --dashboard to enable the Copilot session dashboard, which
serves a web UI for managing Copilot CLI sessions from your browser
or phone. Use --tunnel to expose the dashboard via Azure DevTunnels.`,
	Example: `  otto server start
  otto server start --foreground
  otto server start --port 9090
  otto server start --dashboard
  otto server start --dashboard --tunnel
  otto server start --dashboard-only --tunnel`,
	RunE: func(cmd *cobra.Command, args []string) error {
		port := portFlag
		if port == 0 {
			port = appConfig.Server.Port
		}
		if port == 0 {
			port = 4097
		}
		logDir := appConfig.Server.LogDir

		// Apply dashboard flags to config so they propagate.
		if dashboardFlag || dashboardOnlyFlag {
			appConfig.Dashboard.Enabled = true
			os.Setenv("OTTO_DASHBOARD", "1")
		}
		if dashboardPortFlag > 0 {
			appConfig.Dashboard.Port = dashboardPortFlag
			os.Setenv("OTTO_DASHBOARD_PORT", fmt.Sprintf("%d", dashboardPortFlag))
		}
		if tunnelFlag {
			appConfig.Dashboard.AutoStartTunnel = true
			os.Setenv("OTTO_TUNNEL", "1")
		}

		return server.StartDaemon(port, logDir, foregroundFlag)
	},
}

var serverStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the otto daemon",
	Long: `Stop the running otto daemon process.

Sends a shutdown signal to the daemon identified by its PID file.
Returns an error if no daemon is currently running.`,
	Example: `  otto server stop`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := server.StopDaemon(); err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), "daemon stopped")
		return nil
	},
}

var serverStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show daemon status",
	Long: `Show whether the otto daemon is running.

Displays the PID and uptime if the daemon is active, or reports
that it is not running.`,
	Example: `  otto server status`,
	RunE: func(cmd *cobra.Command, args []string) error {
		running, pid, uptime, err := server.DaemonStatus()
		if err != nil {
			return err
		}

		if running {
			fmt.Fprintf(cmd.OutOrStdout(), "daemon is running (PID %d, uptime %s)\n", pid, uptime.Round(1*1e9))
		} else {
			fmt.Fprintln(cmd.OutOrStdout(), "daemon is not running")
		}
		return nil
	},
}

var serverLogsCmd = &cobra.Command{
	Use:     "logs",
	Short:   "Show the daemon log file path",
	Long:    `Print the path to the otto daemon log file.`,
	Example: `  otto server logs`,
	RunE: func(cmd *cobra.Command, args []string) error {
		path := server.LogFilePath()
		if path == "" {
			return fmt.Errorf("cannot determine log file path")
		}
		fmt.Fprintln(cmd.OutOrStdout(), path)
		return nil
	},
}

var serverInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Install as systemd user service",
	Long: `Install the otto daemon as a systemd user service.

Creates a systemd unit file under ~/.config/systemd/user/ so the
daemon starts automatically on login. Use 'systemctl --user' to
manage the service after installation.`,
	Example: `  otto server install`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return server.InstallSystemdService()
	},
}
