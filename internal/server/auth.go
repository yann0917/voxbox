package server

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/yann0917/voxbox/internal/store"
)

// 会话与 API token 的常量。API token 明文形如 tbx_<32hex>，只在签发/轮换响应中出现一次，
// 库里仅存 sha256；Web 会话走服务端 Session 行（可吊销），不走签名 Cookie。
const (
	sessionCookieName = "tbx_session"
	sessionTTL        = 7 * 24 * time.Hour
	apiTokenPrefix    = "tbx_"

	CodeUnauthorized = 7 // 未登录 / 会话或 token 失效（CLI 退出码同语义）
	CodeForbidden    = 8 // 已登录但权限不足（非 admin 触碰 admin 端点）
)

const principalKey = "voxbox.principal"

// Principal 当前请求身份：requireAuth 从 Cookie 会话或 Bearer token 解析后注入 gin context。
type Principal struct {
	ID                 string
	Username           string
	Role               string
	MustChangePassword bool
}

func (p *Principal) IsAdmin() bool { return p.Role == "admin" }

// searchScopeUserID 查询范围：admin 全量（含无主历史任务），普通用户仅本人。
func searchScopeUserID(p *Principal) string {
	if p.IsAdmin() {
		return ""
	}
	return p.ID
}

func principalFrom(c *gin.Context) *Principal {
	p, _ := c.Get(principalKey)
	pp, _ := p.(*Principal)
	return pp
}

type userDTO struct {
	ID                 string `json:"id"`
	Username           string `json:"username"`
	Role               string `json:"role"`
	Desktop            bool   `json:"desktop"`
	MustChangePassword bool   `json:"must_change_password"`
	HasToken           bool   `json:"has_token"`
}

func toUserDTO(u *store.User) userDTO {
	hasToken := u.APITokenHash != nil && *u.APITokenHash != ""
	return userDTO{ID: u.ID, Username: u.Username, Role: u.Role, MustChangePassword: u.MustChangePassword, HasToken: hasToken}
}

// EnsureBootstrapAdmin 首次启动（users 表为空）创建管理员：密码取 VOXBOX_ADMIN_PASSWORD
// 环境变量（部署自动化），否则生成 10 位无歧义随机密码，由 serve 打印到控制台（仅此一次）。
// 返回空串表示已存在用户，无需引导。
func (s *Server) EnsureBootstrapAdmin() (string, error) {
	n, err := s.svc.DB().CountUsers()
	if err != nil {
		return "", err
	}
	if n > 0 {
		return "", nil
	}
	username := "admin"
	if v := strings.TrimSpace(os.Getenv("VOXBOX_ADMIN_USERNAME")); v != "" {
		username = v
	}
	password := os.Getenv("VOXBOX_ADMIN_PASSWORD")
	generated := false
	if password == "" {
		password, err = randomPassword(10)
		if err != nil {
			return "", err
		}
		generated = true
	}
	hash, err := hashPassword(password)
	if err != nil {
		return "", err
	}
	u := &store.User{
		ID:                 newUUID(),
		Username:           username,
		PasswordHash:       hash,
		Role:               "admin",
		MustChangePassword: true,
	}
	if err := s.svc.DB().CreateUser(u); err != nil {
		return "", err
	}
	if !generated {
		return "", nil // 环境变量提供的密码不回显
	}
	return password, nil
}

// hashPassword bcrypt（x/crypto 官方实现，cost 默认 10）。
func hashPassword(pw string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	return string(b), err
}

func verifyPassword(hash, pw string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) == nil
}

func newAPIToken() (plain string, hash string, err error) {
	b := make([]byte, 16)
	if _, err = rand.Read(b); err != nil {
		return "", "", err
	}
	plain = apiTokenPrefix + hex.EncodeToString(b)
	return plain, hashAPIToken(plain), nil
}

func hashAPIToken(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}

func newSessionToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// randomPassword 生成无歧义随机密码（去掉 0O1lI 易混字符）。
func randomPassword(n int) (string, error) {
	const alphabet = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	out := make([]byte, n)
	for i, v := range b {
		out[i] = alphabet[int(v)%len(alphabet)]
	}
	return string(out), nil
}

func newUUID() string { return uuid.NewString() }

