//go:build darwin

package main

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type launchdServiceBackend struct{}

func currentServiceBackend() serviceBackend {
	return launchdServiceBackend{}
}

func (launchdServiceBackend) Name() string {
	return "launchd"
}

func (launchdServiceBackend) Install(ctx context.Context, spec serviceSpec, force bool, start bool) error {
	if err := ensureServiceLogDir(spec); err != nil {
		return err
	}
	path, err := launchdPlistPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create LaunchAgents dir: %w", err)
	}
	if !force {
		if existing, err := os.ReadFile(path); err == nil && !bytes.Equal(existing, []byte(renderLaunchdPlist(spec))) {
			return fmt.Errorf("Builder background service is already installed at %s; use --force to rewrite it", path)
		}
	}
	if err := os.WriteFile(path, []byte(renderLaunchdPlist(spec)), 0o644); err != nil {
		return fmt.Errorf("write launchd plist: %w", err)
	}
	if start {
		if err := reloadLaunchdService(ctx, spec, path); err != nil {
			return err
		}
	}
	return nil
}

func (launchdServiceBackend) Uninstall(ctx context.Context, spec serviceSpec, stop bool) error {
	if stop {
		_ = launchdServiceBackend{}.Stop(ctx, spec)
	}
	path, err := launchdPlistPath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove launchd plist: %w", err)
	}
	return nil
}

func (launchdServiceBackend) Start(ctx context.Context, spec serviceSpec) error {
	path, err := launchdPlistPath()
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return errors.New("Builder background service is not installed; run `builder service install`")
		}
		return fmt.Errorf("stat launchd plist: %w", err)
	}
	if loaded, _ := launchdLoaded(ctx); !loaded {
		return bootstrapLaunchdService(ctx, spec, path)
	}
	_, err = runServiceCommand(ctx, "launchctl", "kickstart", "-k", launchdDomain()+"/"+serviceLaunchdLabel)
	return err
}

func (launchdServiceBackend) Stop(ctx context.Context, spec serviceSpec) error {
	if loaded, _ := launchdLoaded(ctx); !loaded {
		return nil
	}
	_, err := runServiceCommand(ctx, "launchctl", "bootout", launchdDomain()+"/"+serviceLaunchdLabel)
	return err
}

func (launchdServiceBackend) Restart(ctx context.Context, spec serviceSpec) error {
	if loaded, _ := launchdLoaded(ctx); !loaded {
		return launchdServiceBackend{}.Start(ctx, spec)
	}
	_, err := runServiceCommand(ctx, "launchctl", "kickstart", "-k", launchdDomain()+"/"+serviceLaunchdLabel)
	return err
}

func (launchdServiceBackend) Status(ctx context.Context, spec serviceSpec) (serviceStatus, error) {
	path, err := launchdPlistPath()
	if err != nil {
		return serviceStatus{}, err
	}
	installed := false
	if _, err := os.Stat(path); err == nil {
		installed = true
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return serviceStatus{}, fmt.Errorf("stat launchd plist: %w", err)
	}
	loaded, output := launchdLoaded(ctx)
	pid := launchdPID(output)
	command := readLaunchdRegisteredCommand(path)
	if loadedCommand := parseLaunchdPrintProgramArguments(output); len(loadedCommand) > 0 {
		command = loadedCommand
	}
	return serviceStatus{
		Backend:     "launchd",
		Installed:   installed,
		Loaded:      loaded,
		Running:     pid > 0 || launchdState(output) == "running",
		PID:         pid,
		Command:     command,
		Endpoint:    spec.Endpoint,
		Logs:        []string{spec.StdoutLogPath, spec.StderrLogPath},
		InstallPath: path,
	}, nil
}

func reloadLaunchdService(ctx context.Context, spec serviceSpec, path string) error {
	if loaded, _ := launchdLoaded(ctx); loaded {
		if _, err := runServiceCommand(ctx, "launchctl", "bootout", launchdDomain()+"/"+serviceLaunchdLabel); err != nil {
			return err
		}
	}
	return bootstrapLaunchdService(ctx, spec, path)
}

func bootstrapLaunchdService(ctx context.Context, spec serviceSpec, path string) error {
	if _, err := runServiceCommand(ctx, "launchctl", "bootstrap", launchdDomain(), path); err != nil {
		if !isTransientLaunchdBootstrapError(err) {
			return err
		}
		return kickstartMatchingLoadedLaunchdService(ctx, spec, err)
	}
	return nil
}

func isTransientLaunchdBootstrapError(err error) bool {
	var commandErr serviceCommandError
	if !errors.As(err, &commandErr) {
		return false
	}
	return commandErr.Name == "launchctl" && commandErr.Result.Code == 5
}

