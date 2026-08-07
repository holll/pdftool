package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

// splitBySpan 按 span 拆分（每 span 页一个文件），输出到 outDir
func splitBySpan(inFile, outDir string, span, angle int) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("创建输出目录失败: %w", err)
	}

	conf := model.NewDefaultConfiguration()
	if err := api.SplitFile(inFile, outDir, span, conf); err != nil {
		return fmt.Errorf("split failed: %w", err)
	}

	return rotatePDFsInDir(outDir, angle)
}

// splitByPages 按分割点拆分：pages 为类似 "3,6" 的分割点，生成 [1-2],[3-5],[6-end] 等区间文件
func splitByPages(inFile, outDir, pages string, angle int) error {
	parts := parseIntsFromCSV(pages)
	if len(parts) == 0 {
		return fmt.Errorf("pages 参数为空或包含非法值")
	}
	sort.Ints(parts)

	conf := model.NewDefaultConfiguration()
	total, err := api.PageCountFile(inFile)
	if err != nil {
		return fmt.Errorf("无法获取页数: %w", err)
	}

	// 过滤无效分割点：越界报错；分割点 1 或重复值无意义，跳过
	valid := make([]int, 0, len(parts))
	seen := make(map[int]bool, len(parts))
	for _, p := range parts {
		if p < 1 || p > total {
			return fmt.Errorf("分割点 %d 超出页数范围 1-%d", p, total)
		}
		if p == 1 || seen[p] {
			continue
		}
		seen[p] = true
		valid = append(valid, p)
	}
	if len(valid) == 0 {
		return fmt.Errorf("pages 中无有效分割点（分割点 1 或重复值无意义）")
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("创建输出目录失败: %w", err)
	}
	if err := api.SplitByPageNrFile(inFile, outDir, valid, conf); err != nil {
		return fmt.Errorf("按分割点拆分失败: %w", err)
	}
	return rotatePDFsInDir(outDir, angle)
}

// exportPages 导出指定页（每页为单独文件 page_N.pdf）
func exportPages(inFile, outDir, exportCSV string, angle int) error {
	pages := parseIntsFromCSV(exportCSV)
	if len(pages) == 0 {
		return fmt.Errorf("export 参数为空或包含非法值")
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("创建输出目录失败: %w", err)
	}

	conf := model.NewDefaultConfiguration()
	for _, p := range pages {
		// ExtractPagesFile 的第二个参数是输出目录，且输出命名固定为 {文件名}_page_{n}.pdf，
		// 因此先导出到临时子目录再重命名为 page_N.pdf
		tmpDir := filepath.Join(outDir, fmt.Sprintf(".tmp_%d", p))
		if err := os.MkdirAll(tmpDir, 0o755); err != nil {
			return fmt.Errorf("创建临时目录失败: %w", err)
		}
		if err := api.ExtractPagesFile(inFile, tmpDir, []string{strconv.Itoa(p)}, conf); err != nil {
			os.RemoveAll(tmpDir)
			return fmt.Errorf("导出页 %d 失败: %w", p, err)
		}
		files, err := listPDFs(tmpDir)
		if err != nil || len(files) != 1 {
			os.RemoveAll(tmpDir)
			return fmt.Errorf("导出页 %d 输出异常", p)
		}
		target := filepath.Join(outDir, fmt.Sprintf("page_%d.pdf", p))
		if err := os.Rename(files[0], target); err != nil {
			os.RemoveAll(tmpDir)
			return err
		}
		os.RemoveAll(tmpDir)

		if angle != 0 {
			if err := rotateFileInplace(target, angle); err != nil {
				return err
			}
		}
	}
	return nil
}

// rotatePDFsInDir 将目录下所有 PDF 按 angle 旋转（angle 为 0 则跳过）
func rotatePDFsInDir(dir string, angle int) error {
	if angle == 0 {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("读取输出目录失败: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(strings.ToLower(name), ".pdf") {
			path := filepath.Join(dir, name)
			if err := rotateFileInplace(path, angle); err != nil {
				return err
			}
		}
	}
	return nil
}

// parseIntsFromCSV 解析 CSV 中的整数
func parseIntsFromCSV(s string) []int {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	var out []int
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if strings.Contains(p, "-") {
			// 支持像 3-6 的范围（这里将其拆成单个点，供上层逻辑使用）
			bounds := strings.SplitN(p, "-", 2)
			a, err1 := strconv.Atoi(strings.TrimSpace(bounds[0]))
			b, err2 := strconv.Atoi(strings.TrimSpace(bounds[1]))
			if err1 == nil && err2 == nil {
				for i := a; i <= b; i++ {
					out = append(out, i)
				}
			}
		} else {
			if n, err := strconv.Atoi(p); err == nil {
				out = append(out, n)
			}
		}
	}
	return out
}
