package refine

import (
	"strings"
	"testing"
)

// BuildMessages 内置模式的 system 要点与输出契约（minutes=纯 JSON 对象纪要；
// todos/events=纯 JSON 数组；mindmap/custom=自由文本），user 消息承载调用方渲染
// 好的逐行转写（HH:MM:SS 说话人N：文本，说话人前缀原样保留不解析）。

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

func TestBuildMessagesMinutes(t *testing.T) {
	system, user := BuildMessages("minutes", "", "00:00:00 说话人1：项目按期上线\n00:00:03 说话人2：同意")
	if !strings.Contains(system, "会议纪要") || !strings.Contains(system, "JSON") {
		t.Fatalf("system: %s", system)
	}
	// 输出契约：结构化对象 + evidence 行号锚点 + 反编造约束
	for _, want := range []string{`"topics"`, `"conclusions"`, `"todos"`, `"terms"`, "evidence", "不得编造", "行号"} {
		if !strings.Contains(system, want) {
			t.Errorf("system 应含 %q: %s", want, system)
		}
	}
	if !strings.Contains(user, "00:00:03 说话人2：同意") {
		t.Fatalf("user 应原样承载转写行: %s", user)
	}
}

func TestBuildMessagesIncremental(t *testing.T) {
	// 首稿（无已有纪要）：走全量提示词
	system, user := BuildMinutesIncremental("", "[L1 00:00:01 张三] 大家好")
	if !strings.Contains(system, "会议纪要") || strings.Contains(system, "增量更新") {
		t.Fatalf("首稿应走全量提示词: %s", system)
	}
	if !strings.Contains(user, "[L1 00:00:01 张三] 大家好") {
		t.Fatalf("首稿 user: %s", user)
	}
	// 增量：system 附增量规则，user 回显当前纪要 + 新增行
	system, user = BuildMinutesIncremental(`{"summary":"旧概述"}`, "[L4 00:01:00 张三] 新增内容")
	if !strings.Contains(system, "增量更新") || !strings.Contains(system, "完整 JSON") {
		t.Fatalf("增量应声明整体覆盖契约: %s", system)
	}
	if !strings.Contains(user, `{"summary":"旧概述"}`) || !strings.Contains(user, "[L4 00:01:00 张三] 新增内容") {
		t.Fatalf("增量 user 应回显纪要与新增行: %s", user)
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
	_, user := BuildMessages("minutes", "", exact)
	if strings.Contains(user, truncatedSuffix) {
		t.Fatal("恰好 30000 字不应截断")
	}
	if !strings.Contains(user, exact) {
		t.Fatal("未超长时转写应完整保留")
	}
	// 超 1 字：截到 30000 并追加标记，完整原文不再保留
	_, user = BuildMessages("minutes", "", strings.Repeat("字", maxTranscriptRunes+1))
	if !strings.Contains(user, truncatedSuffix) {
		t.Fatal("超长转写应追加截断标记")
	}
	if strings.Contains(user, strings.Repeat("字", maxTranscriptRunes+1)) {
		t.Fatal("截断后不应保留完整原文")
	}
	// 增量消息同样受截断保护
	_, user = BuildMinutesIncremental(`{"summary":"s"}`, strings.Repeat("字", maxTranscriptRunes+1))
	if !strings.Contains(user, truncatedSuffix) {
		t.Fatal("增量超长转写应追加截断标记")
	}
}

func TestValidMode(t *testing.T) {
	for _, m := range []string{"minutes", "todos", "events", "mindmap", "custom"} {
		if !ValidMode(m) {
			t.Errorf("%s 应为合法模式", m)
		}
	}
	for _, m := range []string{"", "summary", "Summary", "todo", "其他"} {
		if ValidMode(m) {
			t.Errorf("%q 不应为合法模式", m)
		}
	}
}
