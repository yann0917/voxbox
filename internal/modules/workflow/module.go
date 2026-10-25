// Package workflow 工作流模块：用户编排图的 CRUD、内置模板库与服务端执行。
// 每个节点就是一个普通任务（历史/WS/重跑全复用任务引擎），本模块只承担
// 编排层：图校验、入度调度执行器（internal/workflow）与 HTTP 面。
package workflow

import (
	"encoding/json"
	"errors"
	"strconv"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/yann0917/voxbox/internal/httpx"
	"github.com/yann0917/voxbox/internal/module"
	"github.com/yann0917/voxbox/internal/service"
	"github.com/yann0917/voxbox/internal/store"
	"github.com/yann0917/voxbox/internal/workflow"
)

// Module 工作流功能模块。
func Module() module.Module { return mod{} }

type mod struct{}

func (mod) ID() string { return "workflow" }

func (mod) Register(m *module.Mount) {
	h := &handlers{svc: m.Svc}
	g := m.API.Group("/workflows")
	g.GET("", h.list)
	g.POST("", h.create)
	g.GET("/:id", h.get)
	g.PUT("/:id", h.update)
	g.DELETE("/:id", h.delete)
	g.POST("/:id/run", h.runWorkflow)
	// 模板快捷跑独立前缀（gin 路由树 :id 与静态段 templates 不能同层共存）
	m.API.POST("/workflow-templates/:key/run", h.runTemplate)

	rg := m.API.Group("/workflow-runs")
	rg.GET("", h.listRuns)
	rg.GET("/:id", h.getRun)
	rg.POST("/:id/cancel", h.cancelRun)
}

// handlers 执行器惰性构造：Register 时机早于 StartEngine（桌面测试只挂路由
// 不起引擎），首次真正用到执行器的请求才构造。
type handlers struct {
	svc       *service.Service
	once      sync.Once
	runnerRef *workflow.Runner
}

func (h *handlers) runner() *workflow.Runner {
	h.once.Do(func() {
		h.runnerRef = workflow.NewRunner(h.svc.DB(), h.svc.Engine(), h.svc.Registry(), h.svc.Config().DataDir)
	})
	return h.runnerRef
}

// ---- DTO ----

