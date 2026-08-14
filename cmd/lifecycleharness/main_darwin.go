//go:build darwin

package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/pondpilot/quackridge/internal/lifecycle"
)

func main() {
	binary := flag.String("binary", "", "backend executable")
	extensions := flag.String("extensions", "", "verified extension directory")
	flag.Parse()
	if *binary == "" || *extensions == "" {
		fatal("binary and extensions are required")
	}
	root, err := os.MkdirTemp("", "quackridge-lifecycle-smoke-")
	if err != nil {
		fatal(err.Error())
	}
	defer os.RemoveAll(root)
	eventPath := filepath.Join(root, "event.sock")
	controlPath := filepath.Join(root, "control.sock")
	configPath := filepath.Join(root, "config.json")
	listener, err := net.Listen("unix", eventPath)
	if err != nil {
		fatal(err.Error())
	}
	defer listener.Close()
	if err := os.Chmod(eventPath, 0o600); err != nil {
		fatal(err.Error())
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		fatal(err.Error())
	}
	defer writer.Close()
	command := exec.Command(*binary, "serve", "--config", configPath, "--control", controlPath, "--extensions", *extensions,
		"--event-socket", eventPath, "--lifecycle-fd", "3", "--startup-timeout", "60s", "--json")
	command.ExtraFiles = []*os.File{reader}
	command.Stdout = os.Stderr
	command.Stderr = os.Stderr
	command.Env = []string{"HOME=" + root, "USER=quackridge-smoke", "LOGNAME=quackridge-smoke", "TMPDIR=" + root, "LANG=en_US.UTF-8", "LC_ALL=en_US.UTF-8"}
	if err := command.Start(); err != nil {
		fatal(err.Error())
	}
	_ = reader.Close()
	accept := make(chan net.Conn, 1)
	go func() { connection, _ := listener.Accept(); accept <- connection }()
	var connection net.Conn
	select {
	case connection = <-accept:
	case <-time.After(65 * time.Second):
		terminate(command)
		fatal("private event connection timed out")
	}
	if connection == nil {
		terminate(command)
		fatal("private event connection failed")
	}
	defer connection.Close()
	uid, pid, err := peerIdentity(connection)
	if err != nil || uid != os.Getuid() || pid != command.Process.Pid {
		terminate(command)
		fatal("private event peer identity mismatch")
	}
	_ = connection.SetReadDeadline(time.Now().Add(65 * time.Second))
	scanner := bufio.NewScanner(connection)
	scanner.Buffer(make([]byte, 4096), lifecycle.MaxFrameSize)
	ready := false
	for scanner.Scan() {
		var event lifecycle.Event
		if json.Unmarshal(scanner.Bytes(), &event) != nil {
			continue
		}
		if event.Type == "failure" {
			terminate(command)
			fatal("backend reported startup failure")
		}
		if event.Readiness != nil {
			if event.Readiness.PID != command.Process.Pid {
				fatal("readiness PID mismatch")
			}
			ready = true
			break
		}
	}
	if !ready {
		terminate(command)
		fatal("readiness frame was not received")
	}
	_ = writer.Close()
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			fatal("parent EOF shutdown failed: " + err.Error())
		}
	case <-time.After(12 * time.Second):
		terminate(command)
		fatal("parent EOF shutdown timed out")
	}
	fmt.Println("macOS app-owned lifecycle smoke passed for PID " + strconv.Itoa(pid))
}

func peerIdentity(connection net.Conn) (int, int, error) {
	raw, ok := connection.(syscall.Conn)
	if !ok {
		return 0, 0, fmt.Errorf("connection has no descriptor")
	}
	var uid, pid int
	var callErr error
	rawConnection, err := raw.SyscallConn()
	if err != nil {
		return 0, 0, err
	}
	err = rawConnection.Control(func(fd uintptr) {
		credential, credentialErr := unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
		callErr = credentialErr
		if callErr == nil {
			uid = int(credential.Uid)
			pid, callErr = unix.GetsockoptInt(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERPID)
		}
	})
	if err != nil {
		return 0, 0, err
	}
	return uid, pid, callErr
}
func terminate(command *exec.Cmd) {
	if command.Process != nil {
		_ = command.Process.Signal(os.Interrupt)
		time.Sleep(time.Second)
		_ = command.Process.Kill()
	}
}
func fatal(message string) { fmt.Fprintln(os.Stderr, message); os.Exit(1) }
