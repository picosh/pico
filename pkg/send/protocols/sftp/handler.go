package sftp

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/picosh/pico/pkg/pssh"
	"github.com/picosh/pico/pkg/send/utils"
	"github.com/pkg/sftp"
)

type listerat []os.FileInfo

func (f listerat) ListAt(ls []os.FileInfo, offset int64) (int, error) {
	var n int
	if offset >= int64(len(f)) {
		return 0, io.EOF
	}
	n = copy(ls, f[offset:])
	if n < len(ls) {
		return n, io.EOF
	}
	return n, nil
}

type handler struct {
	session      *pssh.SSHServerConnSession
	writeHandler utils.CopyFromClientHandler

	mu      sync.Mutex
	pending map[string]*pendingFile
	// ignored counts open handles to files the handler ignores. A stat of
	// one is refused so a mounted filesystem reports that it wasn't saved.
	ignored map[string]int
	// hidden holds stored files renamed away while open, by the open file
	// that will replace them. They look deleted to this session.
	hidden map[string]*pendingFile
}

func (f *handler) pendingFile(name string) *pendingFile {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pending[name]
}

func (f *handler) isHidden(name string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hidden[name] != nil
}

// release returns the files p was renamed from that are still waiting on
// it, and shows them again. The caller holds f.mu.
func (f *handler) release(p *pendingFile) []renamedFile {
	var out []renamedFile
	for _, old := range p.replaces {
		if f.hidden[old.path] == p {
			delete(f.hidden, old.path)
			out = append(out, old)
		}
	}
	p.replaces = nil
	return out
}

func (f *handler) Filecmd(r *sftp.Request) error {
	switch r.Method {
	case "Rmdir", "Remove":
		entry := toFileEntry(r)

		if r.Method == "Rmdir" {
			entry.Mode = os.ModeDir
			return f.writeHandler.Delete(f.session, entry)
		}

		f.mu.Lock()
		hidden := f.hidden[r.Filepath] != nil
		p, ok := f.pending[r.Filepath]
		var replaced []renamedFile
		if ok {
			delete(f.pending, r.Filepath)
			replaced = f.release(p)
		}
		f.mu.Unlock()
		if hidden && !ok {
			return os.ErrNotExist
		}
		// Removing a renamed file removes what it was renamed from.
		for _, old := range replaced {
			if err := f.deleteIfUnchanged(old.path, old.sum); err != nil {
				return err
			}
		}
		if ok {
			// An open file may have been renamed over a stored one.
			stored, _, err := f.stored(entry, false)
			if err != nil || !stored {
				return err
			}
		}
		return f.writeHandler.Delete(f.session, entry)
	case "Mkdir":
		entry := toFileEntry(r)

		entry.Mode = os.ModeDir

		_, err := f.writeHandler.Write(f.session, entry)

		return err
	case "Rename":
		return f.rename(r.Filepath, r.Target, false)
	case "Setstat":
		// Only an open file's size can change, as when an editor
		// truncates what it is writing.
		if p := f.pendingFile(r.Filepath); p != nil && r.AttrFlags().Size {
			f.mu.Lock()
			p.dirty = true
			f.mu.Unlock()
			p.buf.truncate(int64(r.Attributes().Size))
		}
		return nil
	}
	return errors.New("unsupported")
}

func (f *handler) Filelist(r *sftp.Request) (sftp.ListerAt, error) {
	switch r.Method {
	case "List", "Stat":
		list := r.Method == "List"

		if !list {
			f.mu.Lock()
			p, ignored := f.pending[r.Filepath], f.ignored[r.Filepath] > 0
			hidden := f.hidden[r.Filepath] != nil
			f.mu.Unlock()
			if ignored {
				return nil, os.ErrPermission
			}
			if hidden && p == nil {
				return nil, os.ErrNotExist
			}
			if p != nil {
				return listerat{p.info(filepath.Base(r.Filepath))}, nil
			}
		}

		listData, err := f.writeHandler.List(f.session, r.Filepath, list, false)
		if err != nil {
			return nil, err
		}
		if list {
			listData = f.withPending(r.Filepath, listData)
		}

		// an empty string or exact match from filepath base name is what we want
		if !list {
			listData = slices.DeleteFunc(listData, func(f os.FileInfo) bool {
				return f.Name() != "" && f.Name() != filepath.Base(r.Filepath)
			})
		}

		if r.Filepath == "/" {
			listData = slices.DeleteFunc(listData, func(f os.FileInfo) bool {
				return f.Name() == "/"
			})
			listData = slices.Insert(listData, 0, os.FileInfo(&utils.VirtualFile{
				FName:  ".",
				FIsDir: true,
			}))
		}

		return listerat(listData), nil
	}

	return nil, errors.New("unsupported")
}

