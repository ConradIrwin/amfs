package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/automerge/automerge-go"
	"github.com/go-git/go-billy/v5"
	"github.com/juju/fslock"
	"github.com/willscott/go-nfs-client/nfs"
)

type atx struct {
	d   *automerge.Doc
	ops []func() error
}

func Tx(d *automerge.Doc) *atx {
	return &atx{d: d, ops: []func() error{}}
}

func (tx *atx) Commit() error {
	if err := tx.CommitOnly(); err != nil {
		return err
	}

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	encoder.Encode(mustGet(tx.d.RootMap().Values()))

	return os.WriteFile("fs/folder.automerge", tx.d.Save(), 0o666)
}

func (tx *atx) CommitOnly() error {
	for _, op := range tx.ops {
		if err := op(); err != nil {
			return err
		}
	}
	_, err := tx.d.Commit("")
	return err
}

type atxSet struct {
	tx   *atx
	path *automerge.Path
}

func (tx *atx) Set(path ...any) *atxSet {
	return &atxSet{tx: tx, path: tx.d.Path(path...)}
}

func (txs *atxSet) To(value any) *atx {
	txs.tx.ops = append(txs.tx.ops, func() error {
		return txs.path.Set(value)
	})
	return txs.tx
}

func (tx *atx) Inc(path ...any) *atx {
	p := tx.d.Path(path...)
	tx.ops = append(tx.ops, func() error {
		return p.Counter().Inc(1)
	})
	return tx
}

func (tx *atx) Del(path ...any) *atx {
	p := tx.d.Path(path...)
	tx.ops = append(tx.ops, func() error {
		return p.Delete()
	})
	return tx
}

func mustGet[T any](t T, err error) T {
	if err != nil {
		panic(err)
	}
	return t
}

type AMFS struct {
	doc *automerge.Doc
}

type AMFileSystem struct {
	Files   map[AMID]*AMFile         `json:"files"`
	Folders map[AMID]map[string]AMID `json:"folders"`
}

type AMID string

type AMType int

const None AMType = 0
const Folder AMType = 1
const Blob AMType = 2
const Mergeable AMType = 3

type AMFile struct {
	Permissions os.FileMode `json:"perm"`
	Size        int64       `json:"size"`
	// We use modtime and modcount to ensure that whenever the doc might have changed,
	// the reported modtime changes.
	// The case that is problematic is when a new change is synced that adds an "earlier"
	// modtime.
	// Adding the modcount ensures that that causes the time to change too.
	ModTime  time.Time `json:"modtime"`
	ModCount int64     `json:"modcount,omitempty"`
	Type     AMType    `json:"type"`
	// For blobs the first head is the sha256 of the current content
	// For mergeables, the heads are from the doc.
	Heads [][]byte `json:"heads,omitempty"`
}

type AMFileInfo struct {
	name string
	amid AMID
	file *AMFile
}

type AMFileHandle struct {
	info *AMFileInfo
	fs   *AMFS
	file *os.File
	lock *fslock.Lock
	mode int
}

func newID() AMID {
	bytes := make([]byte, 32)
	_, err := rand.Read(bytes)
	if err != nil {
		panic(err)
	}
	return AMID(base64.RawURLEncoding.EncodeToString(bytes))
}

var ROOT = AMID("ROOT")

func NewAMFS() *AMFS {
	bytes, err := os.ReadFile("fs/folder.automerge")

	if err != nil {
		doc := automerge.New()
		err := Tx(doc).
			Set("files", ROOT).To(
			&AMFile{
				Permissions: 0o777 | os.ModeDir,
				Type:        Folder,
				ModTime:     time.Now(),
			}).
			Set("files", ROOT, "modcount").To(automerge.NewCounter(1)).
			Set("folders", ROOT).To(automerge.NewMap()).
			Commit()

		if err != nil {
			panic(err)
		}

		if err := os.WriteFile("fs/folder.automerge", doc.Save(), 0o666); err != nil {
			panic(err)
		}
		bytes, err = os.ReadFile("fs/folder.automerge")
		if err != nil {
			panic(err)
		}
	}

	doc, err := automerge.Load(bytes)
	if err != nil {
		panic(err)
	}
	return &AMFS{doc: doc}
}

