package main

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"os"
	"sync"
)

// syncWriter 保证多 goroutine（每条消息一个）写 stdout 时不交错。
// MCP stdio 的帧格式是「一行一条 JSON 消息」。
type syncWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func newSyncWriter(w io.Writer) *syncWriter {
	return &syncWriter{w: w}
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, err := s.w.Write(p)
	if err != nil {
		return n, err
	}
	if _, err := s.w.Write([]byte("\n")); err != nil {
		return n, err
	}
	return n, nil
}

// runStdioServer 用换行分隔的 JSON 在 stdin/stdout 上提供服务。
//
// 每条消息在独立 goroutine 中处理：工具执行可能持续几分钟，如果串行读取，
// 客户端发来的 notifications/cancelled 就要等工具跑完才能被读到，
// 取消功能形同虚设。
func runStdioServer() error {
	out := newSyncWriter(os.Stdout)

	sess := newSession("stdio")
	sess.notify = func(msg []byte) {
		if len(msg) > 0 {
			_, _ = out.Write(msg)
		}
	}

	server := newMCPServer()
	ctx := context.Background()

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 32*1024*1024)

	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		// Scanner 会复用底层数组，必须先拷贝。
		raw := append([]byte(nil), line...)

		go func(raw []byte) {
			resp := server.handleMessage(ctx, sess, raw)
			if resp != nil {
				if _, err := out.Write(resp); err != nil {
					// stdout 已关闭，客户端的后半段也收不到了，安静退出即可。
					return
				}
			}
		}(raw)
	}

	if err := scanner.Err(); err != nil {
		return err
	}
	// stdin EOF：父进程（客户端）已关闭管道，直接返回让进程退出。
	return nil
}
