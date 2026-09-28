package server

// 任务端点：提交（三输入通道互斥）、列表/详情/删除/取消/重跑。
// 产物 id → 本地路径的解析链（resolveArtifactInputs）供提交与重跑共用。

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/yann0917/voxbox/internal/store"
	"github.com/yann0917/voxbox/internal/task"
)

type createTaskReq struct {
	Provider       string         `json:"provider"`
	Tool           string         `json:"tool"`
	Params         map[string]any `json:"params"`
	FileIDs        []string       `json:"file_ids"`        // /api/uploads 返回的上传文件 id
	ArtifactInput  string         `json:"artifact_input"`  // 已有产物 id（跨工具联动：如分离人声轨送 ASR）
	ArtifactInputs []string       `json:"artifact_inputs"` // 多产物输入按序 → audio/audio2…（mix：[伴奏id, 人声id]）
}

func (s *Server) createTask(c *gin.Context) {
	p := principalFrom(c)
	var req createTaskReq
	if err := c.ShouldBindJSON(&req); err != nil || req.Provider == "" || req.Tool == "" {
		fail(c, CodeBadRequest, "参数错误：provider/tool 必填")
		return
	}
	// _out 是 CLI 内部约定（仅 cmd/voxbox 显式设置产物输出路径），
	// Web 用户不可通过 params 透传，否则 TTS Tool 会把产物写到服务器任意路径。
	if req.Params == nil {
		req.Params = map[string]any{}
	}
	delete(req.Params, "_out")
	// 产物/文件输入通道三选一：artifact_input（单产物）、artifact_inputs（多产物按序）、
	// file_ids（上传文件）——任何两个同传都拒绝。互斥在产物存在性校验之前执行。
	channels := 0
	if req.ArtifactInput != "" {
		channels++
	}
	if len(req.ArtifactInputs) > 0 {
		channels++
	}
	if len(req.FileIDs) > 0 {
		channels++
	}
	if channels > 1 {
		fail(c, CodeBadRequest, "artifact_input、artifact_inputs、file_ids 只能提供其一")
		return
	}
	// artifact_input 单值归一为长度 1 的 artifact_inputs，与数组共用同一条解析链：
	// GetArtifact + 越权同报不存在（不泄露他人产物存在性）+ artifactAbsPath（IsAbs/jail 防御）。
	artifactIDs := req.ArtifactInputs
	if req.ArtifactInput != "" {
		artifactIDs = []string{req.ArtifactInput}
	}
	var files map[string]string
	switch {
	case len(artifactIDs) > 0:
		f, err := s.resolveArtifactInputs(artifactIDs, p)
		if err != nil {
			switch {
			case errors.Is(err, errArtifactNotFound):
				fail(c, CodeNotFound, "产物不存在")
			case errors.Is(err, errArtifactMissing):
				fail(c, CodeNotFound, "产物文件缺失")
			default:
				failErr(c, err)
			}
			return
		}
		files = f
	case len(req.FileIDs) > 0:
		// file_ids → 上传文件绝对路径（key 固定 "audio"），交给 Engine 走本地文件通道。
		f, err := fileIDsToFiles(s.uploadSearchDirs(p.ID, p), req.FileIDs)
		if err != nil {
			if errors.Is(err, errInvalidFileID) {
				fail(c, CodeBadRequest, err.Error())
			} else {
				fail(c, CodeNotFound, err.Error())
			}
			return
		}
		files = f
	}
	id, err := s.svc.Engine().SubmitUserRef(p.ID, req.Provider, req.Tool, req.Params, files, &task.InputRef{
		FileIDs:        req.FileIDs,
		ArtifactInput:  req.ArtifactInput,
		ArtifactInputs: req.ArtifactInputs,
	})
	if err != nil {
		failErr(c, err)
		return
	}
	ok(c, gin.H{"task_id": id})
}

func (s *Server) listTasks(c *gin.Context) {
	p := principalFrom(c)
	uid := p.ID
	if p.IsAdmin() {
		uid = "" // admin 全量可见（含无主历史任务）
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))
	if page < 1 {
		page = 1
	}
	items, total, err := s.svc.DB().ListTasks(c.Query("provider"), nil, size, (page-1)*size, uid)
	if err != nil {
		failErr(c, err)
		return
	}
	dtos := make([]taskDTO, 0, len(items))
	for _, t := range items {
		dtos = append(dtos, toTaskDTO(t))
	}
	ok(c, gin.H{"items": dtos, "total": total})
}

func (s *Server) getTask(c *gin.Context) {
	t, err := s.svc.DB().GetTask(c.Param("id"))
	if err == store.ErrNotFound {
		fail(c, CodeNotFound, "任务不存在")
		return
	} else if err != nil {
		failErr(c, err)
		return
	}
	p := principalFrom(c)
	if !canAccessTask(t.UserID, p) {
		fail(c, CodeNotFound, "任务不存在")
		return
	}
	arts, err := s.svc.DB().ListArtifacts(t.ID)
	if err != nil {
		failErr(c, err)
		return
	}
	adtos := make([]artifactDTO, 0, len(arts))
	for _, a := range arts {
		adtos = append(adtos, toArtifactDTO(a))
	}
	ok(c, gin.H{"task": toTaskDTO(*t), "artifacts": adtos})
}