// Create creates the named file with mode 0666 (before umask), truncating
// it if it already exists. If successful, methods on the returned File can
// be used for I/O; the associated file descriptor has mode O_RDWR.
func (fs *AMFS) Create(filename string) (billy.File, error) {
	fmt.Println("> Create", filename)
	return fs.OpenFile(filename, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0666)
}

// Open opens the named file for reading. If successful, methods on the
// returned file can be used for reading; the associated file descriptor has
// mode O_RDONLY.
func (fs *AMFS) Open(filename string) (billy.File, error) {
	fmt.Println("> Open", filename)
	return fs.OpenFile(filename, os.O_RDONLY, 0)
}

// OpenFile is the generalized open call; most users will use Open or Create
// instead. It opens the named file with specified flag (O_RDONLY etc.) and
// perm, (0666 etc.) if applicable. If successful, methods on the returned
// File can be used for I/O.
func (fs *AMFS) OpenFile(filename string, flag int, perm os.FileMode) (billy.File, error) {
	fmt.Println("> OpenFile", filename, flag, perm)
	create := None
	if flag&os.O_CREATE > 0 {
		create = Blob
	}
	info, err := fs.getFileInfo(filename, create, perm)
	if err != nil {
		return nil, err
	}

	file, err := os.CreateTemp("", "")
	if err != nil {
		return nil, err
	}
	if len(info.file.Heads) > 0 {
		if info.file.Type == Blob {
			content, err := os.ReadFile("fs/" + hex.EncodeToString(info.file.Heads[0]))
			if err != nil {
				return nil, err
			}
			_, err = file.Write(content)
			if err != nil {
				return nil, err
			}
		} else {
			saved, err := os.ReadFile("fs/" + string(info.amid))
			if err != nil {
				return nil, err
			}
			doc, err := automerge.Load(saved)
			if err != nil {
				return nil, err
			}

			content, err := automerge.As[string](doc.Path("content").Get())
			if err != nil {
				return nil, err
			}

			_, err = file.Write([]byte(content))
			if err != nil {
				return nil, err
			}
		}
	}

	file.Close()
	fmt.Println(file.Name())

	if len(info.file.Heads) == 0 && flag&os.O_EXCL > 0 {
		os.Remove(file.Name())
	}

	f, err := os.OpenFile(file.Name(), flag, perm)
	if err != nil {
		return nil, err
	}

	return &AMFileHandle{info: info, fs: fs, file: f, lock: fslock.New(file.Name()), mode: os.O_RDONLY}, nil
}

// Stat returns a FileInfo describing the named file.
func (fs *AMFS) Stat(filename string) (os.FileInfo, error) {
	return fs.getFileInfo(filename, 0, 0)
}

