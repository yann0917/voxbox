package server

import (
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/yann0917/voxbox/internal/service"
	"github.com/yann0917/voxbox/internal/store"
)

// newBareServer 干净服务（未引导、未登录），鉴权链路测试自行动作。
func newBareServer(t *testing.T) (*httptest.Server, *Server) {
	t.Helper()
	svc, err := service.NewWithHome(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	s := New(svc)
	svc.StartEngine(s.Hub().Notify, 2)
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return ts, s
}

// testUser 直接建测试账号（用户管理端点属 P2，测试经 DB 建号）。
func testUser(t *testing.T, s *Server, username, role string) *store.User {
	t.Helper()
	hash, err := hashPassword("password-" + username)
	if err != nil {
		t.Fatal(err)
	}
	u := &store.User{ID: username + "-id", Username: username, PasswordHash: hash, Role: role}
	if err := s.svc.DB().CreateUser(u); err != nil {
		t.Fatal(err)
	}
	return u
}

// loginAs 用指定账号登录，返回带会话的客户端。
func loginAs(t *testing.T, ts *httptest.Server, username, password string) *http.Client {
	t.Helper()
	limiter = &loginLimiter{fails: map[string][]time.Time{}} // 全局限速器跨用例污染隔离
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	ac := &http.Client{Jar: jar}
	resp, err := ac.Post(ts.URL+"/api/auth/login", "application/json",
		strings.NewReader(`{"username":"`+username+`","password":"`+password+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var e envelope
	_ = json.NewDecoder(resp.Body).Decode(&e)
	if e.Code != 0 {
		t.Fatalf("login %s code = %d (%s)", username, e.Code, e.Message)
	}
	return ac
}

// getEnvelopeAllowStatus 同 getEnvelope 但返回原始 HTTP 状态码（401 断言用）。
func getEnvelopeAllowStatus(t *testing.T, ac *http.Client, url string) (envelope, int) {
	t.Helper()
	resp, err := ac.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var e envelope
	_ = json.NewDecoder(resp.Body).Decode(&e)
	return e, resp.StatusCode
}

func TestBootstrapAdminRandomPassword(t *testing.T) {
	ts, s := newBareServer(t)

	pw, err := s.EnsureBootstrapAdmin()
	if err != nil || pw == "" {
		t.Fatalf("首启应生成随机密码: %q err=%v", pw, err)
	}
	if len(pw) != 10 {
		t.Errorf("密码长度 = %d, want 10", len(pw))
	}
	// 二次调用不再生成（幂等）
	if pw2, err := s.EnsureBootstrapAdmin(); err != nil || pw2 != "" {
		t.Fatalf("已有用户时不应再生成: %q err=%v", pw2, err)
	}
	// 随机密码可登录，且 MustChangePassword=true
	ac := loginAs(t, ts, "admin", pw)
	me := getEnvelope(t, ac, ts.URL+"/api/auth/me")
	data, _ := me.Data.(map[string]any)
	if data["must_change_password"] != true {
		t.Errorf("首启 admin 应强制改密: %v", data)
	}
	if data["role"] != "admin" {
		t.Errorf("role = %v", data["role"])
	}
}

func TestLoginWrongPasswordAndRateLimit(t *testing.T) {
	ts, s := newBareServer(t)
	if _, err := s.EnsureBootstrapAdmin(); err != nil {
		t.Fatal(err)
	}
	// 重置限速窗口（同测试进程内其他用例可能已记失败）
	limiter = &loginLimiter{fails: map[string][]time.Time{}}

	// 错误密码：code 7（未认证语义），HTTP 200 包络
	resp, err := http.Post(ts.URL+"/api/auth/login", "application/json",
		strings.NewReader(`{"username":"admin","password":"wrong"}`))
	if err != nil {
		t.Fatal(err)
	}
	var e envelope
	_ = json.NewDecoder(resp.Body).Decode(&e)
	resp.Body.Close()
	if e.Code != CodeUnauthorized {
		t.Fatalf("code = %d (%s), want %d", e.Code, e.Message, CodeUnauthorized)
	}
	// 累计 5 次失败后限速
	for i := 0; i < 4; i++ {
		r, _ := http.Post(ts.URL+"/api/auth/login", "application/json",
			strings.NewReader(`{"username":"admin","password":"wrong"}`))
		r.Body.Close()
	}
	r, _ := http.Post(ts.URL+"/api/auth/login", "application/json",
		strings.NewReader(`{"username":"admin","password":"wrong"}`))
	var le envelope
	_ = json.NewDecoder(r.Body).Decode(&le)
	r.Body.Close()
	if !strings.Contains(le.Message, "1 分钟") {
		t.Errorf("应触发限速提示: %v", le)
	}
}

func TestChangePasswordFlow(t *testing.T) {
	ts, s := newBareServer(t)
	pw, err := s.EnsureBootstrapAdmin()
	if err != nil {
		t.Fatal(err)
	}
	ac := loginAs(t, ts, "admin", pw)

	// 改密：旧密码错 → code 2
	bad := postJSON(t, ac, ts.URL+"/api/auth/password", `{"old_password":"nope","new_password":"newpassword1"}`)
	if bad.Code != CodeBadRequest {
		t.Fatalf("旧密码错误应 code %d: %v", CodeBadRequest, bad)
	}
	// 改密成功 → 会话全端下线（当前 Cookie 失效）
	good := postJSON(t, ac, ts.URL+"/api/auth/password", `{"old_password":"`+pw+`","new_password":"newpassword1"}`)
	if good.Code != 0 {
		t.Fatalf("改密失败: %v", good)
	}
	me, status := getEnvelopeAllowStatus(t, ac, ts.URL+"/api/auth/me")
	if status != http.StatusUnauthorized || me.Code != CodeUnauthorized {
		t.Fatalf("改密后旧会话应失效: status=%d env=%v", status, me)
	}
	// 新密码可登录，强制改密标记已清除
	ac2 := loginAs(t, ts, "admin", "newpassword1")
	me2 := getEnvelope(t, ac2, ts.URL+"/api/auth/me")
	data, _ := me2.Data.(map[string]any)
	if data["must_change_password"] != false {
		t.Errorf("改密后 must_change_password 应为 false: %v", data)
	}
}

func TestTaskScoping(t *testing.T) {
	ts, s := newBareServer(t)
	pw, err := s.EnsureBootstrapAdmin()
	if err != nil {
		t.Fatal(err)
	}
	admin := loginAs(t, ts, "admin", pw)
	testUser(t, s, "alice", "user")
	testUser(t, s, "bob", "user")
	alice := loginAs(t, ts, "alice", "password-alice")
	bob := loginAs(t, ts, "bob", "password-bob")

	// 三类任务：alice 的、无主（历史存量）、bob 的
	for _, tk := range []store.Task{
		{ID: "t-alice", UserID: "alice-id", Provider: "volcengine", Tool: "tts", Status: store.StatusSucceeded, Params: "{}"},
		{ID: "t-legacy", Provider: "volcengine", Tool: "tts", Status: store.StatusSucceeded, Params: "{}"},
		{ID: "t-bob", UserID: "bob-id", Provider: "volcengine", Tool: "tts", Status: store.StatusSucceeded, Params: "{}"},
	} {
		if err := s.svc.DB().CreateTask(&tk); err != nil {
			t.Fatal(err)
		}
	}

	// 列表：alice/bob 只见自己的；admin 见全部（含无主）
	count := func(ac *http.Client) int {
		e := getEnvelope(t, ac, ts.URL+"/api/tasks?size=100")
		data, _ := e.Data.(map[string]any)
		items, _ := data["items"].([]any)
		return len(items)
	}
	if got := count(alice); got != 1 {
		t.Errorf("alice 可见任务数 = %d, want 1", got)
	}
	if got := count(bob); got != 1 {
		t.Errorf("bob 可见任务数 = %d, want 1", got)
	}
	if got := count(admin); got != 3 {
		t.Errorf("admin 可见任务数 = %d, want 3", got)
	}

	// 越权读：bob 读 alice 的任务 → 404 语义（不暴露存在性，HTTP 200 包络 code 6）
	if e := getEnvelope(t, bob, ts.URL+"/api/tasks/t-alice"); e.Code != CodeNotFound {
		t.Errorf("越权读 code = %d (%s), want %d", e.Code, e.Message, CodeNotFound)
	}
	// 无主任务：普通用户不可见，admin 可见
	if e := getEnvelope(t, alice, ts.URL+"/api/tasks/t-legacy"); e.Code != CodeNotFound {
		t.Errorf("无主任务对普通用户应不可见: %v", e)
	}
	if e := getEnvelope(t, admin, ts.URL+"/api/tasks/t-legacy"); e.Code != 0 {
		t.Errorf("admin 应可见无主任务: %v", e)
	}
}

func TestUploadBucketing(t *testing.T) {
	ts, s := newBareServer(t)
	pw, err := s.EnsureBootstrapAdmin()
	if err != nil {
		t.Fatal(err)
	}
	admin := loginAs(t, ts, "admin", pw)
	testUser(t, s, "alice", "user")
	alice := loginAs(t, ts, "alice", "password-alice")

	// alice 上传 → 落 alice 分桶目录
	e := uploadMultipart(t, alice, ts.URL+"/api/uploads", "a.mp3", "FAKE")
	if e.Code != 0 {
		t.Fatalf("upload code = %d (%s)", e.Code, e.Message)
	}
	fileID, _ := e.Data.(map[string]any)["file_id"].(string)

	me := getEnvelope(t, alice, ts.URL+"/api/auth/me")
	uid, _ := me.Data.(map[string]any)["id"].(string)
	matches, gerr := filepath.Glob(filepath.Join(s.uploadsDir(), uid, fileID+"-*"))
	if gerr != nil || len(matches) != 1 {
		t.Fatalf("上传应落用户分桶目录 %s: %v err=%v", uid, matches, gerr)
	}
	if resp, err := alice.Get(ts.URL + "/api/uploads/" + fileID + "/stream"); err != nil || resp.StatusCode != 200 {
		t.Fatalf("本人拉流 status=%v err=%v", resp, err)
	}
	testUser(t, s, "bob2", "user")
	bob := loginAs(t, ts, "bob2", "password-bob2")
	if resp, err := bob.Get(ts.URL + "/api/uploads/" + fileID + "/stream"); err != nil || resp.StatusCode != 404 {
		t.Fatalf("他人拉流应 404: status=%v err=%v", resp, err)
	}
	// admin 可跨桶拉流
	if resp, err := admin.Get(ts.URL + "/api/uploads/" + fileID + "/stream"); err != nil || resp.StatusCode != 200 {
		t.Fatalf("admin 拉流 status=%v err=%v", resp, err)
	}
}

func TestPasswordHashedNotPlaintext(t *testing.T) {
	_, s := newBareServer(t)
	u := testUser(t, s, "hashcheck", "user")
	if strings.Contains(u.PasswordHash, "password-hashcheck") {
		t.Fatal("密码不能明文落库")
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte("password-hashcheck")) != nil {
		t.Fatal("bcrypt 哈希应可校验通过")
	}
}
