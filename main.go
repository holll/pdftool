package main

import (
	"context"
	"flag"
	"fmt"
	"os"
)

var (
	// 这些变量会被 build.bat 通过 -ldflags 自动覆盖
	Version   = "dev"
	Commit    = ""
	BuildTime = ""
)

// fail 输出错误到 stderr 并以非零码退出
func fail(msg string) {
	fmt.Fprintln(os.Stderr, msg)
	os.Exit(1)
}

func printHelp() {
	fmt.Print(`PDFTool - PDF Split/Merge Utility

用法:
  pdftool <command> [参数]

命令:
  split      拆分 PDF 文件
  merge      合并 PDF 文件
  mcp        启动 MCP (Model Context Protocol) 服务器（stdio 或 HTTP）
  version    显示程序版本

示例:
  pdftool split -in input.pdf -outdir out -span 1 -rotate 90
  pdftool merge -dir pdfs -out merged.pdf -rotate 180
  pdftool mcp                                  # stdio 传输
  pdftool mcp -transport http -addr 127.0.0.1:8765   # Streamable HTTP

查看帮助:
  pdftool split -h
  pdftool merge -h
  pdftool mcp -h
`)
}

func main() {
	if len(os.Args) < 2 {
		printHelp()
		return
	}

	cmd := os.Args[1]

	// -------------------------
	// 版本号显示
	// -------------------------
	if cmd == "version" || cmd == "-version" || cmd == "--version" {
		fmt.Printf("PDFTool %s (commit %s, built %s)\n", Version, Commit, BuildTime)
		return
	}

	switch cmd {

	// -------------------------
	// MCP 服务器
	// -------------------------
	case "mcp":
		fs := flag.NewFlagSet("mcp", flag.ExitOnError)

		transport := fs.String("transport", "stdio", "传输方式：stdio 或 http")
		addr := fs.String("addr", "127.0.0.1:8765", "HTTP 监听地址（transport=http 时生效）")
		path := fs.String("path", "/mcp", "HTTP 端点路径")
		token := fs.String("token", "", "HTTP Bearer 令牌（设置后请求必须带 Authorization: Bearer <token>）")
		origins := fs.String("allow-origin", "", "额外允许的 Origin 来源，逗号分隔，可用 * 允许全部")

		fs.Parse(os.Args[2:])

		if err := runMCPServer(*transport, *addr, *path, *token, *origins); err != nil {
			fail("MCP 服务异常：" + err.Error())
		}
		return

	// -------------------------
	// 拆分
	// -------------------------
	case "split":
		fs := flag.NewFlagSet("split", flag.ExitOnError)

		inFile := fs.String("in", "", "输入 PDF 文件路径")
		outDir := fs.String("outdir", "", "输出目录")
		span := fs.Int("span", 0, "每多少页拆分一次（与 pages/export 三选一）")
		pages := fs.String("pages", "", "按分割点拆分（如: 3,6 表示第 3、6 页为分割点，与 span/export 三选一）")
		export := fs.String("export", "", "导出指定页（如: 1,3,5，与 span/pages 三选一）")
		rotate := fs.Int("rotate", 0, "旋转角度（0/90/180/270）")

		fs.Parse(os.Args[2:])

		if *inFile == "" {
			fail("错误：必须指定 -in 输入文件")
		}
		if *outDir == "" {
			fail("错误：必须指定 -outdir 输出目录")
		}
		if err := validateAngle(*rotate); err != nil {
			fail("错误：-rotate " + err.Error())
		}
		if *span < 0 {
			fail("错误：-span 不能为负数")
		}

		modes := 0
		if *span > 0 {
			modes++
		}
		if *pages != "" {
			modes++
		}
		if *export != "" {
			modes++
		}
		if modes == 0 {
			fail("错误：必须指定 -span、-pages 或 -export")
		}
		if modes > 1 {
			fail("错误：-span、-pages、-export 只能指定一个")
		}

		// 执行拆分（CLI 不做进度上报与取消）
		ctx := context.Background()
		var err error
		switch {
		case *export != "":
			err = exportPages(ctx, *inFile, *outDir, *export, *rotate, nil)
		case *span > 0:
			err = splitBySpan(ctx, *inFile, *outDir, *span, *rotate, nil)
		default:
			err = splitByPages(ctx, *inFile, *outDir, *pages, *rotate, nil)
		}

		if err != nil {
			fail("拆分失败：" + err.Error())
		}
		fmt.Printf("拆分完成，输出目录: %s\n", *outDir)
		return

	// -------------------------
	// 合并
	// -------------------------
	case "merge":
		fs := flag.NewFlagSet("merge", flag.ExitOnError)

		dir := fs.String("dir", "", "合并目录内所有 PDF 文件")
		outFile := fs.String("out", "merged.pdf", "输出 PDF 文件")
		rotate := fs.Int("rotate", 0, "旋转角度（0/90/180/270）")

		fs.Parse(os.Args[2:])

		if err := validateAngle(*rotate); err != nil {
			fail("错误：-rotate " + err.Error())
		}

		var inputFiles []string
		var err error

		if *dir != "" {
			inputFiles, err = listPDFs(*dir)
			if err != nil {
				fail("读取目录失败：" + err.Error())
			}
			if len(inputFiles) == 0 {
				fail("目录中没有 PDF 文件")
			}
		} else {
			// 从命令行获取 PDF 文件列表
			args := fs.Args()
			if len(args) == 0 {
				fail("错误：必须指定 -dir 或 PDF 文件列表")
			}
			inputFiles = args
		}

		// 合并并旋转
		err = mergeFiles(context.Background(), *outFile, inputFiles, *rotate, nil)
		if err != nil {
			fail("合并失败：" + err.Error())
		}
		fmt.Printf("合并完成: %s（%d 个文件）\n", *outFile, len(inputFiles))
		return

	default:
		printHelp()
		return
	}
}
