package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"builder/internal/llm"
)

func TestSkillsContextMessageIncludesCodexPromptAndSkillEntries(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	workspace := t.TempDir()
	homeSkillPath := writeTestSkill(t, filepath.Join(home, ".builder", "skills", "home-skill"), "home-skill", "from home")
	workspaceSkillPath := writeTestSkill(t, filepath.Join(workspace, ".builder", "skills", "workspace-skill"), "workspace-skill", "from workspace")

	content, found, err := skillsContextMessage(workspace)
	if err != nil {
		t.Fatalf("skillsContextMessage: %v", err)
	}
	if !found {
		t.Fatal("expected skills context to be found")
	}

	for _, required := range []string{
		skillsInjectedHeader,
		skillsInjectedDescription,
		skillsAvailableHeader,
		skillsHowToUseHeader,
		"- home-skill: from home (file: " + filepath.ToSlash(homeSkillPath) + ")",
		"- workspace-skill: from workspace (file: " + filepath.ToSlash(workspaceSkillPath) + ")",
		"- Trigger rules: If the task matches a skill's description shown above",
	} {
		if !strings.Contains(content, required) {
			t.Fatalf("expected skills context to include %q, got %q", required, content)
		}
	}
}

func TestSkillsContextMessageSkipsInvalidSkills(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	workspace := t.TempDir()
	invalidSkillDir := filepath.Join(workspace, ".builder", "skills", "invalid")
	if err := os.MkdirAll(invalidSkillDir, 0o755); err != nil {
		t.Fatalf("mkdir invalid skill dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(invalidSkillDir, skillFileName), []byte("---\nname: invalid\n---\n"), 0o644); err != nil {
		t.Fatalf("write invalid skill: %v", err)
	}

	content, found, err := skillsContextMessage(workspace)
	if err != nil {
		t.Fatalf("skillsContextMessage: %v", err)
	}
	if found {
		t.Fatalf("expected no skills context for invalid skill, got %q", content)
	}
}

func TestAppendMissingReviewerMetaContextPrependsSkillsWhenMissing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	workspace := t.TempDir()
	writeTestSkill(t, filepath.Join(workspace, ".builder", "skills", "workspace-skill"), "workspace-skill", "from workspace")

	in := []llm.Message{{Role: llm.RoleUser, Content: "request"}}
	got := appendMissingReviewerMetaContext(in, workspace, "gpt-5", "high", false)
	if len(got) != 3 {
		t.Fatalf("expected skills+environment prepended plus original, got %d", len(got))
	}
	if got[0].Role != llm.RoleDeveloper || got[0].MessageType != llm.MessageTypeSkills || !strings.Contains(got[0].Content, skillsAvailableHeader) {
		t.Fatalf("expected first prepended message to be skills context, got %+v", got[0])
	}
	if got[1].Role != llm.RoleDeveloper || got[1].MessageType != llm.MessageTypeEnvironment || !strings.Contains(got[1].Content, environmentInjectedHeader) {
		t.Fatalf("expected second prepended message to be environment context, got %+v", got[1])
	}
	if got[2].Role != llm.RoleUser || got[2].Content != "request" {
		t.Fatalf("expected original message at tail, got %+v", got[2])
	}
}

func TestSplitReviewerMetaMessagesDeduplicatesSkillsContext(t *testing.T) {
	skillsMessage := llm.Message{Role: llm.RoleDeveloper, MessageType: llm.MessageTypeSkills, Content: "## Skills\n### Available skills"}
	messages := []llm.Message{
		skillsMessage,
		skillsMessage,
		{Role: llm.RoleDeveloper, MessageType: llm.MessageTypeEnvironment, Content: environmentInjectedHeader + "\nOS: darwin"},
		{Role: llm.RoleUser, Content: "request"},
	}

	meta, transcript := splitReviewerMetaMessages(messages)
	if len(meta) != 2 {
		t.Fatalf("expected one skills + one environment meta message, got %d", len(meta))
	}
	if meta[0].MessageType != llm.MessageTypeSkills {
		t.Fatalf("expected first meta message to be skills context, got %+v", meta[0])
	}
	if meta[1].MessageType != llm.MessageTypeEnvironment {
		t.Fatalf("expected second meta message to be environment context, got %+v", meta[1])
	}
	if len(transcript) != 1 || transcript[0].Role != llm.RoleUser || transcript[0].Content != "request" {
		t.Fatalf("expected transcript to contain only user request, got %+v", transcript)
	}
}

func TestSplitReviewerMetaMessagesTreatsHeadlessContextAsMeta(t *testing.T) {
	headless := llm.Message{Role: llm.RoleDeveloper, MessageType: llm.MessageTypeHeadlessMode, Content: "headless mode instructions"}
	messages := []llm.Message{
		headless,
		headless,
		{Role: llm.RoleUser, Content: "request"},
	}

	meta, transcript := splitReviewerMetaMessages(messages)
	if len(meta) != 1 {
		t.Fatalf("expected one headless meta message, got %d", len(meta))
	}
	if meta[0].MessageType != llm.MessageTypeHeadlessMode {
		t.Fatalf("expected headless meta message, got %+v", meta[0])
	}
	if len(transcript) != 1 || transcript[0].Role != llm.RoleUser || transcript[0].Content != "request" {
		t.Fatalf("expected transcript to contain only user request, got %+v", transcript)
	}
}

func TestSplitReviewerMetaMessagesTreatsHeadlessExitContextAsMeta(t *testing.T) {
	headlessExit := llm.Message{Role: llm.RoleDeveloper, MessageType: llm.MessageTypeHeadlessModeExit, Content: "interactive mode instructions"}
	messages := []llm.Message{
		headlessExit,
		headlessExit,
		{Role: llm.RoleUser, Content: "request"},
	}

	meta, transcript := splitReviewerMetaMessages(messages)
	if len(meta) != 1 {
		t.Fatalf("expected one headless exit meta message, got %d", len(meta))
	}
	if meta[0].MessageType != llm.MessageTypeHeadlessModeExit {
		t.Fatalf("expected headless exit meta message, got %+v", meta[0])
	}
	if len(transcript) != 1 || transcript[0].Role != llm.RoleUser || transcript[0].Content != "request" {
		t.Fatalf("expected transcript to contain only user request, got %+v", transcript)
	}
}

func TestBuildReviewerTranscriptMessagesSkipsSkillsContextEntries(t *testing.T) {
	messages := []llm.Message{
		{Role: llm.RoleDeveloper, MessageType: llm.MessageTypeSkills, Content: "## Skills\n### Available skills\n- demo: desc"},
		{Role: llm.RoleUser, Content: "request"},
	}

	transcript := buildReviewerTranscriptMessages(messages)
	if len(transcript) != 1 {
		t.Fatalf("expected one transcript entry, got %d", len(transcript))
	}
	if !strings.Contains(transcript[0].Content, "User:") || !strings.Contains(transcript[0].Content, "request") {
		t.Fatalf("expected transcript entry to include user request, got %q", transcript[0].Content)
	}
	if strings.Contains(transcript[0].Content, skillsInjectedHeader) {
		t.Fatalf("did not expect skills context in transcript entry, got %q", transcript[0].Content)
	}
}

func writeTestSkill(t *testing.T, dir string, name string, description string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	skillPath := filepath.Join(dir, skillFileName)
	content := "---\nname: " + name + "\ndescription: " + description + "\n---\n\n# Body\n"
	if err := os.WriteFile(skillPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write skill file: %v", err)
	}
	if canonical, err := filepath.EvalSymlinks(skillPath); err == nil {
		return canonical
	}
	return skillPath
}
