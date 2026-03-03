package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	skillsDirName                = "skills"
	skillFileName                = "SKILL.md"
	skillsInjectedHeader         = "## Skills"
	skillsInjectedDescription    = "A skill is a set of local instructions to follow that is stored in a `SKILL.md` file. Below is the list of skills that can be used. Each entry includes a name, description, and file path so you can open the source for full instructions when using a specific skill."
	skillsAvailableHeader        = "### Available skills"
	skillsHowToUseHeader         = "### How to use skills"
	skillsHowToUseRules          = "- Discovery: The list above is the skills available in this session (name + description + file path). Skill bodies live on disk at the listed paths.\n- Trigger rules: If the user names a skill (with `$SkillName` or plain text) OR the task clearly matches a skill's description shown above, you must use that skill for that turn. Multiple mentions mean use them all. Do not carry skills across turns unless re-mentioned.\n- Missing/blocked: If a named skill isn't in the list or the path can't be read, say so briefly and continue with the best fallback.\n- How to use a skill (progressive disclosure):\n  1) After deciding to use a skill, open its `SKILL.md`. Read only enough to follow the workflow.\n  2) When `SKILL.md` references relative paths (e.g., `scripts/foo.py`), resolve them relative to the skill directory listed above first, and only consider other paths if needed.\n  3) If `SKILL.md` points to extra folders such as `references/`, load only the specific files needed for the request; don't bulk-load everything.\n  4) If `scripts/` exist, prefer running or patching them instead of retyping large code blocks.\n  5) If `assets/` or templates exist, reuse them instead of recreating from scratch.\n- Coordination and sequencing:\n  - If multiple skills apply, choose the minimal set that covers the request and state the order you'll use them.\n  - Announce which skill(s) you're using and why (one short line). If you skip an obvious skill, say why.\n- Context hygiene:\n  - Keep context small: summarize long sections instead of pasting them; only load extra files when needed.\n  - Avoid deep reference-chasing: prefer opening only files directly linked from `SKILL.md` unless you're blocked.\n  - When variants exist (frameworks, providers, domains), pick only the relevant reference file(s) and note that choice.\n- Safety and fallback: If a skill can't be applied cleanly (missing files, unclear instructions), state the issue, pick the next-best approach, and continue."
)

type injectedSkill struct {
	Name        string
	Description string
	Path        string
}

type skillFrontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

func skillsContextMessage(workspaceRoot string) (string, bool, error) {
	skills, err := discoverInjectedSkills(workspaceRoot)
	if err != nil {
		return "", false, err
	}
	if len(skills) == 0 {
		return "", false, nil
	}
	return renderSkillsContext(skills), true, nil
}

func discoverInjectedSkills(workspaceRoot string) ([]injectedSkill, error) {
	roots, err := skillsInjectionRoots(workspaceRoot)
	if err != nil {
		return nil, err
	}
	out := make([]injectedSkill, 0)
	seenPaths := map[string]bool{}
	for _, root := range roots {
		entries, readErr := os.ReadDir(root)
		if readErr != nil {
			if os.IsNotExist(readErr) {
				continue
			}
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			skillPath := filepath.Join(root, entry.Name(), skillFileName)
			skill, ok := parseInjectedSkill(skillPath)
			if !ok {
				continue
			}
			if seenPaths[skill.Path] {
				continue
			}
			seenPaths[skill.Path] = true
			out = append(out, skill)
		}
	}
	return out, nil
}

func skillsInjectionRoots(workspaceRoot string) ([]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve home dir: %w", err)
	}

	paths := make([]string, 0, 2)
	seen := map[string]bool{}
	addPath := func(path string) {
		cleaned := filepath.Clean(path)
		if cleaned == "" || seen[cleaned] {
			return
		}
		seen[cleaned] = true
		paths = append(paths, cleaned)
	}

	addPath(filepath.Join(home, agentsGlobalDirName, skillsDirName))
	if strings.TrimSpace(workspaceRoot) != "" {
		addPath(filepath.Join(workspaceRoot, agentsGlobalDirName, skillsDirName))
	}
	return paths, nil
}

func parseInjectedSkill(path string) (injectedSkill, bool) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return injectedSkill{}, false
	}
	frontmatter, ok := extractSkillFrontmatter(string(contents))
	if !ok {
		return injectedSkill{}, false
	}
	var parsed skillFrontmatter
	if err := yaml.Unmarshal([]byte(frontmatter), &parsed); err != nil {
		return injectedSkill{}, false
	}
	name := sanitizeSkillSingleLine(parsed.Name)
	if name == "" {
		name = sanitizeSkillSingleLine(filepath.Base(filepath.Dir(path)))
	}
	description := sanitizeSkillSingleLine(parsed.Description)
	if name == "" || description == "" {
		return injectedSkill{}, false
	}
	resolvedPath := path
	if canonical, err := filepath.EvalSymlinks(path); err == nil {
		resolvedPath = canonical
	}
	return injectedSkill{
		Name:        name,
		Description: description,
		Path:        filepath.ToSlash(resolvedPath),
	}, true
}

func extractSkillFrontmatter(contents string) (string, bool) {
	lines := strings.Split(contents, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return "", false
	}

	frontmatterLines := make([]string, 0)
	foundClosing := false
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "---" {
			foundClosing = true
			break
		}
		frontmatterLines = append(frontmatterLines, line)
	}
	if len(frontmatterLines) == 0 || !foundClosing {
		return "", false
	}
	return strings.Join(frontmatterLines, "\n"), true
}

func sanitizeSkillSingleLine(raw string) string {
	parts := strings.Fields(raw)
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " ")
}

func renderSkillsContext(skills []injectedSkill) string {
	lines := make([]string, 0, len(skills)+5)
	lines = append(lines, skillsInjectedHeader)
	lines = append(lines, skillsInjectedDescription)
	lines = append(lines, skillsAvailableHeader)
	for _, skill := range skills {
		lines = append(lines, fmt.Sprintf("- %s: %s (file: %s)", skill.Name, skill.Description, skill.Path))
	}
	lines = append(lines, skillsHowToUseHeader)
	lines = append(lines, skillsHowToUseRules)
	return strings.Join(lines, "\n")
}
