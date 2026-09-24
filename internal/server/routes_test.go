package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yann0917/voxbox/internal/service"
	"github.com/yann0917/voxbox/internal/store"
)

// envelope 由 apierr.go 提供，测试直接复用生产包络类型。

func getEnvelope(t *testing.T, ac *http.Client, url string) envelope {
	t.Helper()
	resp, err := ac.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("HTTP status = %d, want 200 (envelope)", resp.StatusCode)
	}
	var e envelope
	_ = json.NewDecoder(resp.Body).Decode(&e)
	return e
}

func newTestServer(t *testing.T) (*httptest.Server, *Server, *http.Client) {
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
	ac := loginTestClient(t, s, ts)
	// 最后注册 → 最先执行：先等后台任务落定，再关服务、删临时目录。
	// 任务在独立 goroutine 中执行，测试若先结束，随后写库会与 t.TempDir() 清理竞争
	//（表现为 "directory not empty" / "attempt to write a readonly database"，-race 下更易触发）。
	t.Cleanup(func() { waitTasksSettled(t, ts, ac) })
	return ts, s, ac
}

// waitTasksSettled 轮询任务列表，直到没有 pending/running 的任务或超时。
func waitTasksSettled(t *testing.T, ts *httptest.Server, ac *http.Client) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := ac.Get(ts.URL + "/api/tasks?size=100")
		if err != nil {
			return // 服务已不可达，交给后续清理步骤
		}
		var e struct {
			Data struct {
				Items []struct {
					Status string `json:"status"`
				} `json:"items"`
			} `json:"data"`
		}
		decodeErr := json.NewDecoder(resp.Body).Decode(&e)
		_ = resp.Body.Close()
		if decodeErr != nil {
			return
		}
		busy := false
		for _, it := range e.Data.Items {
			if it.Status == "pending" || it.Status == "running" {
				busy = true
				break
			}
		}
		if !busy {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Logf("等待任务落定超时（5s），可能仍有后台任务在写库")
}

