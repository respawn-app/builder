package readimage

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"builder/server/tools"
	patchtool "builder/server/tools/patch"
	"builder/shared/toolspec"
)

const maxFileSizeBytes int64 = 500 << 10

const outsideWorkspaceRejectionInstruction = "If it's essential to the task, ask the user to place the file inside the workspace root."

var supportedImageMIMEs = map[string]struct{}{
	"image/png":  {},
	"image/jpeg": {},
	"image/gif":  {},
	"image/webp": {},
}

type Tool struct {
	workspaceRoot             string
	workspaceRootReal         string
	workspaceRootInfo         os.FileInfo
	workspaceOnly             bool
	allowOutsideWorkspace     bool
	outsideWorkspaceApprover  patchtool.OutsideWorkspaceApprover
	outsideWorkspaceAudit     OutsideWorkspaceAuditLogger
	outsideWorkspaceSessionMu sync.RWMutex
	outsideWorkspaceAllowed   bool
	supported                 bool
}

type OutsideWorkspaceAudit struct {
	RequestedPath string
	ResolvedPath  string
	Reason        string
}

type OutsideWorkspaceAuditLogger func(OutsideWorkspaceAudit)

type Option func(*Tool)

func WithAllowOutsideWorkspace(allow bool) Option {
	return func(t *Tool) {
		t.allowOutsideWorkspace = allow
	}
}

func WithOutsideWorkspaceApprover(approver patchtool.OutsideWorkspaceApprover) Option {
	return func(t *Tool) {
		t.outsideWorkspaceApprover = approver
	}
}

func WithOutsideWorkspaceAuditLogger(logger OutsideWorkspaceAuditLogger) Option {
	return func(t *Tool) {
		t.outsideWorkspaceAudit = logger
	}
}

type input struct {
	Path string `json:"path"`
}

type contentItem struct {
	Type     string `json:"type"`
	ImageURL string `json:"image_url,omitempty"`
	FileData string `json:"file_data,omitempty"`
	Filename string `json:"filename,omitempty"`
}

func New(workspaceRoot string, supported bool, opts ...Option) (*Tool, error) {
	rootAbs, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace root: %w", err)
	}
	rootReal, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, tools.WrapMissingWorkspaceRootError(rootAbs, fmt.Errorf("resolve workspace real path: %w", err))
		}
		return nil, fmt.Errorf("resolve workspace real path: %w", err)
	}
	rootInfo, err := os.Stat(rootReal)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, tools.WrapMissingWorkspaceRootError(rootAbs, fmt.Errorf("stat workspace root: %w", err))
		}
		return nil, fmt.Errorf("stat workspace root: %w", err)
	}
	t := &Tool{workspaceRoot: rootAbs, workspaceRootReal: rootReal, workspaceRootInfo: rootInfo, workspaceOnly: true, supported: supported}
	for _, opt := range opts {
		if opt != nil {
			opt(t)
		}
	}
	return t, nil
}

func (t *Tool) Name() toolspec.ID {
	return toolspec.ToolViewImage
}

func (t *Tool) Call(ctx context.Context, c tools.Call) (tools.Result, error) {
	if !t.supported {
		return tools.ErrorResult(c, "view_image is not allowed because this model does not support image/file inputs"), nil
	}

	var in input
	if err := json.Unmarshal(c.Input, &in); err != nil {
		return tools.ErrorResult(c, fmt.Sprintf("invalid input: %v", err)), nil
	}
	requestedPath := strings.TrimSpace(in.Path)
	if requestedPath == "" {
		return tools.ErrorResult(c, "path is required"), nil
	}

	approvedOutside := map[string]bool{}
	resolvedPath, err := t.resolvePath(ctx, requestedPath, approvedOutside)
	if err != nil {
		return tools.ErrorResult(c, err.Error()), nil
	}

	info, err := os.Stat(resolvedPath)
	if err != nil {
		return tools.ErrorResult(c, fmt.Sprintf("unable to locate file at %q: %v", resolvedPath, err)), nil
	}
	if !info.Mode().IsRegular() {
		return tools.ErrorResult(c, fmt.Sprintf("path %q is not a regular file", resolvedPath)), nil
	}
	if info.Size() > maxFileSizeBytes {
		return tools.ErrorResult(c, fmt.Sprintf("file %q is too large (%d bytes). max supported size is %d bytes (500 KiB). compress the image or PDF and try again", resolvedPath, info.Size(), maxFileSizeBytes)), nil
	}

	data, err := os.ReadFile(resolvedPath)
	if err != nil {
		return tools.ErrorResult(c, fmt.Sprintf("unable to read file at %q: %v", resolvedPath, err)), nil
	}
	mimeType := detectFileMIME(resolvedPath, data)

	items, buildErr := buildContentItemsForFile(resolvedPath, mimeType, data)
	if buildErr != nil {
		return tools.ErrorResult(c, buildErr.Error()), nil
	}
	body, marshalErr := json.Marshal(items)
	if marshalErr != nil {
		return tools.Result{}, marshalErr
	}

	return tools.Result{CallID: c.ID, Name: c.Name, Output: body}, nil
}

