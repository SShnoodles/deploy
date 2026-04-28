package sshutil

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/crypto/ssh"
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

// RunRemoteStream executes cmd on the remote server and streams its output
// until the command exits or the user presses Ctrl+C. Ctrl+C is treated as a
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
