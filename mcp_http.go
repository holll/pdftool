package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	maxMCPBodyBytes = 32 << 20 // 32 MiB
	sseKeepAlive    = 20 * time.Second
	sseBufferSize   = 64
)

// sseStream 是一条 GET SSE 长连接的消息队列；客户端没连时丢弃通知。
type sseStream struct {
	ch chan []byte
}

func newSSEStream() *sseStream {
	return &sseStream{ch: make(chan []byte, sseBufferSize)}
}

func (s *sseStream) push(msg []byte) {
	if len(msg) == 0 {
		return
	}
	select {
	case s.ch <- msg:
	default: // 客户端读得慢，丢最旧之外的消息也无妨——进度通知允许丢失
	}
}

// httpServer 是 MCP Streamable HTTP 传输层：
//   - POST：JSON-RPC 请求/通知
//   - GET：SSE，用于服务端推送（进度等）
//   - DELETE：结束会话
type httpServer struct {
	server  *mcpServer
	path    string
	token   string
	origins []string

	mu      sync.Mutex
	streams map[string]*sseStream

	ctx context.Context
}

func newHTTPServer(path, token, origins string) *httpServer {
	var list []string
	for _, o := range strings.Split(origins, ",") {
		if o = strings.TrimSpace(o); o != "" {
			list = append(list, o)
		}
	}
	return &httpServer{
		server:  newMCPServer(),
		path:    path,
		token:   token,
		origins: list,
		streams: map[string]*sseStream{},
		ctx:     context.Background(),
	}
}

func newSessionID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("sess-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

func (s *httpServer) stream(id string) *sseStream {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.streams[id]
	if !ok {
		st = newSSEStream()
		s.streams[id] = st
	}
	return st
}

func (s *httpServer) dropStream(id string) {
	s.mu.Lock()
	delete(s.streams, id)
	s.mu.Unlock()
}

// newSession 创建一个会话；notify 推到该会话的 SSE 队列。
func (s *httpServer) newSession(id string) *session {
	sess := newSession(id)
	st := s.stream(id)
	sess.notify = st.push
	s.server.addSession(sess)
	return sess
}

func (s *httpServer) originAllowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if len(s.origins) == 0 {
		// 未配置时：允许无 Origin（非浏览器客户端）与同源请求。
		if origin == "" {
			return true
		}
		host := r.Host
		return origin == "http://"+host || origin == "https://"+host
	}
	if origin == "" {
		return true // 非浏览器客户端不带 Origin
	}
	for _, o := range s.origins {
		if o == "*" || strings.EqualFold(o, origin) {
			return true
		}
	}
	return false
}

func (s *httpServer) authorized(r *http.Request) bool {
	if s.token == "" {
		return true
	}
	h := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(h) <= len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return false
	}
	return subtleEqual(strings.TrimSpace(h[len(prefix):]), s.token)
}

// subtleEqual 做常数时间比较，避免令牌被时序侧信道猜测。
func subtleEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var v byte
	for i := 0; i < len(a); i++ {
		v |= a[i] ^ b[i]
	}
	return v == 0
}

func (s *httpServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != s.path {
		http.NotFound(w, r)
		return
	}
	if !s.originAllowed(r) {
		http.Error(w, "origin not allowed", http.StatusForbidden)
		return
	}
	s.setCORS(w, r)

	// CORS 预检必须放在鉴权之前：浏览器预检请求不会携带 Authorization。
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Methods", "POST, GET, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", strings.Join([]string{
			"Content-Type", "Accept", "Authorization", "Mcp-Session-Id", "MCP-Protocol-Version",
		}, ", "))
		w.Header().Set("Access-Control-Max-Age", "600")
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if !s.authorized(r) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="pdftool-mcp"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	switch r.Method {
	case http.MethodPost:
		s.handlePost(w, r)
	case http.MethodGet:
		s.handleGet(w, r)
	case http.MethodDelete:
		s.handleDelete(w, r)
	default:
		w.Header().Set("Allow", "POST, GET, DELETE, OPTIONS")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *httpServer) handlePost(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxMCPBodyBytes+1))
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if len(body) > maxMCPBodyBytes {
		http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		return
	}
	if len(bytes.TrimSpace(body)) == 0 {
		http.Error(w, "empty body", http.StatusBadRequest)
		return
	}

	sessionID := r.Header.Get("Mcp-Session-Id")
	var sess *session

	if isInitialize(body) {
		if sessionID != "" {
			if _, ok := s.server.session(sessionID); !ok {
				http.Error(w, "unknown session", http.StatusNotFound)
				return
			}
		} else {
			sessionID = newSessionID()
			sess = s.newSession(sessionID)
		}
	} else {
		if sessionID == "" {
			http.Error(w, "missing Mcp-Session-Id header (send initialize first)", http.StatusBadRequest)
			return
		}
		var ok bool
		sess, ok = s.server.session(sessionID)
		if !ok {
			http.Error(w, "unknown session", http.StatusNotFound)
			return
		}
	}

	// 协议版本头：客户端在 initialize 之后的请求可选携带。
	if v := r.Header.Get("MCP-Protocol-Version"); v != "" && !isSupportedProtocolVersion(v) {
		http.Error(w, "unsupported MCP-Protocol-Version: "+v, http.StatusBadRequest)
		return
	}

	if sess == nil {
		sess, _ = s.server.session(sessionID)
	}

	// 每次响应都带上会话标识，客户端据此在后续请求里回带。
	w.Header().Set("Mcp-Session-Id", sessionID)

	// 纯通知/响应（无 id）不需要回复体。
	if !hasAnyRequest(body) {
		ctx := r.Context()
		if resp := s.server.handleMessage(ctx, sess, body); resp != nil {
			// 例如批处理里混入的请求被取消后仍有其它响应
			s.writeJSON(w, resp)
			return
		}
		w.WriteHeader(http.StatusAccepted)
		return
	}

	resp := s.server.handleMessage(r.Context(), sess, body)
	if resp == nil {
		// 请求被取消：规范允许不回复
		w.WriteHeader(http.StatusAccepted)
		return
	}
	s.writeJSON(w, resp)
}