func (s *Server) deleteTask(c *gin.Context) {
	t, err := s.svc.DB().GetTask(c.Param("id"))
	if err == store.ErrNotFound {
		fail(c, CodeNotFound, "任务不存在")
		return
	} else if err != nil {
		failErr(c, err)
		return
	}
	if !canAccessTask(t.UserID, principalFrom(c)) {
		fail(c, CodeNotFound, "任务不存在")
		return
	}
	if err := s.svc.DB().DeleteTask(c.Param("id")); err != nil {
		failErr(c, err)
		return
	}
	ok(c, gin.H{"ok": true})
}

func (s *Server) cancelTask(c *gin.Context) {
	t, err := s.svc.DB().GetTask(c.Param("id"))
	if err == store.ErrNotFound {
		fail(c, CodeNotFound, "任务不存在")
		return
	} else if err != nil {
		failErr(c, err)
		return
	}
	if !canAccessTask(t.UserID, principalFrom(c)) {
		fail(c, CodeNotFound, "任务不存在")
		return
	}
	if err := s.svc.Engine().Cancel(c.Param("id")); err != nil {
		fail(c, CodeBadRequest, err.Error())
		return
	}
	ok(c, gin.H{"ok": true})
}

// rerunTask 克隆原任务重新提交：params 原样回传（引擎重新校验），输入引用按
// Task.Input 重新解析——上传文件/产物可能已被清理，缺失时在提交前明确报错。
func (s *Server) rerunTask(c *gin.Context) {
	old, err := s.svc.DB().GetTask(c.Param("id"))
	if err == store.ErrNotFound {
		fail(c, CodeNotFound, "任务不存在")
		return
	} else if err != nil {
		failErr(c, err)
		return
	}
	p := principalFrom(c)
	if !canAccessTask(old.UserID, p) {
		fail(c, CodeNotFound, "任务不存在")
		return
	}
	if _, ok := s.svc.Registry().Get(old.Provider, old.Tool); !ok {
		fail(c, CodeBadRequest, fmt.Sprintf("工具 %s.%s 不可用（凭证未配置或已下线），无法重跑", old.Provider, old.Tool))
		return
	}
	var params map[string]any
	if err := json.Unmarshal([]byte(old.Params), &params); err != nil {
		fail(c, CodeBadRequest, "原任务参数已损坏，无法重跑")
		return
	}
	if params == nil {
		params = map[string]any{}
	}
	delete(params, "_out")
	var ref task.InputRef
	if old.Input != "" {
		_ = json.Unmarshal([]byte(old.Input), &ref)
	}
	var files map[string]string
	switch {
	case ref.ArtifactInput != "" || len(ref.ArtifactInputs) > 0:
		// 单值归一为长度 1 的数组，与多产物共用解析链（p 为当前重跑者：本人或 admin，
		// 产物归属校验据此放行；与上方 file_ids 按原任务所有者目录解析的所有者语义各自独立）。
		ids := ref.ArtifactInputs
		if ref.ArtifactInput != "" {
			ids = []string{ref.ArtifactInput}
		}
		f, err := s.resolveArtifactInputs(ids, p)
		if err != nil {
			switch {
			case errors.Is(err, errArtifactNotFound):
				fail(c, CodeNotFound, "原输入产物已被删除，无法重跑")
			case errors.Is(err, errArtifactMissing):
				fail(c, CodeNotFound, "原输入产物文件缺失，无法重跑")
			default:
				failErr(c, err)
			}
			return
		}
		files = f
	case len(ref.FileIDs) > 0:
		// 按原任务所有者的目录解析（admin 重跑他人任务时输入文件属于原所有者）
		f, err := fileIDsToFiles(s.uploadSearchDirs(old.UserID, p), ref.FileIDs)
		if err != nil {
			fail(c, CodeNotFound, "原上传文件已不存在，无法重跑："+err.Error())
			return
		}
		files = f
	}
	id, err := s.svc.Engine().SubmitUserRef(old.UserID, old.Provider, old.Tool, params, files, &ref)
	if err != nil {
		failErr(c, err)
		return
	}
	ok(c, gin.H{"task_id": id})
}

// resolveArtifactInputs 产物 id 列表 → 本地绝对路径，create（artifact_input/artifact_inputs）
// 与 rerun 两条链共用：逐个 GetArtifact（不存在与越权同报 not found，不泄露他人产物存在性）、
// artifactAbsPath（IsAbs/jail 防御）。key 约定与 fileIDsToFiles 一致：第 1 个 "audio"，
// 第 2 个起 "audio2"、"audio3"…（单产物通道即长度 1 的特例，mix 等多输入工具自校验个数）。
// 校验失败返回 errArtifactNotFound/errArtifactMissing 哨兵由调用方翻译各自文案；
// 其余 DB 错误原样上抛（failErr）。
var (
	errArtifactNotFound = errors.New("产物不存在")
	errArtifactMissing  = errors.New("产物文件缺失")
)

func (s *Server) resolveArtifactInputs(ids []string, p *Principal) (map[string]string, error) {
	files := make(map[string]string, len(ids))
	for i, id := range ids {
		a, err := s.svc.DB().GetArtifact(id)
		if err == store.ErrNotFound {
			return nil, fmt.Errorf("%w: %s", errArtifactNotFound, id)
		} else if err != nil {
			return nil, err
		}
		if !canAccessArtifact(a.UserID, p) {
			return nil, fmt.Errorf("%w: %s", errArtifactNotFound, id)
		}
		abs, err := s.artifactAbsPath(a.Path)
		if err != nil {
			return nil, fmt.Errorf("%w: %s", errArtifactMissing, id)
		}
		key := "audio"
		if i > 0 {
			key = fmt.Sprintf("audio%d", i+1)
		}
		files[key] = abs
	}
	return files, nil
}
