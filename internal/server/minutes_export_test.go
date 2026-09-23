package server

import (
	"net/http"
	"strings"
	"testing"

	"github.com/yann0917/voxbox/internal/store"
)

// TestMinutesExport 验证模板清单与 Markdown/Docx 导出端点。
func TestMinutesExport(t *testing.T) {
	ts, s, ac := newTestServer(t)

	summary := `{"minutes_title":"季度经营会议","summary_text":"讨论了预算与招聘安排。","translation_text":"","features":["全文总结","待办提取"],"sentences":4,"speakers_count":2,"duration_ms":6000,` +
		`"todos":[{"content":"提交预算表","executor":["张三"],"start_time":3000}],` +
		`"chapters":[{"title":"开场","summary":"问候与议程","start_time":0,"end_time":2000}],` +
		`"segments":[{"text":"主持人：大家好。","start_ms":0,"end_ms":2000}]}`

	task := store.Task{ID: "mx-1", Provider: "volcengine", Tool: "minutes",
		Status: store.StatusSucceeded, Params: "{}", Summary: summary}
	if err := s.svc.DB().CreateTask(&task); err != nil {
		t.Fatal(err)
	}

	get := func(path string) (int, string, http.Header) {
		t.Helper()
		resp, err := ac.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var buf strings.Builder
		tmp := make([]byte, 1024)
		for {
			n, err := resp.Body.Read(tmp)
			buf.Write(tmp[:n])
			if err != nil {
				break
			}
		}
		return resp.StatusCode, buf.String(), resp.Header
	}

	// 模板清单
	code, body, _ := get("/api/minutes/templates")
	if code != 200 || !strings.Contains(body, "通用会议纪要") {
		t.Fatalf("模板清单不符: %d %s", code, body)
	}

	// Markdown 导出：标题/待办表格/转写附录
	code, body, _ = get("/api/minutes/mx-1/export?format=markdown&template=standard")
	if code != 200 || !strings.Contains(body, "# 季度经营会议") ||
		!strings.Contains(body, "提交预算表") || !strings.Contains(body, "主持人：大家好。") {
		t.Fatalf("Markdown 导出不符: %d %s", code, body)
	}

	// brief 模板：不含转写与章节
	code, body, _ = get("/api/minutes/mx-1/export?format=markdown&template=brief")
	if code != 200 || strings.Contains(body, "转写全文") || strings.Contains(body, "章节总结") {
		t.Fatalf("brief 模板不应含转写/章节: %d %s", code, body)
	}

	// Docx 导出：zip（PK 头）+ Content-Disposition
	code, body, headers := get("/api/minutes/mx-1/export?format=docx&template=standard")
	if code != 200 {
		t.Fatalf("docx 导出 status = %d", code)
	}
	if cd := headers.Get("Content-Disposition"); !strings.Contains(cd, ".docx") {
		t.Errorf("Content-Disposition 缺 .docx: %s", cd)
	}
	if len(body) < 100 || !strings.HasPrefix(body, "PK") {
		t.Fatalf("docx 应为 zip（PK 头）: len=%d", len(body))
	}

	// 非妙记任务报参数错误
	asr := store.Task{ID: "mx-2", Provider: "volcengine", Tool: "asr",
		Status: store.StatusSucceeded, Params: "{}", Summary: `{"segments":[]}`}
	if err := s.svc.DB().CreateTask(&asr); err != nil {
		t.Fatal(err)
	}
	code, body, _ = get("/api/minutes/mx-2/export?format=markdown")
	if code != 200 || !strings.Contains(body, "仅妙记任务") {
		t.Fatalf("非妙记任务应报错: %d %s", code, body)
	}
}
