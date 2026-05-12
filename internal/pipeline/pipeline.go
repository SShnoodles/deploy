package pipeline

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"deploy/internal/sshutil"

	"github.com/pkg/sftp"
	"github.com/schollz/progressbar/v3"
	"golang.org/x/crypto/ssh"
)

// Config holds every step of the deployment pipeline.
// Empty/nil fields cause the corresponding step to be skipped.
type Config struct {
	// SSH connection
	Host       string
	Port       int
	User       string
	Password   string
	KeyFile    string
	SuPassword string // root password for su-based elevation (when set, all commands run as root)

	// Pipeline steps (in execution order)
	PreCmd       string      // command to run before anything else (e.g. stop service)
	BackupPaths  [][2]string // pairs of [src, dst] to copy as backup before delete
	DeletePaths  []string    // remote paths to delete
	UploadPaths  [][2]string // pairs of [local-src, remote-dst] to upload
	ExtractPaths [][2]string // pairs of [remote-archive, dest-dir] to extract
	PostCmd      string      // command to run after extraction
	TailCmd      string      // command whose output is streamed until the user presses Ctrl+C
}

// Run executes the pipeline: connect → delete → upload → extract → exec.
// Each step is skipped when its config field is empty.
func Run(cfg Config) error {
	fmt.Printf("→ connecting  %s@%s:%d\n", cfg.User, cfg.Host, cfg.Port)
	client, err := sshutil.NewClient(cfg.Host, cfg.Port, cfg.User, cfg.Password, cfg.KeyFile)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer client.Close()
	fmt.Println("  ok")

	if cfg.PreCmd != "" {
		fmt.Printf("→ pre         %s\n", cfg.PreCmd)
		if err := runCmd(client, cfg.PreCmd, cfg.SuPassword); err != nil {
			return fmt.Errorf("pre: %w", err)
		}
	}

	for _, pair := range cfg.BackupPaths {
		src, dst := pair[0], pair[1]
		fmt.Printf("→ backup      %s → %s\n", src, dst)
		cmd := fmt.Sprintf("cp -r %q %q", src, dst)
		if err := runCmd(client, cmd, cfg.SuPassword); err != nil {
			return fmt.Errorf("backup %s: %w", src, err)
		}
	}

	for _, p := range cfg.DeletePaths {
		fmt.Printf("→ delete      %s\n", p)
		if err := runCmd(client, fmt.Sprintf("rm -rf %q", p), cfg.SuPassword); err != nil {
			return fmt.Errorf("delete %s: %w", p, err)
		}
	}

	for _, pair := range cfg.UploadPaths {
		src, dst := pair[0], pair[1]
		fmt.Printf("→ upload      %s → %s\n", src, dst)
		if err := uploadFile(client, src, dst); err != nil {
			return fmt.Errorf("upload %s: %w", src, err)
		}
	}

	for _, pair := range cfg.ExtractPaths {
		archive, dest := pair[0], pair[1]
		if dest == "" {
			dest = filepath.Dir(archive)
		}
		fmt.Printf("→ extract     %s → %s\n", archive, dest)
		cmd, err := buildExtractCmd(archive, dest)
		if err != nil {
			return err
		}
		if err := runCmd(client, cmd, cfg.SuPassword); err != nil {
			return fmt.Errorf("extract %s: %w", archive, err)
		}
	}

	if cfg.PostCmd != "" {
		fmt.Printf("→ post        %s\n", cfg.PostCmd)
		if err := runCmd(client, cfg.PostCmd, cfg.SuPassword); err != nil {
			return fmt.Errorf("post: %w", err)
		}
	}

	if cfg.TailCmd != "" {
		fmt.Printf("→ tail        %s\n", cfg.TailCmd)
		fmt.Println("  (press Ctrl+C to stop)")
		if err := sshutil.RunRemoteStream(client, cfg.TailCmd); err != nil {
			return fmt.Errorf("tail: %w", err)
		}
	}

	fmt.Println("→ done")
	const autoKillDelay = 5 * time.Minute
	fmt.Printf("  (auto-exit in %.0f minutes — press Ctrl+C to exit now)\n", autoKillDelay.Minutes())
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	select {
	case <-time.After(autoKillDelay):
		fmt.Println("  auto-exit timeout reached, exiting")
	case <-sig:
	}
	signal.Stop(sig)
	return nil
}

func uploadFile(client *ssh.Client, localPath, remotePath string) error {
	sc, err := sftp.NewClient(client)
	if err != nil {
		return fmt.Errorf("sftp: %w", err)
	}
	defer sc.Close()

	if err := sc.MkdirAll(filepath.Dir(remotePath)); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}

	src, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer src.Close()

	fi, err := src.Stat()
	if err != nil {
		return err
	}

	dst, err := sc.Create(remotePath)
	if err != nil {
		return err
	}
	defer dst.Close()

	bar := progressbar.NewOptions64(
		fi.Size(),
		progressbar.OptionSetDescription("  uploading"),
		progressbar.OptionSetWidth(40),
		progressbar.OptionShowBytes(true),
		progressbar.OptionShowCount(),
		progressbar.OptionSetTheme(progressbar.Theme{
			Saucer:        "=",
			SaucerHead:    ">",
			SaucerPadding: " ",
			BarStart:      "[",
			BarEnd:        "]",
		}),
		progressbar.OptionOnCompletion(func() { fmt.Println() }),
	)

	if _, err := io.Copy(io.MultiWriter(dst, bar), src); err != nil {
		return err
	}
	return nil
}

func buildExtractCmd(path, dest string) (string, error) {
	switch {
	case hasSuffix(path, ".tar.gz", ".tgz"):
		return fmt.Sprintf("mkdir -p %q && tar -xzf %q -C %q", dest, path, dest), nil
	case hasSuffix(path, ".tar.bz2", ".tbz2"):
		return fmt.Sprintf("mkdir -p %q && tar -xjf %q -C %q", dest, path, dest), nil
	case hasSuffix(path, ".tar"):
		return fmt.Sprintf("mkdir -p %q && tar -xf %q -C %q", dest, path, dest), nil
	case hasSuffix(path, ".zip"):
		return fmt.Sprintf("mkdir -p %q && unzip -o %q -d %q", dest, path, dest), nil
	default:
		return "", fmt.Errorf("unsupported archive format: %s", path)
	}
}

func hasSuffix(name string, suffixes ...string) bool {
	for _, s := range suffixes {
		if strings.HasSuffix(name, s) {
			return true
		}
	}
	return false
}

// runCmd executes a remote command, routing through su root when suPassword is
// set, through interactive mode (PTY) for su/sudo commands, or plain otherwise.
func runCmd(client *ssh.Client, cmd, suPassword string) error {
	if suPassword != "" {
		return sshutil.RunRemoteCmdAsSu(client, cmd, suPassword)
	}
	if containsSuOrSudo(cmd) {
		return sshutil.RunRemoteCmdInteractive(client, cmd)
	}
	return sshutil.RunRemoteCmd(client, cmd)
}

var suOrSudoRe = regexp.MustCompile(`\b(su|sudo)\b`)

// containsSuOrSudo reports whether cmd contains the word "su" or "sudo".
func containsSuOrSudo(cmd string) bool {
	return suOrSudoRe.MatchString(cmd)
}
