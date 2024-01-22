package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	stdpath "path"
	"regexp"
	"strconv"
	"strings"
	"time"

	archiver "github.com/SheltonZhu/remote-archive-decompression-server"
	bufra "github.com/avvmoto/buf-readerat"
	"github.com/gin-gonic/gin"
	stdArchiever "github.com/mholt/archiver/v4"
	"github.com/snabb/httpreaderat"
)

func main() {
	port := flag.Int("port", 8080, "port to listen on")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage of %s:\n", os.Args[0])
		flag.PrintDefaults()
	}

	flag.Parse()

	r := gin.Default()

	r.Any("/list", List)
	r.Any("/get", Get)
	r.Any("/down", Down)

	r.Run(fmt.Sprintf(":%d", *port))
}

var (
	ErrNotSupport   = errors.New("not support")
	ErrRelativePath = errors.New("access using relative path is not allowed")
)

type PageReq struct {
	Page    int `json:"page"     form:"page"`
	PerPage int `json:"per_page" form:"per_page"`
}

type ListReq struct {
	PageReq
	RawLink string `json:"link"    form:"link"    binding:"required"`
	Path    string `json:"path"    form:"path"`
	Cascade bool   `json:"cascade" form:"cascade"`
}

type ObjResp struct {
	Name          string    `json:"name"`
	Size          int64     `json:"size"`
	IsDir         bool      `json:"is_dir"`
	Modified      time.Time `json:"modified"`
	Created       time.Time `json:"created"`
	NameInArchive string    `json:"name_in_archive"`
	LinkTarget    string    `json:"link_target"`
}

type ListResp struct {
	Content []ObjResp `json:"content"`
	Total   int64     `json:"total"`
}

func List(c *gin.Context) {
	// 非zip, 7zip, 无法流式解压, 限制大文件
	var req ListReq
	if err := c.ShouldBind(&req); err != nil {
		ErrorStrResp(c, err.Error(), 400)
		return
	}

	reqPath, err := handerReqPath(req.Path, true)
	if err != nil {
		ErrorStrResp(c, err.Error(), 500)
		return
	}

	arc, err := getArchive(c, req.RawLink)
	if err != nil {
		ErrorStrResp(c, err.Error(), 500)
		return
	}

	dirFunc := arc.ExtractDirs
	if req.Cascade {
		dirFunc = arc.CascadeExtractDirs
	}
	dFiles, err := dirFunc(c, reqPath)
	if err != nil {
		ErrorStrResp(c, ErrNotSupport.Error(), 500)
		return
	}

	objs := make([]ObjResp, 0, len(dFiles))
	for _, f := range dFiles {
		objs = append(objs, buildObj(&f))
	}
	total, objs := pagination(objs, &req.PageReq)

	SuccessResp(c, ListResp{
		Content: objs,
		Total:   int64(total),
	})
}

type GetReq struct {
	RawLink string `json:"link" form:"link" binding:"required"`
	Path    string `json:"path" form:"path"`
}

type GetResp struct {
	ObjResp
}

func Get(c *gin.Context) {
	// 非zip, 7zip, 无法流式解压, 限制大文件
	var req GetReq
	if err := c.ShouldBind(&req); err != nil {
		ErrorStrResp(c, err.Error(), 400)
		return
	}

	reqPath, err := handerReqPath(req.Path, false)
	if err != nil {
		ErrorStrResp(c, err.Error(), 500)
		return
	}

	arc, err := getArchive(c, req.RawLink)
	if err != nil {
		ErrorStrResp(c, err.Error(), 500)
		return
	}

	dFile, err := arc.ExtractFile(c, reqPath)
	if err != nil {
		ErrorStrResp(c, ErrNotSupport.Error(), 500)
		return
	}

	SuccessResp(c, GetResp{ObjResp: buildObj(dFile)})
}

func Down(c *gin.Context) {
	// 非zip, 7zip, 无法流式解压, 限制大文件
	var req GetReq
	if err := c.ShouldBind(&req); err != nil {
		ErrorStrResp(c, err.Error(), 400)
		return
	}

	reqPath, err := handerReqPath(req.Path, false)
	if err != nil {
		ErrorStrResp(c, err.Error(), 500)
		return
	}

	arc, err := getArchive(c, req.RawLink)
	if err != nil {
		ErrorStrResp(c, err.Error(), 500)
		return
	}

	dFile, err := arc.ExtractFile(c, reqPath)
	if err != nil {
		ErrorStrResp(c, ErrNotSupport.Error(), 500)
		return
	}

	SuccessStreamResp(c, *dFile)
}

