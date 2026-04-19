package readimage

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode"

	"builder/server/tools"
	patchtool "builder/server/tools/patch"
	"builder/shared/toolspec"
)

var tinyPNG = []byte{
	137, 80, 78, 71, 13, 10, 26, 10, 0, 0, 0, 13, 73, 72, 68, 82, 0, 0, 0, 1, 0, 0, 0, 1,
	8, 6, 0, 0, 0, 31, 21, 196, 137, 0, 0, 0, 11, 73, 68, 65, 84, 120, 156, 99, 96, 0, 2,
	0, 0, 5, 0, 1, 122, 94, 171, 63, 0, 0, 0, 0, 73, 69, 78, 68, 174, 66, 96, 130,
}

func TestCall_ImagePathReturnsInputImageContentItem(t *testing.T) {
	workspace := t.TempDir()
	imagePath := filepath.Join(workspace, "img.png")
	if err := os.WriteFile(imagePath, tinyPNG, 0o644); err != nil {
		t.Fatalf("write image: %v", err)
	}

	tool, err := New(workspace, true)
	if err != nil {
		t.Fatalf("new tool: %v", err)
	}

	result, err := tool.Call(context.Background(), tools.Call{
		ID:    "call-1",
		Name:  toolspec.ToolViewImage,
		Input: json.RawMessage(`{"path":"img.png"}`),
	})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success result, got error payload: %s", string(result.Output))
	}

	var items []map[string]any
	if err := json.Unmarshal(result.Output, &items); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected one content item, got %d", len(items))
	}
	if got := items[0]["type"]; got != "input_image" {
		t.Fatalf("expected input_image type, got %#v", got)
	}
	url, ok := items[0]["image_url"].(string)
	if !ok {
		t.Fatalf("expected image_url string, got %#v", items[0]["image_url"])
	}
	prefix := "data:image/png;base64,"
	if !strings.HasPrefix(url, prefix) {
		t.Fatalf("expected png data URL prefix, got %q", url)
	}
	decoded, decodeErr := base64.StdEncoding.DecodeString(strings.TrimPrefix(url, prefix))
	if decodeErr != nil {
		t.Fatalf("decode base64 image: %v", decodeErr)
	}
	if string(decoded) != string(tinyPNG) {
		t.Fatalf("decoded image bytes mismatch")
	}
}

func TestCall_PDFPathReturnsInputFileContentItem(t *testing.T) {
	workspace := t.TempDir()
	pdfBytes := []byte("%PDF-1.4\n1 0 obj\n<<>>\nendobj\ntrailer\n<<>>\n%%EOF\n")
	pdfPath := filepath.Join(workspace, "doc.pdf")
	if err := os.WriteFile(pdfPath, pdfBytes, 0o644); err != nil {
		t.Fatalf("write pdf: %v", err)
	}

	tool, err := New(workspace, true)
	if err != nil {
		t.Fatalf("new tool: %v", err)
	}

	result, err := tool.Call(context.Background(), tools.Call{
		ID:    "call-1",
		Name:  toolspec.ToolViewImage,
		Input: json.RawMessage(`{"path":"doc.pdf"}`),
	})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success result, got error payload: %s", string(result.Output))
	}

	var items []map[string]any
	if err := json.Unmarshal(result.Output, &items); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected one content item, got %d", len(items))
	}
	if got := items[0]["type"]; got != "input_file" {
		t.Fatalf("expected input_file type, got %#v", got)
	}
	if got := items[0]["filename"]; got != "doc.pdf" {
		t.Fatalf("expected filename doc.pdf, got %#v", got)
	}
	encoded, ok := items[0]["file_data"].(string)
	if !ok {
		t.Fatalf("expected file_data string, got %#v", items[0]["file_data"])
	}
	const prefix = "data:application/pdf;base64,"
	if !strings.HasPrefix(encoded, prefix) {
		t.Fatalf("expected data URL prefix %q, got %q", prefix, encoded)
	}
	decoded, decodeErr := base64.StdEncoding.DecodeString(strings.TrimPrefix(encoded, prefix))
	if decodeErr != nil {
		t.Fatalf("decode base64 file_data: %v", decodeErr)
	}
	if string(decoded) != string(pdfBytes) {
		t.Fatalf("decoded PDF bytes mismatch")
	}
}

