package main

import (
	"bytes"
	"encoding/json"
)

// JSON-RPC 2.0 标准错误码（外加 MCP 常用的扩展码）
const (
	rpcParseError     = -32700
	rpcInvalidRequest = -32600
	rpcMethodNotFound = -32601
	rpcInvalidParams  = -32602
	rpcInternalError  = -32603
	// rpcRequestCancelled 为 LSP/MCP 生态里通用的「请求已取消」码。
	rpcRequestCancelled = -32800
)

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (e *rpcError) Error() string { return e.Message }

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

func newRPCError(id json.RawMessage, code int, msg string, data any) rpcResponse {
	return rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg, Data: data}}
}

// rpcNotification 是服务端主动发给客户端的消息（如 notifications/progress）。
type rpcNotification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// encodeNotification 序列化一条通知；失败时返回 nil（通知丢失不影响主流程）。
func encodeNotification(method string, params any) []byte {
	b, err := json.Marshal(rpcNotification{JSONRPC: "2.0", Method: method, Params: params})
	if err != nil {
		return nil
	}
	return b
}

// hasID 判断请求是否带 id（通知没有 id，不应回复）。
func hasID(req *rpcRequest) bool {
	return len(req.ID) > 0 && string(req.ID) != "null"
}

// canonicalID 把 id 规范化为字符串，用于 in-flight 表键与取消匹配。
func canonicalID(id json.RawMessage) string {
	var buf bytes.Buffer
	if err := json.Compact(&buf, id); err != nil {
		return string(id)
	}
	return buf.String()
}