// PosixRename is a rename that replaces an existing file at the target.
func (f *handler) PosixRename(r *sftp.Request) error {
	return f.rename(r.Filepath, r.Target, true)
}

// rename moves a file or a directory. Storage has no move, so each file
// is written to its new path through the handler, which applies its usual
// checks, and the sources are deleted once every write succeeded.
func (f *handler) rename(from, to string, replace bool) error {
	if from == to {
		return nil
	}
	if strings.HasPrefix(to, from+"/") {
		return fmt.Errorf("cannot move %s into itself", from)
	}
	if moved, err := f.renamePending(from, to, replace); moved || err != nil {
		return err
	}
	if f.isHidden(from) {
		return os.ErrNotExist
	}
	toHidden := f.isHidden(to)

	isFile, _, err := f.stored(&utils.FileEntry{Filepath: from}, false)
	if err != nil {
		return err
	}
	files, dirs := []string{"."}, []string(nil)
	if !isFile {
		if files, dirs, err = f.tree(from); err != nil {
			return err
		}
		if len(files)+len(dirs) == 0 {
			if ok, err := f.isDir(from); err != nil || !ok {
				return errors.Join(os.ErrNotExist, err)
			}
		}
	}
	if !replace || !isFile {
		exists, _, err := f.stored(&utils.FileEntry{Filepath: to}, false)
		if err != nil {
			return err
		}
		exists = exists && !toHidden
		belowFiles, belowDirs, err := f.tree(to)
		if err != nil {
			return err
		}
		if exists || len(belowFiles)+len(belowDirs) > 0 {
			return os.ErrExist
		}
	}

	if !isFile {
		for _, d := range append([]string{"."}, dirs...) {
			if _, err := f.writeHandler.Write(f.session, &utils.FileEntry{Filepath: filepath.Join(to, d), Mode: os.ModeDir}); err != nil {
				return err
			}
		}
	}
	var written []string
	sums := make([][]byte, len(files))
	for i, name := range files {
		dst := filepath.Join(to, name)
		sum, err := f.copyFile(filepath.Join(from, name), dst)
		if err != nil {
			for _, w := range written {
				_ = f.writeHandler.Delete(f.session, &utils.FileEntry{Filepath: w})
			}
			return err
		}
		sums[i] = sum
		written = append(written, dst)
	}
	for i, name := range files {
		if err := f.deleteIfUnchanged(filepath.Join(from, name), sums[i]); err != nil {
			return err
		}
	}
	if !isFile {
		// Children before parents.
		slices.SortFunc(dirs, func(a, b string) int { return strings.Count(b, "/") - strings.Count(a, "/") })
		for _, d := range append(dirs, ".") {
			_ = f.writeHandler.Delete(f.session, &utils.FileEntry{Filepath: filepath.Join(from, d), Mode: os.ModeDir})
		}
	}
	f.mu.Lock()
	delete(f.hidden, to)
	f.mu.Unlock()
	return nil
}

// renamePending moves a file that is open for writing, which happens when
// sshfs delivers the close after a rename. Its contents are stored at the
// new path when it closes, and a stored source is kept until then.
func (f *handler) renamePending(from, to string, replace bool) (bool, error) {
	f.mu.Lock()
	p, ok := f.pending[from]
	if !ok {
		defer f.mu.Unlock()
		for name := range f.pending {
			if strings.HasPrefix(name, from+"/") {
				return false, fmt.Errorf("cannot move %s while %s is open for writing", from, name)
			}
		}
		return false, nil
	}
	_, toOpen := f.pending[to]
	toHidden := f.hidden[to] != nil
	stored := p.stored
	f.mu.Unlock()
	if v, ok := f.writeHandler.(utils.WriteValidator); ok {
		if err := v.ValidateWrite(f.session, &utils.FileEntry{Filepath: to, Mode: p.entry.Mode}); err != nil {
			return true, err
		}
	}
	if !replace {
		exists, _, err := f.stored(&utils.FileEntry{Filepath: to}, false)
		if err != nil {
			return true, err
		}
		if (exists && !toHidden) || toOpen {
			return true, os.ErrExist
		}
	}

	var sum []byte
	if stored {
		var err error
		if sum, stored, err = f.checksum(from); err != nil {
			return true, err
		}
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.pending, from)
	delete(f.hidden, to)
	f.pending[to] = p
	p.path = to
	p.dirty = true
	if stored {
		p.replaces = append(p.replaces, renamedFile{path: from, sum: sum})
		f.hidden[from] = p
	}
	p.stored = false
	return true, nil
}