func TestCall_UnsupportedFileReturnsToolError(t *testing.T) {
	workspace := t.TempDir()
	textPath := filepath.Join(workspace, "note.txt")
	if err := os.WriteFile(textPath, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write text file: %v", err)
	}

	tool, err := New(workspace, true)
	if err != nil {
		t.Fatalf("new tool: %v", err)
	}

	result, err := tool.Call(context.Background(), tools.Call{
		ID:    "call-1",
		Name:  toolspec.ToolViewImage,
		Input: json.RawMessage(`{"path":"note.txt"}`),
	})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected tool error result for unsupported file type")
	}
}

func TestCall_DirectoryPathReturnsToolError(t *testing.T) {
	workspace := t.TempDir()

	tool, err := New(workspace, true)
	if err != nil {
		t.Fatalf("new tool: %v", err)
	}

	result, err := tool.Call(context.Background(), tools.Call{
		ID:    "call-1",
		Name:  toolspec.ToolViewImage,
		Input: json.RawMessage(`{"path":"."}`),
	})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected tool error result for directory path")
	}
}

func TestCall_OversizedFileReturnsCompressionGuidance(t *testing.T) {
	workspace := t.TempDir()
	oversized := make([]byte, int(maxFileSizeBytes)+1)

	for _, name := range []string{"huge.png", "huge.pdf"} {
		path := filepath.Join(workspace, name)
		if err := os.WriteFile(path, oversized, 0o644); err != nil {
			t.Fatalf("write oversized file %q: %v", name, err)
		}
	}

	tool, err := New(workspace, true)
	if err != nil {
		t.Fatalf("new tool: %v", err)
	}

	for _, name := range []string{"huge.png", "huge.pdf"} {
		name := name
		t.Run(name, func(t *testing.T) {
			result, callErr := tool.Call(context.Background(), tools.Call{
				ID:    "call-oversized",
				Name:  toolspec.ToolViewImage,
				Input: json.RawMessage(`{"path":"` + name + `"}`),
			})
			if callErr != nil {
				t.Fatalf("call: %v", callErr)
			}
			if !result.IsError {
				t.Fatalf("expected tool error result for oversized file")
			}
			errMessage := toolError(t, result)
			if !strings.Contains(errMessage, "max supported size is 512000 bytes (500 KiB)") {
				t.Fatalf("expected size limit in error, got %q", errMessage)
			}
			if !strings.Contains(errMessage, "compress the image or PDF and try again") {
				t.Fatalf("expected compression guidance in error, got %q", errMessage)
			}
		})
	}
}

func TestCall_FileSizeBoundary(t *testing.T) {
	workspace := t.TempDir()
	exactPath := filepath.Join(workspace, "exact.png")
	oversizedPath := filepath.Join(workspace, "oversized.png")

	if err := os.WriteFile(exactPath, make([]byte, int(maxFileSizeBytes)), 0o644); err != nil {
		t.Fatalf("write exact-size file: %v", err)
	}
	if err := os.WriteFile(oversizedPath, make([]byte, int(maxFileSizeBytes)+1), 0o644); err != nil {
		t.Fatalf("write oversized file: %v", err)
	}

	tool, err := New(workspace, true)
	if err != nil {
		t.Fatalf("new tool: %v", err)
	}

	exactResult, err := tool.Call(context.Background(), tools.Call{
		ID:    "call-exact-size",
		Name:  toolspec.ToolViewImage,
		Input: json.RawMessage(`{"path":"exact.png"}`),
	})
	if err != nil {
		t.Fatalf("exact-size call: %v", err)
	}
	if exactResult.IsError {
		t.Fatalf("expected exact-size file to be allowed, got %s", string(exactResult.Output))
	}

	oversizedResult, err := tool.Call(context.Background(), tools.Call{
		ID:    "call-oversized-size",
		Name:  toolspec.ToolViewImage,
		Input: json.RawMessage(`{"path":"oversized.png"}`),
	})
	if err != nil {
		t.Fatalf("oversized call: %v", err)
	}
	if !oversizedResult.IsError {
		t.Fatalf("expected oversized file to be rejected")
	}
}

