package mvsep

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// mvsepMock MVSep mock：记录请求（方法/路径/查询/表单/文件），按 respBody 回复。
type mvsepMock struct {
	method   string
	path     string
	query    map[string]string
	form     map[string]string
	fileName string
	fileBody []byte
}

func newMockServer(t *testing.T, respBody any, got *mvsepMock) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got != nil {
			got.method = r.Method
			got.path = r.URL.Path
			got.query = map[string]string{}
			for k, v := range r.URL.Query() {
				got.query[k] = v[0]
			}
			got.form = map[string]string{}
			ct := r.Header.Get("Content-Type")
			if strings.HasPrefix(ct, "multipart/form-data") {
				_ = r.ParseMultipartForm(32 << 20)
				if r.MultipartForm != nil {
					for k, vs := range r.MultipartForm.Value {
						if len(vs) > 0 {
							got.form[k] = vs[0]
						}
					}
					if fss := r.MultipartForm.File["audiofile"]; len(fss) > 0 {
						got.fileName = fss[0].Filename
						fr, _ := fss[0].Open()
						got.fileBody, _ = io.ReadAll(fr)
						fr.Close()
					}
				}
			} else if ct == "application/x-www-form-urlencoded" {
				_ = r.ParseForm()
				for k, vs := range r.PostForm {
					if len(vs) > 0 {
						got.form[k] = vs[0]
					}
				}
			}
		}
		w.Header().Set("Content-Type", "application/json")
		switch b := respBody.(type) {
		case nil:
		case string:
			_, _ = w.Write([]byte(b))
		default:
			_ = json.NewEncoder(w).Encode(b)
		}
	}))
}

// upstreamAlgorithms 按 /api/app/algorithms 真实形态构造（options 为 JSON 字符串、
// required 为数字、render_id 与 id 不同值、algorithm_group 嵌套）。
const upstreamAlgorithms = `[
 {"id": 27, "render_id": 26, "name": "Ensemble (vocals, instrum)",
  "algorithm_group": {"id": 23, "name": "Ensembles"},
  "price_coefficient": 2, "orientation": 2, "is_active": 1,
  "description": "Ensemble of best vocal models.",
  "algorithm_fields": [
   {"name": "add_opt2", "text": "Model Type", "default_key": "7",
    "options": "{\n \"1\": \"SDR 10.44\",\n \"7\": \"SDR 11.93\"\n}",
    "input_type": "select", "required": 1},
   {"name": "add_opt1", "text": "Output files", "default_key": "0",
    "options": {"0": "Standard set"}, "input_type": "select", "required": false}
  ]}
]`

func TestAlgorithms(t *testing.T) {
	var got mvsepMock
	srv := newMockServer(t, upstreamAlgorithms, &got)
	defer srv.Close()

	c := New("tok-1", srv.URL)
	algos, err := c.Algorithms(context.Background())
	if err != nil {
		t.Fatalf("Algorithms() err = %v", err)
	}
	if got.query["api_token"] != "tok-1" {
		t.Errorf("api_token query = %q, want tok-1（带 token 才返回分组名）", got.query["api_token"])
	}
	if len(algos) != 1 {
		t.Fatalf("算法数量 = %d, want 1", len(algos))
	}
	a := algos[0]
	if a.SepType != 26 || a.ID != 27 {
		t.Errorf("SepType/ID = %d/%d, want 26/27（render_id 才是 sep_type）", a.SepType, a.ID)
	}
	if a.GroupName != "Ensembles" || a.GroupID != 23 {
		t.Errorf("GroupName/GroupID = %q/%d, want Ensembles/23", a.GroupName, a.GroupID)
	}
	if len(a.Fields) != 2 {
		t.Fatalf("字段数量 = %d, want 2", len(a.Fields))
	}
	f := a.Fields[0]
	if !f.Required {
		t.Errorf("required=1 应解析为 true")
	}
	if len(f.Options) != 2 || f.Options["7"] != "SDR 11.93" {
		t.Errorf("options（JSON 字符串形态）解析错误: %+v", f.Options)
	}
	if a.Fields[1].Required {
		t.Errorf("required=false 应解析为 false")
	}
}

func TestAlgorithmsWithoutGroupFallback(t *testing.T) {
	// 无 token 时上游不返回 algorithm_group 嵌套：回落 algorithm_group_id。
	srv := newMockServer(t, `[{"id": 5, "name": "Noise reduction", "algorithm_group_id": 3, "is_active": 1}]`, nil)
	defer srv.Close()
	algos, err := New("", srv.URL).Algorithms(context.Background())
	if err != nil {
		t.Fatalf("Algorithms() err = %v", err)
	}
	if algos[0].SepType != 5 || algos[0].GroupID != 3 || algos[0].GroupName != "" {
		t.Errorf("回落解析错误: %+v", algos[0])
	}
}