func getArchive(c *gin.Context, rawURL string) (*archiver.ArchiverExtractor, error) {
	httpReaderAtReq, _ := http.NewRequest(http.MethodGet, rawURL, nil)
	httpReaderAtReq.Header.Set("Cookie", c.GetHeader("Cookie"))
	httpReaderAtReq.Header.Set("User-Agent", c.GetHeader("User-Agent"))
	htrdr, err := httpreaderat.New(nil, httpReaderAtReq, nil)
	if err != nil {
		return nil, err
	}
	bhtrdr := bufra.NewBufReaderAt(htrdr, 1024*1024)
	return archiver.DetectArchive(rawURL, io.NewSectionReader(bhtrdr, 0, htrdr.Size()))
}

func buildObj(f *stdArchiever.File) ObjResp {
	return ObjResp{
		Name:          f.Name(),
		Size:          f.Size(),
		IsDir:         f.IsDir(),
		Created:       f.ModTime(),
		Modified:      f.ModTime(),
		NameInArchive: f.NameInArchive,
		LinkTarget:    f.LinkTarget,
	}
}

func JoinBasePath(basePath, reqPath string) (string, error) {
	/** relative path:
	 * 1. ..
	 * 2. ../
	 * 3. /..
	 * 4. /../
	 * 5. /a/b/..
	 */
	if reqPath == ".." ||
		strings.HasSuffix(reqPath, "/..") ||
		strings.HasPrefix(reqPath, "../") ||
		strings.Contains(reqPath, "/../") {
		return "", ErrRelativePath
	}
	return stdpath.Join(FixAndCleanPath(basePath), FixAndCleanPath(reqPath)), nil
}

// FixAndCleanPath
// The upper layer of the root directory is still the root directory.
// So ".." And "." will be cleared
// for example
// 1. ".." or "." => "/"
// 2. "../..." or "./..." => "/..."
// 3. "../.x." or "./.x." => "/.x."
// 4. "x//\\y" = > "/z/x"
func FixAndCleanPath(path string) string {
	path = strings.ReplaceAll(path, "\\", "/")
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return stdpath.Clean(path)
}

func handerReqPath(path string, isDir bool) (string, error) {
	reqPath, err := JoinBasePath("", path)
	if err != nil {
		return "", err
	}
	if path != "/" {
		reqPath = stdpath.Join(path + "/")
	}
	if isDir && reqPath != "/" {
		reqPath += "/"
	}
	return reqPath, nil
}

func pagination[T any](objs []T, req *PageReq) (int, []T) {
	pageIndex, pageSize := req.Page, req.PerPage
	if pageIndex <= 0 {
		pageIndex = 1
	}
	if pageSize <= 0 {
		pageSize = 10
	}
	total := len(objs)
	start := (pageIndex - 1) * pageSize
	if start > total {
		return total, []T{}
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	return total, objs[start:end]
}

func ErrorStrResp(c *gin.Context, msg string, code int) {
	c.JSON(200, Resp[interface{}]{
		Code:    code,
		Message: msg,
		Data:    nil,
	})
	c.Abort()
}

func SuccessResp(c *gin.Context, data ...interface{}) {
	if len(data) == 0 {
		c.JSON(200, Resp[interface{}]{
			Code:    200,
			Message: "success",
			Data:    nil,
		})
		return
	}
	c.JSON(200, Resp[interface{}]{
		Code:    200,
		Message: "success",
		Data:    data,
	})
}

type Resp[T any] struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    T      `json:"data"`
}