// copyFile copies a stored file and returns the checksum of what it copied.
func (f *handler) copyFile(from, to string) ([]byte, error) {
	info, r, err := f.writeHandler.Read(f.session, &utils.FileEntry{Filepath: from})
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()
	h := sha256.New()
	msg, err := f.writeHandler.Write(f.session, &utils.FileEntry{
		Filepath: to,
		Mode:     info.Mode().Perm(),
		Size:     info.Size(),
		Mtime:    info.ModTime().Unix(),
		Atime:    info.ModTime().Unix(),
		Reader:   io.TeeReader(r, h),
	})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", to, err)
	}
	if msg != "" {
		_, _ = fmt.Fprintf(f.session.Stderr(), "%s\r\n", msg)
	}
	return h.Sum(nil), nil
}

// checksum hashes the stored file at name.
func (f *handler) checksum(name string) ([]byte, bool, error) {
	stored, data, err := f.stored(&utils.FileEntry{Filepath: name}, true)
	if err != nil || !stored {
		return nil, stored, err
	}
	sum := sha256.Sum256(data)
	return sum[:], true, nil
}

// deleteIfUnchanged deletes a stored file unless another session replaced
// it since it had sum. Storage has no conditional delete, so a write
// between the check and the delete can still be lost.
func (f *handler) deleteIfUnchanged(name string, sum []byte) error {
	cur, stored, err := f.checksum(name)
	if err != nil || !stored || !bytes.Equal(cur, sum) {
		return err
	}
	return f.writeHandler.Delete(f.session, &utils.FileEntry{Filepath: name})
}

// tree returns the files and directories below dir, relative to it.
func (f *handler) tree(dir string) (files, dirs []string, err error) {
	entries, err := f.writeHandler.List(f.session, dir, true, true)
	if err != nil {
		return nil, nil, err
	}
	for _, e := range entries {
		name := strings.Trim(e.Name(), "/")
		switch {
		case name == "" || name == ".":
		case e.IsDir():
			dirs = append(dirs, name)
		default:
			files = append(files, name)
		}
	}
	return files, dirs, nil
}

func (f *handler) isDir(name string) (bool, error) {
	entries, err := f.writeHandler.List(f.session, filepath.Dir(name), true, false)
	if err != nil {
		return false, err
	}
	base := filepath.Base(name)
	return slices.ContainsFunc(entries, func(e os.FileInfo) bool {
		return e.IsDir() && strings.Trim(e.Name(), "/") == base
	}), nil
}

// withPending adds the files open for writing in dir to its listing and
// drops the ones renamed away.
func (f *handler) withPending(dir string, listData []os.FileInfo) []os.FileInfo {
	f.mu.Lock()
	defer f.mu.Unlock()
	for name := range f.hidden {
		if filepath.Dir(name) == filepath.Clean(dir) {
			base := filepath.Base(name)
			listData = slices.DeleteFunc(listData, func(info os.FileInfo) bool { return info.Name() == base })
		}
	}
	for name, p := range f.pending {
		if filepath.Dir(name) != filepath.Clean(dir) {
			continue
		}
		base := filepath.Base(name)
		listData = slices.DeleteFunc(listData, func(info os.FileInfo) bool { return info.Name() == base })
		listData = append(listData, p.info(base))
	}
	return listData
}

// stored returns the contents of the file at name if storage has it.
// Contents are only read when withContents is set.
func (f *handler) stored(entry *utils.FileEntry, withContents bool) (bool, []byte, error) {
	list, err := f.writeHandler.List(f.session, entry.Filepath, false, false)
	if err != nil {
		return false, nil, err
	}
	base := filepath.Base(entry.Filepath)
	if !slices.ContainsFunc(list, func(info os.FileInfo) bool { return !info.IsDir() && info.Name() == base }) {
		return false, nil, nil
	}
	if !withContents {
		return true, nil, nil
	}
	_, r, err := f.writeHandler.Read(f.session, entry)
	if err != nil {
		return false, nil, err
	}
	defer func() { _ = r.Close() }()
	data, err := io.ReadAll(r)
	return true, data, err
}