// loginTestClient 走真实 /api/auth/login 换取会话 Cookie 的测试客户端（bootstrap admin，
// 密码来自 EnsureBootstrapAdmin 的首启随机生成）。
func loginTestClient(t *testing.T, s *Server, ts *httptest.Server) *http.Client {
	t.Helper()
	limiter = &loginLimiter{fails: map[string][]time.Time{}} // 全局限速器跨用例污染隔离
	password, err := s.EnsureBootstrapAdmin()
	if err != nil {
		t.Fatal(err)
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	ac := &http.Client{Jar: jar}
	resp, err := ac.Post(ts.URL+"/api/auth/login", "application/json",
		strings.NewReader(`{"username":"admin","password":"`+password+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("test login HTTP status = %d", resp.StatusCode)
	}
	var e envelope
	_ = json.NewDecoder(resp.Body).Decode(&e)
	if e.Code != 0 {
		t.Fatalf("test login code = %d (%s)", e.Code, e.Message)
	}
	return ac
}

// sessionCookieHeader 从测试客户端 CookieJar 提取会话头（WS 拨号用）。
func sessionCookieHeader(t *testing.T, ac *http.Client, ts *httptest.Server) string {
	t.Helper()
	u, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	for _, ck := range ac.Jar.Cookies(u) {
		if ck.Name == sessionCookieName {
			return ck.Name + "=" + ck.Value
		}
	}
	t.Fatal("测试客户端无会话 Cookie")
	return ""
}

func TestHealth(t *testing.T) {
	ts, _, ac := newTestServer(t)
	e := getEnvelope(t, ac, ts.URL+"/api/health")
	if e.Code != 0 {
		t.Fatalf("code = %d", e.Code)
	}
	data, _ := e.Data.(map[string]any)
	if data["status"] != "ok" {
		t.Errorf("data = %v", data)
	}
}

func TestToolsAndTaskSubmit(t *testing.T) {
	ts, _, ac := newTestServer(t)
	e := getEnvelope(t, ac, ts.URL+"/api/tools")
	tools, _ := e.Data.([]any)
	if len(tools) != 35 { // 火山 8 + MVSep 1 + 千问 2 + gsgc 11 + zhuanhuanmao 1 + 音频剪辑 12
		t.Fatalf("tools = %v", e.Data)
	}
	// Registry().List() 基于 map 遍历，顺序不定：按 name 断言而非下标。
	found := map[string]bool{}
	for _, it := range tools {
		m, _ := it.(map[string]any)
		meta, _ := m["meta"].(map[string]any)
		if name, _ := meta["name"].(string); name != "" {
			found[name] = true
		}
	}
	if !found["tts"] || !found["asr"] || !found["podcast"] || !found["separate"] || !found["translate"] {
		t.Fatalf("tool names = %v", found)
	}

	body := `{"provider":"volcengine","tool":"tts","params":{"text":"缺凭证也入库","format":"mp3"}}`
	resp, err := ac.Post(ts.URL+"/api/tasks", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var created envelope
	_ = json.NewDecoder(resp.Body).Decode(&created)
	if created.Code != 0 {
		t.Fatalf("submit code = %d (%s)", created.Code, created.Message)
	}
	createdData, _ := created.Data.(map[string]any)
	if createdData["task_id"] == "" {
		t.Errorf("created = %v", created)
	}

	// 任务列表
	list := getEnvelope(t, ac, ts.URL+"/api/tasks")
	listData, _ := list.Data.(map[string]any)
	if listData["total"].(float64) != 1 {
		t.Errorf("list = %v", list)
	}
}

// doJSON 任意方法的 JSON 请求：返回 HTTP 状态码与业务包络（无路由的 404 响应体
// 非包络，envelope 为零值，只看状态码）。
func doJSON(t *testing.T, ac *http.Client, method, url, body string) (int, envelope) {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := ac.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var e envelope
	_ = json.NewDecoder(resp.Body).Decode(&e)
	return resp.StatusCode, e
}

// providerFieldsFromSettings 从 GET /api/settings 的 providers 数组取指定卡的字段映射
// （key → 字段对象：text/select 有 value，secret 有 has_value）。
func providerFieldsFromSettings(t *testing.T, data map[string]any, card string) map[string]map[string]any {
	t.Helper()
	providers, _ := data["providers"].([]any)
	for _, it := range providers {
		p, _ := it.(map[string]any)
		if p["name"] != card {
			continue
		}
		out := map[string]map[string]any{}
		fields, _ := p["fields"].([]any)
		for _, fit := range fields {
			f, _ := fit.(map[string]any)
			key, _ := f["key"].(string)
			out[key] = f
		}
		return out
	}
	t.Fatalf("providers 缺卡 %s: %v", card, data["providers"])
	return nil
}

// TestGetSettingsProviders 新读端点：providers 数组在场（六卡齐全、kind 合法），
// volc/mvsep 平铺段已删除，storage/data_dir 保留。
func TestGetSettingsProviders(t *testing.T) {
	ts, _, ac := newTestServer(t)
	got := getEnvelope(t, ac, ts.URL+"/api/settings")
	data, _ := got.Data.(map[string]any)
	providers, okArr := data["providers"].([]any)
	if !okArr || len(providers) < 6 {
		t.Fatalf("providers = %v", data["providers"])
	}
	if _, exists := data["volc"]; exists {
		t.Error("旧 volc 平铺段应已删除")
	}
	if _, exists := data["mvsep"]; exists {
		t.Error("旧 mvsep 平铺段应已删除")
	}
	if _, exists := data["storage"]; !exists {
		t.Error("storage 段应保留")
	}
	if _, exists := data["data_dir"]; !exists {
		t.Error("data_dir 应保留")
	}
	names := map[string]bool{}
	for _, it := range providers {
		p, _ := it.(map[string]any)
		name, _ := p["name"].(string)
		names[name] = true
		if kind, _ := p["kind"].(string); kind != "cloud" && kind != "local" {
			t.Errorf("卡 %s kind = %q", name, kind)
		}
	}
	for _, want := range []string{"volcengine", "mediakit", "mvsep", "qianwen", "audiotool", "gsgc"} {
		if !names[want] {
			t.Errorf("缺少卡 %s", want)
		}
	}
}

// TestPutSettingsHotReload 按卡保存凭证后立即 GET 应读到新值（热加载，无需重启）：
// text 字段回 value、secret 字段只回 has_value。SaveProviderFields 写
// $HOME/.voxbox/config.yaml，须隔离 HOME。
func TestPutSettingsHotReload(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ts, _, ac := newTestServer(t)

	if _, e := doJSON(t, ac, http.MethodPut, ts.URL+"/api/settings/providers/volcengine",
		`{"fields":{"app_id":"app-1","access_token":"tok-1","api_key":"key-1"}}`); e.Code != CodeOK {
		t.Fatalf("volcengine 保存 code = %d (%s)", e.Code, e.Message)
	}
	if _, e := doJSON(t, ac, http.MethodPut, ts.URL+"/api/settings/providers/mediakit",
		`{"fields":{"api_key":"mk-1"}}`); e.Code != CodeOK {
		t.Fatalf("mediakit 保存 code = %d (%s)", e.Code, e.Message)
	}

	got := getEnvelope(t, ac, ts.URL+"/api/settings")
	data, _ := got.Data.(map[string]any)
	volc := providerFieldsFromSettings(t, data, "volcengine")
	if f := volc["app_id"]; f["value"] != "app-1" {
		t.Errorf("app_id = %v, want app-1（保存后应即时生效）", f["value"])
	}
	if f := volc["api_key"]; f["has_value"] != true || f["value"] != nil {
		t.Errorf("api_key secret 字段应只回 has_value=true，got %v", f)
	}
	mk := providerFieldsFromSettings(t, data, "mediakit")
	if f := mk["api_key"]; f["has_value"] != true {
		t.Errorf("mediakit.api_key has_value = %v, want true", f["has_value"])
	}
}

// TestPutSettingsProviders 新写端点契约：按卡保存成功；未知卡拒绝（业务包络非 0）；
// 旧 PUT /api/settings 整表端点已删除，必须 HTTP 404。
func TestPutSettingsProviders(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ts, _, ac := newTestServer(t)

	if _, e := doJSON(t, ac, http.MethodPut, ts.URL+"/api/settings/providers/qianwen",
		`{"fields":{"api_key":"sk-route-1"}}`); e.Code != CodeOK {
		t.Fatalf("按卡保存 code = %d (%s)", e.Code, e.Message)
	}
	if _, e := doJSON(t, ac, http.MethodPut, ts.URL+"/api/settings/providers/nope",
		`{"fields":{}}`); e.Code == CodeOK {
		t.Error("未知卡应拒绝（业务码非 0）")
	}
	status, _ := doJSON(t, ac, http.MethodPut, ts.URL+"/api/settings", `{"api_key":"x"}`)
	if status != http.StatusNotFound {
		t.Errorf("旧端点应 HTTP 404, got %d", status)
	}
}

// TestTestConnectionShape test-connection 新结构：results 数组逐卡回
// name/ok/message（未配置也须在场且 message 非空）+ storage 独立段。
// 测试环境无凭证：各卡未配置检查均在发网络请求前返回，不会外联。
func TestTestConnectionShape(t *testing.T) {
	ts, _, ac := newTestServer(t)
	e := postJSON(t, ac, ts.URL+"/api/settings/test-connection", "")
	if e.Code != CodeOK {
		t.Fatalf("code = %d (%s)", e.Code, e.Message)
	}
	data, _ := e.Data.(map[string]any)
	results, okArr := data["results"].([]any)
	if !okArr || len(results) < 4 {
		t.Fatalf("results = %v", data["results"])
	}
	seen := map[string]bool{}
	for _, it := range results {
		r, _ := it.(map[string]any)
		name, _ := r["name"].(string)
		seen[name] = true
		if msg, _ := r["message"].(string); msg == "" {
			t.Errorf("卡 %s 缺 message", name)
		}
		if _, exists := r["ok"]; !exists {
			t.Errorf("卡 %s 缺 ok 键", name)
		}
	}
	for _, want := range []string{"volcengine", "mediakit", "mvsep", "qianwen"} {
		if !seen[want] {
			t.Errorf("results 缺卡 %s", want)
		}
	}
	st, okObj := data["storage"].(map[string]any)
	if !okObj {
		t.Fatalf("data.storage 应为对象: %v", data)
	}
	if _, exists := st["ok"]; !exists {
		t.Error("storage 缺 ok 键")
	}
	if msg, _ := st["message"].(string); msg == "" {
		t.Errorf("storage.message 应为非空字符串: %v", st)
	}
}

func TestErrorEnvelope(t *testing.T) {
	ts, _, ac := newTestServer(t)
	// 未知工具：code 2，HTTP 仍 200
	body := `{"provider":"volcengine","tool":"nope","params":{}}`
	resp, _ := ac.Post(ts.URL+"/api/tasks", "application/json", strings.NewReader(body))
	var e envelope
	_ = json.NewDecoder(resp.Body).Decode(&e)
	if resp.StatusCode != 200 || e.Code != 2 {
		t.Errorf("status=%d code=%d message=%s", resp.StatusCode, e.Code, e.Message)
	}
	// 不存在的任务：code 6
	resp2, _ := ac.Get(ts.URL + "/api/tasks/not-exist")
	var e2 envelope
	_ = json.NewDecoder(resp2.Body).Decode(&e2)
	if resp2.StatusCode != 200 || e2.Code != 6 {
		t.Errorf("status=%d code=%d", resp2.StatusCode, e2.Code)
	}
}

// TestTaskDetailSummaryExposed 详情 DTO 应透出任务 summary JSON（ASR segments 所在），
// 空 summary 的任务不得出现 summary 键（omitempty）。
func TestTaskDetailSummaryExposed(t *testing.T) {
	ts, s, ac := newTestServer(t)
	summary := `{"segments":[{"text":"你好","start_ms":0,"end_ms":900}],"duration_ms":900,"source":"file"}`
	if err := s.svc.DB().CreateTask(&store.Task{
		ID: "t-sum", Provider: "volcengine", Tool: "asr", Status: store.StatusSucceeded,
		Params: `{}`, Summary: summary,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.svc.DB().CreateTask(&store.Task{
		ID: "t-nosum", Provider: "volcengine", Tool: "tts", Status: store.StatusFailed, Params: `{}`,
	}); err != nil {
		t.Fatal(err)
	}

	e := getEnvelope(t, ac, ts.URL+"/api/tasks/t-sum")
	data, _ := e.Data.(map[string]any)
	task, _ := data["task"].(map[string]any)
	if task == nil {
		t.Fatalf("data = %v", e.Data)
	}
	sum, ok := task["summary"].(map[string]any)
	if !ok {
		t.Fatalf("task.summary 应为 JSON 对象: %v", task["summary"])
	}
	segs, _ := sum["segments"].([]any)
	if len(segs) != 1 {
		t.Fatalf("summary.segments = %v, want 1 条", sum["segments"])
	}
	seg0, _ := segs[0].(map[string]any)
	if seg0["text"] != "你好" || seg0["start_ms"].(float64) != 0 || seg0["end_ms"].(float64) != 900 {
		t.Errorf("segments[0] = %v", seg0)
	}

	e2 := getEnvelope(t, ac, ts.URL+"/api/tasks/t-nosum")
	data2, _ := e2.Data.(map[string]any)
	task2, _ := data2["task"].(map[string]any)
	if task2 == nil {
		t.Fatalf("data = %v", e2.Data)
	}
	if _, exists := task2["summary"]; exists {
		t.Errorf("空 summary 不应出现 summary 键: %v", task2)
	}
}

// TestCreateTaskStripsOutParam 验证 Web 入口剥离 params 中的 _out（Task 9 安全修复）。
// _out 是 CLI 内部约定，透传会导致任意路径写文件。无凭证时任务会 failed，
// 但 params 落库不受影响，故以落库后的 params 为最稳断言。
func TestCreateTaskStripsOutParam(t *testing.T) {
	ts, _, ac := newTestServer(t)
	body := `{"provider":"volcengine","tool":"tts","params":{"text":"剥离测试","format":"mp3","_out":"/tmp/evil.mp3"}}`
	resp, err := ac.Post(ts.URL+"/api/tasks", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var created envelope
	_ = json.NewDecoder(resp.Body).Decode(&created)
	if created.Code != 0 {
		t.Fatalf("submit code = %d (%s)", created.Code, created.Message)
	}
	createdData, _ := created.Data.(map[string]any)
	taskID, _ := createdData["task_id"].(string)
	if taskID == "" {
		t.Fatalf("created = %v", created)
	}

	e := getEnvelope(t, ac, ts.URL+"/api/tasks/"+taskID)
	data, _ := e.Data.(map[string]any)
	task, _ := data["task"].(map[string]any)
	if task == nil {
		t.Fatalf("data = %v", e.Data)
	}
	params, _ := task["params"].(map[string]any)
	if params == nil {
		t.Fatalf("task.params = %v", task["params"])
	}
	if _, exists := params["_out"]; exists {
		t.Errorf("params 落库后仍含 _out: %v", params)
	}
	if params["text"] != "剥离测试" {
		t.Errorf("其余 params 应保留，got %v", params)
	}
}

// TestCreateTaskWithArtifactInput 跨工具联动：已有产物（如分离任务的人声轨）作为新任务输入。
// 手工造产物记录（db.CreateArtifact 指向 data 目录内真实文件）→ createTask 带 artifact_input
// → 任务创建成功。测试环境无凭证：任务最终 failed 不影响「创建成功」断言。
func TestCreateTaskWithArtifactInput(t *testing.T) {
	ts, s, ac := newTestServer(t)
	dataDir := s.svc.Config().DataDir
	rel := filepath.Join("sep-test", "voice.mp3")
	if err := os.MkdirAll(filepath.Join(dataDir, filepath.Dir(rel)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, rel), []byte("fake-audio"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.svc.DB().CreateArtifact(&store.Artifact{
		ID: "art-voice", TaskID: "t-src", Kind: "audio", Path: rel,
		Filename: "voice.mp3", Format: "mp3",
	}); err != nil {
		t.Fatal(err)
	}

	body := `{"provider":"volcengine","tool":"asr","params":{"srt":true},"artifact_input":"art-voice"}`
	resp, err := ac.Post(ts.URL+"/api/tasks", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var created envelope
	_ = json.NewDecoder(resp.Body).Decode(&created)
	if created.Code != 0 {
		t.Fatalf("submit code = %d (%s)", created.Code, created.Message)
	}
	createdData, _ := created.Data.(map[string]any)
	taskID, _ := createdData["task_id"].(string)
	if taskID == "" {
		t.Fatalf("created = %v", created)
	}

	// 断言产物路径真的进了 files：无凭证时 ASR 的失败原因必须是「凭证未配置」，
	// 而不是「缺少输入」——后者说明 artifact → files["audio"] 的组装没生效。
	deadline := time.Now().Add(3 * time.Second)
	for {
		detail := getEnvelope(t, ac, ts.URL+"/api/tasks/"+taskID)
		d, _ := detail.Data.(map[string]any)
		task, _ := d["task"].(map[string]any)
		status, _ := task["status"].(string)
		if status == "succeeded" || status == "failed" || status == "canceled" || status == "interrupted" {
			errMsg, _ := task["error"].(string)
			if !strings.Contains(errMsg, "凭证") {
				t.Errorf("任务失败原因 = %q，期望包含「凭证」（说明 artifact 未注入 files）", errMsg)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("任务未在 3s 内到达终态，status = %q", status)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestCreateTaskArtifactInputConflict artifact_input 与 file_ids 互斥：同传 → code 2，
// 且在产物存在性校验之前拒绝（不必存在真实产物）。
func TestCreateTaskArtifactInputConflict(t *testing.T) {
	ts, _, ac := newTestServer(t)
	body := `{"provider":"volcengine","tool":"asr","params":{"srt":true},"artifact_input":"art-x","file_ids":["f-1"]}`
	resp, err := ac.Post(ts.URL+"/api/tasks", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var e envelope
	_ = json.NewDecoder(resp.Body).Decode(&e)
	if resp.StatusCode != 200 || e.Code != 2 {
		t.Errorf("status=%d code=%d message=%s", resp.StatusCode, e.Code, e.Message)
	}
	if !strings.Contains(e.Message, "只能提供其一") {
		t.Errorf("message 应说明互斥原因，got %q", e.Message)
	}
}

// TestCreateTaskArtifactNotFound artifact_input 指向不存在的产物 → code 6。
func TestCreateTaskArtifactNotFound(t *testing.T) {
	ts, _, ac := newTestServer(t)
	body := `{"provider":"volcengine","tool":"asr","params":{"srt":true},"artifact_input":"no-such-artifact"}`
	resp, err := ac.Post(ts.URL+"/api/tasks", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var e envelope
	_ = json.NewDecoder(resp.Body).Decode(&e)
	if resp.StatusCode != 200 || e.Code != 6 {
		t.Errorf("status=%d code=%d message=%s", resp.StatusCode, e.Code, e.Message)
	}
}

// TestCreateTaskWithArtifactInputs 多产物输入通道：artifact_inputs 按序映射
// files["audio"]（伴奏）/files["audio2"]（人声…依 fileIDsToFiles 同一约定），与
// artifact_input/file_ids 三选一互斥。手工造产物记录（db.CreateArtifact 指向
// data 目录内真实文件）。断言锚点与 TestCreateTaskWithArtifactInput 同理：
// 测试环境有无 ffmpeg 皆可跑——若两轨未注入 files，mix 报「恰好 2 个输入」的
// 零输入错误；注入生效则失败原因必然是别的东西（探测失败/ffmpeg 缺失/直接成功）。
func TestCreateTaskWithArtifactInputs(t *testing.T) {
	ts, s, ac := newTestServer(t)
	dataDir := s.svc.Config().DataDir
	writeArtifact := func(id, rel, name string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(dataDir, filepath.Dir(rel)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dataDir, rel), []byte("fake-audio"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := s.svc.DB().CreateArtifact(&store.Artifact{
			ID: id, TaskID: "t-src", UserID: "t-src-owner", Kind: "audio", Path: rel,
			Filename: name, Format: "mp3",
		}); err != nil {
			t.Fatal(err)
		}
	}
	writeArtifact("art-mix-music", filepath.Join("mix-test", "稻香_instrumental.mp3"), "稻香_instrumental.mp3")
	writeArtifact("art-mix-vocal", filepath.Join("mix-test", "稻香_vocals.mp3"), "稻香_vocals.mp3")

	// (a) happy path：两轨按序提交 → 任务创建成功；终态错误不得是零输入错误
	created := postJSON(t, ac, ts.URL+"/api/tasks",
		`{"provider":"audio","tool":"mix","params":{},"artifact_inputs":["art-mix-music","art-mix-vocal"]}`)
	if created.Code != 0 {
		t.Fatalf("createTask code = %d (%s)", created.Code, created.Message)
	}
	createdData, _ := created.Data.(map[string]any)
	taskID, _ := createdData["task_id"].(string)
	if taskID == "" {
		t.Fatalf("created = %v", created)
	}
	// waitTerminal 轮询任务到终态，返回失败原因（成功为空串）。
	waitTerminal := func(id string) string {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			detail := getEnvelope(t, ac, ts.URL+"/api/tasks/"+id)
			d, _ := detail.Data.(map[string]any)
			task, _ := d["task"].(map[string]any)
			status, _ := task["status"].(string)
			if status == "succeeded" || status == "failed" || status == "canceled" || status == "interrupted" {
				errMsg, _ := task["error"].(string)
				if strings.Contains(errMsg, "恰好 2 个输入") {
					t.Errorf("任务失败原因 = %q，零输入错误说明 artifact_inputs 未注入 files", errMsg)
				}
				return errMsg
			}
			if time.Now().After(deadline) {
				t.Fatalf("任务未在 5s 内到达终态，status = %q", status)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	waitTerminal(taskID)

	// (a2) 克隆重跑：InputRef.ArtifactInputs 溯源重解析，重跑任务两轨同样注入
	//（若重跑链漏掉 artifact_inputs，重跑任务会以零输入错误失败，同一锚点可判）。
	rerun := postJSON(t, ac, ts.URL+"/api/tasks/"+taskID+"/rerun", "")
	if rerun.Code != 0 {
		t.Fatalf("rerun code = %d (%s)", rerun.Code, rerun.Message)
	}
	rerunData, _ := rerun.Data.(map[string]any)
	rerunID, _ := rerunData["task_id"].(string)
	if rerunID == "" || rerunID == taskID {
		t.Fatalf("rerun = %v", rerun)
	}
	waitTerminal(rerunID)

	// (b) file_ids 与 artifact_inputs 同传 → 400（互斥，在产物校验之前拒绝）
	e := postJSON(t, ac, ts.URL+"/api/tasks",
		`{"provider":"audio","tool":"mix","params":{},"artifact_inputs":["art-mix-music"],"file_ids":["f-1"]}`)
	if e.Code != CodeBadRequest {
		t.Errorf("file_ids+artifact_inputs code = %d (%s), want %d", e.Code, e.Message, CodeBadRequest)
	}
	// (c) artifact_input 与 artifact_inputs 同传 → 400
	e = postJSON(t, ac, ts.URL+"/api/tasks",
		`{"provider":"audio","tool":"mix","params":{},"artifact_input":"art-mix-music","artifact_inputs":["art-mix-music"]}`)
	if e.Code != CodeBadRequest {
		t.Errorf("artifact_input+artifact_inputs code = %d (%s), want %d", e.Code, e.Message, CodeBadRequest)
	}
	// (d) 未知产物 id → 404（业务码 6）
	e = postJSON(t, ac, ts.URL+"/api/tasks",
		`{"provider":"audio","tool":"mix","params":{},"artifact_inputs":["no-such-art"]}`)
	if e.Code != CodeNotFound {
		t.Errorf("unknown artifact code = %d (%s), want %d", e.Code, e.Message, CodeNotFound)
	}
	// (e) 他人产物 → 404：admin 无视归属，须以普通用户身份访问他人产物
	testUser(t, s, "alice", "user")
	acAlice := loginAs(t, ts, "alice", "password-alice")
	e = postJSON(t, acAlice, ts.URL+"/api/tasks",
		`{"provider":"audio","tool":"mix","params":{},"artifact_inputs":["art-mix-music"]}`)
	if e.Code != CodeNotFound {
		t.Errorf("foreign artifact code = %d (%s), want %d", e.Code, e.Message, CodeNotFound)
	}
}

// TestResolveArtifactInputsOrder 共用解析器（create 与 rerun 两条链共用）的按序映射契约：
// 第 1 个 id → "audio"，第 2 个 → "audio2"（与 fileIDsToFiles 同一约定，mix 的
// [伴奏, 人声] 轨序由此保证；顺序颠倒零输入检测无法发现，须直接断言键值）。
func TestResolveArtifactInputsOrder(t *testing.T) {
	_, s, _ := newTestServer(t)
	dataDir := s.svc.Config().DataDir
	writeArtifact := func(id, rel string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(dataDir, filepath.Dir(rel)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dataDir, rel), []byte("fake-audio"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := s.svc.DB().CreateArtifact(&store.Artifact{
			ID: id, TaskID: "t-src", UserID: "t-src-owner", Kind: "audio", Path: rel,
		}); err != nil {
			t.Fatal(err)
		}
	}
	writeArtifact("art-mix-music", filepath.Join("mix-test", "稻香_instrumental.mp3"))
	writeArtifact("art-mix-vocal", filepath.Join("mix-test", "稻香_vocals.mp3"))

	files, err := s.resolveArtifactInputs([]string{"art-mix-music", "art-mix-vocal"}, &Principal{ID: "t-src-owner"})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("files = %v, want 2 项", files)
	}
	if want := filepath.Join(dataDir, "mix-test", "稻香_instrumental.mp3"); files["audio"] != want {
		t.Errorf("files[audio] = %q, want %q（第 1 个 id 必须是伴奏轨）", files["audio"], want)
	}
	if want := filepath.Join(dataDir, "mix-test", "稻香_vocals.mp3"); files["audio2"] != want {
		t.Errorf("files[audio2] = %q, want %q（第 2 个 id 必须是人声轨）", files["audio2"], want)
	}
}

// TestArtifactAbsPathJail 验证产物路径封闭在 data 目录内（Task 9 安全修复）。
func TestArtifactAbsPathJail(t *testing.T) {
	svc, err := service.NewWithHome(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	s := New(svc)

	// 相对路径穿越 data 目录：必须拒绝
	if abs, err := s.artifactAbsPath("../outside.txt"); err == nil {
		t.Fatalf("../outside.txt 应返回 error，got abs=%q", abs)
	}

	// data 目录内不存在的相对路径：返回 stat 错误（而非穿越错误），且不返回路径
	abs, err := s.artifactAbsPath("sub/ok.txt")
	if err == nil {
		t.Fatalf("sub/ok.txt 不存在，应返回 error，got %q", abs)
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("应返回 stat not-exist 错误，got %v", err)
	}
	if abs != "" {
		t.Errorf("出错时不应返回路径，got %q", abs)
	}

	// 正例：data 目录内的真实文件可解析
	dataDir := svc.Config().DataDir
	if err := os.MkdirAll(filepath.Join(dataDir, "ok"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "ok", "a.mp3"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	abs, err = s.artifactAbsPath("ok/a.mp3")
	if err != nil {
		t.Fatalf("data 目录内文件应可解析，got %v", err)
	}
	if abs != filepath.Join(dataDir, "ok", "a.mp3") {
		t.Errorf("abs = %q, want %q", abs, filepath.Join(dataDir, "ok", "a.mp3"))
	}
}

// TestSettingsStorageChannels 存储配置分段契约：PUT /api/settings/storage 按类型
// 保存后，GET 回传 storage.channels.<名> 独立段（secret 只回 has_secret_key 布尔），
// 切类型再切回值仍在。
func TestSettingsStorageChannels(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ts, _, ac := newTestServer(t)

	put := func(body string) {
		t.Helper()
		if _, e := doJSON(t, ac, http.MethodPut, ts.URL+"/api/settings/storage", body); e.Code != CodeOK {
			t.Fatalf("put storage code = %d (%s)", e.Code, e.Message)
		}
	}
	put(`{"provider":"tos","endpoint":"ep","region":"r","bucket":"bk","access_key":"AK","secret_key":"SK","prefix":"pp"}`)
	// 停用（仅选择器清空），再启用
	put(`{"provider":""}`)
	put(`{"provider":"tos","endpoint":"ep","region":"r","bucket":"bk","access_key":"AK","secret_key":"","prefix":"pp"}`)

	got := getEnvelope(t, ac, ts.URL+"/api/settings")
	data, _ := got.Data.(map[string]any)
	storage, _ := data["storage"].(map[string]any)
	if storage["provider"] != "tos" || storage["bucket"] != "bk" {
		t.Fatalf("flat storage = %v", storage)
	}
	chans, _ := storage["channels"].(map[string]any)
	tos, ok := chans["tos"].(map[string]any)
	if !ok {
		t.Fatalf("channels missing tos: %v", chans)
	}
	if tos["bucket"] != "bk" || tos["endpoint"] != "ep" {
		t.Fatalf("tos channel = %v", tos)
	}
	if tos["has_secret_key"] != true {
		t.Fatalf("tos has_secret_key = %v, want true（secret 留空保存后应沿用已存值）", tos["has_secret_key"])
	}
	if _, leak := tos["secret_key"]; leak {
		t.Fatal("channels 段不得回传 secret_key 明文")
	}
}