func TestCall_UnsupportedModelReturnsToolError(t *testing.T) {
	workspace := t.TempDir()
	tool, err := New(workspace, false)
	if err != nil {
		t.Fatalf("new tool: %v", err)
	}

	result, err := tool.Call(context.Background(), tools.Call{
		ID:    "call-1",
		Name:  toolspec.ToolViewImage,
		Input: json.RawMessage(`{"path":"img.png"}`),
	})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected tool error result for unsupported model")
	}
}

func TestCall_PathTraversalOutsideWorkspaceRejectedByDefault(t *testing.T) {
	parent := outsideNonTempDir(t)
	workspace := filepath.Join(parent, "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	outsidePath := filepath.Join(parent, "outside.png")
	if err := os.WriteFile(outsidePath, tinyPNG, 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}

	tool, err := New(workspace, true)
	if err != nil {
		t.Fatalf("new tool: %v", err)
	}

	result, err := tool.Call(context.Background(), tools.Call{
		ID:    "call-traversal",
		Name:  toolspec.ToolViewImage,
		Input: json.RawMessage(`{"path":"../outside.png"}`),
	})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected error for outside-workspace traversal path")
	}
	if !strings.Contains(toolError(t, result), "outside workspace") {
		t.Fatalf("expected outside workspace error, got %q", toolError(t, result))
	}
}

func TestCall_SymlinkEscapeOutsideWorkspaceRejectedByDefault(t *testing.T) {
	workspace := t.TempDir()
	outside := filepath.Join(outsideNonTempDir(t), "outside.png")
	if err := os.WriteFile(outside, tinyPNG, 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	linkPath := filepath.Join(workspace, "symlink.png")
	if err := os.Symlink(outside, linkPath); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	tool, err := New(workspace, true)
	if err != nil {
		t.Fatalf("new tool: %v", err)
	}

	result, err := tool.Call(context.Background(), tools.Call{
		ID:    "call-symlink",
		Name:  toolspec.ToolViewImage,
		Input: json.RawMessage(`{"path":"symlink.png"}`),
	})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected error for symlink escape outside workspace")
	}
	if !strings.Contains(toolError(t, result), "outside workspace") {
		t.Fatalf("expected outside workspace error, got %q", toolError(t, result))
	}
}

