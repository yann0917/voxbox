package store

import (
	"time"

	"gorm.io/gorm"
)

type TaskStatus string

// User 服务器账号（公网部署的登录门；本地单机自用即单 admin）。
// APITokenHash 只存 sha256——API token（MCP/CLI 用）明文仅在签发与轮换响应里出现一次。
type User struct {
	ID                 string  `gorm:"primaryKey;size:36"`
	Username           string  `gorm:"uniqueIndex;size:64"`
	PasswordHash       string  `gorm:"size:128"` // bcrypt
	Role               string  `gorm:"size:16;default:user"`
	MustChangePassword bool    // 首次登录（引导随机密码）后强制改密，改完清除
	APITokenHash       *string `gorm:"uniqueIndex;size:64"` // sha256(tbx_<32hex>)，明文不落库；NULL=未签发（空串会撞唯一索引）
	TokenIssuedAt      time.Time
	CreatedAt          time.Time
	UpdatedAt          time.Time // 密码/token 变更留痕；不做软删——username 唯一索引与软删冲突，停用走显式标志（P2）
}

// Session 服务端会话：Cookie 值即 Token 明文，改密/登出删行即吊销（签名 Cookie 做不到服务端吊销）。
type Session struct {
	Token     string `gorm:"primaryKey;size:64"` // 32B 随机 hex
	UserID    string `gorm:"size:36;index"`
	ExpiresAt time.Time
	CreatedAt time.Time
}

const (
	StatusPending     TaskStatus = "pending"
	StatusRunning     TaskStatus = "running"
	StatusSucceeded   TaskStatus = "succeeded"
	StatusFailed      TaskStatus = "failed"
	StatusCanceled    TaskStatus = "canceled"
	StatusInterrupted TaskStatus = "interrupted"
)

type Task struct {
	ID           string     `gorm:"primaryKey;size:36"`
	UserID       string     `gorm:"size:36;index"` // 所有者（公网多用户隔离）；空=本地/历史数据，仅 admin 可见
	Provider     string     `gorm:"size:32;index"`
	Tool         string     `gorm:"size:32;index"`
	Status       TaskStatus `gorm:"size:16;index"`
	Params       string     `gorm:"type:text"`
	Input        string     `gorm:"type:text"`              // 原始输入引用 JSON（file_ids/artifact_input），供重跑与回放溯源；CLI 直传本地路径时为空
	Title        string     `gorm:"size:255"`               // 人类可读标题（URL/文本取摘要）：历史列表与搜索展示
	TitleEdited  bool       `gorm:"not null;default:false"` // 用户手改过标题：ASR 完成时自动派生让位
	Tags         string     `gorm:"size:512"`               // 用户标签 JSON 数组字符串（如 ["会议","粤语"]），搜索 LIKE 覆盖
	Summary      string     `gorm:"type:text"`              // 任务完成摘要 JSON（provider.TaskOutput.Summary 序列化）
	Progress     int
	ProgressNote string
	Error        string `gorm:"type:text"`
	CostMS       int64
	// DeletedAt 软删除（gorm 约定，查询自动过滤）：删除可恢复；磁盘产物文件本就不随
	// 删除清理，软删不新增磁盘负担。彻底清除（含磁盘）将来走 Unscoped + 文件 GC。
	DeletedAt gorm.DeletedAt `gorm:"index"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Artifact struct {
	ID         string `gorm:"primaryKey;size:36"`
	TaskID     string `gorm:"size:36;index"`
	UserID     string `gorm:"size:36;index"` // 冗余所属任务的所有者，读取鉴权免联表（引擎随任务写入）
	Kind       string `gorm:"size:16"`       // audio|transcript|dialog|subtitle|translation|minutes
	Path       string // data 目录相对路径；_out 重定向时可为绝对路径
	Filename   string `gorm:"size:255"`
	Format     string `gorm:"size:16"`
	Size       int64
	DurationMS int64
	Meta       string `gorm:"type:text"` // JSON
	CreatedAt  time.Time
}

// Prompt 提示词库的用户自定义条目；内置条目烤在 internal/prompts 不入库。
// 内容是发给大模型的系统提示模板，朗读约束由服务端在调用时统一追加，不依赖用户自觉。
type Prompt struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	UserID      string    `gorm:"size:36;index" json:"-"` // 所有者（公网多用户隔离）；提示词始终按人隔离，admin 也不例外
	Name        string    `gorm:"size:64" json:"name"`
	Category    string    `gorm:"size:32" json:"category"` // 分组标签，与内置条目的 Category 同一命名空间
	Description string    `gorm:"size:255" json:"description"`
	Content     string    `gorm:"type:text" json:"content"`
	Kind        string    `gorm:"size:16;default:generate" json:"kind"` // generate|polish
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// TranslateGlossary AI 字幕翻译自动沉淀的术语表:按目标语言隔离,机器生成与维护,
// 无 UI(区别于 config.yaml 手工 dicts 资产)。同 (target_lang, src) 唯一,重复学习
// 覆盖译法并刷新 updated_at;修剪按最近使用保留 glossaryKeepMax 条。
type TranslateGlossary struct {
	ID         uint   `gorm:"primaryKey"`
	TargetLang string `gorm:"size:16;uniqueIndex:idx_glossary_lang_src"`
	Src        string `gorm:"size:128;uniqueIndex:idx_glossary_lang_src"`
	Dst        string `gorm:"size:255"`
	CreatedAt  time.Time
	UpdatedAt  time.Time
}
