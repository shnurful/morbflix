package main

import (
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

type StreamState struct {
	Cmd      *exec.Cmd // Can now be nil if finished!
	Start    string
	Audio    string
	Mono     string
	LastPing time.Time
}

var activeStreams = make(map[string]*StreamState)
var streamMu sync.Mutex

func serveHLS(c *gin.Context) {
	rawPath := c.Param("filepath")
	startParam := c.DefaultQuery("start", "0")
	audioParam := c.DefaultQuery("audio", "0")
	monoParam := c.DefaultQuery("mono", "0")

	decodedPath, err := url.QueryUnescape(rawPath)
	if err != nil {
		decodedPath = rawPath
	}
	cleaned := strings.TrimPrefix(decodedPath, "/")
	lastSlash := strings.LastIndex(cleaned, "/")
	if lastSlash == -1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid path"})
		return
	}

	filename := cleaned[:lastSlash]
	chunk := cleaned[lastSlash+1:]

	outDir := filepath.Join(ramDiskDir, filename)
	playlistPath := filepath.Join(outDir, "playlist.m3u8")

	if chunk == "playlist.m3u8" {
		streamMu.Lock()
		state, exists := activeStreams[filename]

		needsRestart := !exists || state.Start != startParam || state.Audio != audioParam || state.Mono != monoParam

		if needsRestart {
			if exists && state.Cmd != nil && state.Cmd.Process != nil {
				state.Cmd.Process.Kill()
			}

			os.RemoveAll(outDir)
			os.MkdirAll(outDir, 0755)

			videoFilePath := filepath.Join(hostMoviesDir, filename)
			segmentPath := filepath.Join(outDir, "chunk_%03d.m4s")

			if _, err := os.Stat(videoFilePath); os.IsNotExist(err) {
				streamMu.Unlock()
				c.JSON(http.StatusNotFound, gin.H{"error": "movie not found"})
				return
			}

			ffmpegArgs := []string{
				"-ss", startParam,
				"-i", videoFilePath,
				"-map", "0:v:0",
				"-map", fmt.Sprintf("0:a:%s?", audioParam),
				"-c:v", "copy",
				"-c:a", "aac",
			}

			if monoParam == "1" {
				ffmpegArgs = append(ffmpegArgs, "-ac", "1")
			} else {
				ffmpegArgs = append(ffmpegArgs, "-ac", "2")
			}

			ffmpegArgs = append(ffmpegArgs,
				"-b:a", "128k",
				"-sn", "-dn",
				"-avoid_negative_ts", "make_zero",
				"-f", "hls",
				"-hls_time", "2",
				"-hls_list_size", "0",
				"-hls_playlist_type", "event",
				"-hls_segment_type", "fmp4",
				"-hls_segment_filename", segmentPath,
				playlistPath,
			)

			cmd := exec.Command("ffmpeg", ffmpegArgs...)
			cmd.Stderr = os.Stderr

			if err := cmd.Start(); err == nil {
				activeStreams[filename] = &StreamState{
					Cmd:      cmd,
					Start:    startParam,
					Audio:    audioParam,
					Mono:     monoParam,
					LastPing: time.Now(),
				}
				
				// CRITICAL FIX: Keep state alive but mark as finished when done!
				go func(runningCmd *exec.Cmd, f string) {
					_ = runningCmd.Wait()
					streamMu.Lock()
					if st, ok := activeStreams[f]; ok && st.Cmd == runningCmd {
						st.Cmd = nil // Mark as finished. Files stay safely in RAM!
					}
					streamMu.Unlock()
				}(cmd, filename)
			}
		} else {
			if state, exists := activeStreams[filename]; exists {
				state.LastPing = time.Now()
			}
		}
		streamMu.Unlock()

		if needsRestart {
			firstChunk := filepath.Join(outDir, "chunk_000.m4s")
			startTime := time.Now()
			for {
				_, errPlay := os.Stat(playlistPath)
				_, errChunk := os.Stat(firstChunk)
				if errPlay == nil && errChunk == nil {
					time.Sleep(50 * time.Millisecond)
					break
				}
				if time.Since(startTime) > 10*time.Second {
					c.JSON(http.StatusInternalServerError, gin.H{"error": "timeout"})
					return
				}
				time.Sleep(100 * time.Millisecond)
			}
		}
	}

	c.Header("Cache-Control", "no-cache, no-store, must-revalidate")
	c.Header("Pragma", "no-cache")
	c.Header("Expires", "0")

	if strings.HasSuffix(chunk, ".m3u8") {
		c.Header("Content-Type", "application/vnd.apple.mpegurl")
	} else if strings.HasSuffix(chunk, ".m4s") {
		c.Header("Content-Type", "video/iso.segment")
	} else if strings.HasSuffix(chunk, ".m4f") || strings.HasSuffix(chunk, ".mp4") {
		c.Header("Content-Type", "video/mp4")
	}

	c.File(filepath.Join(outDir, chunk))
}

func cleanupDeadStreams() {
	for {
		time.Sleep(5 * time.Second)
		streamMu.Lock()
		for file, state := range activeStreams {
			// If we haven't received a ping in 15 seconds, clean it up
			if time.Since(state.LastPing) > 15*time.Second {
				if state.Cmd != nil && state.Cmd.Process != nil {
					state.Cmd.Process.Kill()
				}
				delete(activeStreams, file)
				
				// CRITICAL FIX: Wipe the RAM disk folder to free memory when abandoned!
				os.RemoveAll(filepath.Join(ramDiskDir, file))
				log.Printf("[Morbflix] Stream abandoned. Cleaned up: %s\n", file)
			}
		}
		streamMu.Unlock()
	}
}

func pingStream(c *gin.Context) {
	file := c.PostForm("file")
	streamMu.Lock()
	if state, exists := activeStreams[file]; exists {
		state.LastPing = time.Now()
	}
	streamMu.Unlock()
	c.Status(http.StatusOK)
}

func stopStream(c *gin.Context) {
	file := c.PostForm("file")
	streamMu.Lock()
	if state, exists := activeStreams[file]; exists {
		if state.Cmd != nil && state.Cmd.Process != nil {
			state.Cmd.Process.Kill()
		}
		delete(activeStreams, file)
		// CRITICAL FIX: Wipe the RAM disk folder when user closes the tab
		os.RemoveAll(filepath.Join(ramDiskDir, file))
		log.Printf("[Morbflix] User left. Cleaned up: %s\n", file)
	}
	streamMu.Unlock()
	c.Status(http.StatusOK)
}
