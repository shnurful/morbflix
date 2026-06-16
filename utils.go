package main

import (
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
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

var subMu sync.Mutex

func extractSubs(c *gin.Context) {
	rawPath := c.Param("filepath")
	decodedPath, err := url.QueryUnescape(rawPath)
	if err != nil {
		decodedPath = rawPath
	}
	cleaned := strings.TrimPrefix(decodedPath, "/")
	videoFilePath := filepath.Join(hostMoviesDir, cleaned)

	safeName := strings.ReplaceAll(cleaned, "/", "_")
	outSubPath := filepath.Join(ramDiskDir, safeName+".ass")

	subMu.Lock()
	if _, statErr := os.Stat(outSubPath); os.IsNotExist(statErr) {
		tmp := outSubPath + ".tmp"
		// -f ass is required here: ffmpeg normally infers the muxer from the
		// output extension, and ".tmp" isn't one it recognizes.
		cmd := exec.Command("ffmpeg", "-y", "-i", videoFilePath,
			"-map", "0:s:0", "-c:s", "ass", "-f", "ass", tmp)
		if runErr := cmd.Run(); runErr != nil {
			subMu.Unlock()
			log.Printf("subtitle extraction failed for %s: %v", cleaned, runErr)
			c.Status(http.StatusNotFound)
			return
		}
		os.Rename(tmp, outSubPath)
	}
	subMu.Unlock()

	c.File(outSubPath)
}
