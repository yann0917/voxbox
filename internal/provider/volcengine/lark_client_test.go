package volcengine

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newLarkMock 妙记 mock：按 handler 决策响应，记录请求路径/头/body 供断言。
func newLarkMock(t *testing.T, handler func(w http.ResponseWriter, r *http.Request, body map[string]any, n int)) *ttsLongMockServer {
	m := &ttsLongMockServer{t: t}
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&body)
		}
		headers := map[string]string{}
		for _, k := range []string{"X-Api-Key", "X-Api-App-Key", "X-Api-Access-Key", "X-Api-Resource-Id", "X-Api-Request-Id", "X-Api-Sequence", "X-Tt-Logid"} {
			headers[k] = r.Header.Get(k)
		}
		m.mu.Lock()
		n := len(m.reqs)
		m.reqs = append(m.reqs, mockReq{path: r.URL.Path, headers: headers, body: body})
		m.mu.Unlock()
		w.Header().Set("X-Tt-Logid", "logid-lark")
		if handler != nil {
			handler(w, r, body, n)
			return
		}
		w.Header().Set("X-Api-Status-Code", larkCodeOK)
		writeJSON(w, map[string]any{"Data": map[string]any{"TaskID": "t-1"}})
	}))
	t.Cleanup(m.srv.Close)
	return m
}

