package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"wada-desktop-go/internal/protocol"
)

const editorMaxBytes = 256 << 10

var errEditorConflict = errors.New("file changed on disk; keep your draft and open the latest version")

func (s *Server) dispatchEditor(ctx context.Context, method string, payload json.RawMessage) (any, *protocol.RPCError) {
	var wID string
	var query string
	var file protocol.WorkspaceFileSaveRequest
	if method == protocol.MethodWorkspaceFiles {
		var req protocol.WorkspaceFilesRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, invalidErr(err)
		}
		wID, query = req.Workspace, req.Query
	} else {
		if err := json.Unmarshal(payload, &file); err != nil {
			return nil, invalidErr(err)
		}
		wID = file.Workspace
	}
	w, ok := s.workspaces.Workspace(wID)
	if !ok || w.Archived != 0 {
		return nil, &protocol.RPCError{Code: protocol.ErrCodeNotFound, Message: "active workspace not found"}
	}
	if method == protocol.MethodWorkspaceFiles {
		entries, err := s.cfg.Backend.FindFiles(ctx, w.Directory, query, 120)
		if err != nil {
			return nil, internalErr(err)
		}
		paths := []string{}
		for _, entry := range entries {
			if _, err := editorPath(entry.Path); !entry.IsDir && err == nil {
				paths = append(paths, entry.Path)
			}
		}
		return paths, nil
	}
	if method == protocol.MethodWorkspaceFileSave {
		s.editorMu.Lock()
		defer s.editorMu.Unlock()
	}
	if err := ctx.Err(); err != nil {
		return nil, internalErr(err)
	}
	dir, name, err := editorParent(w.Directory, file.Path)
	if err != nil {
		return nil, invalidErr(err)
	}
	defer dir.Close()
	data, mode, err := editorRead(dir, name)
	if err != nil {
		return nil, invalidErr(err)
	}
	before := editorSnapshot(data)
	if method == protocol.MethodWorkspaceFileOpen {
		return before, nil
	}
	if before.Revision != file.Revision {
		return nil, &protocol.RPCError{Code: protocol.ErrCodeConflict, Message: errEditorConflict.Error()}
	}
	saved, err := editorSave(ctx, dir, name, data, mode, file.Text)
	if errors.Is(err, errEditorConflict) {
		return nil, &protocol.RPCError{Code: protocol.ErrCodeConflict, Message: err.Error()}
	}
	if err != nil {
		return nil, invalidErr(err)
	}
	return editorSnapshot(saved), nil
}

func editorPath(path string) (string, error) {
	local, err := filepath.Localize(path)
	if err != nil || path == "." {
		return "", errors.New("enter a file path relative to the workspace")
	}
	for part := range strings.SplitSeq(path, "/") {
		if strings.EqualFold(part, ".git") {
			return "", errors.New("the workspace editor cannot open Git metadata")
		}
	}
	return local, nil
}

func editorParent(directory, path string) (*os.Root, string, error) {
	local, err := editorPath(path)
	if err != nil {
		return nil, "", err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, "", err
	}
	defer root.Close()
	parent, name := filepath.Split(local)
	for component := filepath.Clean(parent); component != "."; component = filepath.Dir(component) {
		info, err := root.Lstat(component)
		if err != nil || !info.IsDir() {
			return nil, "", errors.New("the file's parent must be a directory without symbolic links")
		}
	}
	dir, err := root.OpenRoot(filepath.Clean(parent))
	return dir, name, err
}

func editorRead(dir *os.Root, name string) ([]byte, os.FileMode, error) {
	info, err := dir.Lstat(name)
	if err != nil {
		return nil, 0, err
	}
	if !info.Mode().IsRegular() || info.Size() > editorMaxBytes {
		return nil, 0, errors.New("the editor supports regular text files up to 256 KiB")
	}
	f, err := dir.Open(name)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	actual, err := f.Stat()
	if err != nil || !os.SameFile(info, actual) {
		return nil, 0, errEditorConflict
	}
	data, err := io.ReadAll(io.LimitReader(f, editorMaxBytes+1))
	if err != nil {
		return nil, 0, err
	}
	if !validEditorText(string(data)) {
		return nil, 0, errors.New("the editor supports UTF-8 text files up to 256 KiB")
	}
	return data, info.Mode(), nil
}

func validEditorText(text string) bool {
	return len(text) <= editorMaxBytes && utf8.ValidString(text) && !strings.ContainsRune(text, 0)
}

func editorSnapshot(data []byte) protocol.WorkspaceFile {
	sum := sha256.Sum256(data)
	return protocol.WorkspaceFile{Text: strings.ReplaceAll(string(data), "\r\n", "\n"), Revision: hex.EncodeToString(sum[:])}
}

func editorSave(ctx context.Context, dir *os.Root, name string, before []byte, mode os.FileMode, text string) ([]byte, error) {
	if mode.Perm()&0o222 == 0 {
		return nil, errors.New("the file is read-only")
	}
	original := string(before)
	if strings.Contains(original, "\r\n") && !strings.Contains(strings.ReplaceAll(original, "\r\n", ""), "\n") {
		text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\n", "\r\n")
	}
	if !validEditorText(text) {
		return nil, errors.New("the editor supports UTF-8 text files up to 256 KiB")
	}
	temp := ".wada-edit-" + uuid.NewString()
	f, err := dir.OpenFile(temp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	defer dir.Remove(temp)
	_, err = io.WriteString(f, text)
	if err == nil {
		err = f.Chmod(mode.Perm())
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, err
	}
	current, currentMode, err := editorRead(dir, name)
	if err != nil || currentMode != mode || string(current) != original {
		return nil, errEditorConflict
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return []byte(text), dir.Rename(temp, name)
}