func TestCall_OutsideWorkspaceTempDirAllowedWithoutApproval(t *testing.T) {
	workspace := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.png")
	if err := os.WriteFile(outside, tinyPNG, 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}

	approveCalls := 0
	tool, err := New(
		workspace,
		true,
		WithOutsideWorkspaceApprover(func(context.Context, patchtool.OutsideWorkspaceRequest) (patchtool.OutsideWorkspaceApproval, error) {
			approveCalls++
			return patchtool.OutsideWorkspaceApproval{Decision: patchtool.OutsideWorkspaceDecisionDeny}, nil
		}),
	)
	if err != nil {
		t.Fatalf("new tool: %v", err)
	}

	input := json.RawMessage(`{"path":"` + strings.ReplaceAll(outside, `\`, `\\`) + `"}`)
	result, err := tool.Call(context.Background(), tools.Call{ID: "call-temp-allow", Name: toolspec.ToolViewImage, Input: input})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success for temp outside path, got %s", string(result.Output))
	}
	if approveCalls != 0 {
		t.Fatalf("expected temp outside path to bypass approver, got %d calls", approveCalls)
	}
}

func TestCall_OutsideWorkspaceAllowSessionSkipsFuturePrompts(t *testing.T) {
	workspace := t.TempDir()
	outsideRoot := outsideNonTempDir(t)
	outside1 := filepath.Join(outsideRoot, "outside1.png")
	outside2 := filepath.Join(outsideRoot, "outside2.png")
	if err := os.WriteFile(outside1, tinyPNG, 0o644); err != nil {
		t.Fatalf("write outside1: %v", err)
	}
	if err := os.WriteFile(outside2, tinyPNG, 0o644); err != nil {
		t.Fatalf("write outside2: %v", err)
	}

	approveCalls := 0
	tool, err := New(
		workspace,
		true,
		WithOutsideWorkspaceApprover(func(context.Context, patchtool.OutsideWorkspaceRequest) (patchtool.OutsideWorkspaceApproval, error) {
			approveCalls++
			return patchtool.OutsideWorkspaceApproval{Decision: patchtool.OutsideWorkspaceDecisionAllowSession}, nil
		}),
	)
	if err != nil {
		t.Fatalf("new tool: %v", err)
	}

	result, err := tool.Call(context.Background(), tools.Call{
		ID:    "call-1",
		Name:  toolspec.ToolViewImage,
		Input: json.RawMessage(`{"path":"` + strings.ReplaceAll(outside1, `\`, `\\`) + `"}`),
	})
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected first call success, got %s", string(result.Output))
	}

	result, err = tool.Call(context.Background(), tools.Call{
		ID:    "call-2",
		Name:  toolspec.ToolViewImage,
		Input: json.RawMessage(`{"path":"` + strings.ReplaceAll(outside2, `\`, `\\`) + `"}`),
	})
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected second call success, got %s", string(result.Output))
	}

	if approveCalls != 1 {
		t.Fatalf("expected one approval call, got %d", approveCalls)
	}
}

func TestCall_OutsideWorkspaceAllowOncePromptsEachCall(t *testing.T) {
	workspace := t.TempDir()
	outside := filepath.Join(outsideNonTempDir(t), "outside.png")
	if err := os.WriteFile(outside, tinyPNG, 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}

	approveCalls := 0
	tool, err := New(
		workspace,
		true,
		WithOutsideWorkspaceApprover(func(context.Context, patchtool.OutsideWorkspaceRequest) (patchtool.OutsideWorkspaceApproval, error) {
			approveCalls++
			return patchtool.OutsideWorkspaceApproval{Decision: patchtool.OutsideWorkspaceDecisionAllowOnce}, nil
		}),
	)
	if err != nil {
		t.Fatalf("new tool: %v", err)
	}

	input := json.RawMessage(`{"path":"` + strings.ReplaceAll(outside, `\`, `\\`) + `"}`)
	result, err := tool.Call(context.Background(), tools.Call{ID: "call-1", Name: toolspec.ToolViewImage, Input: input})
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected first call success, got %s", string(result.Output))
	}

	result, err = tool.Call(context.Background(), tools.Call{ID: "call-2", Name: toolspec.ToolViewImage, Input: input})
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected second call success, got %s", string(result.Output))
	}

	if approveCalls != 2 {
		t.Fatalf("expected two approval calls, got %d", approveCalls)
	}
}

func TestCall_OutsideWorkspaceApprovalAuditsResolvedPath(t *testing.T) {
	workspace := t.TempDir()
	outside := filepath.Join(outsideNonTempDir(t), "outside.png")
	if err := os.WriteFile(outside, tinyPNG, 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}

	audits := make([]OutsideWorkspaceAudit, 0, 2)
	tool, err := New(
		workspace,
		true,
		WithOutsideWorkspaceApprover(func(context.Context, patchtool.OutsideWorkspaceRequest) (patchtool.OutsideWorkspaceApproval, error) {
			return patchtool.OutsideWorkspaceApproval{Decision: patchtool.OutsideWorkspaceDecisionAllowSession}, nil
		}),
		WithOutsideWorkspaceAuditLogger(func(entry OutsideWorkspaceAudit) {
			audits = append(audits, entry)
		}),
	)
	if err != nil {
		t.Fatalf("new tool: %v", err)
	}

	input := json.RawMessage(`{"path":"` + strings.ReplaceAll(outside, `\`, `\\`) + `"}`)
	result, err := tool.Call(context.Background(), tools.Call{ID: "call-1", Name: toolspec.ToolViewImage, Input: input})
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected first call success, got %s", string(result.Output))
	}

	result, err = tool.Call(context.Background(), tools.Call{ID: "call-2", Name: toolspec.ToolViewImage, Input: input})
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected second call success, got %s", string(result.Output))
	}

	if len(audits) != 2 {
		t.Fatalf("expected 2 audit entries, got %d", len(audits))
	}
	realOutside, err := filepath.EvalSymlinks(outside)
	if err != nil {
		t.Fatalf("resolve outside real path: %v", err)
	}
	if audits[0].ResolvedPath != realOutside {
		t.Fatalf("unexpected first audit resolved path: %q", audits[0].ResolvedPath)
	}
	if audits[0].Reason != "allow_session" {
		t.Fatalf("unexpected first audit reason: %q", audits[0].Reason)
	}
	if audits[1].Reason != "session_allow" {
		t.Fatalf("unexpected second audit reason: %q", audits[1].Reason)
	}
}

func TestCall_OutsideWorkspaceApprovalFailureUsesReadSpecificWording(t *testing.T) {
	workspace := t.TempDir()
	outside := filepath.Join(outsideNonTempDir(t), "outside.png")
	if err := os.WriteFile(outside, tinyPNG, 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}

	tool, err := New(
		workspace,
		true,
		WithOutsideWorkspaceApprover(func(context.Context, patchtool.OutsideWorkspaceRequest) (patchtool.OutsideWorkspaceApproval, error) {
			return patchtool.OutsideWorkspaceApproval{}, errors.New("ask failed")
		}),
	)
	if err != nil {
		t.Fatalf("new tool: %v", err)
	}

	input := json.RawMessage(`{"path":"` + strings.ReplaceAll(outside, `\`, `\\`) + `"}`)
	result, err := tool.Call(context.Background(), tools.Call{ID: "call-approval-error", Name: toolspec.ToolViewImage, Input: input})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected error result")
	}
	errMessage := toolError(t, result)
	if !strings.Contains(errMessage, "outside-workspace read approval failed") {
		t.Fatalf("expected read approval failure wording, got %q", errMessage)
	}
	if strings.Contains(errMessage, "edit approval failed") || strings.Contains(errMessage, "patch target outside workspace") {
		t.Fatalf("unexpected patch wording, got %q", errMessage)
	}
}

func TestCall_OutsideWorkspaceRejectionIncludesReadSpecificGuidance(t *testing.T) {
	workspace := t.TempDir()
	outside := filepath.Join(outsideNonTempDir(t), "outside.png")
	if err := os.WriteFile(outside, tinyPNG, 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}

	tool, err := New(
		workspace,
		true,
		WithOutsideWorkspaceApprover(func(context.Context, patchtool.OutsideWorkspaceRequest) (patchtool.OutsideWorkspaceApproval, error) {
			return patchtool.OutsideWorkspaceApproval{Decision: patchtool.OutsideWorkspaceDecisionDeny, Commentary: "keep it inside the repo"}, nil
		}),
	)
	if err != nil {
		t.Fatalf("new tool: %v", err)
	}

	input := json.RawMessage(`{"path":"` + strings.ReplaceAll(outside, `\`, `\\`) + `"}`)
	result, err := tool.Call(context.Background(), tools.Call{ID: "call-deny-guidance", Name: toolspec.ToolViewImage, Input: input})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected error result")
	}
	errMessage := toolError(t, result)
	want := `view_image path outside workspace rejected by user: ` + outside + `. User rejected the approval request for this tool call, and said: "keep it inside the repo". Do not attempt to circumvent, hack around, or re-execute the same path. Treat this rejection as authoritative. If it's essential to the task, ask the user to place the file inside the workspace root.`
	if errMessage != want {
		t.Fatalf("unexpected rejection error, got %q want %q", errMessage, want)
	}
}

func TestCall_CaseVariantAbsolutePathInsideWorkspaceDoesNotTriggerOutsideApproval(t *testing.T) {
	workspace := t.TempDir()
	imagePath := filepath.Join(workspace, "img.png")
	if err := os.WriteFile(imagePath, tinyPNG, 0o644); err != nil {
		t.Fatalf("write image: %v", err)
	}

	variantWorkspace, ok := findCaseVariantExistingAlias(workspace)
	if !ok {
		t.Skip("filesystem does not provide a case-variant alias for workspace path")
	}
	variantImagePath := filepath.Join(variantWorkspace, "img.png")

	approveCalls := 0
	tool, err := New(
		workspace,
		true,
		WithOutsideWorkspaceApprover(func(context.Context, patchtool.OutsideWorkspaceRequest) (patchtool.OutsideWorkspaceApproval, error) {
			approveCalls++
			return patchtool.OutsideWorkspaceApproval{Decision: patchtool.OutsideWorkspaceDecisionDeny}, nil
		}),
	)
	if err != nil {
		t.Fatalf("new tool: %v", err)
	}

	input := json.RawMessage(`{"path":"` + strings.ReplaceAll(variantImagePath, `\`, `\\`) + `"}`)
	result, err := tool.Call(context.Background(), tools.Call{ID: "call-case-variant", Name: toolspec.ToolViewImage, Input: input})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success for case-variant absolute in-workspace path, got %s", string(result.Output))
	}
	if approveCalls != 0 {
		t.Fatalf("expected no outside-workspace approval prompts, got %d", approveCalls)
	}
}

func findCaseVariantExistingAlias(path string) (string, bool) {
	canonical := filepath.Clean(path)
	canonicalInfo, err := os.Stat(canonical)
	if err != nil {
		return "", false
	}
	if candidate, ok := caseAliasUsersSubstitution(canonical, canonicalInfo); ok {
		return candidate, true
	}

	parts := strings.Split(canonical, string(filepath.Separator))
	start := 0
	if filepath.IsAbs(canonical) && len(parts) > 0 && parts[0] == "" {
		start = 1
	}

	for idx := start; idx < len(parts); idx++ {
		variantPart := toggleFirstLetterCase(parts[idx])
		if variantPart == parts[idx] {
			continue
		}
		candidateParts := append([]string(nil), parts...)
		candidateParts[idx] = variantPart
		candidate := strings.Join(candidateParts, string(filepath.Separator))
		if candidate == canonical {
			continue
		}
		candidateInfo, statErr := os.Stat(candidate)
		if statErr != nil {
			continue
		}
		if os.SameFile(candidateInfo, canonicalInfo) {
			return candidate, true
		}
	}

	return "", false
}

func outsideNonTempDir(t *testing.T) string {
	t.Helper()
	bases := make([]string, 0, 2)
	if wd, err := os.Getwd(); err == nil {
		bases = append(bases, wd)
	}
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		bases = append(bases, home)
	}
	for _, base := range bases {
		dir, err := os.MkdirTemp(base, "builder-readimage-outside-*")
		if err != nil {
			continue
		}
		abs, err := filepath.Abs(dir)
		if err != nil {
			_ = os.RemoveAll(dir)
			continue
		}
		if patchtool.IsPathInTemporaryDir(abs) {
			_ = os.RemoveAll(dir)
			continue
		}
		t.Cleanup(func() {
			_ = os.RemoveAll(dir)
		})
		return abs
	}
	t.Skip("unable to create non-temporary outside directory for test")
	return ""
}

func caseAliasUsersSubstitution(canonical string, canonicalInfo os.FileInfo) (string, bool) {
	if strings.HasPrefix(canonical, "/Users/") {
		candidate := "/users/" + strings.TrimPrefix(canonical, "/Users/")
		if info, err := os.Stat(candidate); err == nil && os.SameFile(info, canonicalInfo) {
			return candidate, true
		}
	}
	if strings.HasPrefix(canonical, "/users/") {
		candidate := "/Users/" + strings.TrimPrefix(canonical, "/users/")
		if info, err := os.Stat(candidate); err == nil && os.SameFile(info, canonicalInfo) {
			return candidate, true
		}
	}
	return "", false
}

func toggleFirstLetterCase(value string) string {
	runes := []rune(value)
	if len(runes) == 0 {
		return value
	}
	first := runes[0]
	upper := unicode.ToUpper(first)
	lower := unicode.ToLower(first)
	if first == upper && first == lower {
		return value
	}
	if first == upper {
		runes[0] = lower
		return string(runes)
	}
	runes[0] = upper
	return string(runes)
}

func toolError(t *testing.T, result tools.Result) string {
	t.Helper()
	payload := map[string]string{}
	if err := json.Unmarshal(result.Output, &payload); err != nil {
		t.Fatalf("decode tool error output: %v", err)
	}
	return payload["error"]
}