func (fs *AMFS) getFileInfo(filename string, create AMType, perm fs.FileMode) (*AMFileInfo, error) {
	fmt.Println(" > getFileInfo", filename)

	if filename == "" {
		file, err := automerge.As[*AMFile](fs.doc.Path("files", ROOT).Get())
		if err != nil {
			return nil, err
		}
		fmt.Println(" > > returning root", file)
		return &AMFileInfo{name: "", amid: ROOT, file: file}, nil
	}

	parent := ROOT
	path := fs.Split(filename)
	path2 := path
	fmt.Println(" > > navigating...", path)

	if len(path) >= 2 && path[0] == ".amfs" {
		if strings.HasPrefix(path[1], "=") {
			parent = AMID(strings.TrimPrefix(path[1], "="))
			path2 = path[2:]
		}
	} else if len(path) >= 1 && path[0] == ".amfs" {
		path2 = path[1:]
	}

	for i, p := range path2 {
		id, err := automerge.As[AMID](fs.doc.Path("folders", parent, p).Get())
		if err != nil {
			return nil, err
		}
		if id != "" {
			fmt.Println(" > > found", parent, p)
			parent = id
			continue
		}

		if create > 0 && i == len(path2)-1 {
			fmt.Println("CREATING", create, p)
			id := newID()

			if create == Folder {
				perm |= os.ModeDir
			}

			tx := Tx(fs.doc).
				Set("files", id).To(&AMFile{
				Permissions: perm,
				Type:        create,
				ModTime:     time.Now(),
			}).
				Inc("files", id, "modcount").
				Set("folders", parent, p).To(id).
				Inc("files", parent, "modcount").
				Set("files", parent, "modtime").To(time.Now())

			if create == Folder {
				tx = tx.Set("folders", id).To(automerge.NewMap())
			}
			parent = id

			if err := tx.Commit(); err != nil {
				panic(err)
			}

			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", " ")
			enc.Encode(fs.doc.Root().Interface())
		} else {
			fmt.Println(" > > not found", parent, p)
			return nil, os.ErrNotExist
		}
	}

	file, err := automerge.As[*AMFile](fs.doc.Path("files", parent).Get())
	fmt.Println(" > > found2", parent, path[len(path)-1], file, err)
	if err != nil {
		panic(err)
	}
	return &AMFileInfo{name: path[len(path)-1], amid: parent, file: file}, nil

}

// Rename renames (moves) oldpath to newpath. If newpath already exists and
// is not a directory, Rename replaces it. OS-specific restrictions may
// apply when oldpath and newpath are in different directories.
func (fs *AMFS) Rename(oldpath, newpath string) error {
	fmt.Println("> Rename", oldpath, newpath)
	oldparent, oldtarget := filepath.Split(oldpath)
	newparent, newtarget := filepath.Split(newpath)

	oldinfo, err := fs.getFileInfo(oldparent, 0, 0)
	if err != nil {
		return err
	}
	newinfo, err := fs.getFileInfo(newparent, 0, 0)
	if err != nil {
		return err
	}

	amid, err := automerge.As[AMID](fs.doc.Path("folders", oldinfo.amid, oldtarget).Get())
	if err != nil {
		panic(err)
	}

	err = Tx(fs.doc).
		Set("folders", newinfo.amid, newtarget).To(amid).
		Del("folders", oldinfo.amid, oldtarget).
		Inc("files", oldinfo.amid, "modcount").
		Set("files", oldinfo.amid, "modtime").To(time.Now()).
		Inc("files", newinfo.amid, "modcount").
		Set("files", newinfo.amid, "modtime").To(time.Now()).
		Commit()

	if err != nil {
		panic(err)
	}

	return nil
}

// Remove removes the named file or directory.
func (fs *AMFS) Remove(filename string) error {
	fmt.Println("> Remove", filename)
	parent, name := filepath.Split(filename)
	info, err := fs.getFileInfo(parent, 0, 0)
	if err != nil {
		return err
	}
	if info.file.Type != Folder {
		return os.ErrInvalid
	}

	return Tx(fs.doc).
		Del("folders", info.amid, name).
		Inc("files", info.amid, "modcount").
		Commit()

}

// Join joins any number of path elements into a single path, adding a
// Separator if necessary. Join calls filepath.Clean on the result; in
// particular, all empty strings are ignored. On Windows, the result is a
// UNC path if and only if the first path element is a UNC path.
func (fs *AMFS) Join(elem ...string) string {
	fmt.Printf("Join %#v\n", filepath.Join(elem...))
	return filepath.Join(elem...)
}

// Split converts a path into list of segments
// e.g. a/b => "a" "b", ".git/" => ".git"
func (fs *AMFS) Split(path string) []string {
	return strings.Split(strings.TrimRight(path, "/"), "/")
}

