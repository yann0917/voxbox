package server

// 说话人改名端点（PATCH /api/tasks/:id/speakers）测试：ASR 上游 segments[].speaker
// 只有「1/2/3」编号，真实姓名由用户改——覆盖式写入 Summary.speaker_names 键，
// 其余键（segments/duration_ms 等）不动；属主与工具类型双校验，越权同报不存在。

import (
	"net/http"
	"testing"

	"github.com/yann0917/voxbox/internal/store"
)

// speakersFixture 建两个任务：asr 成功任务（Summary 预置 segments）归 alice，
// tts 任务（非 asr 拒改校验用）同属 alice。直接落库不经引擎，状态不受影响。
func speakersFixture(t *testing.T, s *Server) {
	t.Helper()
	if err := s.svc.DB().CreateTask(&store.Task{
		ID: "spk-asr", UserID: "alice-id", Provider: "volcengine", Tool: "asr",
		Status: store.StatusSucceeded, Params: `{}`,
		Summary: `{"segments":[{"text":"大家好","start_ms":0,"end_ms":900,"speaker":"1"}],"duration_ms":900,"speakers_count":1}`,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.svc.DB().CreateTask(&store.Task{
		ID: "spk-tts", UserID: "alice-id", Provider: "volcengine", Tool: "tts",
		Status: store.StatusSucceeded, Params: `{}`,
		Summary: `{"text":"合成结果"}`,
	}); err != nil {
		t.Fatal(err)
	}
}

// taskSummary 经 GET /api/tasks/:id 读回 task.summary（同时验证 DTO 原样带出）。
func taskSummary(t *testing.T, ac *http.Client, baseURL, id string) map[string]any {
	t.Helper()
	e := getEnvelope(t, ac, baseURL+"/api/tasks/"+id)
	data, _ := e.Data.(map[string]any)
	task, _ := data["task"].(map[string]any)
	if task == nil {
		t.Fatalf("data = %v", e.Data)
	}
	sum, ok := task["summary"].(map[string]any)
	if !ok {
		t.Fatalf("task.summary 应为 JSON 对象: %v", task["summary"])
	}
	return sum
}

// TestRenameTaskSpeakers 属主改名 + 覆盖式替换：speaker_names 整体替换（旧键清除），
// 其余 Summary 键（segments/duration_ms/speakers_count）原样保留。
func TestRenameTaskSpeakers(t *testing.T) {
	ts, s, _ := newTestServer(t)
	testUser(t, s, "alice", "user")
	speakersFixture(t, s)
	acAlice := loginAs(t, ts, "alice", "password-alice")

	// (a) 属主首次改名：两人名称写入，GET 经 DTO 原样带出
	if _, e := doJSON(t, acAlice, http.MethodPatch, ts.URL+"/api/tasks/spk-asr/speakers",
		`{"names":{"1":"张三","2":"李四"}}`); e.Code != CodeOK {
		t.Fatalf("rename code = %d (%s)", e.Code, e.Message)
	}
	sum := taskSummary(t, acAlice, ts.URL, "spk-asr")
	names, _ := sum["speaker_names"].(map[string]any)
	if names == nil || names["1"] != "张三" || names["2"] != "李四" {
		t.Fatalf("speaker_names = %v, want {1:张三, 2:李四}", sum["speaker_names"])
	}
	if sum["duration_ms"].(float64) != 900 || sum["speakers_count"].(float64) != 1 {
		t.Errorf("其余 Summary 键应保留: %v", sum)
	}
	if _, exists := sum["segments"]; !exists {
		t.Errorf("segments 键应保留: %v", sum)
	}

	// (b) 再次改名：覆盖式整体替换，旧键「2」清除，其余键仍不动
	if _, e := doJSON(t, acAlice, http.MethodPatch, ts.URL+"/api/tasks/spk-asr/speakers",
		`{"names":{"1":"王五"}}`); e.Code != CodeOK {
		t.Fatalf("rename-2 code = %d (%s)", e.Code, e.Message)
	}
	sum = taskSummary(t, acAlice, ts.URL, "spk-asr")
	names, _ = sum["speaker_names"].(map[string]any)
	if len(names) != 1 || names["1"] != "王五" {
		t.Fatalf("覆盖替换后 speaker_names = %v, want 仅 {1:王五}", names)
	}
	if sum["duration_ms"].(float64) != 900 {
		t.Errorf("覆盖替换不应动其它键: %v", sum)
	}
}

// TestRenameTaskSpeakersDegenerateSummaries 退化 Summary 分支：空串与非法 JSON 的
// 任务改名单不报错、不 panic——两者解析失败后都从空对象起步，Patch 后 Summary 被
// 整体替换为仅含 speaker_names 的新 JSON（speakers.go 与 persistRefined 同款兜底）。
func TestRenameTaskSpeakersDegenerateSummaries(t *testing.T) {
	ts, s, _ := newTestServer(t)
	testUser(t, s, "alice", "user")
	acAlice := loginAs(t, ts, "alice", "password-alice")
	for _, tc := range []struct {
		name, id, summary string
	}{
		{"空串 Summary", "spk-empty", ""},
		{"非法 JSON Summary", "spk-bad", "not-json"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := s.svc.DB().CreateTask(&store.Task{
				ID: tc.id, UserID: "alice-id", Provider: "volcengine", Tool: "asr",
				Status: store.StatusSucceeded, Params: `{}`, Summary: tc.summary,
			}); err != nil {
				t.Fatal(err)
			}
			if _, e := doJSON(t, acAlice, http.MethodPatch, ts.URL+"/api/tasks/"+tc.id+"/speakers",
				`{"names":{"1":"张三"}}`); e.Code != CodeOK {
				t.Fatalf("rename code = %d (%s)", e.Code, e.Message)
			}
			sum := taskSummary(t, acAlice, ts.URL, tc.id)
			names, _ := sum["speaker_names"].(map[string]any)
			if len(names) != 1 || names["1"] != "张三" {
				t.Fatalf("speaker_names = %v, want 仅 {1:张三}", sum["speaker_names"])
			}
			if len(sum) != 1 {
				t.Errorf("原 Summary 不可解析时应整体替换为仅含 speaker_names 的新 JSON: %v", sum)
			}
		})
	}
}

// TestRenameTaskSpeakersDenied 越权与工具校验：非属主同报不存在（不泄露存在性）；
// 非 asr 工具拒绝；任务不存在同报 6。
func TestRenameTaskSpeakersDenied(t *testing.T) {
	ts, s, _ := newTestServer(t)
	testUser(t, s, "alice", "user")
	testUser(t, s, "bob", "user")
	speakersFixture(t, s)
	acAlice := loginAs(t, ts, "alice", "password-alice")
	acBob := loginAs(t, ts, "bob", "password-bob")

	// 非属主 → code 6（与 getTask/deleteTask 同款文案，不泄露他人任务存在性）
	if _, e := doJSON(t, acBob, http.MethodPatch, ts.URL+"/api/tasks/spk-asr/speakers",
		`{"names":{"1":"张三"}}`); e.Code != CodeNotFound {
		t.Errorf("非属主 code = %d (%s), want %d", e.Code, e.Message, CodeNotFound)
	}
	// 越权请求不得真的写入
	if sum := taskSummary(t, acAlice, ts.URL, "spk-asr"); sum["speaker_names"] != nil {
		t.Errorf("越权 PATCH 不应写入: %v", sum["speaker_names"])
	}

	// 非 asr 工具 → code 2
	if _, e := doJSON(t, acAlice, http.MethodPatch, ts.URL+"/api/tasks/spk-tts/speakers",
		`{"names":{"1":"张三"}}`); e.Code != CodeBadRequest {
		t.Errorf("非 asr 工具 code = %d (%s), want %d", e.Code, e.Message, CodeBadRequest)
	}

	// 任务不存在 → code 6
	if _, e := doJSON(t, acAlice, http.MethodPatch, ts.URL+"/api/tasks/no-such-task/speakers",
		`{"names":{"1":"张三"}}`); e.Code != CodeNotFound {
		t.Errorf("任务不存在 code = %d (%s), want %d", e.Code, e.Message, CodeNotFound)
	}

	// names 缺失 → code 2（避免把 speaker_names 写成 null 破坏 Summary 结构）
	if _, e := doJSON(t, acAlice, http.MethodPatch, ts.URL+"/api/tasks/spk-asr/speakers",
		`{}`); e.Code != CodeBadRequest {
		t.Errorf("names 缺失 code = %d (%s), want %d", e.Code, e.Message, CodeBadRequest)
	}
}