func SuccessStreamResp(c *gin.Context, f stdArchiever.File) {
	frc, err := f.Open()
	if err != nil {
		ErrorStrResp(c, ErrNotSupport.Error(), 500)
		return
	}
	defer frc.Close()

	defaultMIME := "application/octet-stream"
	switch {
	case strings.HasSuffix(f.Name(), ".zip"):
		defaultMIME = "application/zip"
	case strings.HasSuffix(f.Name(), ".7z"):
		defaultMIME = "application/x-7z-compressed"
	case strings.HasSuffix(f.Name(), ".rar"):
		defaultMIME = "application/x-rar-compressed"
	case strings.HasSuffix(f.Name(), ".tar"):
		defaultMIME = "application/x-tar"
	case strings.HasSuffix(f.Name(), ".gz"):
		defaultMIME = "application/gzip"
	case strings.HasSuffix(f.Name(), ".bz2"):
		defaultMIME = "application/x-bzip2"
	case strings.HasSuffix(f.Name(), ".xz"):
		defaultMIME = "application/x-xz"
	case strings.HasSuffix(f.Name(), ".lz4"):
		defaultMIME = "application/x-lz4"
	case strings.HasSuffix(f.Name(), ".zst"):
		defaultMIME = "application/zstd"
	case strings.HasSuffix(f.Name(), ".mkv"):
		defaultMIME = "video/x-matroska"
	case strings.HasSuffix(f.Name(), ".mp4"):
		defaultMIME = "video/mp4"
	case strings.HasSuffix(f.Name(), ".mp3"):
		defaultMIME = "audio/mpeg"
	case strings.HasSuffix(f.Name(), ".flac"):
		defaultMIME = "audio/flac"
	case strings.HasSuffix(f.Name(), ".wav"):
		defaultMIME = "audio/wav"
	case strings.HasSuffix(f.Name(), ".ogg"):
		defaultMIME = "audio/ogg"
	case strings.HasSuffix(f.Name(), ".jpg"):
		defaultMIME = "image/jpeg"
	case strings.HasSuffix(f.Name(), ".jpeg"):
		defaultMIME = "image/jpeg"
	case strings.HasSuffix(f.Name(), ".png"):
		defaultMIME = "image/png"
	case strings.HasSuffix(f.Name(), ".gif"):
		defaultMIME = "image/gif"
	case strings.HasSuffix(f.Name(), ".webp"):
		defaultMIME = "image/webp"
	case strings.HasSuffix(f.Name(), ".pdf"):
		defaultMIME = "application/pdf"
	}
	totalLength := strconv.FormatInt(f.Size(), 10)
	c.Writer.Header().Set("Accept-Ranges", "bytes")
	c.Writer.Header().Set("Content-Type", defaultMIME)
	c.Writer.Header().Set("Content-Disposition", "attachment; filename="+f.Name())
	// c.Writer.Header().Set("Content-Transfer-Encoding", "binary")
	c.Writer.Header().Set("Content-Length", totalLength)
	c.Writer.Header().Set("Expires", "0")
	c.Writer.Header().Set("Cache-Control", "must-revalidate")
	c.Writer.Header().Set("Pragma", "public")
	rangeHeader := c.GetHeader("Range")
	if rangeHeader != "" {
		ranges, err := parseRangeHeader(rangeHeader, f.Size())
		if err != nil {
			ErrorStrResp(c, err.Error(), http.StatusRequestedRangeNotSatisfiable)
			return
		}

		// 只处理单个范围的情况，例如 "bytes=0-999"
		if len(ranges) == 1 {
			start := ranges[0].Start
			end := ranges[0].End

			// 计算范围长度
			length := end - start + 1

			// 创建字节切片来接收读取的数据
			buf := make([]byte, length)

			myReader := NewMyReadAtReader(frc, f.Size())
			// 调用 ReadAt 方法读取指定范围的数据
			_, err := myReader.ReadAt(buf, start)
			if err != nil {
				ErrorStrResp(c, err.Error(), 500)
				return
			}

			// 设置响应头部
			c.Writer.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%s", start, end, totalLength))
			c.Writer.Header().Set("Content-Length", strconv.FormatInt(length, 10))
			c.Writer.WriteHeader(http.StatusPartialContent)

			// 将数据写入响应体
			_, _ = c.Writer.Write(buf)
			return
		}
	}
	c.Status(200)

	io.Copy(c.Writer, frc)
}

type httpRange struct {
	Start int64
	End   int64
}

// 解析 Range 头部
func parseRangeHeader(header string, totalLength int64) ([]httpRange, error) {
	ranges := make([]httpRange, 0)

	// 检查 Range 头部格式是否合法
	pattern := regexp.MustCompile(`^\s*bytes\s*=\s*(.*)$`)
	match := pattern.FindStringSubmatch(header)
	if len(match) != 2 {
		return nil, errors.New("Invalid Range Header")
	}

	rangeSpecs := strings.Split(match[1], ",")
	for _, rangeSpec := range rangeSpecs {
		rangeParts := strings.Split(rangeSpec, "-")
		if len(rangeParts) != 2 {
			return nil, errors.New("Invalid Range Header")
		}

		var start, end int64

		// 解析起始位置
		if rangeParts[0] == "" {
			start = totalLength - 1 - toInt64(rangeParts[1])
			end = totalLength - 1
		} else if rangeParts[1] == "" {
			start = toInt64(rangeParts[0])
			end = totalLength - 1
		} else {
			start = toInt64(rangeParts[0])
			end = toInt64(rangeParts[1])
		}

		// 检查范围是否合法
		if start > end || start >= totalLength || end >= totalLength {
			return nil, errors.New("Invalid Range Header")
		}

		ranges = append(ranges, httpRange{Start: start, End: end})
	}

	return ranges, nil
}

func toInt64(s string) int64 {
	val, _ := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	return val
}

type MyReadAtReader struct {
	r    io.Reader
	data []byte
	size int64
}

func NewMyReadAtReader(r io.Reader, size int64) *MyReadAtReader {
	return &MyReadAtReader{r: r, size: size}
}

func (r *MyReadAtReader) ReadAt(p []byte, off int64) (n int, err error) {
	if off < 0 || off >= int64(r.size) {
		return 0, io.EOF
	}

	discat := make([]byte, off)
	if _, err := r.r.Read(discat); err != nil {
		return 0, err
	}

	if n, err := r.r.Read(p); err != nil {
		return 0, err
	} else if n < len(p) {
		err = io.EOF
	}

	return n, err
}
