package sshutil

import (
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"golang.org/x/crypto/ssh"
	"golang.org/x/term"
)

// NewClient creates an SSH client using password or private key authentication.
func NewClient(host string, port int, user, password, keyFile string) (*ssh.Client, error) {
	var authMethods []ssh.AuthMethod

	if keyFile != "" {
		key, err := os.ReadFile(keyFile)
		if err != nil {
			return nil, fmt.Errorf("read key file: %w", err)
		}
		signer, err := ssh.ParsePrivateKey(key)
		if err != nil {
			return nil, fmt.Errorf("parse private key: %w", err)
		}
		authMethods = append(authMethods, ssh.PublicKeys(signer))
	}

	if password != "" {
		authMethods = append(authMethods, ssh.Password(password))
	}

	if len(authMethods) == 0 {
		return nil, fmt.Errorf("provide --password or --key for authentication")
	}

	cfg := &ssh.ClientConfig{
		User:            user,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), //nolint:gosec — allow user-controlled host
	}

	addr := fmt.Sprintf("%s:%d", host, port)
	client, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect to %s: %w", addr, err)
	}
	return client, nil
}

// RunRemoteCmd executes a shell command on the remote server, streaming
// stdout/stderr to the local terminal.
func RunRemoteCmd(client *ssh.Client, cmd string) error {
	sess, err := client.NewSession()
	if err != nil {
		return err
	}
	defer sess.Close()
	sess.Stdout = os.Stdout
	sess.Stderr = os.Stderr
	return sess.Run(cmd)
}

// RunRemoteCmdInteractive executes a shell command on the remote server with
// a PTY allocated and stdin connected to the local terminal, allowing
// interactive password prompts (e.g. for su / sudo commands) with proper
// masking and Enter-key handling.
func RunRemoteCmdInteractive(client *ssh.Client, cmd string) error {
	sess, err := client.NewSession()
	if err != nil {
		return err
	}
	defer sess.Close()

	// Request a PTY so programs like su/sudo get proper terminal support
	// (masked password input, correct Enter handling, etc.).
	fd := int(os.Stdin.Fd())
	width, height, err := term.GetSize(fd)
	if err != nil {
		width, height = 80, 24
	}
	termType := os.Getenv("TERM")
	if termType == "" {
		termType = "xterm-256color"
	}
	modes := ssh.TerminalModes{
		ssh.ECHO:          1,
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}
	if err := sess.RequestPty(termType, height, width, modes); err != nil {
		return fmt.Errorf("request pty: %w", err)
	}

	// Put the local terminal in raw mode so keystrokes are forwarded as-is.
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return fmt.Errorf("make raw terminal: %w", err)
	}
	defer term.Restore(fd, oldState) //nolint:errcheck

	sess.Stdin = os.Stdin
	sess.Stdout = os.Stdout
	sess.Stderr = os.Stderr
	return sess.Run(cmd)
}

// RunRemoteCmdAsSu executes cmd on the remote server as root by wrapping it
// with "su root -c '...'" and piping suPassword to stdin. No PTY is needed;
// su reads the password from the pipe and exits after the command completes.
func RunRemoteCmdAsSu(client *ssh.Client, cmd, suPassword string) error {
	sess, err := client.NewSession()
	if err != nil {
		return err
	}
	defer sess.Close()
	wrapped := "su root -c " + shellQuote(cmd)
	sess.Stdin = strings.NewReader(suPassword + "\n")
	sess.Stdout = os.Stdout
	sess.Stderr = os.Stderr
	return sess.Run(wrapped)
}

// shellQuote wraps s in single quotes, escaping any embedded single quotes.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// RunRemoteStream executes cmd on the remote server and streams its output// until the command exits or the user presses Ctrl+C. Ctrl+C is treated as a
// normal exit (returns nil), not an error.
func RunRemoteStream(client *ssh.Client, cmd string) error {
	sess, err := client.NewSession()
	if err != nil {
		return err
	}
	sess.Stdout = os.Stdout
	sess.Stderr = os.Stderr

	if err := sess.Start(cmd); err != nil {
		_ = sess.Close()
		return err
	}

	// Run Wait in a goroutine so we can race it against a signal.
	waitDone := make(chan error, 1)
	go func() { waitDone <- sess.Wait() }()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sig)

	select {
	case <-sig:
		// Close the session immediately; the remote process gets HUP/killed.
		_ = sess.Close()
		return nil
	case err := <-waitDone:
		if isExitSignal(err) {
			return nil
		}
		return err
	}
}

func isExitSignal(err error) bool {
	type signalError interface{ Signal() string }
	_, ok := err.(signalError)
	return ok
}
