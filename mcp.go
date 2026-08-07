package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// MCP (Model Context Protocol) stdio 服务器实现。
// 协议版本: 2025-06-18（支持回显客户端版本以保证兼容）。
// 传输方式: 标准输入输出，每行一条 JSON-RPC 消息（newline-delimited JSON）。

const mcpProtocolVersion = "2025-06-18"

// MCP 工具名
const (
	toolSplit   = "split"
	toolMerge   = "merge"
	toolVersion = "version"
)

type jsonrpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonrpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// JSON-RPC 标准错误码
const (
	rpcParseError     = -32700
	rpcInvalidRequest = -32600
	rpcMethodNotFound = -32601
	rpcInvalidParams  = -32602
	rpcInternalError  = -32603
)

// toolDef 描述一个 MCP 工具（tools/list 返回项）
type toolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

// runMCP 启动 MCP stdio 服务器，阻塞直到 stdin 关闭
func runMCP() {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var req jsonrpcRequest
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			// 无法解析的消息无法关联 id，只能丢弃（协议允许）
			continue
		}
		// 通知（无 id）不需要响应
		if len(req.ID) == 0 || string(req.ID) == "null" {
			continue
		}
		resp := handleMCPRequest(&req)
		b, err := json.Marshal(resp)
		if err != nil {
			continue
		}
		fmt.Println(string(b))
	}
}

func handleMCPRequest(req *jsonrpcRequest) jsonrpcResponse {
	makeErr := func(code int, msg string) jsonrpcResponse {
		return jsonrpcResponse{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{Code: code, Message: msg}}
	}

	switch req.Method {
	case "initialize":
		params := map[string]any{}
		json.Unmarshal(req.Params, &params)
		clientVer, _ := params["protocolVersion"].(string)
		if clientVer == "" {
			clientVer = mcpProtocolVersion
		}
		return jsonrpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{
			"protocolVersion": clientVer,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "pdftool", "version": Version},
		}}

	case "ping":
		return jsonrpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{}}

	case "tools/list":
		return jsonrpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{
			"tools": mcpTools(),
		}}

	case "tools/call":
		var params struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return makeErr(rpcInvalidParams, "tools/call 参数解析失败: "+err.Error())
		}
		result, err := callMCPTool(params.Name, params.Arguments)
		if err != nil {
			// 工具执行错误按 MCP 规范通过 isError 返回，而非 JSON-RPC error
			return jsonrpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{
				"content": []any{map[string]any{"type": "text", "text": err.Error()}},
				"isError": true,
			}}
		}
		return jsonrpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{
			"content": []any{map[string]any{"type": "text", "text": result}},
		}}

	default:
		return makeErr(rpcMethodNotFound, "未知方法: "+req.Method)
	}
}

func mcpTools() []toolDef {
	obj := func(props map[string]any, required []string) map[string]any {
		return map[string]any{"type": "object", "properties": props, "required": required}
	}
	strProp := map[string]any{"type": "string"}
	intProp := map[string]any{"type": "integer"}
	arrProp := map[string]any{"type": "array", "items": map[string]any{"type": "string"}}

	return []toolDef{
		{
			Name: toolSplit,
			Description: "拆分 PDF 文件。支持按每 N 页拆分(-span)、按分割点拆分(-pages, 如 \"3,6\" 表示第3、6页为分割点)" +
				"、导出指定页(-export, 如 \"1,3,5\")，三种方式三选一，可附带旋转(-rotate: 0/90/180/270)",
			InputSchema: obj(map[string]any{
				"in":     strProp, // 必填
				"outdir": strProp, // 必填
				"span":   intProp,
				"pages":  strProp,
				"export": strProp,
				"rotate": intProp,
			}, []string{"in", "outdir"}),
		},
		{
			Name: toolMerge,
			Description: "合并 PDF 文件。指定 dir 合并目录内所有 PDF，或指定 files 文件列表，可附带旋转(-rotate: 0/90/180/270)",
			InputSchema: obj(map[string]any{
				"dir":    strProp,
				"files":  arrProp,
				"out":    strProp,
				"rotate": intProp,
			}, []string{"out"}),
		},
		{
			Name:        toolVersion,
			Description: "显示 PDFTool 版本信息",
			InputSchema: obj(map[string]any{}, []string{}),
		},
	}
}

// callMCPTool 执行工具调用，返回文本结果
func callMCPTool(name string, args map[string]any) (string, error) {
	if args == nil {
		args = map[string]any{}
	}
	getStr := func(k string) string {
		v, _ := args[k].(string)
		return v
	}
	getInt := func(k string) int {
		switch v := args[k].(type) {
		case float64:
			return int(v)
		case int:
			return v
		}
		return 0
	}

	switch name {
	case toolSplit:
		inFile := getStr("in")
		outDir := getStr("outdir")
		if inFile == "" || outDir == "" {
			return "", fmt.Errorf("split 工具缺少必填参数: in、outdir")
		}
		span := getInt("span")
		pages := getStr("pages")
		export := getStr("export")
		rotate := getInt("rotate")

		if span == 0 && pages == "" && export == "" {
			return "", fmt.Errorf("必须指定 span、pages、export 之一")
		}
		n := 0
		if span > 0 {
			n++
		}
		if pages != "" {
			n++
		}
		if export != "" {
			n++
		}
		if n > 1 {
			return "", fmt.Errorf("span、pages、export 只能指定一个")
		}

		switch {
		case export != "":
			if err := exportPages(inFile, outDir, export, rotate); err != nil {
				return "", err
			}
		case span > 0:
			if err := splitBySpan(inFile, outDir, span, rotate); err != nil {
				return "", err
			}
		default:
			if err := splitByPages(inFile, outDir, pages, rotate); err != nil {
				return "", err
			}
		}
		count := countPDFs(outDir)
		return fmt.Sprintf("拆分完成，输出目录: %s，共 %d 个文件", outDir, count), nil

	case toolMerge:
		outFile := getStr("out")
		if outFile == "" {
			outFile = "merged.pdf"
		}
		rotate := getInt("rotate")

		var inputFiles []string
		if dir := getStr("dir"); dir != "" {
			files, err := listPDFs(dir)
			if err != nil {
				return "", err
			}
			if len(files) == 0 {
				return "", fmt.Errorf("目录中没有 PDF 文件: %s", dir)
			}
			inputFiles = files
		} else if files, ok := args["files"].([]any); ok {
			for _, f := range files {
				if s, ok := f.(string); ok && s != "" {
					inputFiles = append(inputFiles, s)
				}
			}
		}
		if len(inputFiles) == 0 {
			return "", fmt.Errorf("必须指定 dir 或 files 参数")
		}

		if err := mergeFiles(outFile, inputFiles, rotate); err != nil {
			return "", err
		}
		return fmt.Sprintf("合并完成: %s（%d 个文件）", outFile, len(inputFiles)), nil

	case toolVersion:
		return fmt.Sprintf("PDFTool %s (commit %s, built %s)", Version, Commit, BuildTime), nil

	default:
		return "", fmt.Errorf("未知工具: %s", name)
	}
}

// countPDFs 统计目录下 .pdf 文件数量（用于结果反馈）
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
