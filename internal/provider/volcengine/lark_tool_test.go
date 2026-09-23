package volcengine

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yann0917/voxbox/internal/provider"
)

const larkTranscriptJSON = `[
 {"sentence_id":"0","lang":"zh_cn","content":"是我们的人来了吗？","start_time":2250,"end_time":3330,"speaker":{"id":"1","name":"说话人1","type":101}},
 {"sentence_id":"1","lang":"zh_cn","content":"行，那我们过一遍。","start_time":119330,"end_time":131170,"speaker":{"id":"2","name":"说话人2","type":101}}
]`

const larkSummaryJSON = `{"title":"会议主题：测试会议","paragraph":"会议讨论了测试事项。"}`

const larkExtractionJSON = `{"todo_list":[
 {"content":"整理纪要","executor":["茂哥"],"start_time":461630,"sentence_id":["30","31"]},
 {"content":"深入分析耗时","executor":["无"],"start_time":660790,"sentence_id":["37"]}
]}`

const larkChapterJSON = `{"chapter_summary":[
 {"start_time":2000,"end_time":119000,"title":"会议开场","summary":"开场讨论。"},
 {"start_time":119000,"end_time":196000,"title":"内容过一遍","summary":"过一遍内容。"}
]}`

const larkTranslationJSON = `[
 {"sentence_id":"0","lang":"en_us","content":"Is that our man?","start_time":2250,"end_time":3330}
]`

// newMinutesMock 全流程 mock：提交 → 两次 running → success（全功能结果 URL）→ 文件下载。
func newMinutesMock(t *testing.T) *ttsLongMockServer {
	m := &ttsLongMockServer{t: t}
	calls := 0
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		m.mu.Lock()
		m.reqs = append(m.reqs, mockReq{path: r.URL.Path, headers: map[string]string{}, body: body})
		m.mu.Unlock()
		switch {
		case r.URL.Path == larkSubmitPath:
			w.Header().Set("X-Api-Status-Code", larkCodeOK)
			writeJSON(w, map[string]any{"Data": map[string]any{"TaskID": "task-9"}})
		case r.URL.Path == larkQueryPath:
			calls++
			if calls <= 2 {
				w.Header().Set("X-Api-Status-Code", larkCodeProcessing)
				writeJSON(w, map[string]any{"Code": 0, "Data": map[string]any{"TaskID": "task-9", "Status": "running"}})
				return
			}
			w.Header().Set("X-Api-Status-Code", larkCodeOK)
			base := "http://" + r.Host
			writeJSON(w, map[string]any{"Code": 0, "Data": map[string]any{
				"TaskID": "task-9", "Status": "success",
				"Result": map[string]any{
					"AudioTranscriptionFile":    base + "/transcription.json",
					"SummarizationFile":         base + "/summary.json",
					"InformationExtractionFile": base + "/extraction.json",
					"ChapterFile":               base + "/chapter.json",
					"TranslationFile":           base + "/translation.json",
				},
			}})
		default:
			// 结果文件下载：按路径名返回对应 JSON
			name := strings.Split(r.URL.Path, "/")[len(strings.Split(r.URL.Path, "/"))-1]
			var payload string
			switch {
			case strings.HasPrefix(name, "transcription"):
				payload = larkTranscriptJSON
			case strings.HasPrefix(name, "summary"):
				payload = larkSummaryJSON
			case strings.HasPrefix(name, "extraction"):
				payload = larkExtractionJSON
			case strings.HasPrefix(name, "chapter"):
				payload = larkChapterJSON
			case strings.HasPrefix(name, "translation"):
				payload = larkTranslationJSON
			default:
				payload = "{}"
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(payload))
		}
	}))
	t.Cleanup(m.srv.Close)
	return m
}

