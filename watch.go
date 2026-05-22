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
	Cmd      *exec.Cmd
	Start    string
	Subs     string
	LastPing time.Time // NEW: Track the last heartbeat
}

var activeStreams = make(map[string]*StreamState)
var streamMu sync.Mutex

func serveHLS(c *gin.Context) {
	rawPath := c.Param("filepath")
	startParam := c.DefaultQuery("start", "0")
	subsParam := c.DefaultQuery("subs", "0")

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

		needsRestart := !exists || state.Start != startParam || state.Subs != subsParam

		if needsRestart {
			if exists && state.Cmd.Process != nil {
				state.Cmd.Process.Kill()
			}

			os.RemoveAll(outDir)
			os.MkdirAll(outDir, 0755)

			videoFilePath := filepath.Join(hostMoviesDir, filename)
			segmentPath := filepath.Join(outDir, "chunk_%03d.ts")

			if _, err := os.Stat(videoFilePath); os.IsNotExist(err) {
				streamMu.Unlock()
				c.JSON(http.StatusNotFound, gin.H{"error": "movie not found"})
				return
			}

			ffmpegArgs := []string{"-ss", startParam}

			if subsParam == "1" {
				ffmpegArgs = append(ffmpegArgs, "-copyts")
			}

			ffmpegArgs = append(ffmpegArgs,
				"-i", videoFilePath,
				"-c:v", "libx264",
				"-preset", "ultrafast",
				"-threads", "0",
				"-crf", "23",
				"-pix_fmt", "yuv420p",
				"-g", "48",
			)

			if subsParam == "1" {
				safePath := strings.ReplaceAll(videoFilePath, `\`, `\\`)
				safePath = strings.ReplaceAll(safePath, `'`, `\'`)
				safePath = strings.ReplaceAll(safePath, `:`, `\:`)

				filterArg := fmt.Sprintf("subtitles='%s',setpts=PTS-%s/TB", safePath, startParam)
				audioArg := fmt.Sprintf("asetpts=PTS-%s/TB", startParam)

				ffmpegArgs = append(ffmpegArgs, "-vf", filterArg, "-c:a", "aac", "-ac", "2", "-af", audioArg)
			} else {
				ffmpegArgs = append(ffmpegArgs, "-c:a", "aac", "-ac", "2")
			}

			ffmpegArgs = append(ffmpegArgs,
				"-b:a", "128k",
				"-sn", "-dn",
				"-f", "hls",
				"-hls_time", "2",
				"-hls_list_size", "0",
				"-hls_playlist_type", "event",
				"-hls_segment_filename", segmentPath,
				playlistPath,
			)

			cmd := exec.Command("ffmpeg", ffmpegArgs...)
			cmd.Stderr = os.Stderr

			if err := cmd.Start(); err == nil {
				activeStreams[filename] = &StreamState{Cmd: cmd, Start: startParam, Subs: subsParam, LastPing: time.Now()}
				go func() {
					_ = cmd.Wait()
					streamMu.Lock()
					if activeStreams[filename] != nil && activeStreams[filename].Start == startParam && activeStreams[filename].Subs == subsParam {
						delete(activeStreams, filename)
					}
					streamMu.Unlock()
				}()
			}
		}
		streamMu.Unlock()

		if needsRestart {
			firstChunk := filepath.Join(outDir, "chunk_000.ts")
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

	if strings.HasSuffix(chunk, ".m3u8") {
		c.Header("Content-Type", "application/vnd.apple.mpegurl")
	} else if strings.HasSuffix(chunk, ".ts") {
		c.Header("Content-Type", "video/mp2t")
	}

	c.File(filepath.Join(outDir, chunk))
}

func cleanupDeadStreams() {
	for {
		time.Sleep(5 * time.Second)
		streamMu.Lock()
		for file, state := range activeStreams {
			// If we haven't received a ping in 15 seconds, kill the process!
			if time.Since(state.LastPing) > 15*time.Second {
				if state.Cmd.Process != nil {
					state.Cmd.Process.Kill()
				}
				delete(activeStreams, file)
				log.Printf("[Morbflix] Stream abandoned. Killed FFmpeg for: %s\n", file)
			}
		}
		streamMu.Unlock()
	}
}

func pingStream(c *gin.Context) {
	file := c.PostForm("file")
	streamMu.Lock()
	if state, exists := activeStreams[file]; exists {
		state.LastPing = time.Now() // Update the heartbeat
	}
	streamMu.Unlock()
	c.Status(http.StatusOK)
}

func stopStream(c *gin.Context) {
	file := c.PostForm("file")
	streamMu.Lock()
	if state, exists := activeStreams[file]; exists {
		if state.Cmd.Process != nil {
			state.Cmd.Process.Kill()
		}
		delete(activeStreams, file)
		log.Printf("[Morbflix] User navigated away. Killed FFmpeg for: %s\n", file)
	}
	streamMu.Unlock()
	c.Status(http.StatusOK)
}
