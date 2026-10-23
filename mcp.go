package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"strings"
	"sync"

	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

// MCP (Model Context Protocol) 服务端实现。
//
// 传输层见 mcp_stdio.go（newline-delimited JSON）与 mcp_http.go（Streamable HTTP）。
// 本文件只负责与传输无关的协议逻辑：会话、方法分发、工具定义与参数校验。
const (
	mcpProtocolVersion = "2025-06-18"

	toolSplit   = "split"
	toolMerge   = "merge"
	toolVersion = "version"
)

// supportedProtocolVersions 按从新到旧排列；客户端请求其中之一则回显，
// 否则回复服务端最新版本（协议要求服务端给出自己支持的版本）。
var supportedProtocolVersions = []string{mcpProtocolVersion, "2025-03-26", "2024-11-05"}

func isSupportedProtocolVersion(v string) bool {
	for _, s := range supportedProtocolVersions {
		if s == v {
			return true
		}
	}
	return false
}

func mcpServerInfo() map[string]any {
	return map[string]any{
		"name":    "pdftool",
		"version": Version,
		"title":   "PDFTool",
	}
}

// session 表示一个 MCP 会话：stdio 全程一个，HTTP 每次 initialize 一个。
type session struct {
	id string

	mu       sync.Mutex
	inflight map[string]context.CancelFunc

	// notify 是服务端主动推送（进度等）的出口：stdio 直接写 stdout，
	// HTTP 推到 SSE 通道；为 nil 时丢弃。
	notify func(msg []byte)

	protocolVersion string
}

func newSession(id string) *session {
	return &session{id: id, inflight: map[string]context.CancelFunc{}}
}

func (s *session) push(msg []byte) {
	if msg == nil || s.notify == nil {
		return
	}
	s.notify(msg)
}

func (s *session) track(id string, cancel context.CancelFunc) {
	s.mu.Lock()
	s.inflight[id] = cancel
	s.mu.Unlock()
}

func (s *session) untrack(id string) {
	s.mu.Lock()
	delete(s.inflight, id)
	s.mu.Unlock()
}

func (s *session) cancelRequest(id string) bool {
	s.mu.Lock()
	cancel, ok := s.inflight[id]
	s.mu.Unlock()
	if ok {
		cancel()
	}
	return ok
}

// mcpServer 持有会话表（HTTP 用；stdio 只有一个会话）。
type mcpServer struct {
	mu       sync.Mutex
	sessions map[string]*session
}

func newMCPServer() *mcpServer {
	return &mcpServer{sessions: map[string]*session{}}
}

func (s *mcpServer) addSession(sess *session) {
	s.mu.Lock()
	s.sessions[sess.id] = sess
	s.mu.Unlock()
}

func (s *mcpServer) session(id string) (*session, bool) {
	s.mu.Lock()
	sess, ok := s.sessions[id]
	s.mu.Unlock()
	return sess, ok
}

func (s *mcpServer) removeSession(id string) bool {
	s.mu.Lock()
	_, ok := s.sessions[id]
	delete(s.sessions, id)
	s.mu.Unlock()
	return ok
}

func mustMarshal(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte(`{"jsonrpc":"2.0","id":null,"error":{"code":-32603,"message":"序列化响应失败"}}`)
	}
	return b
}

// handleMessage 处理单条或批量（JSON array）消息，返回需要回写的响应；
// 纯通知返回 nil。
func (s *mcpServer) handleMessage(ctx context.Context, sess *session, raw []byte) []byte {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil
	}

	if trimmed[0] == '[' {
		var items []json.RawMessage
		if err := json.Unmarshal(trimmed, &items); err != nil {
			return mustMarshal(newRPCError(nil, rpcParseError, "批处理 JSON 解析失败: "+err.Error(), nil))
		}
		if len(items) == 0 {
			return mustMarshal(newRPCError(nil, rpcInvalidRequest, "空的批处理请求", nil))
		}
		out := make([]json.RawMessage, 0, len(items))
		for _, item := range items {
			if b := s.handleOne(ctx, sess, item, true); b != nil {
				out = append(out, b)
			}
		}
		if len(out) == 0 {
			return nil
		}
		return mustMarshal(out)
	}

	return s.handleOne(ctx, sess, trimmed, false)
}

