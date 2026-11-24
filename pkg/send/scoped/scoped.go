// Package scoped limits uploads and downloads to a connection's scopes.
package scoped

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path"
	"slices"
	"strings"

	"github.com/picosh/pico/pkg/pssh"
	"github.com/picosh/pico/pkg/send/utils"
	"github.com/picosh/pico/pkg/shared"
)

// Rules are how a service's files map to scopes.
type Rules struct {
	// Read and Write are the roles that allow each.
	Read  []string
	Write []string
	// Resource names what a cleaned path that isn't the root belongs to.
	Resource func(fpath string) string
}

// Project is the first segment of a path, where projects are directories.
func Project(fpath string) string {
	project, _, _ := strings.Cut(strings.TrimPrefix(fpath, "/"), "/")
	return project
}

// Handler passes files through to another handler when the connection's
// scopes cover them.
type Handler struct {
	inner utils.CopyFromClientHandler
	rules Rules
}

var (
	_ utils.CopyFromClientHandler = (*Handler)(nil)
	_ utils.WriteValidator        = (*Handler)(nil)
	_ utils.FileRenamer           = (*Handler)(nil)
	_ utils.RootProtector         = (*Handler)(nil)
	_ utils.ImplicitDirs          = (*Handler)(nil)
)

func NewHandler(inner utils.CopyFromClientHandler, rules Rules) *Handler {
	return &Handler{inner: inner, rules: rules}
}

// resource names what fpath belongs to, or "" for the root.
func (h *Handler) resource(fpath string) (string, error) {
	// Handlers clean paths differently, so one that could mean two files
	// isn't checked as either.
	if slices.Contains(strings.Split(fpath, "/"), "..") {
		return "", fmt.Errorf("invalid path (%s), \"..\" is not allowed", fpath)
	}
	fpath = path.Clean("/" + fpath)
	if fpath == "/" {
		return "", nil
	}
	return h.rules.Resource(fpath), nil
}

func (h *Handler) check(s *pssh.SSHServerConnSession, action, fpath string, roles []string) error {
	scopes := shared.SessionScopes(s)
	if !scopes.Limited() {
		return nil
	}
	resource, err := h.resource(fpath)
	if err != nil {
		return err
	}
	return scopes.Check(action, resource, roles...)
}

func (h *Handler) checkRead(s *pssh.SSHServerConnSession, fpath string) error {
	return h.check(s, "reading", fpath, h.rules.Read)
}

func (h *Handler) checkWrite(s *pssh.SSHServerConnSession, fpath string) error {
	return h.check(s, "writing", fpath, h.rules.Write)
}

func (h *Handler) Write(s *pssh.SSHServerConnSession, entry *utils.FileEntry) (string, error) {
	if err := h.checkWrite(s, entry.Filepath); err != nil {
		return "", err
	}
	return h.inner.Write(s, entry)
}

func (h *Handler) ValidateWrite(s *pssh.SSHServerConnSession, entry *utils.FileEntry) error {
	if err := h.checkWrite(s, entry.Filepath); err != nil {
		return err
	}
	if v, ok := h.inner.(utils.WriteValidator); ok {
		return v.ValidateWrite(s, entry)
	}
	return nil
}

func (h *Handler) Delete(s *pssh.SSHServerConnSession, entry *utils.FileEntry) error {
	if err := h.checkWrite(s, entry.Filepath); err != nil {
		return err
	}
	return h.inner.Delete(s, entry)
}

// Rename needs both ends writable, since it replaces one and removes the
// other.
func (h *Handler) Rename(s *pssh.SSHServerConnSession, from, to *utils.FileEntry) error {
	if err := errors.Join(h.checkWrite(s, from.Filepath), h.checkWrite(s, to.Filepath)); err != nil {
		return err
	}
	if r, ok := h.inner.(utils.FileRenamer); ok {
		return r.Rename(s, from, to)
	}
	return errors.ErrUnsupported
}

func (h *Handler) Read(s *pssh.SSHServerConnSession, entry *utils.FileEntry) (os.FileInfo, utils.ReadAndReaderAtCloser, error) {
	if err := h.checkRead(s, entry.Filepath); err != nil {
		return nil, nil, err
	}
	return h.inner.Read(s, entry)
}

// List leaves what the scopes don't cover out of the root, which anyone may
// list.
func (h *Handler) List(s *pssh.SSHServerConnSession, fpath string, isDir bool, recursive bool) ([]os.FileInfo, error) {
	scopes := shared.SessionScopes(s)
	if !scopes.Limited() {
		return h.inner.List(s, fpath, isDir, recursive)
	}

	resource, err := h.resource(fpath)
	if err != nil {
		return nil, err
	}
	if resource != "" {
		if err := scopes.Check("reading", resource, h.rules.Read...); err != nil {
			return nil, err
		}
		return h.inner.List(s, fpath, isDir, recursive)
	}

	entries, err := h.inner.List(s, fpath, isDir, recursive)
	return slices.DeleteFunc(entries, func(e os.FileInfo) bool {
		resource, err := h.resource(e.Name())
		return err != nil || (resource != "" && !scopes.Allows(resource, h.rules.Read...))
	}), err
}

func (h *Handler) GetLogger(s *pssh.SSHServerConnSession) *slog.Logger {
	return h.inner.GetLogger(s)
}

func (h *Handler) Validate(s *pssh.SSHServerConnSession) error {
	return h.inner.Validate(s)
}

func (h *Handler) ProtectRoot() bool {
	p, ok := h.inner.(utils.RootProtector)
	return ok && p.ProtectRoot()
}

func (h *Handler) IsImplicitDir(s *pssh.SSHServerConnSession, fpath string) bool {
	d, ok := h.inner.(utils.ImplicitDirs)
	return ok && h.checkRead(s, fpath) == nil && d.IsImplicitDir(s, fpath)
}