// loginLimiter 同 IP 登录失败限速：滑动窗口 5 次/分钟，纯内存（重启即清，公网防爆破够用）。
type loginLimiter struct {
	mu    sync.Mutex
	fails map[string][]time.Time
}

var limiter = &loginLimiter{fails: map[string][]time.Time{}}

func (l *loginLimiter) blocked(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	keep := l.fails[ip][:0]
	for _, t := range l.fails[ip] {
		if now.Sub(t) < time.Minute {
			keep = append(keep, t)
		}
	}
	l.fails[ip] = keep
	return len(keep) >= 5
}

func (l *loginLimiter) recordFail(ip string) {
	l.mu.Lock()
	l.fails[ip] = append(l.fails[ip], time.Now())
	l.mu.Unlock()
}

// ---- 中间件 ----

func unauthorized(c *gin.Context) {
	c.AbortWithStatusJSON(http.StatusUnauthorized, envelope{Code: CodeUnauthorized, Message: "未登录或登录已过期"})
}

// requireAuth Web/API 通用鉴权：Cookie 会话或 Bearer API token 任一命中即放行。
func (s *Server) requireAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		if p := s.authenticate(c.GetHeader("Authorization")); p != nil {
			c.Set(principalKey, p)
			c.Next()
			return
		}
		if ck, err := c.Cookie(sessionCookieName); err == nil && ck != "" {
			if p := s.sessionPrincipal(ck); p != nil {
				c.Set(principalKey, p)
				c.Next()
				return
			}
		}
		if p := s.desktopPrincipal(); p != nil {
			c.Set(principalKey, p)
			c.Next()
			return
		}
		unauthorized(c)
	}
}

// requireAdmin 管理员端点守卫（须挂在 requireAuth 之后）。
func (s *Server) requireAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		p := principalFrom(c)
		if p == nil || !p.IsAdmin() {
			c.AbortWithStatusJSON(http.StatusForbidden, envelope{Code: CodeForbidden, Message: "需要管理员权限"})
			return
		}
		c.Next()
	}
}

// sessionPrincipal 会话 token → 身份（过期行顺带清除）。
func (s *Server) sessionPrincipal(token string) *Principal {
	sess, err := s.svc.DB().GetSession(token)
	if err != nil {
		return nil
	}
	u, err := s.svc.DB().GetUserByID(sess.UserID)
	if err != nil {
		return nil
	}
	return &Principal{ID: u.ID, Username: u.Username, Role: u.Role, MustChangePassword: u.MustChangePassword}
}

// desktopPrincipal 桌面形态（VOXBOX_DESKTOP=1）免登录身份：以库内 admin 用户注入，
// 任务归属/搜索范围与真实用户行一致；admin 行由 EnsureBootstrapAdmin 保证存在。
// MustChangePassword 不透传（保持 false），首启强制改密流程自然跳过。
func (s *Server) desktopPrincipal() *Principal {
	if !s.desktop {
		return nil
	}
	u, err := s.svc.DB().GetUserByUsername("admin")
	if err != nil {
		return nil
	}
	return &Principal{ID: u.ID, Username: u.Username, Role: u.Role}
}

// authenticate "Authorization: Bearer tbx_..." → 身份；非 tbx_ 前缀（如 MediaKit 的
// Bearer）不在此通道，返回 nil 交给 Cookie 分支。
func (s *Server) authenticate(header string) *Principal {
	if !strings.HasPrefix(header, "Bearer "+apiTokenPrefix) {
		return nil
	}
	plain := strings.TrimPrefix(header, "Bearer ")
	u, err := s.svc.DB().GetUserByTokenHash(hashAPIToken(plain))
	if err != nil {
		return nil
	}
	return &Principal{ID: u.ID, Username: u.Username, Role: u.Role, MustChangePassword: u.MustChangePassword}
}

// mcpAuth MCP Streamable HTTP 专用：只认 Bearer API token（MCP 客户端可配自定义 header，
// 不携带 Cookie），失败按 JSON-RPC 错误体返回 401。
func (s *Server) mcpAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		p := s.authenticate(c.GetHeader("Authorization"))
		if p == nil {
			p = s.desktopPrincipal()
		}
		if p == nil {
			c.Header("WWW-Authenticate", `Bearer realm="voxbox"`)
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"jsonrpc": "2.0", "id": nil,
				"error": gin.H{"code": -32001, "message": "unauthorized: 缺少或无效的 API token"},
			})
			return
		}
		c.Set(principalKey, p)
		c.Next()
	}
}

