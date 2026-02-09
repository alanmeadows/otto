package cli

import (
	"fmt"

	"github.com/alanmeadows/otto/internal/server"
	"github.com/spf13/cobra"
)

var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Manage the otto daemon",
	Long:  `Start, stop, and manage the otto background daemon.`,
}

var foregroundFlag bool
var portFlag int

func init() {
	serverCmd.AddCommand(serverStartCmd)
	serverCmd.AddCommand(serverStopCmd)
	serverCmd.AddCommand(serverStatusCmd)
	serverCmd.AddCommand(serverInstallCmd)

	serverStartCmd.Flags().BoolVar(&foregroundFlag, "foreground", false, "Run in foreground (don't daemonize)")
	serverStartCmd.Flags().IntVar(&portFlag, "port", 0, "Server port (default from config or 4097)")
}

var serverStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the otto daemon",
	RunE: func(cmd *cobra.Command, args []string) error {
		port := portFlag
		if port == 0 {
			port = appConfig.Server.Port
		}
		if port == 0 {
			port = 4097
		}
		logDir := appConfig.Server.LogDir

		return server.StartDaemon(port, logDir, foregroundFlag)
	},
}

var serverStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the otto daemon",
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

var serverInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Install as systemd user service",
	RunE: func(cmd *cobra.Command, args []string) error {
		return server.InstallSystemdService()
	},
}