// TempFile creates a new temporary file in the directory dir with a name
// beginning with prefix, opens the file for reading and writing, and
// returns the resulting *os.File. If dir is the empty string, TempFile
// uses the default directory for temporary files (see os.TempDir).
// Multiple programs calling TempFile simultaneously will not choose the
// same file. The caller can use f.Name() to find the pathname of the file.
// It is the caller's responsibility to remove the file when no longer
// needed.
func (fs *AMFS) TempFile(dir, prefix string) (billy.File, error) {
	fmt.Println("> TempFile", dir, prefix)
	return nil, nfs.NFS3Error(nfs.NFS3ErrNotSupp)
}

// ReadDir reads the directory named by dirname and returns a list of
// directory entries sorted by filename.
func (fs *AMFS) ReadDir(path string) ([]os.FileInfo, error) {
	fmt.Println("> ReadDir", path)
	i, err := fs.Stat(path)
	if err != nil {
		return nil, err
	}
	info := i.(*AMFileInfo)

	ret := []os.FileInfo{}

	files, err := automerge.As[map[string]AMID](fs.doc.Path("folders", info.amid).Get())
	if err != nil {
		return nil, err
	}

	for n, id := range files {
		file, err := automerge.As[*AMFile](fs.doc.Path("files", id).Get())
		if err != nil {
			return nil, err
		}
		if file != nil {
			ret = append(ret, &AMFileInfo{name: n, amid: id, file: file})
		}
	}
	return ret, nil
}

// MkdirAll creates a directory named path, along with any necessary
// parents, and returns nil, or else returns an error. The permission bits
// perm are used for all directories that MkdirAll creates. If path is/
// already a directory, MkdirAll does nothing and returns nil.
func (fs *AMFS) MkdirAll(filename string, perm os.FileMode) error {
	fmt.Println("> MkdirAll", filename, perm)
	fs.getFileInfo(filename, Folder, perm)

	return nil
}

// Lstat returns a FileInfo describing the named file. If the file is a
// symbolic link, the returned FileInfo describes the symbolic link. Lstat
// makes no attempt to follow the link.
func (fs *AMFS) Lstat(filename string) (os.FileInfo, error) {
	fmt.Println("> Lstat", filename)
	return fs.Stat(filename)
}

// Symlink creates a symbolic-link from link to target. target may be an
// absolute or relative path, and need not refer to an existing node.
// Parent directories of link are created as necessary.
func (*AMFS) Symlink(target, link string) error {
	fmt.Println("> Symlink", target, link)
	return nfs.NFS3Error(nfs.NFS3ErrNotSupp)
}

// Readlink returns the target path of link.
func (*AMFS) Readlink(link string) (string, error) {
	fmt.Println("> ReadLink", link)
	return "", nfs.NFS3Error(nfs.NFS3ErrNotSupp)
}

// Chroot returns a new filesystem from the same type where the new root is
// the given path. Files outside of the designated directory tree cannot be
// accessed.
func (a *AMFS) Chroot(path string) (billy.Filesystem, error) {
	return nil, nfs.NFS3Error(nfs.NFS3ErrNotSupp)
}

// Root returns the root path of the filesystem.
func (*AMFS) Root() string {
	return "/AMFS/"
}

// Chmod changes the mode of the named file to mode. If the file is a
// symbolic link, it changes the mode of the link's target.
func (fs *AMFS) Chmod(name string, mode os.FileMode) error {
	fmt.Println("> Chmod", name, mode)
	info, err := fs.getFileInfo(name, 0, 0)
	if err != nil {
		return err
	}
	return Tx(fs.doc).
		Set("files", info.amid, "perm").To(mode).
		Inc("files", info.amid, "modcount").
		Commit()
}