// ---- 端点 ----

func (s *Server) login(c *gin.Context) {
	ip := c.ClientIP()
	if limiter.blocked(ip) {
		fail(c, CodeTaskFailed, "登录失败次数过多，请 1 分钟后再试")
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Username == "" || req.Password == "" {
		fail(c, CodeBadRequest, "参数错误：username/password 必填")
		return
	}
	u, err := s.svc.DB().GetUserByUsername(req.Username)
	if err == store.ErrNotFound || (err == nil && !verifyPassword(u.PasswordHash, req.Password)) {
		limiter.recordFail(ip)
		fail(c, CodeUnauthorized, "用户名或密码错误")
		return
	} else if err != nil {
		failErr(c, err)
		return
	}
	token, err := newSessionToken()
	if err != nil {
		failErr(c, err)
		return
	}
	if err := s.svc.DB().CreateSession(&store.Session{Token: token, UserID: u.ID, ExpiresAt: time.Now().Add(sessionTTL)}); err != nil {
		failErr(c, err)
		return
	}
	_ = s.svc.DB().CleanExpiredSessions()
	s.setSessionCookie(c, token, sessionTTL)
	ok(c, toUserDTO(u))
}

func (s *Server) logout(c *gin.Context) {
	if ck, err := c.Cookie(sessionCookieName); err == nil && ck != "" {
		_ = s.svc.DB().DeleteSession(ck)
	}
	s.setSessionCookie(c, "", -1)
	ok(c, gin.H{"ok": true})
}

func (s *Server) me(c *gin.Context) {
	p := principalFrom(c)
	u, err := s.svc.DB().GetUserByID(p.ID)
	if err != nil {
		failErr(c, err)
		return
	}
	d := toUserDTO(u)
	d.Desktop = s.desktop
	if s.desktop {
		// 桌面形态首启强制改密流程整体跳过（spec：注入 Principal MustChangePassword=false）；
		// must_change_password 透传自库内 admin 行（引导后恒 true），此处须压掉，
		// 否则前端会弹强制改密。
		d.MustChangePassword = false
	}
	ok(c, d)
}

// changePassword 自助改密：验证旧密码 → 更新 → 全端会话下线（前端改密后回登录页）。
func (s *Server) changePassword(c *gin.Context) {
	p := principalFrom(c)
	var req struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.OldPassword == "" || req.NewPassword == "" {
		fail(c, CodeBadRequest, "参数错误：old_password/new_password 必填")
		return
	}
	if len(req.NewPassword) < 8 {
		fail(c, CodeBadRequest, "新密码至少 8 位")
		return
	}
	u, err := s.svc.DB().GetUserByID(p.ID)
	if err != nil {
		failErr(c, err)
		return
	}
	if !verifyPassword(u.PasswordHash, req.OldPassword) {
		fail(c, CodeBadRequest, "旧密码错误")
		return
	}
	hash, err := hashPassword(req.NewPassword)
	if err != nil {
		failErr(c, err)
		return
	}
	if err := s.svc.DB().UpdateUserPassword(u.ID, hash, false); err != nil {
		failErr(c, err)
		return
	}
	_ = s.svc.DB().DeleteUserSessions(u.ID)
	ok(c, gin.H{"ok": true})
}

// rotateToken 轮换当前用户的 API token，明文仅本次响应返回。
func (s *Server) rotateToken(c *gin.Context) {
	p := principalFrom(c)
	plain, hash, err := newAPIToken()
	if err != nil {
		failErr(c, err)
		return
	}
	if err := s.svc.DB().UpdateUserToken(p.ID, hash); err != nil {
		failErr(c, err)
		return
	}
	ok(c, gin.H{"api_token": plain})
}

func (s *Server) setSessionCookie(c *gin.Context, value string, maxAge time.Duration) {
	// Secure 跟随请求：TLS 直连或反代透传 X-Forwarded-Proto 时启用（本地 HTTP 调试不启用）。
	secure := c.Request.TLS != nil ||
		strings.EqualFold(c.GetHeader("X-Forwarded-Proto"), "https")
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     sessionCookieName,
		Value:    value,
		Path:     "/",
		MaxAge:   int(maxAge.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   secure,
	})
}
