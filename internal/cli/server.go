package cli

import (
	"fmt"
	"os"
	"time"

	"github.com/alanmeadows/otto/internal/config"
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
var noDashboardFlag bool
var noPRMonitoringFlag bool
var dashboardPortFlag int
var noTunnelFlag bool
var upgradeChannelFlag string
var insecureFlag bool
var openDashboardFlag bool

func init() {
	serverCmd.AddCommand(serverStartCmd)
	serverCmd.AddCommand(serverStopCmd)
	serverCmd.AddCommand(serverRestartCmd)
	serverCmd.AddCommand(serverUpgradeCmd)
	serverCmd.AddCommand(serverStatusCmd)
	serverCmd.AddCommand(serverInstallCmd)
	serverCmd.AddCommand(serverLogsCmd)

	serverStartCmd.Flags().BoolVar(&foregroundFlag, "foreground", false, "Run in foreground (don't daemonize)")
	serverStartCmd.Flags().IntVar(&portFlag, "port", 0, "Server port (default from config or 4097)")
	serverStartCmd.Flags().BoolVar(&noDashboardFlag, "no-dashboard", false, "Disable the Copilot session dashboard")
	serverStartCmd.Flags().BoolVar(&noPRMonitoringFlag, "no-pr-monitoring", false, "Disable PR monitoring loop")
	serverStartCmd.Flags().IntVar(&dashboardPortFlag, "dashboard-port", 0, "Dashboard port (default from config or 4098)")
	serverStartCmd.Flags().BoolVar(&noTunnelFlag, "no-tunnel", false, "Disable Azure DevTunnel for dashboard")
	serverStartCmd.Flags().BoolVar(&insecureFlag, "insecure", false, "Launch tunnel without authentication (anonymous access)")
	serverStartCmd.Flags().BoolVar(&openDashboardFlag, "open-dashboard", false, "Disable dashboard passcode requirement (fully open)")
	serverUpgradeCmd.Flags().StringVar(&upgradeChannelFlag, "channel", "", "Upgrade channel: \"release\" (default) or \"main\" (build from source)")
}

var serverStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the otto daemon",
	Long: `Start the otto daemon process.

By default the daemon forks into the background. Use --foreground
to run in the current terminal (useful for debugging). The port
defaults to the config value or 4097.

The dashboard and tunnel are enabled by default. Use --no-dashboard
or --no-tunnel to disable them. Use --no-pr-monitoring to skip the
PR polling loop (dashboard-only mode).`,
	Example: `  otto server start
  otto server start --foreground
  otto server start --port 9090
  otto server start --no-dashboard
  otto server start --no-tunnel
  otto server start --no-pr-monitoring
  otto server start --insecure
  otto server start --insecure --open-dashboard`,
	RunE: func(cmd *cobra.Command, args []string) error {
		port := portFlag
		if port == 0 {
			port = appConfig.Server.Port
		}
		if port == 0 {
			port = 4097
		}
		logDir := appConfig.Server.LogDir

		// Dashboard and tunnel are enabled by default.
		// --no-dashboard / --no-tunnel disable them.
		tunnelEnabled := !noTunnelFlag && !noDashboardFlag

		// Auto-generate tunnel_id from $USER if not configured.
		if tunnelEnabled && appConfig.Dashboard.TunnelID == "" {
			if u := os.Getenv("USER"); u != "" {
				appConfig.Dashboard.TunnelID = "otto-" + u
				os.Setenv("OTTO_TUNNEL_ID", appConfig.Dashboard.TunnelID)
				fmt.Fprintf(cmd.ErrOrStderr(), "Using auto-generated tunnel ID: %s (override with: otto config set dashboard.tunnel_id \"myname-otto\")\n", appConfig.Dashboard.TunnelID)
			} else {
				fmt.Fprintf(cmd.ErrOrStderr(), "Warning: tunnel requires dashboard.tunnel_id and $USER is not set. Set one with:\n  otto config set dashboard.tunnel_id \"yourname-otto\"\nStarting without tunnel.\n\n")
				tunnelEnabled = false
			}
		}

		if noDashboardFlag {
			os.Setenv("OTTO_NO_DASHBOARD", "1")
		}
		if !tunnelEnabled {
			os.Setenv("OTTO_NO_TUNNEL", "1")
		}

		// --insecure: set tunnel to anonymous access.
		if insecureFlag {
			appConfig.Dashboard.TunnelAccess = "anonymous"
			os.Setenv("OTTO_TUNNEL_ACCESS", "anonymous")
			fmt.Fprintln(cmd.ErrOrStderr(), "⚠️  WARNING: --insecure mode enabled. The tunnel is open to anyone on the internet.")
			fmt.Fprintln(cmd.ErrOrStderr(), "   Anyone with the URL can access the dashboard. Use only on trusted networks.")
		}

		// --open-dashboard: disable passcode requirement.
		if openDashboardFlag {
			f := false
			appConfig.Dashboard.RequireKey = &f
			os.Setenv("OTTO_OPEN_DASHBOARD", "1")
			fmt.Fprintln(cmd.ErrOrStderr(), "⚠️  WARNING: --open-dashboard mode enabled. Dashboard passcode is DISABLED.")
			fmt.Fprintln(cmd.ErrOrStderr(), "   Anyone who can reach the dashboard URL has full access. This is NOT recommended.")
		}
		if dashboardPortFlag > 0 {
			appConfig.Dashboard.Port = dashboardPortFlag
			os.Setenv("OTTO_DASHBOARD_PORT", fmt.Sprintf("%d", dashboardPortFlag))
		}
		if verbose {
			os.Setenv("OTTO_VERBOSE", "1")
		}
		if noPRMonitoringFlag {
			os.Setenv("OTTO_NO_PR_MONITORING", "1")
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

var serverRestartCmd = &cobra.Command{
	Use:   "restart",
	Short: "Restart the otto daemon",
	Long: `Restart the otto daemon via bgtask.

The restart runs as an independent bgtask so it survives the current
server process exiting. The dashboard will briefly disconnect and
auto-reconnect after restart.`,
	Example: `  otto server restart`,
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Fprintln(cmd.OutOrStdout(), "restarting otto server via bgtask...")
		return server.RestartDaemon()
	},
}

var serverUpgradeCmd = &cobra.Command{
	Use:   "upgrade",
	Short: "Upgrade and restart the otto daemon",
	Long: `Upgrade otto and restart the daemon.

Default channel "release" installs the latest version via go install.
Channel "main" builds from source (requires server.source_dir in config).
The upgrade runs via bgtask so the binary is not locked during install.`,
	Example: `  otto server upgrade
  otto server upgrade --channel main`,
	RunE: func(cmd *cobra.Command, args []string) error {
		channel := appConfig.Server.UpgradeChannel
		if upgradeChannelFlag != "" {
			channel = upgradeChannelFlag
		}
		fmt.Fprintf(cmd.OutOrStdout(), "upgrading otto (channel: %s) via bgtask...\n", channelLabel(channel))
		return server.UpgradeDaemon(channel, appConfig.Server.SourceDir)
	},
}

var serverStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show daemon status",
	Long: `Show whether the otto daemon is running.

Displays the PID, uptime, endpoints, and tunnel URL when active.`,
	Example: `  otto server status`,
	RunE: func(cmd *cobra.Command, args []string) error {
		running, pid, uptime, err := server.DaemonStatus()
		if err != nil {
			return err
		}

		if !running {
			fmt.Fprintln(cmd.OutOrStdout(), "daemon is not running")
			return nil
		}

		fmt.Fprintf(cmd.OutOrStdout(), "daemon is running (PID %d, uptime %s)\n", pid, uptime.Round(1*1e9))

		// Load config to determine ports.
		cfg, cfgErr := config.Load()
		if cfgErr != nil {
			return nil // still show basic status even if config fails
		}

		apiPort := cfg.Server.Port
		if apiPort == 0 {
			apiPort = 4097
		}
		fmt.Fprintf(cmd.OutOrStdout(), "  api:       http://localhost:%d\n", apiPort)

		dashPort := cfg.Dashboard.Port
		if dashPort == 0 {
			dashPort = 4098
		}
		if cfg.Dashboard.Enabled {
			fmt.Fprintf(cmd.OutOrStdout(), "  dashboard: http://localhost:%d\n", dashPort)

			// Query the dashboard for tunnel status. If the server just
			// started (< 20s uptime), retry a few times since the tunnel
			// needs time to connect.
			var tunnelURL string
			if uptime < 20*time.Second {
				tunnelURL = server.PollTunnelURLBrief(dashPort)
			} else {
				tunnelURL = server.PollTunnelURLQuick(dashPort)
			}
			if tunnelURL != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "  tunnel:    %s\n", tunnelURL)
			}
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

func channelLabel(ch string) string {
	if ch == "main" {
		return "main"
	}
	return "release"
}
