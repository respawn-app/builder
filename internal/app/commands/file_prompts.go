package commands

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

const (
	builderDirName  = ".builder"
	promptsDirName  = "prompts"
	commandsDirName = "commands"
)

type filePromptCommand struct {
	Name        string
	Description string
	Content     string
}

func NewDefaultRegistryWithFilePrompts(workspaceRoot, settingsPath string) (*Registry, error) {
	r := NewDefaultRegistry()
	prompts, err := loadFilePromptCommands(workspaceRoot, settingsPath)
	if err != nil {
		return nil, err
	}
	for _, prompt := range prompts {
		content := prompt.Content
		r.Register(prompt.Name, prompt.Description, func(string) Result {
			return Result{Handled: true, SubmitUser: true, User: content}
		})
	}
	return r, nil
}

func loadFilePromptCommands(workspaceRoot, settingsPath string) ([]filePromptCommand, error) {
	dirs, err := filePromptSearchDirs(workspaceRoot, settingsPath)
	if err != nil {
		return nil, err
	}

	seen := map[string]bool{}
	commands := make([]filePromptCommand, 0)
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("read prompt directory %s: %w", dir, err)
		}
		sort.Slice(entries, func(i, j int) bool {
			return entries[i].Name() < entries[j].Name()
		})
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			if filepath.Ext(name) != ".md" {
				continue
			}
			base := strings.TrimSuffix(name, ".md")
			if strings.TrimSpace(base) == "" {
				continue
			}
			commandName := "prompt:" + strings.ToLower(base)
			if strings.IndexFunc(commandName, unicode.IsSpace) >= 0 {
				return nil, fmt.Errorf("invalid prompt file name %q in %s: whitespace in command id", name, dir)
			}
			if seen[commandName] {
				continue
			}
			fullPath := filepath.Join(dir, name)
			content, err := os.ReadFile(fullPath)
			if err != nil {
				return nil, fmt.Errorf("read prompt file %s: %w", fullPath, err)
			}
			seen[commandName] = true
			commands = append(commands, filePromptCommand{
				Name:        commandName,
				Description: "Run custom Markdown prompt",
				Content:     string(content),
			})
		}
	}
	return commands, nil
}

func filePromptSearchDirs(workspaceRoot, settingsPath string) ([]string, error) {
	workspaceRoot = strings.TrimSpace(workspaceRoot)
	if workspaceRoot == "" {
		return nil, errors.New("workspace root is required")
	}
	globalRoot := strings.TrimSpace(filepath.Dir(settingsPath))
	if globalRoot == "" || globalRoot == "." {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("resolve home dir: %w", err)
		}
		globalRoot = filepath.Join(home, builderDirName)
	}

	localBuilder := filepath.Join(workspaceRoot, builderDirName)
	return []string{
		filepath.Join(localBuilder, promptsDirName),
		filepath.Join(localBuilder, commandsDirName),
		filepath.Join(globalRoot, promptsDirName),
		filepath.Join(globalRoot, commandsDirName),
	}, nil
}
