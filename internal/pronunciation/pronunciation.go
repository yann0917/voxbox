// Package pronunciation 实现发音词典：词条 → 读音替换，在 TTS 合成前做文本预处理。
//
// 词典对所有合成引擎通用（替换发生在文本进引擎之前）：term 是原文写法，replacement
// 是希望引擎读出来的写法（respelling，如 {"GIF": "jiff"}、{"重庆": "chóngqìng"}）。
// 磁盘即真相：<dataDir>/pronunciation.json 单文件存储，无 DB 表；进程内常驻一份
// 内存镜像，Apply 走内存，增删改即时落盘。
//
// 任何词典层故障（文件损坏/未初始化）都静默降级为原文本，绝不阻断合成。
package pronunciation

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"
)

// ErrNotFound 词条不存在（Update/Delete）。
var ErrNotFound = errors.New("词条不存在")

// Entry 一条发音词条。Language 为 "*"（全语言生效）或二字母小写语言码
// （zh/en/ja/ko/yue…，仅请求语言前缀匹配时生效）。
type Entry struct {
	ID          string    `json:"id"`
	Term        string    `json:"term"`
	Replacement string    `json:"replacement"`
	Language    string    `json:"language"`
	Enabled     bool      `json:"enabled"`
	CreatedAt   time.Time `json:"created_at"`
}

// Store 发音词典存储。零值不可用，经 Init（进程单例）或 New（测试独立实例）构造。
type Store struct {
	mu      sync.RWMutex
	path    string
	entries []Entry
}

// Init 进程级单例：指向 <dataDir>/pronunciation.json 并加载存量。service 启动期调用一次。
func Init(dataDir string) {
	defMu.Lock()
	defer defMu.Unlock()
	def = New(filepath.Join(dataDir, fileName))
}

// Default 返回进程级单例（Init 之前调用返回空库，Apply 直通）。
func Default() *Store {
	defMu.Lock()
	defer defMu.Unlock()
	return def
}

const fileName = "pronunciation.json"

var (
	defMu sync.Mutex
	def   = New("")
)

// New 构造独立实例（测试用）；path 为空表示纯内存空库。
func New(path string) *Store {
	s := &Store{path: path}
	if path == "" {
		return s
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return s // 首次使用无文件，空库起步
	}
	var entries []Entry
	if json.Unmarshal(raw, &entries) != nil {
		return s // 文件损坏视为无词典：静默降级，不阻断合成
	}
	s.entries = entries
	return s
}

// List 返回词条快照（按创建时间升序，与文件顺序一致）。
func (s *Store) List() []Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Entry, len(s.entries))
	copy(out, s.entries)
	return out
}

// Add 新增词条：term/replacement 非空，language 归一化（"*" 或二字母码），
// 同一（词条小写折叠, 语言范围）组合不允许重复。成功返回入库后的完整词条。
func (s *Store) Add(term, replacement, language string, enabled bool) (Entry, error) {
	e := Entry{
		Term:        strings.TrimSpace(term),
		Replacement: strings.TrimSpace(replacement),
		Enabled:     enabled,
	}
	if e.Term == "" {
		return e, fmt.Errorf("词条不能为空")
	}
	if e.Replacement == "" {
		return e, fmt.Errorf("替换写法不能为空")
	}
	lang, err := NormalizeLanguage(language)
	if err != nil {
		return e, err
	}
	e.Language = lang

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, old := range s.entries {
		if old.Language == lang && strings.EqualFold(old.Term, e.Term) {
			return e, fmt.Errorf("词条 %q（%s）已存在", e.Term, langDisplay(lang))
		}
	}
	e.ID = newID()
	e.CreatedAt = time.Now()
	s.entries = append(s.entries, e)
	return e, s.persistLocked()
}

// Update 全量更新指定词条（前端按行整行提交）。
func (s *Store) Update(id, term, replacement, language string, enabled bool) (Entry, error) {
	e := Entry{Term: strings.TrimSpace(term), Replacement: strings.TrimSpace(replacement), Enabled: enabled}
	if e.Term == "" {
		return e, fmt.Errorf("词条不能为空")
	}
	if e.Replacement == "" {
		return e, fmt.Errorf("替换写法不能为空")
	}
	lang, err := NormalizeLanguage(language)
	if err != nil {
		return e, err
	}
	e.Language = lang

	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.entries {
		if s.entries[i].ID != id {
			continue
		}
		for j, old := range s.entries {
			if j != i && old.Language == lang && strings.EqualFold(old.Term, e.Term) {
				return e, fmt.Errorf("词条 %q（%s）已存在", e.Term, langDisplay(lang))
			}
		}
		e.ID = id
		e.CreatedAt = s.entries[i].CreatedAt
		s.entries[i] = e
		return e, s.persistLocked()
	}
	return e, ErrNotFound
}

// Delete 删除指定词条；不存在返回 ErrNotFound。
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.entries {
		if s.entries[i].ID == id {
			s.entries = append(s.entries[:i], s.entries[i+1:]...)
			return s.persistLocked()
		}
	}
	return ErrNotFound
}

// persistLocked 原子落盘（tmp + rename）；调用方须持写锁。落盘失败仅向上报错，
// 内存镜像已更新（下次进程启动以盘上旧版为准）。
func (s *Store) persistLocked() error {
	if s.path == "" {
		return nil
	}
	raw, err := json.MarshalIndent(s.entries, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// NormalizeLanguage 语言范围归一：""/"*" → "*"（全语言）；zh-CN/Chinese → zh；
// 不足二字母或含非字母字符报错。
func NormalizeLanguage(language string) (string, error) {
	s := strings.ToLower(strings.TrimSpace(language))
	if s == "" || s == "*" {
		return "*", nil
	}
	if v, ok := langAliases[s]; ok {
		return v, nil
	}
	letters := 0
	for _, r := range s {
		if r >= 'a' && r <= 'z' {
			letters++
			continue
		}
		break // 前导字母段结束（zh-… / zh_… 之类在第二段截断）
	}
	if letters < 2 {
		return "", fmt.Errorf("语言范围须为 * 或二字母语言码（如 zh、en），当前 %q", language)
	}
	return s[:2], nil
}

// langDisplay 面向用户的语言范围展示（错误消息用）。
func langDisplay(lang string) string {
	if lang == "*" {
		return "全语言"
	}
	return lang
}

// langAliases 常见全称语言名 → 二字母码（本地引擎 qwen3 家族的语言参数是全称）。
var langAliases = map[string]string{
	"chinese":   "zh",
	"english":   "en",
	"japanese":  "ja",
	"korean":    "ko",
	"cantonese": "yue",
}

func newID() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// foldRune 匹配用的 rune 归一：小写折叠（对 CJK 无操作）。
func foldRune(r rune) rune { return unicode.ToLower(r) }

// isLatinWordRune 拉丁词字符：ASCII 字母/数字/下划线。词边界判定专用——
// 邻接 CJK 不算词字符（"GIF 图" 里 GIF 后面紧跟汉字仍视为边界，可命中）。
func isLatinWordRune(r rune) bool {
	return r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}
