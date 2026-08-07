package main

import (
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
  mcp        启动 MCP (Model Context Protocol) stdio 服务器
  version    显示程序版本

示例:
  pdftool split -in input.pdf -outdir out -span 1 -rotate 90
  pdftool merge -dir pdfs -out merged.pdf -rotate 180

查看帮助:
  pdftool split -h
  pdftool merge -h
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
		runMCP()
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

		// 执行拆分
		var err error
		switch {
		case *export != "":
			err = exportPages(*inFile, *outDir, *export, *rotate)
		case *span > 0:
			err = splitBySpan(*inFile, *outDir, *span, *rotate)
		default:
			err = splitByPages(*inFile, *outDir, *pages, *rotate)
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
		err = mergeFiles(*outFile, inputFiles, *rotate)
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
