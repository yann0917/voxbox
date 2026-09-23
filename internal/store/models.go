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
	Input        string     `gorm:"type:text"` // 原始输入引用 JSON（file_ids/artifact_input），供重跑与回放溯源；CLI 直传本地路径时为空
	Title        string     `gorm:"size:255"`  // 人类可读标题（URL/文本取摘要）：历史列表与搜索展示
	Summary      string     `gorm:"type:text"` // 任务完成摘要 JSON（provider.TaskOutput.Summary 序列化）
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
