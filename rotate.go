package main

import (
	"fmt"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

// rotateFileInplace 使用 api.RotateFile 覆盖原文件
func rotateFileInplace(path string, angle int) error {
	if angle%90 != 0 {
		return fmt.Errorf("angle must be multiple of 90")
	}
	conf := model.NewDefaultConfiguration()
	// outFile = "" -> 覆盖
	return api.RotateFile(path, "", angle, nil, conf)
}
