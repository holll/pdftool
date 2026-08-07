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
- 导出指定页（例如 `-export "1,3,5"`）： `./pdftool split -in a.pdf -outdir parts -export "1,3,5"`
- 合并目录： `./pdftool merge -dir ./pdfs -out merged.pdf`
- 合并指定文件： `./pdftool merge -out merged.pdf a.pdf b.pdf c.pdf`
- 旋转（支持 split/merge 的 `-rotate` 参数，值为 0/90/180/270）