func kickstartMatchingLoadedLaunchdService(ctx context.Context, spec serviceSpec, cause error) error {
	loaded, output := launchdLoaded(ctx)
	if !loaded || !commandArgsEqual(parseLaunchdPrintProgramArguments(output), serviceCommand(spec)) {
		return cause
	}
	if _, err := runServiceCommand(ctx, "launchctl", "kickstart", "-k", launchdDomain()+"/"+serviceLaunchdLabel); err != nil {
		return errors.Join(cause, err)
	}
	return nil
}

func readLaunchdRegisteredCommand(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return parseLaunchdProgramArguments(data)
}

func parseLaunchdProgramArguments(data []byte) []string {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	lastKey := ""
	inProgramArguments := false
	args := []string{}
	for {
		token, err := decoder.Token()
		if err != nil {
			return args
		}
		switch typed := token.(type) {
		case xml.StartElement:
			switch typed.Name.Local {
			case "key":
				lastKey = strings.TrimSpace(readXMLText(decoder, "key"))
			case "array":
				if lastKey == "ProgramArguments" {
					inProgramArguments = true
				}
			case "string":
				text := readXMLText(decoder, "string")
				if inProgramArguments {
					args = append(args, text)
				}
			}
		case xml.EndElement:
			if typed.Name.Local == "array" && inProgramArguments {
				return args
			}
		}
	}
}

func readXMLText(decoder *xml.Decoder, endElement string) string {
	var builder strings.Builder
	for {
		token, err := decoder.Token()
		if err != nil {
			return builder.String()
		}
		switch typed := token.(type) {
		case xml.CharData:
			builder.Write([]byte(typed))
		case xml.EndElement:
			if typed.Name.Local == endElement {
				return builder.String()
			}
		}
	}
}

func launchdPlistPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, "Library", "LaunchAgents", serviceLaunchdLabel+".plist"), nil
}

func launchdDomain() string {
	return fmt.Sprintf("gui/%d", os.Getuid())
}

func launchdLoaded(ctx context.Context) (bool, string) {
	result, err := runServiceCommand(ctx, "launchctl", "print", launchdDomain()+"/"+serviceLaunchdLabel)
	if err != nil {
		return false, result.Text()
	}
	return true, result.Text()
}

func launchdPID(output string) int {
	for _, line := range strings.Split(output, "\n") {
		parts := strings.SplitN(strings.TrimSpace(line), "=", 2)
		if len(parts) != 2 {
			continue
		}
		if strings.TrimSpace(parts[0]) == "pid" {
			return parsePositiveInt(parts[1])
		}
	}
	return 0
}

func launchdState(output string) string {
	for _, line := range strings.Split(output, "\n") {
		parts := strings.SplitN(strings.TrimSpace(line), "=", 2)
		if len(parts) != 2 {
			continue
		}
		if strings.TrimSpace(parts[0]) == "state" {
			return strings.TrimSpace(parts[1])
		}
	}
	return ""
}

func parseLaunchdPrintProgramArguments(output string) []string {
	args := []string{}
	inArguments := false
	for _, rawLine := range strings.Split(output, "\n") {
		line := strings.TrimSpace(rawLine)
		switch {
		case line == "arguments = {":
			inArguments = true
		case inArguments && line == "}":
			return args
		case inArguments && line != "":
			args = append(args, line)
		}
	}
	return nil
}

func renderLaunchdPlist(spec serviceSpec) string {
	var builder strings.Builder
	builder.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	builder.WriteString(`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n")
	builder.WriteString("<plist version=\"1.0\">\n<dict>\n")
	writeLaunchdString(&builder, "Label", serviceLaunchdLabel)
	builder.WriteString("\t<key>ProgramArguments</key>\n\t<array>\n")
	for _, arg := range serviceCommand(spec) {
		builder.WriteString("\t\t<string>")
		_ = xml.EscapeText(&builder, []byte(arg))
		builder.WriteString("</string>\n")
	}
	builder.WriteString("\t</array>\n")
	writeLaunchdBool(&builder, "RunAtLoad", true)
	writeLaunchdBool(&builder, "KeepAlive", true)
	writeLaunchdString(&builder, "StandardOutPath", spec.StdoutLogPath)
	writeLaunchdString(&builder, "StandardErrorPath", spec.StderrLogPath)
	builder.WriteString("</dict>\n</plist>\n")
	return builder.String()
}

func writeLaunchdString(builder *strings.Builder, key string, value string) {
	builder.WriteString("\t<key>")
	_ = xml.EscapeText(builder, []byte(key))
	builder.WriteString("</key>\n\t<string>")
	_ = xml.EscapeText(builder, []byte(value))
	builder.WriteString("</string>\n")
}

func writeLaunchdBool(builder *strings.Builder, key string, value bool) {
	builder.WriteString("\t<key>")
	_ = xml.EscapeText(builder, []byte(key))
	builder.WriteString("</key>\n")
	if value {
		builder.WriteString("\t<true/>\n")
	} else {
		builder.WriteString("\t<false/>\n")
	}
}
