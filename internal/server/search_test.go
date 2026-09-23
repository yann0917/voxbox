package server

import (
	"encoding/json"
	"net/url"
	"testing"

	"github.com/yann0917/voxbox/internal/store"
)

// TestSearchEndpoint 验证全文搜索：分句精确命中、总结文本命中、任务标题命中
// （分离任务按歌名可搜）、failed 任务不参与、LIKE 键名误命中被上层二次校验过滤。
func TestSearchEndpoint(t *testing.T) {
	ts, s, ac := newTestServer(t)

	tasks := []store.Task{
		{ID: "srch-1", Provider: "volcengine", Tool: "asr", Status: store.StatusSucceeded, Params: "{}",
			Summary: `{"segments":[{"text":"大家好欢迎参加评审会议","start_ms":0,"end_ms":2000},{"text":"第二句讲预算问题","start_ms":2000,"end_ms":4000}]}`},
		{ID: "srch-2", Provider: "volcengine", Tool: "minutes", Status: store.StatusSucceeded, Params: "{}",
			Summary: `{"summary_text":"本季度营收增长百分之二十，超额完成目标"}`},
		{ID: "srch-3", Provider: "volcengine", Tool: "asr", Status: store.StatusFailed, Params: "{}",
			Summary: `{"segments":[{"text":"评审会议失败任务不应命中","start_ms":0,"end_ms":1000}]}`},
		{ID: "srch-4", Provider: "mvsep", Tool: "separate", Title: "稻香 - 周杰伦", Status: store.StatusSucceeded, Params: "{}",
			Summary: `{"algorithm":"BS Roformer","tracks":["vocals","other"]}`},
	}
	for i := range tasks {
		if err := s.svc.DB().CreateTask(&tasks[i]); err != nil {
			t.Fatal(err)
		}
	}

	get := func(q string) map[string]any {
		t.Helper()
		resp, err := ac.Get(ts.URL + "/api/search?q=" + url.QueryEscape(q))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var e envelope
		if err := json.NewDecoder(resp.Body).Decode(&e); err != nil {
			t.Fatal(err)
		}
		if e.Code != 0 {
			t.Fatalf("code = %d, msg = %s", e.Code, e.Message)
		}
		return e.Data.(map[string]any)
	}

	taskIDs := func(data map[string]any) map[string]bool {
		out := map[string]bool{}
		for _, it := range data["items"].([]any) {
			out[it.(map[string]any)["task_id"].(string)] = true
		}
		return out
	}

	// 分句命中：仅成功任务 srch-1，带时间戳片段
	data := get("评审")
	ids := taskIDs(data)
	if !ids["srch-1"] || len(ids) != 1 {
		t.Fatalf("期望仅 srch-1 命中，得到 %v", ids)
	}
	match := data["items"].([]any)[0].(map[string]any)["matches"].([]any)[0].(map[string]any)
	if match["start_ms"].(float64) != 0 {
		t.Errorf("命中片段应带时间戳: %v", match)
	}

	// 总结文本命中：srch-2 给上下文片段
	data = get("营收")
	if !taskIDs(data)["srch-2"] {
		t.Fatalf("期望 srch-2 经总结文本命中: %v", data)
	}

	// failed 任务不参与
	if taskIDs(get("失败任务"))["srch-3"] {
		t.Error("failed 任务不应命中")
	}

	// 标题命中（分离任务按歌名可搜）：item 回传 title，首个匹配为标题行（无时间戳）
	data = get("稻香")
	ids = taskIDs(data)
	if !ids["srch-4"] || len(ids) != 1 {
		t.Fatalf("期望仅 srch-4 按歌名命中，得到 %v", ids)
	}
	item := data["items"].([]any)[0].(map[string]any)
	if item["title"] != "稻香 - 周杰伦" {
		t.Errorf("item.title = %v", item["title"])
	}
	m0 := item["matches"].([]any)[0].(map[string]any)
	if m0["text"] != "稻香 - 周杰伦" || m0["start_ms"].(float64) != 0 {
		t.Errorf("标题匹配行不符: %v", m0)
	}

	// 标题部分子串同样命中（LIKE 语义）
	if !taskIDs(get("周杰伦"))["srch-4"] {
		t.Error("按歌手名应命中")
	}

	// LIKE 键名误命中（"text" 出现在所有 summary JSON 键名中）应被二次校验过滤
	if n := len(taskIDs(get("text"))); n != 0 {
		t.Errorf("键名误命中应被过滤，得到 %d 条", n)
	}
}