// Lchown changes the numeric uid and gid of the named file. If the file is
// a symbolic link, it changes the uid and gid of the link itself.
func (*AMFS) Lchown(name string, uid, gid int) error {
	fmt.Println("> Lchown", name, uid, gid)
	return nfs.NFS3Error(nfs.NFS3ErrNotSupp)
}

// Chown changes the numeric uid and gid of the named file. If the file is a
// symbolic link, it changes the uid and gid of the link's target.
func (*AMFS) Chown(name string, uid, gid int) error {
	fmt.Println("> Chown", name, uid, gid)
	return nfs.NFS3Error(nfs.NFS3ErrNotSupp)
}

// Chtimes changes the access and modification times of the named file,
// similar to the Unix utime() or utimes() functions.
//
// The underlying filesystem may truncate or round the values to a less
// precise time unit.
func (fs *AMFS) Chtimes(name string, atime time.Time, mtime time.Time) error {
	fmt.Println("> Chtimes", name, atime, mtime)
	info, err := fs.getFileInfo(name, 0, 0)
	if err != nil {
		return err
	}

	return Tx(fs.doc).
		Inc("files", info.amid, "modcount").
		Commit()
}

func (f *AMFileInfo) Name() string {
	fmt.Println(" > f.Name:", f.name)
	return f.name
}

func (f *AMFileInfo) Size() int64 {
	fmt.Println(" > f.Size:", f.name, f.file.Size)
	return f.file.Size
}

func (f *AMFileInfo) Mode() fs.FileMode {
	fmt.Println(" > f.Mode:", f.name)
	return f.file.Permissions
}

func (f *AMFileInfo) ModTime() time.Time {
	return f.file.ModTime.Round(time.Second).Add(time.Nanosecond * time.Duration(f.file.ModCount))
}

func (f *AMFileInfo) IsDir() bool {
	return f.file.Type == Folder
}

func (f *AMFileInfo) Sys() any {
	return &syscall.Stat_t{
		Uid:   501,
		Gid:   20,
		Nlink: 1,
	}
}

func (fh *AMFileHandle) Name() string {
	fmt.Println("Handle Name")
	return fh.info.Name()
}

func (fh *AMFileHandle) Write(p []byte) (int, error) {
	fmt.Println("Handle Write")
	return fh.file.Write(p)
}

func (fh *AMFileHandle) Read(p []byte) (int, error) {
	fmt.Println("Handle Read")
	return fh.file.Read(p)
}

func (fh *AMFileHandle) ReadAt(p []byte, off int64) (int, error) {
	fmt.Println("Handle ReadAt", p, off)
	ret, err := fh.file.ReadAt(p, off)
	fmt.Println(ret, p, err)
	return ret, err
}

func (fh *AMFileHandle) Seek(offset int64, whence int) (int64, error) {
	fmt.Println("Handle Seek")
	return fh.file.Seek(offset, whence)
}

func (fh *AMFileHandle) Close() error {
	fmt.Println("Handle Close")
	fh.file.Close()

	bytes, err := os.ReadFile(fh.file.Name())
	if err != nil {
		return err
	}

	h := sha256.Sum256(bytes)

	if err := os.WriteFile("fs/"+hex.EncodeToString(h[:]), bytes, 0o666); err != nil {
		return err
	}

	err = Tx(fh.fs.doc).
		Set("files", fh.info.amid, "size").To(len(bytes)).
		Inc("files", fh.info.amid, "modcount").
		Set("files", fh.info.amid, "heads").To([][]byte{h[:]}).
		Commit()

	if err != nil {
		return err
	}

	return os.Remove(fh.file.Name())
}

func (fh *AMFileHandle) Lock() error {
	fmt.Println("Handle Lock")
	return fh.lock.Lock()
}

func (fh *AMFileHandle) Unlock() error {
	fmt.Println("Handle Unlock")
	return fh.lock.Unlock()
}

func (fh *AMFileHandle) Truncate(size int64) error {
	fmt.Println("Handle Truncate")
	return fh.file.Truncate(size)
}
