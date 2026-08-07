package main

import (
	"fmt"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

// validateAngle 校验旋转角度只能为 0/90/180/270
func validateAngle(angle int) error {
	switch angle {
	case 0, 90, 180, 270:
		return nil
	}
	return fmt.Errorf("旋转角度必须为 0/90/180/270（当前为 %d）", angle)
}

// rotateFileInplace 使用 api.RotateFile 覆盖原文件
func rotateFileInplace(path string, angle int) error {
	if err := validateAngle(angle); err != nil {
		return err
	}
	conf := model.NewDefaultConfiguration()
	// outFile = "" -> 覆盖
	return api.RotateFile(path, "", angle, nil, conf)
}
