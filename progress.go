package main

import "context"

// ProgressFunc 上报长任务进度（MCP notifications/progress 的数据来源）。
//
// done 为已完成的工作量，total 为总量；message 为可选的进度描述。
// 返回非 nil error 表示调用方要求中止（例如 MCP 客户端发来了
// notifications/cancelled），拆分/合并会在当前位置停下。
type ProgressFunc func(ctx context.Context, done, total int, message string) error