func TestMinutesToolRunFullFlow(t *testing.T) {
	// 缩短轮询间隔（var 注入，同 tts_long 先例）
	savedInterval, savedMax := larkPollInterval, larkPollMax
	larkPollInterval, larkPollMax = time.Millisecond, 2*time.Millisecond
	defer func() { larkPollInterval, larkPollMax = savedInterval, savedMax }()

	m := newMinutesMock(t)
	outDir := t.TempDir()
	tool := NewMinutesToolWithBaseURL(SpeechCred{AppID: "app-1", AccessToken: "tok-1"}, outDir, m.srv.URL)

	out, err := tool.Run(context.Background(), provider.TaskInput{Params: map[string]any{
		"url":         "https://example.com/meeting.mp4",
		"features":    "summary,todo,chapter,translation",
		"source_lang": "zh_cn",
		"target_lang": "en_us",
		"speakers":    0,
		"hotwords":    "火山引擎",
	}}, func(progress int, note string, detail map[string]any) {})
	if err != nil {
		t.Fatal(err)
	}

	// 提交请求：video 类型 + 功能段按 features 开启
	sub := m.request(0)
	input := sub.body["Input"].(map[string]any)["Offline"].(map[string]any)
	if input["FileType"] != "video" {
		t.Errorf("FileType = %v（应按扩展名推断 video）", input["FileType"])
	}
	params := sub.body["Params"].(map[string]any)
	if params["TranslationEnable"] != true || params["ChapterEnabled"] != true || params["SummarizationEnabled"] != true {
		t.Errorf("Params = %v", params)
	}
	ie := params["InformationExtractionParams"].(map[string]any)
	if ie["Types"].([]any)[0] != "todo_list" {
		t.Errorf("IE Types = %v（todo 开、qa 关）", ie["Types"])
	}

	// 产物：transcript txt + subtitle srt + 4 个 minutes/translation JSON
	kinds := map[string]int{}
	for _, a := range out.Artifacts {
		kinds[a.Kind]++
		if !strings.HasPrefix(a.Path, "minutes/") && !filepath.IsAbs(a.Path) {
			t.Errorf("artifact path = %q, 应在 minutes/ 下", a.Path)
		}
	}
	if kinds["transcript"] != 1 || kinds["subtitle"] != 1 {
		t.Errorf("kinds = %v", kinds)
	}
	if kinds["minutes"] != 4 { // summary/chapter/extraction/translation 四份纪要 JSON
		t.Errorf("kinds = %v", kinds)
	}

	// 转写 txt：说话人前缀 + 换行分隔
	var txtArt, srtArt provider.Artifact
	for _, a := range out.Artifacts {
		if a.Kind == "transcript" {
			txtArt = a
		}
		if a.Kind == "subtitle" {
			srtArt = a
		}
	}
	raw, err := os.ReadFile(filepath.Join(outDir, txtArt.Path))
	if err != nil {
		t.Fatal(err)
	}
	txt := string(raw)
	if !strings.Contains(txt, "说话人1：是我们的人来了吗？") || !strings.Contains(txt, "\n说话人2：行，那我们过一遍。") {
		t.Errorf("txt = %q", txt)
	}
	srtRaw, _ := os.ReadFile(filepath.Join(outDir, srtArt.Path))
	if !strings.Contains(string(srtRaw), "00:00:02,250 --> 00:00:03,330") ||
		!strings.Contains(string(srtRaw), "说话人2：行，那我们过一遍。") {
		t.Errorf("srt = %q", string(srtRaw))
	}

	// Summary：解析后的总结/待办/章节 + 转写统计
	s := out.Summary
	if s["minutes_title"] != "会议主题：测试会议" || s["summary_text"] != "会议讨论了测试事项。" {
		t.Errorf("summary doc = %v/%v", s["minutes_title"], s["summary_text"])
	}
	todos := s["todos"].([]map[string]any)
	if len(todos) != 2 || todos[0]["content"] != "整理纪要" || todos[0]["executor"].([]string)[0] != "茂哥" {
		t.Errorf("todos = %v", todos)
	}
	chapters := s["chapters"].([]map[string]any)
	if len(chapters) != 2 || chapters[0]["title"] != "会议开场" {
		t.Errorf("chapters = %v", chapters)
	}
	if s["sentences"] != 2 || s["speakers_count"] != 2 || s["duration_ms"] != int64(131170) {
		t.Errorf("stats = %v/%v/%v", s["sentences"], s["speakers_count"], s["duration_ms"])
	}
	if s["translation_text"] != "Is that our man?" {
		t.Errorf("translation_text = %v", s["translation_text"])
	}
	if s["upstream_task_id"] != "task-9" {
		t.Errorf("upstream_task_id = %v", s["upstream_task_id"])
	}
}

func TestMinutesToolOutDirRedirect(t *testing.T) {
	savedInterval, savedMax := larkPollInterval, larkPollMax
	larkPollInterval, larkPollMax = time.Millisecond, 2*time.Millisecond
	defer func() { larkPollInterval, larkPollMax = savedInterval, savedMax }()

	m := newMinutesMock(t)
	outDir := t.TempDir()
	dest := filepath.Join(t.TempDir(), "m")
	tool := NewMinutesToolWithBaseURL(SpeechCred{AppID: "a", AccessToken: "b"}, outDir, m.srv.URL)

	out, err := tool.Run(context.Background(), provider.TaskInput{Params: map[string]any{
		"url": "https://example.com/a.wav", "features": "summary", "_out": dest,
	}}, func(int, string, map[string]any) {})
	if err != nil {
		t.Fatal(err)
	}
	// 全部产物落在 _out 目录且路径回填绝对路径
	for _, a := range out.Artifacts {
		if !strings.HasPrefix(a.Path, dest+string(filepath.Separator)) && a.Path != dest {
			t.Errorf("path = %q, 应在 %s 下", a.Path, dest)
		}
		if _, err := os.Stat(a.Path); err != nil {
			t.Errorf("产物文件缺失 %s: %v", a.Path, err)
		}
	}
}