// handleOne 处理一条 JSON-RPC 消息（batch 中的元素或单条消息）。
func (s *mcpServer) handleOne(ctx context.Context, sess *session, raw []byte, inBatch bool) []byte {
	var req rpcRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		code := rpcParseError
		if inBatch {
			code = rpcInvalidRequest
		}
		return mustMarshal(newRPCError(nil, code, "JSON 解析失败: "+err.Error(), nil))
	}

	if strings.TrimSpace(req.Method) == "" {
		return mustMarshal(newRPCError(validID(req.ID), rpcInvalidRequest, "缺少 method 字段", nil))
	}

	// 通知（无 id）只做副作用，不回复
	if !hasID(&req) {
		s.handleNotification(ctx, sess, &req)
		return nil
	}

	resp := s.dispatch(ctx, sess, &req)
	if resp == nil {
		// 被取消的请求按 MCP 规范不再回复
		return nil
	}
	return mustMarshal(resp)
}

// validID 保证回写的 id 是合法 JSON；无法解析时用 null。
func validID(id json.RawMessage) json.RawMessage {
	if len(id) == 0 || !json.Valid(id) {
		return nil
	}
	return id
}

func (s *mcpServer) handleNotification(ctx context.Context, sess *session, req *rpcRequest) {
	switch req.Method {
	case "notifications/initialized":
		// 握手完成，无需处理

	case "notifications/cancelled":
		var p struct {
			RequestID json.RawMessage `json:"requestId"`
			Reason    string          `json:"reason"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return
		}
		if len(p.RequestID) > 0 {
			sess.cancelRequest(canonicalID(p.RequestID))
		}

	default:
		// 未知通知按规范静默忽略
	}
}

func (s *mcpServer) dispatch(ctx context.Context, sess *session, req *rpcRequest) *rpcResponse {
	switch req.Method {
	case "initialize":
		return s.handleInitialize(sess, req)

	case "ping":
		return &rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{}}

	case "tools/list":
		specs := allToolSpecs()
		tools := make([]map[string]any, 0, len(specs))
		for _, spec := range specs {
			tools = append(tools, map[string]any{
				"name":        spec.name,
				"description": spec.description,
				"inputSchema": spec.inputSchema(),
			})
		}
		return &rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"tools": tools}}

	case "tools/call":
		return s.handleToolCall(ctx, sess, req)

	// 本服务器没有资源与提示词，但按列表语义返回空集合而不是报错，
	// 避免客户端探测能力时看到 -32601。
	case "resources/list":
		return &rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"resources": []any{}}}

	case "resources/templates/list":
		return &rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"resourceTemplates": []any{}}}

	case "prompts/list":
		return &rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"prompts": []any{}}}

	default:
		return &rpcResponse{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{
			Code:    rpcMethodNotFound,
			Message: "未知方法: " + req.Method,
		}}
	}
}

func (s *mcpServer) handleInitialize(sess *session, req *rpcRequest) *rpcResponse {
	var params struct {
		ProtocolVersion string          `json:"protocolVersion"`
		Capabilities    json.RawMessage `json:"capabilities"`
		ClientInfo      json.RawMessage `json:"clientInfo"`
	}
	_ = json.Unmarshal(req.Params, &params)

	negotiated := mcpProtocolVersion
	if isSupportedProtocolVersion(params.ProtocolVersion) {
		negotiated = params.ProtocolVersion
	}
	sess.protocolVersion = negotiated

	return &rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{
		"protocolVersion": negotiated,
		"capabilities": map[string]any{
			"tools": map[string]any{"listChanged": false},
		},
		"serverInfo":   mcpServerInfo(),
		"instructions": "PDF 拆分/合并工具。拆分与合并均支持进度通知（notifications/progress）与取消（notifications/cancelled）；工具参数中的文件路径建议使用绝对路径。",
	}}
}

// -------------------------
// 工具调用
// -------------------------

type toolCallParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
	Meta      struct {
		ProgressToken json.RawMessage `json:"progressToken"`
	} `json:"_meta"`
}

func (s *mcpServer) handleToolCall(ctx context.Context, sess *session, req *rpcRequest) *rpcResponse {
	var p toolCallParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return &rpcResponse{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{
			Code: rpcInvalidParams, Message: "tools/call 参数解析失败: " + err.Error(),
		}}
	}

	spec := findToolSpec(p.Name)
	if spec == nil {
		specs := allToolSpecs()
		names := make([]string, 0, len(specs))
		for _, sp := range specs {
			names = append(names, sp.name)
		}
		return &rpcResponse{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{
			Code:    rpcInvalidParams,
			Message: "未知工具: " + p.Name,
			Data:    map[string]any{"availableTools": names},
		}}
	}

	if errs := validateToolArgs(spec, p.Arguments); len(errs) > 0 {
		return &rpcResponse{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{
			Code:    rpcInvalidParams,
			Message: "参数校验失败: " + strings.Join(errs, "；"),
			Data:    map[string]any{"tool": p.Name, "errors": errs},
		}}
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	key := canonicalID(req.ID)
	sess.track(key, cancel)
	defer sess.untrack(key)

	progress := sess.progressReporter(p.Meta.ProgressToken)

	type outcome struct {
		text string
		err  error
	}
	done := make(chan outcome, 1)
	go func() {
		text, err := executeTool(runCtx, p.Name, p.Arguments, progress)
		done <- outcome{text: text, err: err}
	}()

	select {
	case o := <-done:
		if o.err != nil {
			if errors.Is(o.err, context.Canceled) {
				return nil // 已取消：规范要求不再回复
			}
			return &rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{
				"content": []any{map[string]any{"type": "text", "text": o.err.Error()}},
				"isError": true,
			}}
		}
		return &rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{
			"content": []any{map[string]any{"type": "text", "text": o.text}},
			"isError": false,
		}}
	case <-runCtx.Done():
		return nil // 取消：不回复
	}
}

// progressReporter 把工具内部进度转成 notifications/progress。
// 没有 progressToken 时返回 nil（工具就不上报）。
func (sess *session) progressReporter(token json.RawMessage) ProgressFunc {
	if len(token) == 0 || string(token) == "null" {
		return nil
	}
	return func(ctx context.Context, done, total int, message string) error {
		params := map[string]any{
			"progressToken": token,
			"progress":      done,
			"total":         total,
		}
		if message != "" {
			params["message"] = message
		}
		sess.push(encodeNotification("notifications/progress", params))
		return ctx.Err()
	}
}

// -------------------------
// 工具定义与参数校验
// -------------------------

type argSpec struct {
	name     string
	jsonType string // string | integer | array
	desc     string
	required bool
	enum     []int
	minimum  *int
	items    string
}

type toolSpec struct {
	name        string
	description string
	args        []argSpec
	// crossValidate 处理单字段类型之外的整体约束（如三选一）。
	crossValidate func(args map[string]any) []string
}

func allToolSpecs() []*toolSpec {
	minSpan := 1
	return []*toolSpec{
		{
			name: toolSplit,
			description: "拆分 PDF 文件。支持按每 N 页拆分(span)、按分割点拆分(pages, 如 \"3,6\" 表示第 3、6 页为分割点)" +
				"、导出指定页(export, 如 \"1,3,5\")，三种方式三选一，可附带旋转(rotate: 0/90/180/270)。",
			args: []argSpec{
				{name: "in", jsonType: "string", desc: "输入 PDF 文件路径（建议绝对路径）", required: true},
				{name: "outdir", jsonType: "string", desc: "输出目录（不存在会自动创建）", required: true},
				{name: "span", jsonType: "integer", desc: "每多少页拆一份，须 >= 1", minimum: &minSpan},
				{name: "pages", jsonType: "string", desc: "分割点列表，如 \"3,6\"，与 span/export 三选一"},
				{name: "export", jsonType: "string", desc: "导出指定页，如 \"1,3,5\"，与 span/pages 三选一"},
				{name: "rotate", jsonType: "integer", desc: "旋转角度", enum: []int{0, 90, 180, 270}},
			},
			crossValidate: validateSplitModes,
		},
		{
			name:        toolMerge,
			description: "合并 PDF 文件。指定 dir 合并目录内所有 PDF，或指定 files 文件列表（二选一），可附带旋转(rotate: 0/90/180/270)。",
			args: []argSpec{
				{name: "out", jsonType: "string", desc: "输出 PDF 文件路径（建议绝对路径）", required: true},
				{name: "dir", jsonType: "string", desc: "合并目录内所有 PDF，与 files 二选一"},
				{name: "files", jsonType: "array", items: "string", desc: "待合并的 PDF 文件列表，按顺序合并"},
				{name: "rotate", jsonType: "integer", desc: "旋转角度", enum: []int{0, 90, 180, 270}},
			},
			crossValidate: validateMergeSources,
		},
		{
			name:        toolVersion,
			description: "显示 PDFTool 版本信息。",
			args:        nil,
		},
	}
}

func findToolSpec(name string) *toolSpec {
	for _, s := range allToolSpecs() {
		if s.name == name {
			return s
		}
	}
	return nil
}

func (spec *toolSpec) inputSchema() map[string]any {
	props := map[string]any{}
	required := make([]string, 0, len(spec.args))
	for _, a := range spec.args {
		p := map[string]any{"type": a.jsonType}
		if a.desc != "" {
			p["description"] = a.desc
		}
		if len(a.enum) > 0 {
			p["enum"] = a.enum
		}
		if a.minimum != nil {
			p["minimum"] = *a.minimum
		}
		if a.items != "" {
			p["items"] = map[string]any{"type": a.items}
		}
		props[a.name] = p
		if a.required {
			required = append(required, a.name)
		}
	}
	schema := map[string]any{
		"type":                 "object",
		"properties":           props,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func validateSplitModes(args map[string]any) []string {
	var errs []string
	n := 0
	if v, ok := args["span"]; ok && v != nil {
		if f, ok := toFloat(v); ok && f > 0 {
			n++
		}
	}
	if s, ok := args["pages"].(string); ok && strings.TrimSpace(s) != "" {
		n++
	}
	if s, ok := args["export"].(string); ok && strings.TrimSpace(s) != "" {
		n++
	}
	if n == 0 {
		errs = append(errs, "必须指定 span、pages、export 之一")
	}
	if n > 1 {
		errs = append(errs, "span、pages、export 只能指定一个")
	}
	return errs
}

func validateMergeSources(args map[string]any) []string {
	var errs []string
	hasDir := false
	if s, ok := args["dir"].(string); ok && strings.TrimSpace(s) != "" {
		hasDir = true
	}
	hasFiles := false
	if v, ok := args["files"].([]any); ok && len(v) > 0 {
		hasFiles = true
	}
	if !hasDir && !hasFiles {
		errs = append(errs, "必须指定 dir 或 files 之一")
	}
	if hasDir && hasFiles {
		errs = append(errs, "dir 与 files 只能指定一个")
	}
	return errs
}

// validateToolArgs 做 JSON Schema 子集的强校验：类型、必填、枚举、下界、
// 未知字段与整体约束。返回的错误文本会直接回给客户端（-32602）。
func validateToolArgs(spec *toolSpec, args map[string]any) []string {
	if args == nil {
		args = map[string]any{}
	}
	known := make(map[string]argSpec, len(spec.args))
	for _, a := range spec.args {
		known[a.name] = a
	}

	var errs []string
	for k := range args {
		if _, ok := known[k]; !ok {
			errs = append(errs, fmt.Sprintf("未知参数 %q", k))
		}
	}

	for _, a := range spec.args {
		v, ok := args[a.name]
		if !ok || v == nil {
			if a.required {
				errs = append(errs, fmt.Sprintf("缺少必填参数 %q", a.name))
			}
			continue
		}
		switch a.jsonType {
		case "string":
			s, ok := v.(string)
			if !ok {
				errs = append(errs, fmt.Sprintf("参数 %q 必须是字符串", a.name))
				continue
			}
			if a.required && strings.TrimSpace(s) == "" {
				errs = append(errs, fmt.Sprintf("参数 %q 不能为空字符串", a.name))
			}
		case "integer":
			f, ok := toFloat(v)
			if !ok || f != math.Trunc(f) {
				errs = append(errs, fmt.Sprintf("参数 %q 必须是整数", a.name))
				continue
			}
			i := int(f)
			if a.minimum != nil && i < *a.minimum {
				errs = append(errs, fmt.Sprintf("参数 %q 必须 >= %d", a.name, *a.minimum))
			}
			if len(a.enum) > 0 && !intInSlice(i, a.enum) {
				errs = append(errs, fmt.Sprintf("参数 %q 只能取 %v", a.name, a.enum))
			}
		case "array":
			arr, ok := v.([]any)
			if !ok {
				errs = append(errs, fmt.Sprintf("参数 %q 必须是字符串数组", a.name))
				continue
			}
			for i, item := range arr {
				if _, ok := item.(string); !ok {
					errs = append(errs, fmt.Sprintf("参数 %q 的第 %d 项必须是字符串", a.name, i+1))
				}
			}
		}
	}

	if spec.crossValidate != nil {
		errs = append(errs, spec.crossValidate(args)...)
	}
	return errs
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

func intInSlice(v int, list []int) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// -------------------------
// 工具执行
// -------------------------

// pdfSem 把 pdfcpu 的调用串行化：pdfcpu 在首次使用时会写全局默认配置
// （package-level loadedDefaultConfig + 配置文件），并发调用会触发数据竞争。
// 用容量为 1 的信号量既能避免竞争，又能让等待中的请求被取消唤醒。
var pdfSem = make(chan struct{}, 1)

func acquirePDF(ctx context.Context) error {
	select {
	case pdfSem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func releasePDF() { <-pdfSem }

// warmUpPDFCPU 在开始服务前单线程触发一次默认配置加载，
// 避免服务首启时并发请求同时写配置文件。
func warmUpPDFCPU() {
	_ = model.NewDefaultConfiguration()
}

func executeTool(ctx context.Context, name string, args map[string]any, progress ProgressFunc) (string, error) {
	switch name {
	case toolSplit:
		if err := acquirePDF(ctx); err != nil {
			return "", err
		}
		defer releasePDF()
		return execSplit(ctx, args, progress)
	case toolMerge:
		if err := acquirePDF(ctx); err != nil {
			return "", err
		}
		defer releasePDF()
		return execMerge(ctx, args, progress)
	case toolVersion:
		return fmt.Sprintf("PDFTool %s (commit %s, built %s)", Version, Commit, BuildTime), nil
	}
	return "", fmt.Errorf("未知工具: %s", name)
}

func getString(args map[string]any, key string) string {
	if s, ok := args[key].(string); ok {
		return s
	}
	return ""
}

func getIntDefault(args map[string]any, key string, def int) int {
	if f, ok := toFloat(args[key]); ok {
		return int(f)
	}
	return def
}

func execSplit(ctx context.Context, args map[string]any, progress ProgressFunc) (string, error) {
	inFile := getString(args, "in")
	outDir := getString(args, "outdir")
	rotate := getIntDefault(args, "rotate", 0)

	span := getIntDefault(args, "span", 0)
	pages := getString(args, "pages")
	export := getString(args, "export")

	if err := validateAngle(rotate); err != nil {
		return "", err
	}

	var err error
	switch {
	case strings.TrimSpace(export) != "":
		err = exportPages(ctx, inFile, outDir, export, rotate, progress)
	case span > 0:
		err = splitBySpan(ctx, inFile, outDir, span, rotate, progress)
	default:
		err = splitByPages(ctx, inFile, outDir, pages, rotate, progress)
	}
	if err != nil {
		return "", err
	}
	count := countPDFs(outDir)
	return fmt.Sprintf("拆分完成，输出目录: %s，共 %d 个文件", outDir, count), nil
}

func execMerge(ctx context.Context, args map[string]any, progress ProgressFunc) (string, error) {
	outFile := getString(args, "out")
	rotate := getIntDefault(args, "rotate", 0)
	if err := validateAngle(rotate); err != nil {
		return "", err
	}

	var inputFiles []string
	if dir := getString(args, "dir"); strings.TrimSpace(dir) != "" {
		files, err := listPDFs(dir)
		if err != nil {
			return "", fmt.Errorf("读取目录失败: %w", err)
		}
		if len(files) == 0 {
			return "", fmt.Errorf("目录中没有 PDF 文件: %s", dir)
		}
		inputFiles = files
	} else if files, ok := args["files"].([]any); ok {
		for _, f := range files {
			if s, ok := f.(string); ok && strings.TrimSpace(s) != "" {
				inputFiles = append(inputFiles, s)
			}
		}
	}
	if len(inputFiles) == 0 {
		return "", fmt.Errorf("必须指定 dir 或 files 参数")
	}

	if err := mergeFiles(ctx, outFile, inputFiles, rotate, progress); err != nil {
		return "", err
	}
	return fmt.Sprintf("合并完成: %s（%d 个文件）", outFile, len(inputFiles)), nil
}

// countPDFs 统计目录下 .pdf 文件数量（用于结果反馈）。
func countPDFs(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".pdf") {
			n++
		}
	}
	return n
}
