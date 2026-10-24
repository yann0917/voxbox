package qingmusic

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const sampleLRC = "[00:00.00] 作词 : 周杰伦\n[00:07.73] 听妈妈的话\n"

func TestLyricWy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"code":200,"lrc":{"lyric":` + `"` + strings.ReplaceAll(sampleLRC, "\n", "\\n") + `"}}`))
	}))
	defer srv.Close()
	s := NewSearcher()
	lrc, err := s.lyricWy(context.Background(), srv.URL, "185809")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(lrc, "听妈妈的话") {
		t.Errorf("lrc = %q", lrc)
	}
}

func TestLyricTx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"retcode":0,"lyric":"` + base64.StdEncoding.EncodeToString([]byte(sampleLRC)) + `"}`))
	}))
	defer srv.Close()
	s := NewSearcher()
	lrc, err := s.lyricTx(context.Background(), srv.URL, "003aAYrm3GE0Ac")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(lrc, "听妈妈的话") {
		t.Errorf("lrc = %q", lrc)
	}
}

func TestLyricKg(t *testing.T) {
	search := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":200,"candidates":[{"id":"495847129","accesskey":"AK"}]}`))
	}))
	defer search.Close()
	dl := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("id") != "495847129" {
			t.Errorf("download id = %s", r.URL.Query().Get("id"))
		}
		_, _ = w.Write([]byte(`{"status":200,"content":"` + base64.StdEncoding.EncodeToString([]byte(sampleLRC)) + `"}`))
	}))
	defer dl.Close()

	s := NewSearcher()
	lrc, err := s.lyricKg(context.Background(), search.URL, dl.URL, "ABCDEFHASH", "稻香 周杰伦", 223000)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(lrc, "听妈妈的话") {
		t.Errorf("lrc = %q", lrc)
	}
}

// 无直连端点的音源应返回哨兵错误（上层据此跨源回退），而非空串成功。
func TestLyricUnsupported(t *testing.T) {
	s := NewSearcher()
	if _, err := s.Lyric(context.Background(), SourceKw, "458863095", "稻香", "周杰伦", 0); err != ErrLyricUnsupported {
		t.Errorf("kw err = %v, want ErrLyricUnsupported", err)
	}
	if _, err := s.Lyric(context.Background(), SourceMg, "697066", "", "", 0); err != ErrLyricUnsupported {
		t.Errorf("mg err = %v, want ErrLyricUnsupported", err)
	}
}

func TestBestTrackMatch(t *testing.T) {
	tracks := []Track{
		{Name: "稻香", Artist: "王俊凯", Rid: "cover"},
		{Name: "稻香", Artist: "周杰伦", Rid: "orig"},
		{Name: "稻香 (Live)", Artist: "周杰伦", Rid: "live"},
	}
	if got := BestTrackMatch(tracks, "稻香", "周杰伦"); got == nil || got.Rid != "orig" {
		t.Errorf("match = %+v, want rid=orig（同名优先 + 歌手加分）", got)
	}
	// 歌名完全相等才认：只有模糊包含的不给
	if got := BestTrackMatch([]Track{{Name: "稻香remix", Artist: "路人"}}, "稻香", "周杰伦"); got != nil {
		t.Errorf("match = %+v, want nil", got)
	}
	// 归一化：括号段与空白不影响相等判定
	if got := BestTrackMatch([]Track{{Name: "晴天 （Live版）", Artist: "周杰伦"}}, "晴天", "周杰伦"); got == nil {
		t.Error("归一化后应匹配")
	}
	if BestTrackMatch(nil, "稻香", "周杰伦") != nil {
		t.Error("空结果应返回 nil")
	}
}
