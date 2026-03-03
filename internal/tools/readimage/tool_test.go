package readimage

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"builder/internal/tools"
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

	tool := New(workspace, true)
	result, err := tool.Call(context.Background(), tools.Call{
		ID:    "call-1",
		Name:  tools.ToolViewImage,
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

	tool := New(workspace, true)
	result, err := tool.Call(context.Background(), tools.Call{
		ID:    "call-1",
		Name:  tools.ToolViewImage,
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

	tool := New(workspace, true)
	result, err := tool.Call(context.Background(), tools.Call{
		ID:    "call-1",
		Name:  tools.ToolViewImage,
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

	tool := New(workspace, true)
	result, err := tool.Call(context.Background(), tools.Call{
		ID:    "call-1",
		Name:  tools.ToolViewImage,
		Input: json.RawMessage(`{"path":"."}`),
	})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected tool error result for directory path")
	}
}

func TestCall_UnsupportedModelReturnsToolError(t *testing.T) {
	workspace := t.TempDir()
	tool := New(workspace, false)
	result, err := tool.Call(context.Background(), tools.Call{
		ID:    "call-1",
		Name:  tools.ToolViewImage,
		Input: json.RawMessage(`{"path":"img.png"}`),
	})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected tool error result for unsupported model")
	}
}
