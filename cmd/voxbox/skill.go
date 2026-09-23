package main

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

// skill 命令族：把内嵌的 Agent Skill 导出到本机。
//
// 存在的理由：voxbox 以「单个二进制」形态分发（使用方没有源码、也不一定装了 Go），
// skill 若只躺在仓库里，分发出去就用不上。embed 进二进制后，
// 收到二进制的人执行 `voxbox skill install` 即可让 AI Agent 认领全部能力。
//
//go:embed all:skillsdist
var skillDist embed.FS

// skillRoot embed 内的 skill 名（与 skillsdist/<name>/ 对应）。
const skillRoot = "voxbox"

func newSkillCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skill",
		Short: "导出内嵌的 Agent Skill（随二进制分发，无需源码）",
		Long: `把编译进二进制的 Agent Skill 导出到本机，供 Claude Code / WorkBuddy 等客户端识别。

无需源码或网络：skill 内容 embed 在可执行文件内，命令本身即分发渠道。
安装后 Agent 可据此调用 voxbox 的全部能力（语音/音乐/分离/翻译/妙记）。`,
		Example: `  voxbox skill install              # 安装到探测到的 skill 目录
  voxbox skill install --dir ~/.claude/skills
  voxbox skill print                # 打印 SKILL.md 到 stdout
  voxbox skill list                 # 列出内嵌文件`,
	}
	cmd.AddCommand(newSkillListCmd(), newSkillPrintCmd(), newSkillInstallCmd())
	return cmd
}

// skillFiles 列出 embed 内全部文件（相对 skill 根目录，如 SKILL.md、references/cli.md）。
func skillFiles() ([]string, error) {
	base := filepath.Join("skillsdist", skillRoot)
	var out []string
	err := fs.WalkDir(skillDist, base, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, rerr := filepath.Rel(base, p)
		if rerr != nil {
			return rerr
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	sort.Strings(out)
	return out, err
}

func newSkillListCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "列出内嵌 skill 的文件",
		RunE: func(c *cobra.Command, args []string) error {
			files, err := skillFiles()
			if err != nil {
				return err
			}
			if jsonOut {
				printJSON(map[string]any{"skill": skillRoot, "files": files})
				return nil
			}
			fmt.Printf("%s（内嵌 %d 个文件）\n", skillRoot, len(files))
			for _, f := range files {
				fmt.Printf("  %s\n", f)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "stdout 输出机器可读 JSON")
	return cmd
}

func newSkillPrintCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "print [file]",
		Short: "打印内嵌 skill 文件内容（缺省 SKILL.md）",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			name := "SKILL.md"
			if len(args) == 1 {
				name = args[0]
			}
			b, err := skillDist.ReadFile(filepath.Join("skillsdist", skillRoot, filepath.FromSlash(name)))
			if err != nil {
				return fmt.Errorf("内嵌 skill 中不存在 %s（用 `voxbox skill list` 查看）", name)
			}
			_, err = os.Stdout.Write(b)
			return err
		},
	}
	return cmd
}

func newSkillInstallCmd() *cobra.Command {
	var (
		dir    string
		dryRun bool
	)
	cmd := &cobra.Command{
		Use:   "install",
		Short: "安装内嵌 skill 到本机 skill 目录",
		Long: `把内嵌 skill 写到 <dir>/voxbox/ 下（已存在则覆盖，便于随二进制升级）。

未指定 --dir 时，自动探测本机已存在的 Agent skill 目录并逐个安装：
  ~/.workbuddy/skills   WorkBuddy
  ~/.claude/skills      Claude Code
  ~/.codebuddy/skills   CodeBuddy
一个都没探测到时，回落安装到 ~/.workbuddy/skills。`,
		RunE: func(c *cobra.Command, args []string) error {
			dirs := []string{dir}
			if dir == "" {
				dirs = detectSkillDirs()
			}
			files, err := skillFiles()
			if err != nil {
				return err
			}
			for _, d := range dirs {
				target := filepath.Join(expandHome(d), skillRoot)
				for _, f := range files {
					b, rerr := skillDist.ReadFile(filepath.Join("skillsdist", skillRoot, filepath.FromSlash(f)))
					if rerr != nil {
						return rerr
					}
					dst := filepath.Join(target, filepath.FromSlash(f))
					if dryRun {
						fmt.Printf("[dry-run] %s\n", dst)
						continue
					}
					if merr := os.MkdirAll(filepath.Dir(dst), 0o755); merr != nil {
						return fmt.Errorf("创建目录失败: %w", merr)
					}
					if werr := os.WriteFile(dst, b, 0o644); werr != nil {
						return fmt.Errorf("写入 %s 失败: %w", dst, werr)
					}
					fmt.Printf("已写入 %s\n", dst)
				}
			}
			if dryRun {
				return nil
			}
			fmt.Fprintf(os.Stderr, "完成：%d 个文件 × %d 个目录。重启客户端后 Agent 即可识别 voxbox 能力。\n",
				len(files), len(dirs))
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "", "目标 skill 根目录（缺省自动探测）")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "只打印将要写入的路径，不落盘")
	return cmd
}

// detectSkillDirs 返回本机已存在的 Agent skill 根目录（绝对路径）；全都不存在时回落 WorkBuddy 目录。
func detectSkillDirs() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return []string{filepath.Join(".workbuddy", "skills")}
	}
	var out []string
	for _, rel := range []string{".workbuddy/skills", ".claude/skills", ".codebuddy/skills"} {
		p := filepath.Join(home, filepath.FromSlash(rel))
		if fi, serr := os.Stat(p); serr == nil && fi.IsDir() {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		out = append(out, filepath.Join(home, ".workbuddy", "skills"))
	}
	return out
}

// expandHome 展开开头的 ~（os 包不处理壳层波浪号）。
func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(p, "~"), "/"))
		}
	}
	return p
}