func TestUser(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		var got mvsepMock
		srv := newMockServer(t, map[string]any{
			"success": true,
			"data":    map[string]any{"name": "Yabo", "email": "y@example.com", "premium_minutes": 100, "premium_enabled": 1},
		}, &got)
		defer srv.Close()
		u, err := New("tok-1", srv.URL).User(context.Background())
		if err != nil {
			t.Fatalf("User() err = %v", err)
		}
		if u.Name != "Yabo" || u.Email != "y@example.com" || u.PremiumMinutes != 100 || !u.PremiumEnabled {
			t.Errorf("User 解析错误: %+v", u)
		}
	})
	t.Run("401 → ErrAuth", func(t *testing.T) {
		srv := newMockServer(t, map[string]any{"message": "Unauthenticated."}, nil)
		srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"message":"Unauthenticated."}`))
		})
		defer srv.Close()
		_, err := New("bad", srv.URL).User(context.Background())
		if !errors.Is(err, ErrAuth) {
			t.Fatalf("err = %v, want ErrAuth", err)
		}
	})
	t.Run("未配置 token", func(t *testing.T) {
		_, err := New("", "https://mvsep.com").User(context.Background())
		if err == nil || errors.Is(err, ErrAuth) {
			t.Fatalf("未配置 token 应报中文配置指引, got %v", err)
		}
	})
}

func TestQueue(t *testing.T) {
	var got mvsepMock
	srv := newMockServer(t, map[string]any{
		"queue":            map[string]any{"in_process": 21, "registered": 40, "unregistered": 43},
		"current":          map[string]any{"plan": "registered", "queue": 40},
		"free_separations": map[string]any{"left": 49, "max": 50},
	}, &got)
	defer srv.Close()
	qs, err := New("tok-1", srv.URL).Queue(context.Background())
	if err != nil {
		t.Fatalf("Queue() err = %v", err)
	}
	if qs.Plan != "registered" || qs.FreeLeft != 49 || qs.FreeMax != 50 || qs.InProcess != 21 {
		t.Errorf("QueueStatus 解析错误: %+v", qs)
	}
}

func TestHistory(t *testing.T) {
	var got mvsepMock
	srv := newMockServer(t, map[string]any{
		"success": true,
		"data": []map[string]any{{
			"id": 12, "hash": "20260916-abc.mp3", "created_at": "2026-09-16 12:00:00",
			"job_exists": 1, "algorithm": map[string]any{"name": "BS Roformer"},
		}},
	}, &got)
	defer srv.Close()
	items, err := New("tok-1", srv.URL).History(context.Background(), 0, 2)
	if err != nil {
		t.Fatalf("History() err = %v", err)
	}
	if got.query["start"] != "0" || got.query["limit"] != "2" {
		t.Errorf("分页参数错误: %v", got.query)
	}
	if len(items) != 1 || items[0].Hash != "20260916-abc.mp3" || !items[0].JobExists || items[0].Algorithm != "BS Roformer" {
		t.Errorf("History 解析错误: %+v", items)
	}
}

func TestCreate(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		var got mvsepMock
		srv := newMockServer(t, map[string]any{
			"success": true,
			"data":    map[string]any{"hash": "20260916-abc.mp3", "link": "https://x/get?hash=abc"},
		}, &got)
		defer srv.Close()

		hash, err := New("tok-1", srv.URL).Create(context.Background(), CreateInput{
			Filename: "song.mp3", Reader: stringReader("AUDIO-DATA"), Size: 10,
			SepType: 26, AddOpts: map[string]string{"add_opt1": "0", "add_opt2": "7", "add_opt3": ""},
			OutputFormat: 1,
		})
		if err != nil {
			t.Fatalf("Create() err = %v", err)
		}
		if hash != "20260916-abc.mp3" {
			t.Errorf("hash = %q", hash)
		}
		if got.method != http.MethodPost || got.path != "/api/separation/create" {
			t.Errorf("请求方式/路径错误: %s %s", got.method, got.path)
		}
		for k, want := range map[string]string{
			"api_token": "tok-1", "sep_type": "26", "output_format": "1",
			"add_opt1": "0", "add_opt2": "7", "is_demo": "0",
		} {
			if got.form[k] != want {
				t.Errorf("form[%s] = %q, want %q", k, got.form[k], want)
			}
		}
		if _, ok := got.form["add_opt3"]; ok {
			t.Errorf("空 add_opt3 不应进表单")
		}
		if got.fileName != "song.mp3" || string(got.fileBody) != "AUDIO-DATA" {
			t.Errorf("audiofile 上传错误: name=%q body=%q", got.fileName, got.fileBody)
		}
	})
	t.Run("success=false", func(t *testing.T) {
		srv := newMockServer(t, map[string]any{"success": false, "message": "File is too large"}, nil)
		defer srv.Close()
		_, err := New("tok-1", srv.URL).Create(context.Background(), CreateInput{
			Filename: "a.mp3", Reader: stringReader("x"), SepType: 26,
		})
		if err == nil || !strings.Contains(err.Error(), "File is too large") {
			t.Fatalf("err = %v, 应含上游 message", err)
		}
	})
	t.Run("401 → ErrAuth", func(t *testing.T) {
		srv := newMockServer(t, nil, nil)
		srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		})
		defer srv.Close()
		_, err := New("bad", srv.URL).Create(context.Background(), CreateInput{
			Filename: "a.mp3", Reader: stringReader("x"), SepType: 26,
		})
		if !errors.Is(err, ErrAuth) {
			t.Fatalf("err = %v, want ErrAuth", err)
		}
	})
}

func TestGet(t *testing.T) {
	t.Run("waiting", func(t *testing.T) {
		var got mvsepMock
		srv := newMockServer(t, map[string]any{
			"success": true, "status": "waiting",
			"data": map[string]any{"queue_count": 15, "current_order": 3},
		}, &got)
		defer srv.Close()
		status, res, err := New("", srv.URL).Get(context.Background(), "h1")
		if err != nil || status != "waiting" || res != nil {
			t.Fatalf("status/res/err = %q/%v/%v", status, res, err)
		}
		if got.query["hash"] != "h1" {
			t.Errorf("hash query = %q", got.query["hash"])
		}
	})
	t.Run("done", func(t *testing.T) {
		srv := newMockServer(t, map[string]any{
			"success": true, "status": "done",
			"data": map[string]any{
				"algorithm": "BS Roformer", "output_format": "wav",
				// 上游 2026-09 实测结构：type/url/bytes；size 为 "159.23 KB" 人类可读串
				"files": []map[string]any{
					{"type": "Vocals", "url": "https://cdn/vocals.wav", "bytes": 1024, "size": "1.02 KB"},
					{"type": "Instrum", "url": "https://cdn/instrum.wav", "bytes": "1025", "size": "1.03 KB"},
				},
			},
		}, nil)
		defer srv.Close()
		status, res, err := New("", srv.URL).Get(context.Background(), "h1")
		if err != nil || status != "done" {
			t.Fatalf("status/err = %q/%v", status, err)
		}
		if res.Algorithm != "BS Roformer" || len(res.Files) != 2 {
			t.Fatalf("结果解析错误: %+v", res)
		}
		if res.Files[0].Name != "Vocals" || res.Files[0].Link != "https://cdn/vocals.wav" || res.Files[0].Size != 1024 {
			t.Errorf(" Vocals 轨解析错误: %+v", res.Files[0])
		}
		if res.Files[1].Name != "Instrum" || res.Files[1].Size != 1025 {
			t.Errorf("bytes 字符串形态解析错误: %+v", res.Files[1])
		}
	})
	t.Run("failed", func(t *testing.T) {
		srv := newMockServer(t, map[string]any{
			"success": true, "status": "failed", "data": map[string]any{"message": "bad input"},
		}, nil)
		defer srv.Close()
		_, _, err := New("", srv.URL).Get(context.Background(), "h1")
		if err == nil || !strings.Contains(err.Error(), "bad input") {
			t.Fatalf("err = %v, 应为任务终态失败", err)
		}
	})
	t.Run("not_found", func(t *testing.T) {
		srv := newMockServer(t, map[string]any{"success": false}, nil)
		defer srv.Close()
		_, _, err := New("", srv.URL).Get(context.Background(), "h1")
		if err == nil || !strings.Contains(err.Error(), "不存在") {
			t.Fatalf("err = %v, 应报任务不存在", err)
		}
	})
}

func TestCancel(t *testing.T) {
	var got mvsepMock
	srv := newMockServer(t, map[string]any{"success": true}, &got)
	defer srv.Close()
	if err := New("tok-1", srv.URL).Cancel(context.Background(), "h1"); err != nil {
		t.Fatalf("Cancel() err = %v", err)
	}
	if got.path != "/api/separation/cancel" || got.form["hash"] != "h1" || got.form["api_token"] != "tok-1" {
		t.Errorf("cancel 请求错误: %s %v", got.path, got.form)
	}
}

func TestFlexInt64(t *testing.T) {
	if got := flexInt64([]byte(`"10240000"`)); got != 10240000 {
		t.Errorf(`flexInt64("字符串数字") = %d`, got)
	}
	if got := flexInt64([]byte(`4096`)); got != 4096 {
		t.Errorf("flexInt64(数字) = %d", got)
	}
	if got := flexInt64([]byte(`"abc"`)); got != 0 {
		t.Errorf("flexInt64(非法) = %d, want 0", got)
	}
}

func TestNormalizeBaseURL(t *testing.T) {
	for raw, want := range map[string]string{
		"":                      DefaultBaseURL,
		"https://hk.mvsep.com":  "https://hk.mvsep.com",
		"https://de.mvsep.com/": "https://de.mvsep.com",
	} {
		if got := normalizeBaseURL(raw); got != want {
			t.Errorf("normalizeBaseURL(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestSanitizeName(t *testing.T) {
	cases := map[string]string{
		"vocals.wav":      "vocals.wav",
		"my song (1).mp3": "my_song__1_.mp3",
		// filepath.Base 已先剥目录：.. 逃逸天然失效
		"../evil.wav": "evil.wav",
		"":            "output",
	}
	for in, want := range cases {
		if got := sanitizeName(in); got != want {
			t.Errorf("sanitizeName(%q) = %q, want %q", in, got, want)
		}
	}
}

func stringReader(s string) io.Reader { return strings.NewReader(s) }
