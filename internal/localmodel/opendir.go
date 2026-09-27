package localmodel

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
)

// OpenDir 用平台文件管理器打开模型目录(桌面形态「打开模型目录」按钮)。
// Start 不 Wait:仅找不到可执行程序(如无头 linux 无 xdg-open)时报错,
// 文件管理器自身退出码不校验(explorer 惯例返回非零)。
func OpenDir(dir string) error {
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return fmt.Errorf("模型目录不存在: %s", dir)
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", dir)
	case "windows":
		// explorer 不经 cmd,绕开 cmd 参数转义坑;Start 不 Wait,非零退出码无影响
		cmd = exec.Command("explorer", dir)
	default:
		cmd = exec.Command("xdg-open", dir)
	}
	return cmd.Start()
}
