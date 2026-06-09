package main

import (
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
		
		// If the playlist doesn't exist, we start the stream!
		if _, err := os.Stat(playlistPath); os.IsNotExist(err) {
			os.RemoveAll(outDir)
			os.MkdirAll(outDir, 0755)

			videoFilePath := filepath.Join(hostMoviesDir, filename)
			segmentPath := filepath.Join(outDir, "chunk_%03d.m4s")

			if _, err := os.Stat(videoFilePath); os.IsNotExist(err) {
				streamMu.Unlock()
				c.JSON(http.StatusNotFound, gin.H{"error": "movie not found"})
				return
			}

			// 0% CPU COMMAND: Direct Copy H.265 into Fragmented MP4 chunks
			ffmpegArgs := []string{
				"-i", videoFilePath,
				"-c:v", "copy",    // ZERO CPU USAGE!
				"-c:a", "aac",     // Transcode audio for browsers
				"-ac", "2",
				"-b:a", "128k",
				"-sn", "-dn",      // Strip subs/fonts (WASM handles them)
				"-f", "hls",
				"-hls_time", "2",
				"-hls_list_size", "0",
				"-hls_playlist_type", "event",
				"-hls_segment_type", "fmp4",
				"-hls_segment_filename", segmentPath,
				playlistPath,
			}

			cmd := exec.Command("ffmpeg", ffmpegArgs...)
			cmd.Stderr = os.Stderr

			if err := cmd.Start(); err == nil {
				// We don't need Start or Subs in the struct anymore!
				activeStreams[filename] = &StreamState{Cmd: cmd, LastPing: time.Now()}
				go func() {
					_ = cmd.Wait()
					streamMu.Lock()
					if activeStreams[filename] != nil {
						delete(activeStreams, filename)
					}
					streamMu.Unlock()
				}()
			}

			// Wait for the first MP4 chunk before responding
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
					streamMu.Unlock()
					c.JSON(http.StatusInternalServerError, gin.H{"error": "timeout"})
					return
				}
				time.Sleep(100 * time.Millisecond)
			}
		} else {
			// If it already exists, just update the heartbeat!
			if state, exists := activeStreams[filename]; exists {
				state.LastPing = time.Now()
			}
		}
		streamMu.Unlock()
	}

	c.Header("Cache-Control", "no-cache, no-store, must-revalidate")
	c.Header("Pragma", "no-cache")
	c.Header("Expires", "0")

	if strings.HasSuffix(chunk, ".m3u8") {
		c.Header("Content-Type", "application/vnd.apple.mpegurl")
	} else if strings.HasSuffix(chunk, ".m4s") || strings.HasSuffix(chunk, ".mp4") {
		c.Header("Content-Type", "video/mp4") // Correct MIME type for fMP4
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
