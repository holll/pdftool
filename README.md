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

`pdftool` 内置 MCP (Model Context Protocol) stdio 服务器，供 AI 客户端（如 Claude Desktop、Claude Code）通过标准输入输出以 JSON-RPC 方式调用 PDF 处理能力。

启动方式：

```bash
./pdftool mcp
```

暴露的 MCP 工具：

| 工具 | 参数 | 说明 |
|------|------|------|
| `split` | `in`(必填), `outdir`(必填), `span`, `pages`, `export`, `rotate` | 拆分 PDF（三种拆分方式三选一） |
| `merge` | `out`(必填), `dir` 或 `files`, `rotate` | 合并 PDF 文件 |
| `version` | 无 | 显示版本信息 |

### Claude Desktop 配置示例

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

### 手动调试

```bash
echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}' | ./pdftool mcp
```
