package refine

import (
	"strings"
	"testing"
)

// BuildMessages 三种内置模式的 system 要点与输出契约（summary=markdown 纪要；
// todos/events=纯 JSON 数组），user 消息承载调用方渲染好的逐行转写
// （HH:MM:SS 说话人N：文本，说话人前缀原样保留不解析）。

func TestBuildMessagesTodos(t *testing.T) {
	system, user := BuildMessages("todos", "", "00:00:00 说话人1：我们下周三交方案\n00:00:05 说话人2：好的")
	if !strings.Contains(system, "待办") || !strings.Contains(system, "JSON") {
		t.Fatalf("system: %s", system)
	}
	if !strings.Contains(user, "说话人1：我们下周三交方案") {
		t.Fatalf("user: %s", user)
	}
	if !strings.Contains(system, `"content"`) || !strings.Contains(system, `"owner"`) || !strings.Contains(system, `"due"`) {
		t.Errorf("system 应约定 todos 元素字段: %s", system)
	}
}

func TestBuildMessagesSummary(t *testing.T) {
	system, user := BuildMessages("summary", "", "00:00:00 说话人1：项目按期上线\n00:00:03 说话人2：同意")
	if !strings.Contains(system, "纪要") || !strings.Contains(system, "markdown") {
		t.Fatalf("system: %s", system)
	}
	if !strings.Contains(system, "不要编造") {
		t.Errorf("system 应含反编造约束: %s", system)
	}
	if !strings.Contains(user, "00:00:03 说话人2：同意") {
		t.Fatalf("user 应原样承载转写行: %s", user)
	}
}

func TestBuildMessagesEvents(t *testing.T) {
	system, user := BuildMessages("events", "", "00:00:00 说话人1：下周三开会评审方案")
	if !strings.Contains(system, "事件") || !strings.Contains(system, "JSON") {
		t.Fatalf("system: %s", system)
	}
	// 相对时间推算的锚点：system 声明以录音日期为基准（日期由 handler 附在 user 头部）
	if !strings.Contains(system, "录音日期") {
		t.Errorf("system 应说明以录音日期推算相对时间: %s", system)
	}
	if !strings.Contains(system, `"title"`) || !strings.Contains(system, `"start"`) {
		t.Errorf("system 应约定 events 元素字段: %s", system)
	}
	if !strings.Contains(user, "下周三开会评审方案") {
		t.Fatalf("user: %s", user)
	}
}

func TestBuildMessagesCustom(t *testing.T) {
	system, user := BuildMessages("custom", "把转写改写成会议新闻稿", "00:00:00 说话人1：开会")
	if system != "把转写改写成会议新闻稿" {
		t.Fatalf("custom 的 system 应为调用方指令, got %s", system)
	}
	if !strings.Contains(user, "开会") {
		t.Fatalf("user: %s", user)
	}
}

func TestBuildMessagesTruncate(t *testing.T) {
	// 恰好 30000 字：不截断
	exact := strings.Repeat("字", maxTranscriptRunes)
	_, user := BuildMessages("summary", "", exact)
	if strings.Contains(user, truncatedSuffix) {
		t.Fatal("恰好 30000 字不应截断")
	}
	if !strings.Contains(user, exact) {
		t.Fatal("未超长时转写应完整保留")
	}
	// 超 1 字：截到 30000 并追加标记，完整原文不再保留
	_, user = BuildMessages("summary", "", strings.Repeat("字", maxTranscriptRunes+1))
	if !strings.Contains(user, truncatedSuffix) {
		t.Fatal("超长转写应追加截断标记")
	}
	if strings.Contains(user, strings.Repeat("字", maxTranscriptRunes+1)) {
		t.Fatal("截断后不应保留完整原文")
	}
}

func TestValidMode(t *testing.T) {
	for _, m := range []string{"summary", "todos", "events", "custom"} {
		if !ValidMode(m) {
			t.Errorf("%s 应为合法模式", m)
		}
	}
	for _, m := range []string{"", "Summary", "todo", "其他"} {
		if ValidMode(m) {
			t.Errorf("%q 不应为合法模式", m)
		}
	}
}