func TestMinutesToolParamAndCredErrors(t *testing.T) {
	savedInterval, savedMax := larkPollInterval, larkPollMax
	larkPollInterval, larkPollMax = time.Millisecond, 2*time.Millisecond
	defer func() { larkPollInterval, larkPollMax = savedInterval, savedMax }()

	m := newMinutesMock(t)
	tool := NewMinutesToolWithBaseURL(SpeechCred{AppID: "app-1", AccessToken: "tok-1"}, t.TempDir(), m.srv.URL)

	cases := []struct {
		params map[string]any
		want   string
	}{
		{map[string]any{"features": "summary"}, "缺少输入"},
		{map[string]any{"url": "https://x/a.wav", "features": "video"}, "暂不支持该附加功能"},
		{map[string]any{"url": "https://x/a.wav", "features": ",,"}, "至少选择一项"},
		{map[string]any{"url": "https://x/a.wav", "features": "summary", "source_lang": "ja"}, "仅支持源语种"},
		{map[string]any{"url": "https://x/a.wav", "features": "translation", "target_lang": "fr"}, "仅支持翻译目标语"},
	}
	for _, tc := range cases {
		_, err := tool.Run(context.Background(), provider.TaskInput{Params: tc.params}, func(int, string, map[string]any) {})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("params %v: err = %v, 应含 %q", tc.params, err, tc.want)
		}
	}
	// 参数错误阶段不应发起上游请求
	if n := len(m.reqs); n != 0 {
		t.Errorf("参数错误不应请求上游，实际 %d 次", n)
	}
	// 两种凭证均可用（妙记服务端接受新版单键与旧版双头），AppID+Token 走完全流程
	_, err := tool.Run(context.Background(), provider.TaskInput{Params: map[string]any{
		"url": "https://x/a.wav", "features": "summary",
	}}, func(int, string, map[string]any) {})
	if err != nil {
		t.Errorf("AppID+Token 凭证应可用: %v", err)
	}
	// 无任何凭证 → 凭证错误（不发起上游）
	bare := NewMinutesToolWithBaseURL(SpeechCred{}, t.TempDir(), m.srv.URL)
	_, err = bare.Run(context.Background(), provider.TaskInput{Params: map[string]any{
		"url": "https://x/a.wav", "features": "summary",
	}}, func(int, string, map[string]any) {})
	if err == nil || !strings.Contains(err.Error(), "凭证未配置") {
		t.Errorf("err = %v, 应报凭证未配置", err)
	}
	// 参数错误阶段不发请求；合法用例的首个上游请求必为提交
	if m.request(0).path != larkSubmitPath {
		t.Errorf("首个请求 = %s, 应为提交", m.request(0).path)
	}
}

func TestMinutesToolTaskFailed(t *testing.T) {
	m := &ttsLongMockServer{t: t}
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == larkSubmitPath {
			w.Header().Set("X-Api-Status-Code", larkCodeOK)
			writeJSON(w, map[string]any{"Data": map[string]any{"TaskID": "t-f"}})
			return
		}
		w.Header().Set("X-Api-Status-Code", larkCodeOK)
		writeJSON(w, map[string]any{"Code": 0, "Data": map[string]any{
			"TaskID": "t-f", "Status": "failed", "ErrCode": 4807, "ErrMessage": "too long",
		}})
	}))
	t.Cleanup(m.srv.Close)

	savedInterval, savedMax := larkPollInterval, larkPollMax
	larkPollInterval, larkPollMax = time.Millisecond, 2*time.Millisecond
	defer func() { larkPollInterval, larkPollMax = savedInterval, savedMax }()

	tool := NewMinutesToolWithBaseURL(SpeechCred{AppID: "a", AccessToken: "b"}, t.TempDir(), m.srv.URL)
	_, err := tool.Run(context.Background(), provider.TaskInput{Params: map[string]any{
		"url": "https://x/a.wav", "features": "summary",
	}}, func(int, string, map[string]any) {})
	if err == nil || !strings.Contains(err.Error(), "音频长度超限(4807)") || !strings.Contains(err.Error(), "too long") {
		t.Fatalf("err = %v", err)
	}
}

// summary.segments 序列化键名必须为小写下划线（前端 Segment 类型与 skill 文档契约）。
// 曾因 ASRSegment 无 json tag 序列化成 Text/StartMS，ASR 页分句文本渲染为空。
func TestLarkSummarySegmentsJSONKeys(t *testing.T) {
	raw, err := json.Marshal([]ASRSegment{{Text: "hi", StartMS: 1, EndMS: 2}})
	if err != nil {
		t.Fatal(err)
	}
	var segs []map[string]any
	if err := json.Unmarshal(raw, &segs); err != nil {
		t.Fatal(err)
	}
	if len(segs) != 1 {
		t.Fatalf("segs = %v", segs)
	}
	seg := segs[0]
	for _, key := range []string{"text", "start_ms", "end_ms"} {
		if _, ok := seg[key]; !ok {
			t.Errorf("key %q 缺失: %s", key, raw)
		}
	}
}
