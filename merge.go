package main

import (
	"context"
	"fmt"
	"strconv"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

// mergeFiles 合并 inFiles 到 outFile，随后按 angle 旋转（angle 为 0 则不旋转）。
//
// 采用「首个文件建新文件 + 其余逐个追加」的方式，这样每合并完一个文件就能
// 上报进度，并且客户端取消时可以在文件边界立刻停下。
func mergeFiles(ctx context.Context, outFile string, inFiles []string, angle int, progress ProgressFunc) error {
	if len(inFiles) == 0 {
		return fmt.Errorf("没有可合并的 PDF 文件")
	}
	conf := model.NewDefaultConfiguration()

	if progress != nil {
		if err := progress(ctx, 0, len(inFiles), "开始合并，共 "+strconv.Itoa(len(inFiles))+" 个文件"); err != nil {
			return err
		}
	}
	for i, f := range inFiles {
		if err := ctx.Err(); err != nil {
			return err
		}
		var err error
		if i == 0 {
			err = api.MergeCreateFile([]string{f}, outFile, false, conf)
		} else {
			err = api.MergeAppendFile([]string{f}, outFile, false, conf)
		}
		if err != nil {
			return fmt.Errorf("合并 %s 失败: %w", f, err)
		}
		if progress != nil {
			if err := progress(ctx, i+1, len(inFiles), "已合并 "+f); err != nil {
				return err
			}
		}
	}

	if angle != 0 {
		if err := rotateFileInplace(outFile, angle); err != nil {
			return fmt.Errorf("合并后旋转失败: %w", err)
		}
	}
	return nil
}
