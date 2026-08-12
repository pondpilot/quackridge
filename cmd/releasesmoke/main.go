// Command releasesmoke starts an extracted release binary and verifies its
// native data and control planes before publication.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/pondpilot/quackridge/internal/control"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	flags := flag.NewFlagSet("releasesmoke", flag.ContinueOnError)
	binary := flags.String("binary", "", "extracted release binary")
	extensions := flags.String("extensions", "", "extracted extension directory")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *binary == "" || *extensions == "" {
		return errors.New("binary and extensions are required")
	}
	temporary, err := os.MkdirTemp("", "quackridge-release-smoke-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temporary)
	controlAddress := filepath.Join(temporary, "control.sock")
	if runtime.GOOS == "windows" {
		controlAddress = `\\.\pipe\quackridge-release-smoke-` + fmt.Sprint(os.Getpid())
	}
	command := exec.Command(*binary, "serve", "--config", filepath.Join(temporary, "config.json"),
		"--extensions", *extensions, "--control", controlAddress, "--credential-provider", "environment", "--json")
	stdout, err := command.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		return err
	}
	if err := command.Start(); err != nil {
		return err
	}
	defer func() {
		if command.ProcessState == nil || !command.ProcessState.Exited() {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	}()
	stderrDone := make(chan string, 1)
	go func() {
		data, _ := io.ReadAll(io.LimitReader(stderr, 1<<20))
		stderrDone <- string(data)
	}()
	ready := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		if scanner.Scan() {
			ready <- scanner.Text()
			return
		}
		ready <- ""
	}()
	select {
	case line := <-ready:
		if !strings.Contains(line, `"endpoint"`) {
			_ = command.Process.Kill()
			_ = command.Wait()
			return fmt.Errorf("release did not become ready: %s", <-stderrDone)
		}
	case <-time.After(30 * time.Second):
		return errors.New("timed out waiting for release readiness")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	response, err := control.Call(ctx, controlAddress, control.Request{Version: control.Version, Operation: "status"})
	if err != nil || !response.OK || response.Status == nil || response.Status.Endpoint == "" {
		return errors.New("release control status check failed")
	}
	diagnostics, err := control.Call(ctx, controlAddress, control.Request{Version: control.Version, Operation: "diagnostics"})
	if err != nil || !diagnostics.OK || diagnostics.Diagnostics["settings"] == nil {
		return errors.New("release diagnostics check failed")
	}
	if err := command.Process.Kill(); err != nil {
		return err
	}
	_ = command.Wait()
	return nil
}