func (s *httpServer) writeJSON(w http.ResponseWriter, payload []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(payload)
}

// handleGet 提供 SSE 通道用于服务端推送；没有待推送消息时发 keepalive 注释行。
func (s *httpServer) handleGet(w http.ResponseWriter, r *http.Request) {
	sessionID := r.Header.Get("Mcp-Session-Id")
	if sessionID == "" {
		http.Error(w, "missing Mcp-Session-Id header", http.StatusBadRequest)
		return
	}
	if _, ok := s.server.session(sessionID); !ok {
		http.Error(w, "unknown session", http.StatusNotFound)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache, no-transform")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	if origin := r.Header.Get("Origin"); origin != "" && s.originAllowed(r) {
		h.Set("Access-Control-Allow-Origin", origin)
	}
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	st := s.stream(sessionID)
	ticker := time.NewTicker(sseKeepAlive)
	defer ticker.Stop()
	defer s.dropStream(sessionID)

	for {
		select {
		case <-r.Context().Done():
			return
		case msg := <-st.ch:
			if _, err := fmt.Fprintf(w, "event: message\ndata: %s\n\n", msg); err != nil {
				return
			}
			flusher.Flush()
		case <-ticker.C:
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func (s *httpServer) handleDelete(w http.ResponseWriter, r *http.Request) {
	sessionID := r.Header.Get("Mcp-Session-Id")
	if sessionID == "" {
		http.Error(w, "missing Mcp-Session-Id header", http.StatusBadRequest)
		return
	}
	if !s.server.removeSession(sessionID) {
		http.Error(w, "unknown session", http.StatusNotFound)
		return
	}
	s.dropStream(sessionID)
	w.WriteHeader(http.StatusOK)
}

// setCORS 给来自允许来源的响应加上 CORS 头（浏览器端 MCP 客户端需要）。
func (s *httpServer) setCORS(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	if origin == "" || !s.originAllowed(r) {
		return
	}
	h := w.Header()
	h.Set("Access-Control-Allow-Origin", origin)
	h.Set("Vary", "Origin")
}

// hasAnyRequest 判断消息体里是否含需要回复的请求（有 id 的调用）。
func hasAnyRequest(raw []byte) bool {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return false
	}
	if trimmed[0] == '[' {
		var items []json.RawMessage
		if err := json.Unmarshal(trimmed, &items); err != nil {
			return true // 解析失败也按请求处理，让服务端返回 -32700
		}
		for _, item := range items {
			if isRequestMessage(item) {
				return true
			}
		}
		return false
	}
	return isRequestMessage(trimmed)
}

func isRequestMessage(raw []byte) bool {
	var probe struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return false
	}
	return probe.Method != "" && len(probe.ID) > 0 && string(probe.ID) != "null"
}

// runHTTPServer 在 addr 上启动 MCP Streamable HTTP 服务，阻塞直到进程退出。
func runHTTPServer(addr, path, token, origins string) error {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	hs := newHTTPServer(path, token, origins)

	srv := &http.Server{
		Addr:              addr,
		Handler:           hs,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	log.Printf("MCP HTTP server listening on http://%s%s (transport=streamable-http)", addr, path)
	if token != "" {
		log.Printf("Bearer token required")
	}
	return srv.ListenAndServe()
}

// runMCPServer 按 -transport 选择传输层。
func runMCPServer(transport, addr, path, token, origins string) error {
	warmUpPDFCPU()
	switch strings.ToLower(strings.TrimSpace(transport)) {
	case "", "stdio":
		return runStdioServer()
	case "http", "streamable-http", "streamablehttp":
		if strings.TrimSpace(addr) == "" {
			return fmt.Errorf("-addr 不能为空")
		}
		return runHTTPServer(addr, path, token, origins)
	default:
		return fmt.Errorf("不支持的传输方式 %q（可选：stdio、http）", transport)
	}
}
