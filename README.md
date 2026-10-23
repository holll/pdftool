## 使用说明

1. 初始化并获取依赖：

```bash
go mod init pdftool
go get github.com/pdfcpu/pdfcpu
```

2. 构建：

```bash
go build -o pdftool ./...
```

3. 常用命令：

- 每页拆分： `./pdftool split -in a.pdf -outdir parts`
- 每 5 页拆一份： `./pdftool split -in a.pdf -outdir parts -span 5`
- 按分割点拆分（例如 `-pages "3,6"` -> [1-2],[3-5],[6-end]）： `./pdftool split -in a.pdf -outdir parts -pages "3,6"`
- 导出指定页（例如 `-export "1,3,5"` -> page_1.pdf, page_3.pdf, page_5.pdf）： `./pdftool split -in a.pdf -outdir parts -export "1,3,5"`
- 合并目录： `./pdftool merge -dir ./pdfs -out merged.pdf`
- 合并指定文件： `./pdftool merge -out merged.pdf a.pdf b.pdf c.pdf`
- 旋转（支持 split/merge 的 `-rotate` 参数，值为 0/90/180/270）

> `-span`、`-pages`、`-export` 三选一，不能同时使用。
> 拆分输出命名格式为 `{输入文件名}_{起始页}-{结束页}.pdf`（单页为 `{输入文件名}_{页号}.pdf`），导出指定页命名为 `page_{页号}.pdf`。

## MCP 协议支持

`pdftool` 内置 MCP (Model Context Protocol) 服务器，支持两种传输：

| 传输 | 启动方式 | 适用场景 |
|------|----------|----------|
| stdio | `./pdftool mcp` | 本机客户端（Claude Desktop / Claude Code / Cursor 等）拉起子进程 |
| Streamable HTTP | `./pdftool mcp -transport http -addr 127.0.0.1:8765` | 容器、远程或多客户端共享一个服务 |

协议版本：`2025-06-18`（同时兼容 `2025-03-26`、`2024-11-05`，按客户端请求协商）。

### 工具

| 工具 | 参数 | 说明 |
|------|------|------|
| `split` | `in`(必填), `outdir`(必填), `span` ≥1 / `pages` / `export`（三选一）, `rotate`(0/90/180/270) | 拆分 PDF |
| `merge` | `out`(必填), `dir` 或 `files`（二选一）, `rotate`(0/90/180/270) | 合并 PDF |
| `version` | 无 | 显示版本信息 |

`tools/list` 返回完整的 JSON Schema（`type`、`required`、`enum`、`minimum`、`items`、`additionalProperties: false`），客户端可据此在调用前做参数校验。

服务器另提供 `resources/list`、`resources/templates/list`、`prompts/list`，返回空集合（便于客户端探测能力，不再返回 `-32601`）。

### 进度与取消

- 调用时在 `params._meta.progressToken` 传一个令牌（字符串或数字），服务器会在拆分/合并过程中通过 `notifications/progress` 上报 `progress`/`total`/`message`。
  - stdio：通知直接写入 stdout。
  - HTTP：通知经 `GET /mcp` 的 SSE 通道推送，建议客户端在调用前先建立 SSE 连接。
- 客户端发送 `notifications/cancelled`（`params.requestId` 为被取消请求的 id）后，服务器在下一个文件/分段的边界停止并**不再回复该请求**，符合 MCP 规范。

### 参数校验与错误码

| 错误码 | 场景 |
|--------|------|
| `-32700` | 请求体不是合法 JSON |
| `-32600` | JSON 合法但不是合法 JSON-RPC 请求（如缺 `method`） |
| `-32601` | 未知方法 |
| `-32602` | 未知工具、参数类型/枚举/下界不符、缺必填参数、互斥参数同传（`error.data` 里带 `errors` 列表） |
| `-32603` | 服务端内部错误 |

工具执行期的错误（文件不存在、PDF 损坏等）按 MCP 惯例放在结果里：`result.isError = true`，`content[0].text` 为错误说明。

### stdio 配置示例（Claude Desktop）

在 `claude_desktop_config.json` 中添加：

```json
{
  "mcpServers": {
    "pdftool": {
      "command": "C:\\path\\to\\pdftool.exe",
      "args": ["mcp"]
    }
  }
}
```

### HTTP 配置与安全

```bash
# 仅本机可访问、无鉴权
./pdftool mcp -transport http -addr 127.0.0.1:8765

# 带 Bearer 令牌 + 仅允许指定来源（浏览器端客户端）
./pdftool mcp -transport http -addr 127.0.0.1:8765 -token mysecret -allow-origin https://example.com
```

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `-addr` | `127.0.0.1:8765` | 监听地址；对外暴露前请务必设置 `-token` |
| `-path` | `/mcp` | 端点路径 |
| `-token` | 空 | 设置后所有请求必须带 `Authorization: Bearer <token>` |
| `-allow-origin` | 空 | 逗号分隔的允许来源，`*` 表示全部；留空时只允许无 Origin 或同源请求 |

HTTP 语义：

- `POST /mcp`：JSON-RPC 请求/通知，支持批量数组；`initialize` 响应带 `Mcp-Session-Id`，后续请求需回带。
- `GET /mcp`：SSE 通道，推送 `notifications/progress`，每 20 秒发一次 keepalive。
- `DELETE /mcp`：结束会话。
- 缺少会话头返回 `400`，会话不存在返回 `404`，`OPTIONS` 预检返回 `204`（无需鉴权）。
- 请求体上限 32 MiB；`MCP-Protocol-Version` 头携带不支持的版本时返回 `400`。

### 手动调试

```bash
# stdio 握手
echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}' | ./pdftool mcp

# HTTP 握手
curl -s -X POST http://127.0.0.1:8765/mcp \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}' -D -
```

### 已知限制

- 工具在服务端同步执行，超长 PDF 的合并/拆分建议配合 `progressToken` 观察进度，必要时用 `notifications/cancelled` 中断。
- HTTP 模式下进度通知依赖客户端同时建立 `GET /mcp` 的 SSE 连接；只发 POST 的客户端拿不到进度（但能正常拿到结果）。
- 部分 MCP 客户端（如 `@modelcontextprotocol/sdk` 1.x 的 `callTool`）会在收到结果响应后丢弃晚到的通知，因此最后一条进度通知可能看不到——结果本身不受影响。