func toFileEntry(r *sftp.Request) *utils.FileEntry {
	attrs := r.Attributes()
	var size int64 = 0
	var mtime int64 = 0
	var atime int64 = 0
	var mode fs.FileMode
	if attrs != nil {
		mode = attrs.FileMode()
		size = int64(attrs.Size)
		mtime = int64(attrs.Mtime)
		atime = int64(attrs.Atime)
	}

	entry := &utils.FileEntry{
		Filepath: r.Filepath,
		Mode:     mode,
		Size:     size,
		Mtime:    mtime,
		Atime:    atime,
	}
	return entry
}

// Filewrite opens a file for writing. Nothing is stored until the last
// handle to it closes, so a rejected upload leaves storage untouched.
func (f *handler) Filewrite(r *sftp.Request) (io.WriterAt, error) {
	entry := toFileEntry(r)
	if v, ok := f.writeHandler.(utils.WriteValidator); ok {
		if err := v.ValidateWrite(f.session, entry); err != nil {
			if errors.Is(err, utils.ErrFileIgnored) {
				_, _ = fmt.Fprintf(f.session.Stderr(), "%s\r\n", err)
				f.mu.Lock()
				f.ignored[r.Filepath]++
				f.mu.Unlock()
				return ignoredWriter{handler: f, path: r.Filepath}, nil
			}
			return nil, err
		}
	}

	trunc := r.Pflags().Trunc
	f.mu.Lock()
	defer f.mu.Unlock()
	if p, ok := f.pending[r.Filepath]; ok {
		p.handles++
		if trunc {
			p.buf.truncate(0)
			p.dirty = true
		}
		return &pendingWriter{handler: f, file: p}, nil
	}

	// A file renamed away while open is gone, and this one takes its path.
	var stored bool
	var data []byte
	if f.hidden[r.Filepath] != nil {
		delete(f.hidden, r.Filepath)
		trunc = true
	} else {
		// Opening without truncating keeps what is stored, as touch does.
		var err error
		if stored, data, err = f.stored(entry, !trunc); err != nil {
			return nil, err
		}
	}
	p := &pendingFile{path: r.Filepath, entry: entry, buf: &buffer{}, modTime: time.Now(), handles: 1, stored: stored, dirty: trunc}
	if _, err := p.buf.WriteAt(data, 0); err != nil {
		return nil, err
	}
	f.pending[r.Filepath] = p
	return &pendingWriter{handler: f, file: p}, nil
}

func (f *handler) Fileread(r *sftp.Request) (io.ReaderAt, error) {
	if r.Filepath == "/" {
		return nil, os.ErrInvalid
	}

	if p := f.pendingFile(r.Filepath); p != nil {
		return bytes.NewReader(p.buf.bytes()), nil
	}
	if f.isHidden(r.Filepath) {
		return nil, os.ErrNotExist
	}

	fileEntry := toFileEntry(r)
	_, reader, err := f.writeHandler.Read(f.session, fileEntry)

	return reader, err
}

type handlererr struct {
	Handler *handler
}

func (f *handlererr) Filecmd(r *sftp.Request) error {
	err := f.Handler.Filecmd(r)
	if err != nil {
		_, _ = fmt.Fprintln(f.Handler.session.Stderr(), err)
	}
	return err
}
func (f *handlererr) PosixRename(r *sftp.Request) error {
	err := f.Handler.PosixRename(r)
	if err != nil {
		_, _ = fmt.Fprintln(f.Handler.session.Stderr(), err)
	}
	return err
}
func (f *handlererr) Filelist(r *sftp.Request) (sftp.ListerAt, error) {
	result, err := f.Handler.Filelist(r)
	if err != nil {
		_, _ = fmt.Fprintln(f.Handler.session.Stderr(), err)
	}
	return result, err
}
func (f *handlererr) Filewrite(r *sftp.Request) (io.WriterAt, error) {
	result, err := f.Handler.Filewrite(r)
	if err != nil {
		_, _ = fmt.Fprintln(f.Handler.session.Stderr(), err)
	}
	return result, err
}
func (f *handlererr) Fileread(r *sftp.Request) (io.ReaderAt, error) {
	result, err := f.Handler.Fileread(r)
	if err != nil {
		_, _ = fmt.Fprintln(f.Handler.session.Stderr(), err)
	}
	return result, err
}
