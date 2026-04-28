package cmd

import (
	"fmt"
	"os"
	"strings"

	"deploy/internal/config"
	"deploy/internal/pipeline"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// cfg is built from config file then overridden by explicit CLI flags.
var cfg pipeline.Config

// cfgFile is the path passed via --config; empty means auto-detect.
var cfgFile string

// copyArgs holds raw "src:dst" strings from --backup flags.
var backupArgs []string

// uploadArgs holds raw "src:dst" strings from --upload flags.
var uploadArgs []string

// extractArgs holds raw "archive:dest" strings from --extract flags.
var extractArgs []string
var rootCmd = &cobra.Command{
	Use:   "deploy",
	Short: "SSH pipeline: connect → pre → backup → delete → upload → extract → post",
	Long: `deploy runs a sequential deployment pipeline over SSH.

Steps run in order; omit a flag (or leave it unset in the config file) to skip.

Default pipeline order:
  1. connect
  2. pre     (--pre)             ← e.g. stop service
  3. backup  (--backup, repeatable) ← backup files before delete
  4. delete  (--delete, repeatable)
  5. upload  (--upload, repeatable)
  6. extract (--extract, repeatable)
  7. post    (--post)            ← e.g. start service
  8. tail    (--tail)            ← stream logs until Ctrl+C

A YAML config file (deploy.yaml / deploy.yml) is loaded automatically when
present in the working directory. Use --config to specify a custom path.
CLI flags always override config file values.`,
	// Load config file before running, then let explicit CLI flags win.
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		fileCfg, err := config.Load(cfgFile)
		if err != nil {
			return err
		}

		// Apply config-file values only for fields not explicitly set via CLI.
		changed := func(name string) bool { return cmd.Flags().Changed(name) }

		if !changed("host") && fileCfg.Host != "" {
			cfg.Host = fileCfg.Host
		}
		if !changed("port") && fileCfg.Port != 0 {
			cfg.Port = fileCfg.Port
		}
		if !changed("user") && fileCfg.User != "" {
			cfg.User = fileCfg.User
		}
		if !changed("password") && fileCfg.Password != "" {
			cfg.Password = fileCfg.Password
		}
		if !changed("key") && fileCfg.KeyFile != "" {
			cfg.KeyFile = fileCfg.KeyFile
		}
		if !changed("pre") && fileCfg.PreCmd != "" {
			cfg.PreCmd = fileCfg.PreCmd
		}
		if !changed("backup") && len(fileCfg.BackupPaths) > 0 {
			cfg.BackupPaths = fileCfg.BackupPaths
		} else if changed("backup") {
			for _, raw := range backupArgs {
				parts := strings.SplitN(raw, ":", 2)
				if len(parts) != 2 {
					return fmt.Errorf("--backup %q: expected format src:dst", raw)
				}
				cfg.BackupPaths = append(cfg.BackupPaths, [2]string{parts[0], parts[1]})
			}
		}
		if !changed("delete") && len(fileCfg.DeletePaths) > 0 {
			cfg.DeletePaths = fileCfg.DeletePaths
		}
		if !changed("upload") && len(fileCfg.UploadPaths) > 0 {
			cfg.UploadPaths = fileCfg.UploadPaths
		} else if changed("upload") {
			for _, raw := range uploadArgs {
				parts := strings.SplitN(raw, ":", 2)
				if len(parts) != 2 {
					return fmt.Errorf("--upload %q: expected format src:dst", raw)
				}
				cfg.UploadPaths = append(cfg.UploadPaths, [2]string{parts[0], parts[1]})
			}
		}
		if !changed("extract") && len(fileCfg.ExtractPaths) > 0 {
			cfg.ExtractPaths = fileCfg.ExtractPaths
		} else if changed("extract") {
			for _, raw := range extractArgs {
				parts := strings.SplitN(raw, ":", 2)
				if len(parts) != 2 {
					return fmt.Errorf("--extract %q: expected format archive:dest-dir", raw)
				}
				cfg.ExtractPaths = append(cfg.ExtractPaths, [2]string{parts[0], parts[1]})
			}
		}
		if !changed("post") && fileCfg.PostCmd != "" {
			cfg.PostCmd = fileCfg.PostCmd
		}
		if !changed("tail") && fileCfg.TailCmd != "" {
			cfg.TailCmd = fileCfg.TailCmd
		}

		// Validate required fields after merging.
		if cfg.Host == "" {
			return errRequired("host")
		}
		if cfg.User == "" {
			return errRequired("user")
		}
		// If no password and no key, prompt interactively with masking.
		if cfg.Password == "" && cfg.KeyFile == "" {
			fmt.Fprintf(os.Stderr, "Password for %s@%s: ", cfg.User, cfg.Host)
			raw, err := term.ReadPassword(int(os.Stdin.Fd()))
			fmt.Fprintln(os.Stderr) // newline after the hidden input
			if err != nil {
				return fmt.Errorf("read password: %w", err)
			}
			cfg.Password = string(raw)
		}
		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		return pipeline.Run(cfg)
	},
}

func errRequired(name string) error {
	return fmt.Errorf("required flag %q not set (use --config or --%s)", name, name)
}

// Execute runs the root command.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	f := rootCmd.Flags()

	// config file
	rootCmd.PersistentFlags().StringVarP(&cfgFile, "config", "c", "", "config file (default: deploy.yaml / deploy.yml)")

	// connection
	f.StringVarP(&cfg.Host, "host", "H", "", "remote host")
	f.IntVarP(&cfg.Port, "port", "p", 22, "SSH port")
	f.StringVarP(&cfg.User, "user", "u", "", "SSH user")
	f.StringVar(&cfg.Password, "password", "", "SSH password")
	f.StringVarP(&cfg.KeyFile, "key", "k", "", "path to private key file")

	// pipeline steps
	f.StringVar(&cfg.PreCmd, "pre", "", "command to run first (e.g. stop service)")
	f.StringArrayVar(&backupArgs, "backup", nil, "backup remote files before delete, format: src:dst (repeatable)")
	f.StringArrayVar(&cfg.DeletePaths, "delete", nil, "remote path(s) to delete (repeatable)")
	f.StringArrayVar(&uploadArgs, "upload", nil, "upload local file to remote, format: src:dst (repeatable)")
	f.StringArrayVar(&extractArgs, "extract", nil, "extract remote archive, format: archive:dest-dir (repeatable)")
	f.StringVar(&cfg.PostCmd, "post", "", "command to run last (e.g. start service)")
	f.StringVar(&cfg.TailCmd, "tail", "", "stream remote command output until Ctrl+C (e.g. tail -f /var/log/app.log)")
}