func (t *Tool) resolvePath(ctx context.Context, path string, approvedOutside map[string]bool) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("path is required")
	}

	candidate := path
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(t.workspaceRoot, candidate)
	}
	candidate = filepath.Clean(candidate)
	abs, err := filepath.Abs(candidate)
	if err != nil {
		return "", fmt.Errorf("resolve absolute path %q: %w", path, err)
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("resolve path %q: %w", path, err)
	}
	real = filepath.Clean(real)

	guard := patchtool.NewOutsideWorkspaceGuard(
		t.workspaceRoot,
		t.workspaceRootReal,
		t.workspaceRootInfo,
		t.workspaceOnly,
		t.allowOutsideWorkspace,
		t.outsideWorkspaceApprover,
		func() bool {
			t.outsideWorkspaceSessionMu.RLock()
			defer t.outsideWorkspaceSessionMu.RUnlock()
			return t.outsideWorkspaceAllowed
		},
		func(allow bool) {
			t.outsideWorkspaceSessionMu.Lock()
			t.outsideWorkspaceAllowed = allow
			t.outsideWorkspaceSessionMu.Unlock()
		},
		outsideWorkspaceRejectionInstruction,
		patchtool.OutsideWorkspaceErrorLabels{
			OutsidePath:          "view_image path outside workspace",
			ApprovalFailed:       "outside-workspace read approval failed",
			RejectedByUserPrefix: "view_image path outside workspace rejected by user",
		},
		patchtool.OutsideWorkspaceFailureFactory{
			ApprovalFailed: readImageOutsideWorkspaceApprovalFailed,
			UserDenied:     readImageOutsideWorkspaceUserDenied,
		},
		patchtool.IsPathInTemporaryDir,
		func(req patchtool.OutsideWorkspaceRequest, reason string) {
			t.logOutsideWorkspaceApproval(req, reason)
		},
	)
	return guard.Allow(ctx, path, real, approvedOutside)
}

func (t *Tool) logOutsideWorkspaceApproval(req patchtool.OutsideWorkspaceRequest, reason string) {
	if t.outsideWorkspaceAudit == nil {
		return
	}
	t.outsideWorkspaceAudit(OutsideWorkspaceAudit{
		RequestedPath: req.RequestedPath,
		ResolvedPath:  req.ResolvedPath,
		Reason:        reason,
	})
}

func detectFileMIME(path string, data []byte) string {
	if len(data) == 0 {
		return ""
	}
	sniffed := normalizeMIME(http.DetectContentType(data))
	if sniffed != "" && sniffed != "application/octet-stream" {
		return sniffed
	}
	extMIME := normalizeMIME(mime.TypeByExtension(strings.ToLower(filepath.Ext(path))))
	if extMIME != "" {
		return extMIME
	}
	return sniffed
}

func normalizeMIME(raw string) string {
	main := strings.TrimSpace(strings.Split(raw, ";")[0])
	return strings.ToLower(main)
}

func readImageOutsideWorkspaceApprovalFailed(req patchtool.OutsideWorkspaceRequest, err error) error {
	path := readImageOutsideWorkspacePath(req)
	reason := strings.TrimSpace(err.Error())
	message := "outside-workspace read approval failed"
	if path != "" {
		message += " for " + path + "."
	} else {
		message += "."
	}
	if reason != "" {
		message += "\nReason: " + reason
	}
	return errors.New(message)
}

func readImageOutsideWorkspaceUserDenied(req patchtool.OutsideWorkspaceRequest, approval patchtool.OutsideWorkspaceApproval, rejectionInstruction string) error {
	path := readImageOutsideWorkspacePath(req)
	commentary := strings.TrimSpace(approval.Commentary)

	var builder strings.Builder
	builder.WriteString("view_image path outside workspace rejected by user")
	if path != "" {
		builder.WriteString(": ")
		builder.WriteString(path)
	}
	builder.WriteString(".")
	if commentary != "" {
		builder.WriteString(" User rejected the approval request for this tool call, and said: ")
		builder.WriteString(strconv.Quote(commentary))
		builder.WriteString(".")
	} else {
		builder.WriteString(" User rejected the approval request for this tool call.")
	}
	builder.WriteString(" Do not attempt to circumvent, hack around, or re-execute the same path. Treat this rejection as authoritative.")
	if instruction := strings.TrimSpace(rejectionInstruction); instruction != "" {
		builder.WriteString(" ")
		builder.WriteString(instruction)
	}
	return errors.New(builder.String())
}

func readImageOutsideWorkspacePath(req patchtool.OutsideWorkspaceRequest) string {
	if path := strings.TrimSpace(req.ResolvedPath); path != "" {
		return path
	}
	return strings.TrimSpace(req.RequestedPath)
}

func buildContentItemsForFile(path, mimeType string, data []byte) ([]contentItem, error) {
	if mimeType == "application/pdf" || strings.EqualFold(filepath.Ext(path), ".pdf") {
		filename := filepath.Base(path)
		if strings.TrimSpace(filename) == "" {
			filename = "document.pdf"
		}
		encoded := base64.StdEncoding.EncodeToString(data)
		return []contentItem{{
			Type:     "input_file",
			FileData: "data:application/pdf;base64," + encoded,
			Filename: filename,
		}}, nil
	}

	if strings.HasPrefix(mimeType, "image/") {
		if _, ok := supportedImageMIMEs[mimeType]; !ok {
			return nil, fmt.Errorf("cannot attach image at %q: unsupported image format %q", path, mimeType)
		}
		return []contentItem{{
			Type:     "input_image",
			ImageURL: fmt.Sprintf("data:%s;base64,%s", mimeType, base64.StdEncoding.EncodeToString(data)),
		}}, nil
	}

	return nil, fmt.Errorf("unsupported file type at %q: expected an image or PDF", path)
}
