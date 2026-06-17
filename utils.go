package main

import (
	"encoding/json"
	"fmt"
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

type subProbe struct {
	Streams []struct {
		Index       int `json:"index"`
		Disposition struct {
			Forced          int `json:"forced"`
			HearingImpaired int `json:"hearing_impaired"`
		} `json:"disposition"`
		Tags struct {
			Language string `json:"language"`
			Title    string `json:"title"`
		} `json:"tags"`
	} `json:"streams"`
}

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

// Returns the absolute stream index of the best subtitle track.
func pickSubtitleStream(videoFilePath string) (int, bool) {
	out, err := exec.Command("ffprobe", "-v", "error",
		"-select_streams", "s",
		"-show_entries", "stream=index:stream_disposition=forced,hearing_impaired:stream_tags=language,title",
		"-of", "json", videoFilePath).Output()
	if err != nil {
		return 0, false
	}
	var probe subProbe
	if err := json.Unmarshal(out, &probe); err != nil || len(probe.Streams) == 0 {
		return 0, false
	}

	bestIdx, bestScore := -1, -(1 << 30)
	for _, s := range probe.Streams {
		title := strings.ToLower(s.Tags.Title)
		score := 0
		if s.Disposition.Forced == 1 || strings.Contains(title, "forced") {
			score -= 100 // forced track: signs only, skip it
		}
		if s.Disposition.HearingImpaired == 1 || strings.Contains(title, "sdh") {
			score -= 50 // SDH: usable but not preferred
		}
		if s.Tags.Language == "eng" {
			score += 10
		}
		score -= s.Index // tie-break toward the earlier track
		if score > bestScore {
			bestScore, bestIdx = score, s.Index
		}
	}
	return bestIdx, bestIdx != -1
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

		mapSpec := "0:s:0"

		if idx, ok := pickSubtitleStream(videoFilePath); ok {
			mapSpec = fmt.Sprintf("0:%d", idx)
		}

		cmd := exec.Command("ffmpeg", "-y", "-i", videoFilePath,
			"-map", mapSpec, "-c:s", "ass", "-f", "ass", tmp)
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