func TestLarkSubmitSuccess(t *testing.T) {
	m := newLarkMock(t, func(w http.ResponseWriter, r *http.Request, body map[string]any, n int) {
		w.Header().Set("X-Api-Status-Code", larkCodeOK)
		w.Header().Set("X-Api-Message", "OK")
		writeJSON(w, map[string]any{"Data": map[string]any{"TaskID": "7534288318142352914"}})
	})

	c := NewLarkClientWithBaseURL(SpeechCred{APIKey: "key-1"}, m.srv.URL)
	taskID, err := c.Submit(context.Background(), LarkSubmitReq{
		FileURL: "https://example.com/meeting.mp4", FileType: "video",
		SourceLang: "zh_cn", SpeakerIdentification: true, NumberOfSpeaker: 3,
		HotWords: `[{"word":"火山引擎"}]`, AllActivate: true,
		TranslationEnable: true, TranslationTargetLang: "en_us",
		ExtractTodo: true, ExtractQA: true, SummarizationEnable: true, ChapterEnable: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if taskID != "7534288318142352914" {
		t.Errorf("taskID = %q", taskID)
	}

	req := m.request(0)
	if req.path != "/api/v3/auc/lark/submit" {
		t.Errorf("path = %q", req.path)
	}
	// 新版单键鉴权 + 固定资源/序列
	if req.headers["X-Api-Key"] != "key-1" {
		t.Errorf("X-Api-Key = %q", req.headers["X-Api-Key"])
	}
	if v := req.headers["X-Api-App-Key"]; v != "" {
		t.Errorf("新版鉴权不应携带 X-Api-App-Key: %q", v)
	}
	if req.headers["X-Api-Resource-Id"] != "volc.lark.minutes" {
		t.Errorf("resource = %q", req.headers["X-Api-Resource-Id"])
	}
	if req.headers["X-Api-Sequence"] != "-1" {
		t.Errorf("sequence = %q", req.headers["X-Api-Sequence"])
	}
	if req.headers["X-Api-Request-Id"] == "" {
		t.Errorf("request id 缺失")
	}

	// body 结构（官方大驼峰）
	input := req.body["Input"].(map[string]any)["Offline"].(map[string]any)
	if input["FileURL"] != "https://example.com/meeting.mp4" || input["FileType"] != "video" {
		t.Errorf("Input.Offline = %v", input)
	}
	params := req.body["Params"].(map[string]any)
	if params["AllActivate"] != true || params["SourceLang"] != "zh_cn" || params["AudioTranscriptionEnable"] != true {
		t.Errorf("Params = %v", params)
	}
	at := params["AudioTranscriptionParams"].(map[string]any)
	if at["SpeakerIdentification"] != true || at["NumberOfSpeaker"] != float64(3) || at["HotWords"] == "" {
		t.Errorf("AudioTranscriptionParams = %v", at)
	}
	tp := params["TranslationParams"].(map[string]any)
	if params["TranslationEnable"] != true || tp["TargetLang"] != "en_us" {
		t.Errorf("Translation = %v/%v", params["TranslationEnable"], tp)
	}
	ie := params["InformationExtractionParams"].(map[string]any)
	types := ie["Types"].([]any)
	if len(types) != 2 || types[0] != "todo_list" || types[1] != "question_answer" {
		t.Errorf("IE Types = %v", types)
	}
	sp := params["SummarizationParams"].(map[string]any)
	if params["SummarizationEnabled"] != true || sp["Types"].([]any)[0] != "summary" {
		t.Errorf("Summarization = %v", params)
	}
	if params["ChapterEnabled"] != true {
		t.Errorf("ChapterEnabled = %v", params)
	}
}

// 双头回退：仅配 APP ID + Access Token（旧版控制台）时发 X-Api-App-Key + X-Api-Access-Key。
func TestLarkSubmitLegacyAuthFallback(t *testing.T) {
	m := newLarkMock(t, nil)
	c := NewLarkClientWithBaseURL(SpeechCred{AppID: "app-1", AccessToken: "tok-1"}, m.srv.URL)
	_, err := c.Submit(context.Background(), LarkSubmitReq{
		FileURL: "https://example.com/a.wav", FileType: "audio",
		SourceLang: "zh_cn", AllActivate: true, SummarizationEnable: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	h := m.request(0).headers
	if h["X-Api-App-Key"] != "app-1" || h["X-Api-Access-Key"] != "tok-1" {
		t.Errorf("legacy auth headers = %v", h)
	}
	if v := h["X-Api-Key"]; v != "" {
		t.Errorf("双头模式不应携带 X-Api-Key: %q", v)
	}
}

func TestLarkSubmitMinimalAndOptionalOmission(t *testing.T) {
	m := newLarkMock(t, nil)
	c := NewLarkClientWithBaseURL(SpeechCred{AppID: "app-1", AccessToken: "tok-1"}, m.srv.URL)
	_, err := c.Submit(context.Background(), LarkSubmitReq{
		FileURL: "https://example.com/talk.wav", FileType: "audio",
		SourceLang: "en_us", AllActivate: true, SummarizationEnable: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	params := m.request(0).body["Params"].(map[string]any)
	// 未开启的附加功能段不应下发
	if _, ok := params["TranslationParams"]; ok {
		t.Errorf("TranslationParams 不应下发: %v", params)
	}
	if _, ok := params["InformationExtractionParams"]; ok {
		t.Errorf("InformationExtractionParams 不应下发: %v", params)
	}
	if _, ok := params["SummarizationParams"]; !ok {
		t.Errorf("SummarizationParams 应随开启下发: %v", params)
	}
}

func TestLarkSubmitError(t *testing.T) {
	m := newLarkMock(t, func(w http.ResponseWriter, r *http.Request, body map[string]any, n int) {
		w.Header().Set("X-Api-Status-Code", "45000001")
		w.Header().Set("X-Api-Message", "invalid params")
		writeJSON(w, map[string]any{})
	})
	c := NewLarkClientWithBaseURL(SpeechCred{AppID: "app-1", AccessToken: "tok-1"}, m.srv.URL)
	_, err := c.Submit(context.Background(), LarkSubmitReq{FileURL: "x", SourceLang: "zh_cn", AllActivate: true, SummarizationEnable: true})
	if err == nil || !strings.Contains(err.Error(), "45000001") || !strings.Contains(err.Error(), "logid-lark") {
		t.Fatalf("err = %v, 应含状态码与 logid", err)
	}
}

func TestLarkQueryStates(t *testing.T) {
	calls := 0
	m := newLarkMock(t, func(w http.ResponseWriter, r *http.Request, body map[string]any, n int) {
		calls++
		if calls <= 2 { // 前两次中间态
			w.Header().Set("X-Api-Status-Code", larkCodeProcessing)
			writeJSON(w, map[string]any{"Code": 0, "Data": map[string]any{"TaskID": "t-1", "Status": "running"}})
			return
		}
		w.Header().Set("X-Api-Status-Code", larkCodeOK)
		writeJSON(w, map[string]any{"Code": 0, "Data": map[string]any{
			"TaskID": "t-1", "Status": "success", "ErrCode": 0,
			"Result": map[string]any{
				"AudioTranscriptionFile": "https://mock/tos/transcribe.json?sig=1",
				"SummarizationFile":      "https://mock/tos/summary.json?sig=1",
			},
		}})
	})

	c := NewLarkClientWithBaseURL(SpeechCred{AppID: "app-1", AccessToken: "tok-1"}, m.srv.URL)

	res, status, err := c.Query(context.Background(), "t-1")
	if err != nil || status != larkStatusRunning {
		t.Fatalf("res=%+v status=%s err=%v", res, status, err)
	}
	// 查询请求体与头：X-Api-Request-Id 回传任务 ID
	req := m.request(0)
	if req.body["TaskID"] != "t-1" {
		t.Errorf("body = %v", req.body)
	}
	if req.headers["X-Api-Request-Id"] != "t-1" {
		t.Errorf("查询 X-Api-Request-Id 应为任务 ID，got %q", req.headers["X-Api-Request-Id"])
	}

	for {
		res, status, err = c.Query(context.Background(), "t-1")
		if err != nil {
			t.Fatal(err)
		}
		if status == larkStatusSuccess {
			break
		}
	}
	if res.Result == nil || res.Result.AudioTranscriptionFile == "" || res.Result.SummarizationFile == "" {
		t.Errorf("result = %+v", res.Result)
	}
	if res.Result.ChapterFile != "" {
		t.Errorf("未开启的功能不应有 URL: %+v", res.Result)
	}
}

func TestLarkQueryTaskFailed(t *testing.T) {
	m := newLarkMock(t, func(w http.ResponseWriter, r *http.Request, body map[string]any, n int) {
		w.Header().Set("X-Api-Status-Code", larkCodeOK)
		writeJSON(w, map[string]any{"Code": 0, "Data": map[string]any{
			"TaskID": "t-1", "Status": "failed", "ErrCode": 4809, "ErrMessage": "bad url",
		}})
	})
	c := NewLarkClientWithBaseURL(SpeechCred{AppID: "app-1", AccessToken: "tok-1"}, m.srv.URL)
	res, status, err := c.Query(context.Background(), "t-1")
	if err != nil {
		t.Fatal(err)
	}
	if status != larkStatusFailed || res.ErrCode != 4809 || res.ErrMessage != "bad url" {
		t.Errorf("res=%+v status=%s", res, status)
	}
	if msg := larkTaskErrMsg(res.ErrCode, res.ErrMessage); !strings.Contains(msg, "url 无效(4809)") || !strings.Contains(msg, "bad url") {
		t.Errorf("larkTaskErrMsg = %q", msg)
	}
}

// 实测资源未开通：上游返回 45000030 + "requested resource not granted"（文档错误码表未列），
// 应识别为 ErrNotGranted（退出码 4）而非参数错误。
func TestLarkSubmitNotGranted(t *testing.T) {
	m := newLarkMock(t, func(w http.ResponseWriter, r *http.Request, body map[string]any, n int) {
		w.Header().Set("X-Api-Status-Code", "45000030")
		w.Header().Set("X-Api-Message", "[resource_id=volc.lark.minutes] requested resource not granted")
		writeJSON(w, map[string]any{})
	})
	c := NewLarkClientWithBaseURL(SpeechCred{AppID: "a", AccessToken: "b"}, m.srv.URL)
	_, err := c.Submit(context.Background(), LarkSubmitReq{FileURL: "x", SourceLang: "zh_cn", AllActivate: true, SummarizationEnable: true})
	if err == nil {
		t.Fatal("应返回错误")
	}
	if !strings.Contains(err.Error(), "ErrNotGranted") && !strings.Contains(err.Error(), "未开通") {
		t.Errorf("err = %q, 应报未开通", err.Error())
	}
	if strings.Contains(err.Error(), "请检查凭证与请求参数") {
		t.Errorf("err = %q, 不应误导为参数错误", err.Error())
	}
}

func TestLarkQueryTerminalErrorCode(t *testing.T) {
	m := newLarkMock(t, func(w http.ResponseWriter, r *http.Request, body map[string]any, n int) {
		w.Header().Set("X-Api-Status-Code", "45000151")
		w.Header().Set("X-Api-Message", "bad format")
		writeJSON(w, map[string]any{})
	})
	c := NewLarkClientWithBaseURL(SpeechCred{AppID: "app-1", AccessToken: "tok-1"}, m.srv.URL)
	_, _, err := c.Query(context.Background(), "t-1")
	if err == nil || !strings.Contains(err.Error(), "45000151") {
		t.Fatalf("err = %v", err)
	}
}

func TestLarkDownloadAndFileType(t *testing.T) {
	m := newLarkMock(t, nil)
	c := NewLarkClientWithBaseURL(SpeechCred{AppID: "a", AccessToken: "b"}, m.srv.URL)
	data, err := c.Download(context.Background(), m.srv.URL+"/result.json?X-Tos-Expires=86400")
	if err != nil || len(data) == 0 {
		t.Fatalf("download err=%v len=%d", err, len(data))
	}

	cases := map[string]string{
		"https://x.com/a.mp4?sign=1": "video",
		"https://x.com/a.MOV":        "video",
		"https://x.com/a.mkv":        "video",
		"https://x.com/a.wav":        "audio",
		"https://x.com/a.mp3":        "audio",
		"https://x.com/a":            "audio", // 无扩展名默认音频
	}
	for u, want := range cases {
		if got := larkFileTypeByExt(u); got != want {
			t.Errorf("larkFileTypeByExt(%q) = %q, want %q", u, got, want)
		}
	}
}

func TestLarkParseFeatures(t *testing.T) {
	// 空默认 summary
	f, err := larkParseFeatures("")
	if err != nil || !f.summary || f.names()[0] != "summary" {
		t.Fatalf("f=%+v err=%v", f, err)
	}
	// 组合 + 别名
	f, err = larkParseFeatures("todo, question_answer, chapter")
	if err != nil || !f.todo || !f.qa || !f.chapter || f.summary || f.translation {
		t.Fatalf("f=%+v err=%v", f, err)
	}
	if len(f.names()) != 3 {
		t.Errorf("names = %v", f.names())
	}
	// 未知项 / 全空
	if _, err = larkParseFeatures("summary,video"); err == nil {
		t.Error("未知功能应报错")
	}
	if _, err = larkParseFeatures(",,"); err == nil {
		t.Error("全空清单应报错")
	}
}

func TestLarkHotwordsJSON(t *testing.T) {
	if got := larkHotwordsJSON(""); got != "" {
		t.Errorf("empty = %q", got)
	}
	got := larkHotwordsJSON("火山引擎, bigmodel ,,")
	var parsed []map[string]string
	if err := json.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatalf("json = %q err = %v", got, err)
	}
	if len(parsed) != 2 || parsed[0]["word"] != "火山引擎" || parsed[1]["word"] != "bigmodel" {
		t.Errorf("parsed = %v", parsed)
	}
}
