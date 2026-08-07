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

func printHelp() {
	fmt.Println(`PDFTool - PDF Split/Merge Utility

用法:
  pdftool <command> [参数]

命令:
  split      拆分 PDF 文件
  merge      合并 PDF 文件
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
	// 拆分
	// -------------------------
	case "split":
		fs := flag.NewFlagSet("split", flag.ExitOnError)

		inFile := fs.String("in", "", "输入 PDF 文件路径")
		outDir := fs.String("outdir", "", "输出目录")
		span := fs.Int("span", 0, "每多少页拆分一次（与 pages 二选一）")
		pages := fs.String("pages", "", "指定页面拆分（如: 1-3,5,8-10，与 span 二选一）")
		rotate := fs.Int("rotate", 0, "旋转角度（0/90/180/270）")

		fs.Parse(os.Args[2:])

		if *inFile == "" {
			fmt.Println("错误：必须指定 -in 输入文件")
			return
		}
		if *outDir == "" {
			fmt.Println("错误：必须指定 -outdir 输出目录")
			return
		}

		if *span == 0 && *pages == "" {
			fmt.Println("错误：必须指定 -span 或 -pages")
			return
		}
		if *span != 0 && *pages != "" {
			fmt.Println("错误：-span 与 -pages 不能同时使用")
			return
		}

		// 执行拆分
		var err error
		if *span > 0 {
			err = splitBySpan(*inFile, *outDir, *span, *rotate)
		} else {
			err = splitByPages(*inFile, *outDir, *pages, *rotate)
		}

		if err != nil {
			fmt.Println("拆分失败：", err)
		}
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

		var inputFiles []string
		var err error

		if *dir != "" {
			inputFiles, err = listPDFs(*dir)
			if err != nil {
				fmt.Println("读取目录失败：", err)
				return
			}
			if len(inputFiles) == 0 {
				fmt.Println("目录中没有 PDF 文件")
				return
			}
		} else {
			// 从命令行获取 PDF 文件列表
			args := fs.Args()
			if len(args) == 0 {
				fmt.Println("错误：必须指定 -dir 或 PDF 文件列表")
				return
			}
			inputFiles = args
		}

		// 合并并旋转
		err = mergeFiles(*outFile, inputFiles, *rotate)
		if err != nil {
			fmt.Println("合并失败：", err)
		}
		return

	default:
		printHelp()
		return
	}
}
