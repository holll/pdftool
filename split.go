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

// 按 span 拆分（每 span 页一个文件）
func splitBySpan(inFile, outDir string, span, angle int) error {
	conf := model.NewDefaultConfiguration()
	tmpDir, err := os.MkdirTemp("", "pdftool_split_*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	// 拆分到临时目录
	if err = api.SplitFile(inFile, tmpDir, span, conf); err != nil {
		return err
	}

	entries, _ := os.ReadDir(tmpDir)

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".pdf") {
			continue
		}

		srcPath := filepath.Join(tmpDir, e.Name())
		dstPath := filepath.Join(outDir, e.Name())

		if angle != 0 {
			tmpRotated, _ := createTempFilePath("", "rotate_", ".pdf")
			if err = rotateFile(srcPath, tmpRotated, angle); err != nil {
				return err
			}
			srcPath = tmpRotated
		}

		if err = os.Rename(srcPath, dstPath); err != nil {
			return err
		}
	}
	return nil
}

// 按分割点拆分：pages 为类似 "3,6" 的分割点，生成多个区间文件：part_001.pdf ...
func splitByPages(inFile, outDir, pages string, angle int) error {
	// 解析分割点
	parts := parseIntsFromCSV(pages)
	if len(parts) == 0 {
		return fmt.Errorf("pages 参数为空")
	}
	sort.Ints(parts)

	// 获取原始页数（使用 api.PageCountFile）
	conf := model.NewDefaultConfiguration()
	total, err := api.PageCountFile(inFile)
	if err != nil {
		return fmt.Errorf("无法获取页数: %w", err)
	}

	// 生成区间 [1, p1-1], [p1, p2-1], ..., [last, total]
	ranges := make([]string, 0)
	prev := 1
	for _, p := range parts {
		if p < 1 || p > total {
			return fmt.Errorf("分割点 %d 超出页数范围 1-%d", p, total)
		}
		if p-1 >= prev {
			ranges = append(ranges, fmt.Sprintf("%d-%d", prev, p-1))
		}
		prev = p
	}
	if prev <= total {
		ranges = append(ranges, fmt.Sprintf("%d-%d", prev, total))
	}

	// 导出每个区间
	for i, r := range ranges {
		outFile := filepath.Join(outDir, fmt.Sprintf("part_%03d.pdf", i+1))
		if err := api.ExtractPagesFile(inFile, outFile, []string{r}, conf); err != nil {
			return fmt.Errorf("导出区间 %s 失败: %w", r, err)
		}
		if angle != 0 {
			if err := rotateFile(outFile, "", angle); err != nil {
				return err
			}
		}
	}
	return nil
}

// 导出指定页（每页为单独文件），exportCSV 示例: "1,3,5" -> 导出 page_1.pdf, page_3.pdf ...
func exportPages(inFile, outDir, exportCSV string, angle int) error {
	pages := parseIntsFromCSV(exportCSV)
	if len(pages) == 0 {
		return fmt.Errorf("export 参数为空")
	}
	conf := model.NewDefaultConfiguration()
	for _, p := range pages {
		f := filepath.Join(outDir, fmt.Sprintf("page_%d.pdf", p))
		if err := api.ExtractPagesFile(inFile, f, []string{strconv.Itoa(p)}, conf); err != nil {
			return fmt.Errorf("导出页 %d 失败: %w", p, err)
		}
		if angle != 0 {
			if err := rotateFile(f, "", angle); err != nil {
				return err
			}
		}
	}
	return nil
}

// 解析 CSV 中的整数
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
