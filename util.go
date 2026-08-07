package main

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// listPDFs 返回 dir 目录下按文件名排序的所有 .pdf 文件的完整路径（非递归）。
func listPDFs(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var files []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(strings.ToLower(name), ".pdf") {
			files = append(files, filepath.Join(dir, name))
		}
	}

	sort.Strings(files)
	return files, nil
}
