package localmodel

import (
	"archive/tar"
	"archive/zip"
	"compress/bzip2"
	"compress/gzip"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// extractLimit 解包字节数上限(防 zip 炸弹),与既有管线约定同值(8GB)。
const extractLimit = 8 << 30

// extractArchive 解包归档到 destDir。whitelist 非空时只解出命中成员(文件条目扁平化到
// destDir 根,供模型归档取 onnx/tokens;目录条目如 "espeak-ng-data/" 保留目录内结构);
// 为空时保留包内相对结构(引擎包自包含 rpath,不能挪动 lib 布局)。
// 安全:成员路径 Clean 后必须留在 destDir 内(拒绝绝对路径与 ..);累计解包字节超 extractLimit 报错。
func extractArchive(format, src, destDir string, whitelist []string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	keepFiles, keepDirs := splitWhitelist(whitelist)
	switch format {
	case "zip":
		return extractZip(src, destDir, keepFiles, keepDirs)
	case "tar.gz", "tar.bz2", "tar":
		return extractTar(format, f, destDir, keepFiles, keepDirs)
	default:
		return fmt.Errorf("不支持的归档格式: %s", format)
	}
}

// splitWhitelist 拆分白名单:文件条目按 basename 扁平化命中;目录条目(尾缀 /)按路径
// 组件命中并保留目录内结构。whitelist 为 nil 表示无白名单(整包解出),返回 (nil, nil)。
func splitWhitelist(whitelist []string) (files map[string]bool, dirs []string) {
	if whitelist == nil {
		return nil, nil
	}
	files = map[string]bool{}
	for _, w := range whitelist {
		if d := strings.TrimSuffix(w, "/"); d != w {
			dirs = append(dirs, d)
		} else {
			files[w] = true
		}
	}
	return files, dirs
}

// whitelistRel 白名单命中判定,返回解包落盘的相对路径:文件条目扁平化到 destDir 根;
// 目录条目保留从目录名起的子路径(kokoro 的 espeak-ng-data 等内嵌数据目录不能散架)。
func whitelistRel(name string, files map[string]bool, dirs []string) (string, bool) {
	if files[path.Base(name)] {
		return path.Base(name), true
	}
	for _, d := range dirs {
		if name == d || strings.HasPrefix(name, d+"/") {
			return name, true
		}
		if i := strings.Index(name, "/"+d+"/"); i >= 0 {
			return name[i+1:], true
		}
	}
	return "", false
}

// openTarReader 按格式包一层解压 reader:tar.gz 用 gzip,tar.bz2 用 bzip2(标准库仅解压方向),tar 直读。
// 返回的第一个值是需要关闭的外层 reader(仅 gzip 有 Close)。
func openTarReader(format string, r io.Reader) (io.Reader, *tar.Reader, error) {
	switch format {
	case "tar.gz":
		zr, err := gzip.NewReader(r)
		if err != nil {
			return nil, nil, err
		}
		return zr, tar.NewReader(zr), nil
	case "tar.bz2":
		br := bzip2.NewReader(r)
		return br, tar.NewReader(br), nil
	default:
		return r, tar.NewReader(r), nil
	}
}

func extractTar(format string, f *os.File, destDir string, keepFiles map[string]bool, keepDirs []string) error {
	closer, tr, err := openTarReader(format, f)
	if err != nil {
		return err
	}
	defer func() {
		if c, ok := closer.(io.Closer); ok {
			c.Close()
		}
	}()
	destRoot := filepath.Clean(destDir) + string(os.PathSeparator)
	var written int64
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		name := path.Clean(hdr.Name)
		if name == "." {
			// 归档根目录条目(GNU tar 打包常见 ./ 前缀):必然落在 destDir 内,跳过。
			continue
		}
		if strings.HasPrefix(name, "../") || path.IsAbs(name) {
			return fmt.Errorf("非法归档条目路径: %s", hdr.Name)
		}
		if hdr.Typeflag == tar.TypeDir {
			continue
		}
		rel := name
		if keepFiles != nil || keepDirs != nil {
			var ok bool
			if rel, ok = whitelistRel(name, keepFiles, keepDirs); !ok {
				continue
			}
		}
		dest := filepath.Join(destDir, filepath.FromSlash(rel))
		if !strings.HasPrefix(filepath.Clean(dest)+string(os.PathSeparator), destRoot) {
			return fmt.Errorf("非法归档条目路径: %s", hdr.Name)
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		n, err := copyLimited(dest, tr, hdr.Size, &written)
		if err != nil {
			return err
		}
		_ = n
	}
}

func extractZip(src, destDir string, keepFiles map[string]bool, keepDirs []string) error {
	zr, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer zr.Close()
	destRoot := filepath.Clean(destDir) + string(os.PathSeparator)
	var written int64
	for _, zf := range zr.File {
		name := path.Clean(zf.Name)
		if name == "." {
			// zip 根目录条目:必然落在 destDir 内,跳过(与 tar 分支同语义)。
			continue
		}
		if strings.HasPrefix(name, "../") || path.IsAbs(name) {
			return fmt.Errorf("非法归档条目路径: %s", zf.Name)
		}
		if zf.FileInfo().IsDir() {
			continue
		}
		rel := name
		if keepFiles != nil || keepDirs != nil {
			var ok bool
			if rel, ok = whitelistRel(name, keepFiles, keepDirs); !ok {
				continue
			}
		}
		dest := filepath.Join(destDir, filepath.FromSlash(rel))
		if !strings.HasPrefix(filepath.Clean(dest)+string(os.PathSeparator), destRoot) {
			return fmt.Errorf("非法归档条目路径: %s", zf.Name)
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		rc, err := zf.Open()
		if err != nil {
			return err
		}
		if _, err := copyLimited(dest, rc, int64(zf.UncompressedSize64), &written); err != nil {
			rc.Close()
			return err
		}
		rc.Close()
	}
	return nil
}

// copyLimited 落一个成员并推进全局解包字节计数(超限即失败)。
func copyLimited(dest string, r io.Reader, size int64, written *int64) (int64, error) {
	if *written+size > extractLimit {
		return 0, fmt.Errorf("归档解包超过 %dGB 上限", extractLimit>>30)
	}
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return 0, err
	}
	defer out.Close()
	n, err := io.Copy(out, r)
	*written += n
	if err != nil {
		return n, err
	}
	if size > 0 && n != size {
		return n, fmt.Errorf("归档成员不完整:%s 已解 %d 预期 %d", dest, n, size)
	}
	return n, nil
}

// findBinaries 在解包目录中定位引擎可执行文件(白名单名称,含 .exe 变体);
// 递归 walk 命中子目录(sherpa 包二进制在 bin/ 下);命中后 chmod 0755 并返回绝对路径列表。
// 缺任一即报错(引擎包不完整)。
func findBinaries(dir string, names []string) ([]string, error) {
	var found []string
	for _, name := range names {
		var hits []string
		_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			base := d.Name()
			if base == name || base == name+".exe" {
				hits = append(hits, p)
			}
			return nil
		})
		if len(hits) == 0 {
			return nil, fmt.Errorf("引擎包不完整:未找到 %s", name)
		}
		sort.Strings(hits)
		if err := os.Chmod(hits[0], 0o755); err != nil {
			return nil, err
		}
		found = append(found, hits[0])
	}
	return found, nil
}