// workflowDTO 用户编排（graph 已解析，前端免二次 parse）。Key 仅内置模板非空。
type workflowDTO struct {
	ID          uint           `json:"id"`
	Key         string         `json:"key,omitempty"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Graph       workflow.Graph `json:"graph"`
	CreatedAt   string         `json:"created_at"`
	UpdatedAt   string         `json:"updated_at"`
}

// runDTO 运行详情（nodes 已解析）。
type runDTO struct {
	ID          uint               `json:"id"`
	WorkflowID  uint               `json:"workflow_id"`
	WorkflowKey string             `json:"workflow_key"`
	Name        string             `json:"name"`
	Status      string             `json:"status"`
	Inputs      json.RawMessage    `json:"inputs"`
	Graph       *workflow.Graph    `json:"graph,omitempty"`
	Nodes       []workflow.NodeRun `json:"nodes"`
	Error       string             `json:"error"`
	CreatedAt   string             `json:"created_at"`
	UpdatedAt   string             `json:"updated_at"`
}

// ---- 查询面 ----

func (h handlers) list(c *gin.Context) {
	p := httpx.PrincipalFrom(c)
	mine, err := h.svc.DB().ListWorkflows(p.ID)
	if err != nil {
		httpx.Fail(c, httpx.CodeTaskFailed, err.Error())
		return
	}
	items := make([]workflowDTO, 0, len(mine))
	for _, w := range mine {
		items = append(items, toWorkflowDTO(w))
	}
	templates := make([]workflowDTO, 0, len(Templates))
	for _, t := range Templates {
		templates = append(templates, workflowDTO{
			ID: 0, Key: t.Key, Name: t.Name, Description: t.Description,
			Graph: t.Graph,
		})
	}
	httpx.OK(c, gin.H{"items": items, "templates": templates})
}

func (h handlers) get(c *gin.Context) {
	p := httpx.PrincipalFrom(c)
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		httpx.Fail(c, httpx.CodeBadRequest, "非法 id")
		return
	}
	w, err := h.svc.DB().GetWorkflow(uint(id), p.ID)
	if errors.Is(err, store.ErrNotFound) {
		httpx.Fail(c, httpx.CodeNotFound, "工作流不存在")
		return
	} else if err != nil {
		httpx.Fail(c, httpx.CodeTaskFailed, err.Error())
		return
	}
	httpx.OK(c, toWorkflowDTO(*w))
}

// ---- 保存/更新/删除 ----

type saveReq struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Graph       workflow.Graph `json:"graph"`
}

func (h handlers) create(c *gin.Context) {
	p := httpx.PrincipalFrom(c)
	var req saveReq
	if err := c.ShouldBindJSON(&req); err != nil || req.Name == "" {
		httpx.Fail(c, httpx.CodeBadRequest, "参数错误：name 与 graph 必填")
		return
	}
	if err := req.Graph.ValidateWithTools(h.svc.Registry()); err != nil {
		httpx.Fail(c, httpx.CodeBadRequest, err.Error())
		return
	}
	raw, _ := json.Marshal(req.Graph)
	w := &store.Workflow{UserID: p.ID, Name: req.Name, Description: req.Description, Graph: string(raw)}
	if err := h.svc.DB().CreateWorkflow(w); err != nil {
		httpx.Fail(c, httpx.CodeTaskFailed, err.Error())
		return
	}
	httpx.OK(c, toWorkflowDTO(*w))
}

func (h handlers) update(c *gin.Context) {
	p := httpx.PrincipalFrom(c)
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		httpx.Fail(c, httpx.CodeBadRequest, "非法 id")
		return
	}
	var req saveReq
	if err := c.ShouldBindJSON(&req); err != nil || req.Name == "" {
		httpx.Fail(c, httpx.CodeBadRequest, "参数错误：name 与 graph 必填")
		return
	}
	if err := req.Graph.ValidateWithTools(h.svc.Registry()); err != nil {
		httpx.Fail(c, httpx.CodeBadRequest, err.Error())
		return
	}
	raw, _ := json.Marshal(req.Graph)
	w := &store.Workflow{ID: uint(id), UserID: p.ID, Name: req.Name, Description: req.Description, Graph: string(raw)}
	if err := h.svc.DB().UpdateWorkflow(w); err != nil {
		httpx.Fail(c, httpx.CodeNotFound, "工作流不存在")
		return
	}
	fresh, _ := h.svc.DB().GetWorkflow(uint(id), p.ID)
	httpx.OK(c, toWorkflowDTO(*fresh))
}

func (h handlers) delete(c *gin.Context) {
	p := httpx.PrincipalFrom(c)
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		httpx.Fail(c, httpx.CodeBadRequest, "非法 id")
		return
	}
	if err := h.svc.DB().DeleteWorkflow(uint(id), p.ID); err != nil {
		httpx.Fail(c, httpx.CodeNotFound, "工作流不存在")
		return
	}
	httpx.OK(c, gin.H{"ok": true})
}

// ---- 执行面 ----

type runReq struct {
	Inputs map[string]any `json:"inputs"`
}

func (h handlers) runWorkflow(c *gin.Context) {
	p := httpx.PrincipalFrom(c)
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		httpx.Fail(c, httpx.CodeBadRequest, "非法 id")
		return
	}
	w, err := h.svc.DB().GetWorkflow(uint(id), p.ID)
	if errors.Is(err, store.ErrNotFound) {
		httpx.Fail(c, httpx.CodeNotFound, "工作流不存在")
		return
	} else if err != nil {
		httpx.Fail(c, httpx.CodeTaskFailed, err.Error())
		return
	}
	var g workflow.Graph
	if err := json.Unmarshal([]byte(w.Graph), &g); err != nil {
		httpx.Fail(c, httpx.CodeBadRequest, "工作流图已损坏，无法运行")
		return
	}
	var req runReq
	_ = c.ShouldBindJSON(&req)
	run, err := h.runner().Start(w.Name, &g, req.Inputs, p.ID, w.ID, "")
	if err != nil {
		httpx.Fail(c, httpx.CodeTaskFailed, err.Error())
		return
	}
	httpx.OK(c, toRunDTO(*run))
}

func (h handlers) runTemplate(c *gin.Context) {
	p := httpx.PrincipalFrom(c)
	t, ok := TemplateByKey(c.Param("key"))
	if !ok {
		httpx.Fail(c, httpx.CodeNotFound, "模板不存在")
		return
	}
	var req runReq
	_ = c.ShouldBindJSON(&req)
	run, err := h.runner().Start(t.Name, &t.Graph, req.Inputs, p.ID, 0, t.Key)
	if err != nil {
		httpx.Fail(c, httpx.CodeTaskFailed, err.Error())
		return
	}
	httpx.OK(c, toRunDTO(*run))
}

// ---- 运行记录 ----

func (h handlers) listRuns(c *gin.Context) {
	p := httpx.PrincipalFrom(c)
	var wfID uint
	if v := c.Query("workflow_id"); v != "" {
		id, err := strconv.ParseUint(v, 10, 32)
		if err == nil {
			wfID = uint(id)
		}
	}
	page, size := httpx.PageParams(c)
	items, total, err := h.svc.DB().ListWorkflowRuns(p.ID, wfID, size, (page-1)*size)
	if err != nil {
		httpx.Fail(c, httpx.CodeTaskFailed, err.Error())
		return
	}
	dtos := make([]runDTO, 0, len(items))
	for _, r := range items {
		dtos = append(dtos, toRunDTO(r))
	}
	httpx.OK(c, gin.H{"items": dtos, "total": total})
}

func (h handlers) getRun(c *gin.Context) {
	p := httpx.PrincipalFrom(c)
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		httpx.Fail(c, httpx.CodeBadRequest, "非法 id")
		return
	}
	r, err := h.svc.DB().GetWorkflowRun(uint(id), p.ID)
	if errors.Is(err, store.ErrNotFound) {
		httpx.Fail(c, httpx.CodeNotFound, "运行记录不存在")
		return
	} else if err != nil {
		httpx.Fail(c, httpx.CodeTaskFailed, err.Error())
		return
	}
	httpx.OK(c, toRunDTO(*r))
}

func (h handlers) cancelRun(c *gin.Context) {
	p := httpx.PrincipalFrom(c)
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		httpx.Fail(c, httpx.CodeBadRequest, "非法 id")
		return
	}
	r, err := h.svc.DB().GetWorkflowRun(uint(id), p.ID)
	if errors.Is(err, store.ErrNotFound) {
		httpx.Fail(c, httpx.CodeNotFound, "运行记录不存在")
		return
	} else if err != nil {
		httpx.Fail(c, httpx.CodeTaskFailed, err.Error())
		return
	}
	if err := h.runner().Cancel(r.ID); err != nil {
		httpx.Fail(c, httpx.CodeBadRequest, err.Error())
		return
	}
	httpx.OK(c, gin.H{"ok": true})
}

// ---- 转换 ----

func toWorkflowDTO(w store.Workflow) workflowDTO {
	var g workflow.Graph
	_ = json.Unmarshal([]byte(w.Graph), &g)
	return workflowDTO{
		ID: w.ID, Name: w.Name, Description: w.Description, Graph: g,
		CreatedAt: w.CreatedAt.Format("2006-01-02 15:04"), UpdatedAt: w.UpdatedAt.Format("2006-01-02 15:04"),
	}
}

func toRunDTO(r store.WorkflowRun) runDTO {
	var inputs json.RawMessage
	if r.Inputs != "" {
		inputs = json.RawMessage(r.Inputs)
	}
	var nodes []workflow.NodeRun
	_ = json.Unmarshal([]byte(r.Nodes), &nodes)
	var graph *workflow.Graph
	if r.Graph != "" {
		graph = &workflow.Graph{}
		_ = json.Unmarshal([]byte(r.Graph), graph)
	}
	return runDTO{
		ID: r.ID, WorkflowID: r.WorkflowID, WorkflowKey: r.WorkflowKey,
		Name: r.Name, Status: r.Status, Inputs: inputs, Graph: graph, Nodes: nodes,
		Error:     r.Error,
		CreatedAt: r.CreatedAt.Format("2006-01-02 15:04:05"), UpdatedAt: r.UpdatedAt.Format("2006-01-02 15:04:05"),
	}
}
