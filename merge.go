package main

import (
	"fmt"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

// mergeFiles 合并 inFiles 到 outFile，随后按 angle 旋转（angle 为 0 则不旋转）
func mergeFiles(outFile string, inFiles []string, angle int) error {
	conf := model.NewDefaultConfiguration()

	if err := api.MergeCreateFile(inFiles, outFile, false, conf); err != nil {
		return fmt.Errorf("合并失败: %w", err)
	}

	if angle != 0 {
		if err := rotateFile(outFile, "", angle); err != nil {
			return fmt.Errorf("合并后旋转失败: %w", err)
		}
	}
	return nil
}
