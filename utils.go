package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

func getVideoDuration(videoPath string) int {
	cmd := exec.Command("ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "default=noprint_wrappers=1:nokey=1", videoPath)
	out, err := cmd.Output()
	if err != nil {
		return 0
	}
	durStr := strings.TrimSpace(string(out))
	durFloat, err := strconv.ParseFloat(durStr, 64)
	if err != nil {
		return 0
	}
	return int(durFloat)
}

func findAllVideos(root string) []string {
	var videoFiles []string
	filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext == ".mp4" || ext == ".mkv" || ext == ".webm" || ext == ".avi" {
			if info.Size() > 100*1024*1024 {
				videoFiles = append(videoFiles, path)
			}
		}
		return nil
	})
	return videoFiles
}
