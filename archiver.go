package archiver

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"path"
	"strings"

	"github.com/mholt/archiver/v4"
)

func NewZipArchive(sourceArchive io.Reader) *ArchiverExtractor {
	return &ArchiverExtractor{Extractor: archiver.Zip{
		Compression: archiver.ZipMethodZstd, TextEncoding: "gbk",
	}, sourceArchive: sourceArchive}
}

func DetectArchive(sourceArchiveName string, sourceArchive io.Reader) (*ArchiverExtractor, error) {
	archiverFmt, r, err := archiver.Identify(sourceArchiveName, sourceArchive)
	if err != nil {
		return nil, err
	}
	if ext, ok := archiverFmt.(archiver.Extractor); ok {
		return NewArchive(ext, r), nil
	}
	return nil, fmt.Errorf("type %s not support", archiverFmt.Name())
}

func NewArchive(extractor archiver.Extractor, sourceArchive io.Reader) *ArchiverExtractor {
	if _, ok := extractor.(archiver.Zip); ok {
		return NewZipArchive(sourceArchive)
	}
	return &ArchiverExtractor{Extractor: extractor, sourceArchive: sourceArchive}
}

type ArchiverExtractor struct {
	archiver.Extractor
	sourceArchive   io.Reader
	fileHandlerFunc FileHanderFunc
	pathsInArchive  []string
}

type FileHanderFunc func(files *[]archiver.File) archiver.FileHandler

// SetFileHandler
func (ae *ArchiverExtractor) SetFileHandlerFunc(fileHandlerFunc FileHanderFunc) {
	ae.fileHandlerFunc = fileHandlerFunc
}

// ExtractDirs 级联提取指定目录下的所有文件和目录
func (ae *ArchiverExtractor) ExtractDirs(ctx context.Context, dir string) ([]archiver.File, error) {
	files := make([]archiver.File, 0)
	ff := DirFilter(&files, dir)
	if ae.fileHandlerFunc != nil {
		ff = ae.fileHandlerFunc(&files)
	}
	return files, ae.Extract(ctx, ae.sourceArchive, ae.pathsInArchive, ff)
}

// ExtractDirs 提取指定目录下的所有文件和目录
func (ae *ArchiverExtractor) CascadeExtractDirs(ctx context.Context, dir string) ([]archiver.File, error) {
	files := make([]archiver.File, 0)
	ff := NoFilter(&files)
	if ae.fileHandlerFunc != nil {
		ff = ae.fileHandlerFunc(&files)
	}

	pia := ae.pathsInArchive
	if dir != "/" {
		pia = []string{strings.TrimPrefix(dir, "/")}
	}
	return files, ae.Extract(ctx, ae.sourceArchive, pia, ff)
}

// ExtractFile 提取指定文件
func (ae *ArchiverExtractor) ExtractFile(ctx context.Context, filePath string) (*archiver.File, error) {
	files := make([]archiver.File, 0)
	ff := FileFilter(&files, filePath)
	if ae.fileHandlerFunc != nil {
		ff = ae.fileHandlerFunc(&files)
	}
	err := ae.Extract(ctx, ae.sourceArchive, ae.pathsInArchive, ff)
	if len(files) == 0 {
		return nil, fmt.Errorf("file not found")
	}
	return &files[0], err
}

// NoFilter 级联提取所有文件和目录
func NoFilter(files *[]archiver.File) archiver.FileHandler {
	return func(ctx context.Context, f archiver.File) error {
		*files = append(*files, f)
		return nil
	}
}

// DirFilter 仅提取指定目录下的文件和目录
func DirFilter(files *[]archiver.File, dir string) archiver.FileHandler {
	return func(ctx context.Context, f archiver.File) error {
		dirPath := f.NameInArchive
		fileDir := strings.TrimPrefix("/"+dirPath, dir)
		if (strings.Count(fileDir, "/") == 0 && len(fileDir) > 0) ||
			(strings.Count(fileDir, "/") == 1 && strings.HasSuffix(fileDir, "/")) {
			*files = append(*files, f)
		}
		return nil
	}
}

// FileFilter 仅提取指定文件
func FileFilter(files *[]archiver.File, filePath string) archiver.FileHandler {
	return func(ctx context.Context, f archiver.File) error {
		if f.IsDir() {
			return nil
		}

		fileDir := path.Dir(f.NameInArchive)
		if fileDir == "." {
			fileDir = ""
		}
		if !strings.HasPrefix(filePath, "/"+fileDir) {
			return fs.SkipDir
		}
		if filePath == "/"+f.NameInArchive {
			*files = append(*files, f)
		}
		return nil
	}
}
