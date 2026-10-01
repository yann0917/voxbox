package server

// 说话人改名端点（录音笔记本地覆盖层）：ASR 上游产出的 segments[].speaker 只有
// 「1/2/3」编号，真实姓名由用户在页面改名——写入 Task.Summary 的 speaker_names
// 键（覆盖式整体替换，其余键不动）。GET 任务经 DTO 原样带出，后续加工层以
// speaker_names 为渲染说话人名的最终事实源（原始 segments 不回改）。

import (
	"encoding/json"

	"github.com/gin-gonic/gin"
	"github.com/yann0917/voxbox/internal/store"
)

func (s *Server) renameTaskSpeakers(c *gin.Context) {
	var body struct {
		Names map[string]string `json:"names"`
	}
	// names 缺失（nil）拒绝：写 null 会破坏 Summary 结构，空对象 {} 即清空改名
	if err := c.ShouldBindJSON(&body); err != nil || body.Names == nil {
		fail(c, CodeBadRequest, "参数错误：names 必填（说话人编号→名称映射）")
		return
	}
	t, err := s.svc.DB().GetTask(c.Param("id"))
	if err == store.ErrNotFound {
		fail(c, CodeNotFound, "任务不存在")
		return
	} else if err != nil {
		failErr(c, err)
		return
	}
	// 越权同报不存在（不泄露他人任务存在性），与 getTask/deleteTask 同款
	if !canAccessTask(t.UserID, principalFrom(c)) {
		fail(c, CodeNotFound, "任务不存在")
		return
	}
	// speaker_names 的语义锚定在 segments[].speaker 编号上，仅 ASR 任务可改名
	//（Tool 列存工具名 asr，provider 无关——见 engine.createTask）
	if t.Tool != "asr" {
		fail(c, CodeBadRequest, "仅语音识别（asr）任务支持说话人改名")
		return
	}
	// 整体读→写 speaker_names→整体写回：空/损坏 Summary 从空对象起步
	var sum map[string]any
	_ = json.Unmarshal([]byte(t.Summary), &sum)
	if sum == nil {
		sum = map[string]any{}
	}
	sum["speaker_names"] = body.Names
	b, err := json.Marshal(sum)
	if err != nil {
		failErr(c, err)
		return
	}
	// 条件更新沿用 GetTask 已验证的属主（admin 可代改、本地无主存量 UserID=""
	// 同样可改；按请求者 id 反而会把这两条路径挡死），行内属主二次把关防竞态
	if err := s.svc.DB().UpdateTaskSummary(t.ID, t.UserID, string(b)); err != nil {
		failErr(c, err)
		return
	}
	ok(c, gin.H{"ok": true})
}
