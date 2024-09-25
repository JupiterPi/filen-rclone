// Package filen provides an interface to Filen cloud storage.
package filen

import (
	"bytes"
	"context"
	"fmt"
	sdk "github.com/FilenCloudDienste/filen-sdk-go/filen"
	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/config/configmap"
	"github.com/rclone/rclone/fs/config/configstruct"
	"github.com/rclone/rclone/fs/config/obscure"
	"github.com/rclone/rclone/fs/hash"
	"io"
	pathModule "path"
	"strings"
	"time"
)

func init() {
	fs.Register(&fs.RegInfo{
		Name:        "filen",
		Description: "Filen",
		NewFs:       NewFs,
		Options: []fs.Option{
			{
				Name:     "email",
				Help:     "Filen account email",
				Required: true,
			},
			{
				Name:       "password",
				Help:       "Filen account password",
				Required:   true,
				IsPassword: true,
				Sensitive:  true,
			},
		},
	})
}

func NewFs(_ context.Context, name, root string, m configmap.Mapper) (fs.Fs, error) {
	opt := new(Options)
	err := configstruct.Set(m, opt)
	if err != nil {
		return nil, err
	}

	password, err := obscure.Reveal(opt.Password)
	filen, err := sdk.New(opt.Email, password)
	if err != nil {
		return nil, err
	}
	return &Fs{name, root, filen}, nil
}

type Fs struct {
	name  string
	root  string
	filen *sdk.Filen
}

// resolvePath returns the absolute path specified by the input path, which is seen relative to the remote's root.
func (f *Fs) resolvePath(path string) string {
	return pathModule.Join(f.root, path)
}

type Options struct {
	Email    string `config:"email"`
	Password string `config:"password"`
}

func (f *Fs) Name() string {
	return f.name
}

func (f *Fs) Root() string {
	return f.root
}

func (f *Fs) String() string {
	return fmt.Sprintf("Filen %s at /%s", f.filen.Email, f.root)
}

func (f *Fs) Precision() time.Duration {
	return 1 * time.Second
}

func (f *Fs) Hashes() hash.Set {
	return 0
}

func (f *Fs) Features() *fs.Features {
	return &fs.Features{
		CanHaveEmptyDirectories: true,
		//TODO more optional features?
	}
}

func (f *Fs) List(ctx context.Context, dir string) (entries fs.DirEntries, err error) {
	// find directory
	directoryUUID, err := f.filen.FindItemUUID(f.resolvePath(dir), true)
	if err != nil {
		return nil, err
	}

	// read directory content
	files, directories, err := f.filen.ReadDirectory(directoryUUID)
	if err != nil {
		return nil, err
	}

	for _, directory := range directories {
		entries = append(entries, &Directory{f, pathModule.Join(dir, directory.Name), directory})
	}
	for _, file := range files {
		entries = append(entries, &File{f, pathModule.Join(dir, file.Name), file})
	}
	return entries, nil
}

func (f *Fs) NewObject(ctx context.Context, remote string) (fs.Object, error) {
	file, _, err := f.filen.FindItem(f.resolvePath(remote), false)
	if err != nil {
		return nil, err
	}
	if file == nil {
		return nil, fs.ErrorObjectNotFound
	}
	return &File{f, remote, file}, nil
}

func (f *Fs) Put(ctx context.Context, in io.Reader, src fs.ObjectInfo, options ...fs.OpenOption) (fs.Object, error) {
	return nil, nil //TODO tmp
}

func (f *Fs) Mkdir(ctx context.Context, dir string) error {
	parentUUID := ""
	dirName := ""

	// find parent uuid and dir name
	dir = f.resolvePath(dir)
	lastSlashIdx := strings.LastIndex(dir, "/")
	if lastSlashIdx == -1 {
		// parent is base folder
		uuid, err := f.filen.GetBaseFolderUUID()
		if err != nil {
			return err
		}
		parentUUID = uuid
		dirName = dir
	} else {
		// parent is specified
		parentPath, name := dir[:lastSlashIdx], dir[lastSlashIdx+1:]
		directoryUUID, err := f.filen.FindItemUUID(parentPath, true)
		if err != nil {
			return err
		}
		parentUUID = directoryUUID
		dirName = name
	}

	// create directory
	_, err := f.filen.CreateDirectory(parentUUID, dirName)
	if err != nil {
		return err
	}
	return nil
}

func (f *Fs) Rmdir(ctx context.Context, dir string) error {
	// find directory
	directoryUUID, err := f.filen.FindItemUUID(f.resolvePath(dir), true)
	if err != nil {
		return err
	}

	// trash directory
	err = f.filen.TrashDirectory(directoryUUID)
	if err != nil {
		return err
	}
	return nil
}

// Directory

type Directory struct {
	fs        *Fs
	path      string
	directory *sdk.Directory
}

func (dir *Directory) Fs() fs.Info {
	return dir.fs
}

func (dir *Directory) String() string {
	return dir.path
}

func (dir *Directory) Remote() string {
	return dir.path
}

func (dir *Directory) ModTime(ctx context.Context) time.Time {
	return dir.directory.Created //TODO best guess?
}

func (dir *Directory) Size() int64 {
	return -1
}

func (dir *Directory) Items() int64 {
	return -1
}

func (dir *Directory) ID() string {
	return dir.directory.UUID
}

// File

type File struct {
	fs   *Fs
	path string
	file *sdk.File
}

func (file *File) Fs() fs.Info {
	return file.fs
}

func (file *File) String() string {
	return file.path
}

func (file *File) Remote() string {
	return file.path
}

func (file *File) ModTime(ctx context.Context) time.Time {
	return file.file.LastModified
}

func (file *File) Size() int64 {
	return file.file.Size
}

func (file *File) Hash(ctx context.Context, ty hash.Type) (string, error) {
	return "", nil //TODO tmp
}

func (file *File) Storable() bool {
	return true
}

func (file *File) SetModTime(ctx context.Context, t time.Time) error {
	return nil //TODO tmp
}

func (file *File) Open(ctx context.Context, options ...fs.OpenOption) (io.ReadCloser, error) {
	content, err := file.fs.filen.DownloadFileInMemory(file.file)
	if err != nil {
		return nil, err
	}
	return io.NopCloser(bytes.NewBuffer(content)), nil
}

func (file *File) Update(ctx context.Context, in io.Reader, src fs.ObjectInfo, options ...fs.OpenOption) error {
	uploadedFile, err := file.fs.filen.UploadFile(file.file.Name, file.file.ParentUUID, in)
	if err != nil {
		return err
	}
	file.file = uploadedFile
	return nil
}

func (file *File) Remove(ctx context.Context) error {
	err := file.fs.filen.TrashFile(file.file.UUID)
	if err != nil {
		return err
	}
	return nil
}
