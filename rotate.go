package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

// createTempFilePath 返回一个临时文件路径
// 参数 dir: 临时文件夹，如果为空，则使用系统临时目录
// 参数 prefix: 文件名前缀
// 参数 ext: 文件扩展名（例如 ".pdf"）
func createTempFilePath(dir, prefix, ext string) (string, error) {
	if dir == "" {
		dir = filepath.Join(os.TempDir(), "pdftool")
	}

	// 确保目录存在
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("创建临时目录失败: %w", err)
	}

	// 临时文件名：prefix_随机数字.pdf
	tmpFile, err := os.CreateTemp(dir, prefix+"_*"+ext)
	if err != nil {
		return "", fmt.Errorf("创建临时文件失败: %w", err)
	}

	tmpFile.Close() // 关闭文件，仅返回路径
	return tmpFile.Name(), nil
}

// rotateFile 使用 api.RotateFile 旋转文件
func rotateFile(path, outFile string, angle int) error {
	if angle%90 != 0 {
		return fmt.Errorf("angle must be multiple of 90")
	}
	conf := model.NewDefaultConfiguration()
	return api.RotateFile(path, outFile, angle, nil, conf)
}
