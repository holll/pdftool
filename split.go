package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

// pageRange 表示 [from, thru] 的闭区间页码（从 1 开始）。
type pageRange struct {
	from, thru int
}

// spanFileName 与 pdfcpu 的 SplitFile 命名保持一致：
// 单页 {name}_{page}.pdf，多页 {name}_{from}-{thru}.pdf。
func spanFileName(inFile string, from, thru int) string {
	base := filepath.Base(inFile)
	if ext := filepath.Ext(base); strings.EqualFold(ext, ".pdf") {
		base = strings.TrimSuffix(base, ext)
	}
	if from == thru {
		return fmt.Sprintf("%s_%d.pdf", base, from)
	}
	return fmt.Sprintf("%s_%d-%d.pdf", base, from, thru)
}

// openPDF 读取 PDF 上下文，返回总页数。
func openPDF(inFile string) (*model.Context, int, error) {
	ctx, err := api.ReadContextFile(inFile)
	if err != nil {
		return nil, 0, fmt.Errorf("读取 PDF 失败: %w", err)
	}
	return ctx, ctx.PageCount, nil
}

// writePageRange 从已解析的上下文里抽出 [from, thru] 页单独写出，并按需旋转。
func writePageRange(doc *model.Context, outPath string, from, thru, angle int) error {
	sub, err := pdfcpu.ExtractPages(doc, api.PagesForPageRange(from, thru), false)
	if err != nil {
		return fmt.Errorf("抽取第 %d-%d 页失败: %w", from, thru, err)
	}
	if err := api.WriteContextFile(sub, outPath); err != nil {
		return fmt.Errorf("写出 %s 失败: %w", outPath, err)
	}
	if angle != 0 {
		return rotateFileInplace(outPath, angle)
	}
	return nil
}

// runSplit 是 split/export 的公共执行器：逐段写出、上报进度、支持中途取消。
func runSplit(ctx context.Context, doc *model.Context, inFile, outDir string,
	ranges []pageRange, angle int, fileNames func(pageRange) string, progress ProgressFunc) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("创建输出目录失败: %w", err)
	}
	if progress != nil {
		if err := progress(ctx, 0, len(ranges), "开始拆分，共 "+strconv.Itoa(len(ranges))+" 份"); err != nil {
			return err
		}
	}
	for i, r := range ranges {
		if err := ctx.Err(); err != nil {
			return err
		}
		out := filepath.Join(outDir, fileNames(r))
		if err := writePageRange(doc, out, r.from, r.thru, angle); err != nil {
			return err
		}
		if progress != nil {
			if err := progress(ctx, i+1, len(ranges), "已写出 "+filepath.Base(out)); err != nil {
				return err
			}
		}
	}
	return nil
}

// splitBySpan 按 span 拆分（每 span 页一个文件），输出到 outDir。
func splitBySpan(ctx context.Context, inFile, outDir string, span, angle int, progress ProgressFunc) error {
	if span < 1 {
		return fmt.Errorf("span 必须为正整数")
	}
	doc, total, err := openPDF(inFile)
	if err != nil {
		return err
	}
	var ranges []pageRange
	for from := 1; from <= total; from += span {
		thru := from + span - 1
		if thru > total {
			thru = total
		}
		ranges = append(ranges, pageRange{from, thru})
	}
	if len(ranges) == 0 {
		return fmt.Errorf("PDF 没有可拆分的页面")
	}
	return runSplit(ctx, doc, inFile, outDir, ranges, angle,
		func(r pageRange) string { return spanFileName(inFile, r.from, r.thru) }, progress)
}

// splitByPages 按分割点拆分：pages 为类似 "3,6" 的分割点，生成 [1-2],[3-5],[6-end] 等区间文件。
func splitByPages(ctx context.Context, inFile, outDir, pages string, angle int, progress ProgressFunc) error {
	parts := parseIntsFromCSV(pages)
	if len(parts) == 0 {
		return fmt.Errorf("pages 参数为空或包含非法值")
	}
	sort.Ints(parts)

	doc, total, err := openPDF(inFile)
	if err != nil {
		return err
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

	var ranges []pageRange
	prev := 1
	for _, p := range valid {
		ranges = append(ranges, pageRange{prev, p - 1})
		prev = p
	}
	ranges = append(ranges, pageRange{prev, total})

	return runSplit(ctx, doc, inFile, outDir, ranges, angle,
		func(r pageRange) string { return spanFileName(inFile, r.from, r.thru) }, progress)
}

// exportPages 导出指定页（每页为单独文件 page_N.pdf）。
func exportPages(ctx context.Context, inFile, outDir, exportCSV string, angle int, progress ProgressFunc) error {
	pages := parseIntsFromCSV(exportCSV)
	if len(pages) == 0 {
		return fmt.Errorf("export 参数为空或包含非法值")
	}

	doc, total, err := openPDF(inFile)
	if err != nil {
		return err
	}
	for _, p := range pages {
		if p < 1 || p > total {
			return fmt.Errorf("导出页码 %d 超出页数范围 1-%d", p, total)
		}
	}

	ranges := make([]pageRange, 0, len(pages))
	for _, p := range pages {
		ranges = append(ranges, pageRange{p, p})
	}
	return runSplit(ctx, doc, inFile, outDir, ranges, angle,
		func(r pageRange) string { return fmt.Sprintf("page_%d.pdf", r.from) }, progress)
}

// parseIntsFromCSV 解析 CSV 中的整数（支持 3-6 这类范围写法）。
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
			bounds := strings.SplitN(p, "-", 2)
			a, err1 := strconv.Atoi(strings.TrimSpace(bounds[0]))
			b, err2 := strconv.Atoi(strings.TrimSpace(bounds[1]))
			if err1 == nil && err2 == nil {
				for i := a; i <= b; i++ {
					out = append(out, i)
				}
			}
		} else if n, err := strconv.Atoi(p); err == nil {
			out = append(out, n)
		}
	}
	return out
}
