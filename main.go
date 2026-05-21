package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	_ "modernc.org/sqlite" // Pure Go SQLite driver
)

var db *sql.DB

const hostMoviesDir = "./movies/"
const ramDiskDir = "/dev/shm/morbflix"

type StreamState struct {
	Cmd   *exec.Cmd
	Start string
	Subs  string
}

var activeStreams = make(map[string]*StreamState)
var streamMu sync.Mutex

func initDB() {
	var err error
	db, err = sql.Open("sqlite", "./morbflix.db")
	if err != nil {
		log.Fatal(err)
	}

	// SCHEMA UPDATE: Added "folder" column
	query := `CREATE TABLE IF NOT EXISTS movies (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		hash TEXT,
		title TEXT,
		folder TEXT,
		file_path TEXT UNIQUE,
		duration INTEGER
	);`
	if _, err = db.Exec(query); err != nil {
		log.Fatal(err)
	}
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

// --- HTMX HANDLERS ---

func getLibraryHTMX(c *gin.Context) {
	folderQuery := c.Query("folder")
	var sb strings.Builder

	if folderQuery != "" {
		// 1. SHOW EPISODES INSIDE A FOLDER
		rows, err := db.Query("SELECT title, file_path, duration FROM movies WHERE folder = ? ORDER BY title ASC", folderQuery)
		if err == nil {
			defer rows.Close()

			// Premium Back Button
			sb.WriteString(`
				<button hx-get="/htmx/movies" hx-target="#movie-list" 
					class="w-full flex items-center justify-center gap-2 bg-panelHover hover:bg-gray-700 text-white font-medium py-3 px-4 rounded-xl mb-6 transition-all duration-200 border border-gray-800 hover:border-gray-600 shadow-sm">
					<svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M10 19l-7-7m0 0l7-7m-7 7h18"></path></svg>
					Back to Folders
				</button>`)

			// Folder Title
			sb.WriteString(fmt.Sprintf(`
				<div class="flex items-center gap-3 mb-6 px-2">
					<svg class="w-8 h-8 text-morb" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M3 7v10a2 2 0 002 2h14a2 2 0 002-2V9a2 2 0 00-2-2h-6l-2-2H5a2 2 0 00-2 2z"></path></svg>
					<h3 class="text-2xl font-bold text-white tracking-tight">%s</h3>
				</div>`, folderQuery))

			count := 0
			for rows.Next() {
				count++
				var title, filePath string
				var duration int
				rows.Scan(&title, &filePath, &duration)
				targetUrl := fmt.Sprintf("/watch?v=%s&d=%d", url.QueryEscape(filePath), duration)

				sb.WriteString(fmt.Sprintf(`
					<a href="%s" class="group flex items-center justify-between p-4 bg-panel hover:bg-morb text-gray-300 hover:text-white rounded-xl mb-3 transition-all duration-300 border border-transparent shadow-sm hover:shadow-morb/20">
						<div class="flex items-center gap-4">
							<div class="bg-gray-800 group-hover:bg-black/20 p-2 rounded-full transition-colors">
								<svg class="w-5 h-5 text-gray-400 group-hover:text-white" fill="currentColor" viewBox="0 0 20 20"><path d="M4 4l12 6-12 6z"></path></svg>
							</div>
							<span class="font-medium truncate max-w-md">%s</span>
						</div>
						<span class="text-sm font-mono text-gray-500 group-hover:text-white/80 transition-colors bg-dark/50 px-2 py-1 rounded">%d:%02d</span>
					</a>
				`, targetUrl, title, duration/60, duration%60))
			}
			if count == 0 {
				sb.WriteString(`<div class="text-center py-10 text-gray-500">No episodes found.</div>`)
			}
		}
	} else {
		// 2. SHOW ROOT FOLDERS AND STANDALONE MOVIES
		folderRows, _ := db.Query("SELECT folder, COUNT(*) FROM movies WHERE folder != '' GROUP BY folder ORDER BY folder ASC")
		defer folderRows.Close()

		count := 0
		sb.WriteString(`<div class="grid grid-cols-1 md:grid-cols-2 gap-4">`)
		for folderRows.Next() {
			count++
			var folder string
			var items int
			folderRows.Scan(&folder, &items)
			sb.WriteString(fmt.Sprintf(`
				<div hx-get="/htmx/movies?folder=%s" hx-target="#movie-list" 
					class="group cursor-pointer flex flex-col justify-between p-5 bg-panel hover:bg-panelHover rounded-xl transition-all duration-300 border border-gray-800 hover:border-morb/50 hover:-translate-y-1 shadow-sm">
					<div class="flex items-center gap-3 mb-2">
						<svg class="w-7 h-7 text-gray-400 group-hover:text-morb transition-colors" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M3 7v10a2 2 0 002 2h14a2 2 0 002-2V9a2 2 0 00-2-2h-6l-2-2H5a2 2 0 00-2 2z"></path></svg>
						<span class="font-bold text-white text-lg truncate">%s</span>
					</div>
					<span class="text-sm font-medium text-gray-500">%d items</span>
				</div>
			`, url.QueryEscape(folder), folder, items))
		}
		sb.WriteString(`</div>`)

		// Get standalone movies
		movieRows, _ := db.Query("SELECT title, file_path, duration FROM movies WHERE folder = '' ORDER BY title ASC")
		defer movieRows.Close()

		var moviesSb strings.Builder
		movieCount := 0
		for movieRows.Next() {
			count++
			movieCount++
			var title, filePath string
			var duration int
			movieRows.Scan(&title, &filePath, &duration)
			targetUrl := fmt.Sprintf("/watch?v=%s&d=%d", url.QueryEscape(filePath), duration)
			moviesSb.WriteString(fmt.Sprintf(`
				<a href="%s" class="group flex items-center justify-between p-4 bg-panel hover:bg-morb text-gray-300 hover:text-white rounded-xl mb-3 transition-all duration-300 border border-transparent shadow-sm hover:shadow-morb/20">
					<div class="flex items-center gap-4">
						<div class="bg-gray-800 group-hover:bg-black/20 p-2 rounded-full transition-colors">
							<svg class="w-5 h-5 text-gray-400 group-hover:text-white" fill="currentColor" viewBox="0 0 20 20"><path d="M4 4l12 6-12 6z"></path></svg>
						</div>
						<span class="font-medium truncate max-w-md">%s</span>
					</div>
					<span class="text-sm font-mono text-gray-500 group-hover:text-white/80 transition-colors bg-dark/50 px-2 py-1 rounded">%d:%02d</span>
				</a>
			`, targetUrl, title, duration/60, duration%60))
		}

		if movieCount > 0 {
			sb.WriteString(`<h4 class="text-gray-400 font-semibold tracking-wide uppercase text-sm mt-8 mb-4 px-1">Standalone Movies</h4>`)
			sb.WriteString(moviesSb.String())
		}

		if count == 0 {
			sb.WriteString(`<div class="text-center py-12 text-gray-500 bg-panel rounded-xl border border-gray-800 mt-4">
				<svg class="w-12 h-12 mx-auto text-gray-600 mb-3" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="1.5" d="M7 4v16M17 4v16M3 8h4m10 0h4M3 12h18M3 16h4m10 0h4M4 20h16a1 1 0 001-1V5a1 1 0 00-1-1H4a1 1 0 00-1 1v14a1 1 0 001 1z"></path></svg>
				<p>Your library is empty.</p><p class="text-sm mt-1">Click "Scan Library" above to find files.</p>
			</div>`)
		}
	}

	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(sb.String()))
}

func scanLibraryHTMX(c *gin.Context) {
	log.Println("[Morbflix] Scanning movies directory...")
	videos := findAllVideos(hostMoviesDir)
	for _, videoPath := range videos {
		relPath, _ := filepath.Rel(hostMoviesDir, videoPath)
		title := strings.TrimSuffix(filepath.Base(videoPath), filepath.Ext(videoPath))
		duration := getVideoDuration(videoPath)
		dummyHash := fmt.Sprintf("local-%x", title) // basic hash

		// Determine if it's in a folder or root
		dir := filepath.Dir(relPath)
		if dir == "." {
			dir = "" // Root movie
		}

		db.Exec("INSERT OR IGNORE INTO movies (hash, title, folder, file_path, duration) VALUES (?, ?, ?, ?, ?)", dummyHash, title, dir, relPath, duration)
	}
	getLibraryHTMX(c)
}

func getVideoNavHTMX(c *gin.Context) {
	currentUrl := c.GetHeader("HX-Current-URL")
	u, err := url.Parse(currentUrl)
	if err != nil {
		c.String(http.StatusOK, "")
		return
	}

	currentPath := u.Query().Get("v")
	if currentPath == "" {
		c.String(http.StatusOK, "")
		return
	}

	var currentTitle, currentFolder string
	err = db.QueryRow("SELECT title, folder FROM movies WHERE file_path = ?", currentPath).Scan(&currentTitle, &currentFolder)
	if err != nil {
		c.String(http.StatusOK, "")
		return
	}

	type NavItem struct {
		Title    string
		Path     string
		Duration int
	}
	var next, prev NavItem

	hasPrev := db.QueryRow("SELECT title, file_path, duration FROM movies WHERE folder = ? AND title < ? ORDER BY title DESC LIMIT 1", currentFolder, currentTitle).Scan(&prev.Title, &prev.Path, &prev.Duration) == nil
	hasNext := db.QueryRow("SELECT title, file_path, duration FROM movies WHERE folder = ? AND title > ? ORDER BY title ASC LIMIT 1", currentFolder, currentTitle).Scan(&next.Title, &next.Path, &next.Duration) == nil

	var sb strings.Builder
	sb.WriteString(`<div class="flex items-center justify-between w-full max-w-5xl mx-auto mt-6 bg-panel p-4 rounded-xl border border-gray-800 shadow-xl">`)

	if hasPrev {
		target := fmt.Sprintf("/watch?v=%s&d=%d", url.QueryEscape(prev.Path), prev.Duration)
		sb.WriteString(fmt.Sprintf(`
			<a href="%s" class="flex-1 flex items-center gap-2 px-4 py-2 bg-panelHover hover:bg-morb text-white rounded-lg font-medium transition-colors group">
				<svg class="w-5 h-5 text-gray-400 group-hover:text-white transition-colors" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M11 19l-7-7 7-7m8 14l-7-7 7-7"></path></svg>
				<span class="truncate hidden md:block">%s</span>
			</a>`, target, prev.Title))
	} else {
		sb.WriteString(`<div class="flex-1"></div>`)
	}

	sb.WriteString(fmt.Sprintf(`<span class="flex-[2] text-center text-gray-300 font-semibold text-lg truncate px-6">%s</span>`, currentTitle))

	if hasNext {
		target := fmt.Sprintf("/watch?v=%s&d=%d", url.QueryEscape(next.Path), next.Duration)
		sb.WriteString(fmt.Sprintf(`
			<a href="%s" class="flex-1 flex justify-end items-center gap-2 px-4 py-2 bg-panelHover hover:bg-morb text-white rounded-lg font-medium transition-colors group">
				<span class="truncate hidden md:block">%s</span>
				<svg class="w-5 h-5 text-gray-400 group-hover:text-white transition-colors" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M13 5l7 7-7 7M5 5l7 7-7 7"></path></svg>
			</a>`, target, next.Title))
	} else {
		sb.WriteString(`<div class="flex-1"></div>`)
	}

	sb.WriteString(`</div>`)
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(sb.String()))
}

func addTorrentHTMX(c *gin.Context) {
	magnet := c.PostForm("magnet")
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	writer.WriteField("urls", magnet)
	writer.Close()

	req, _ := http.NewRequest("POST", "http://127.0.0.1:8090/api/v2/torrents/add", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	client := &http.Client{}
	client.Do(req)

	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(`
		<input type="text" id="magnet-input" name="magnet" placeholder="Torrent Added! Paste another link..." required 
			class="flex-1 px-4 py-3 rounded-lg border-2 border-green-500/50 bg-green-500/10 text-white placeholder-green-200 focus:outline-none focus:border-green-500 transition-colors shadow-inner">
	`))
}

func getTorrentStatusHTMX(c *gin.Context) {
	resp, err := http.Get("http://127.0.0.1:8090/api/v2/torrents/info")
	if err != nil {
		c.String(http.StatusInternalServerError, `<div class="text-red-400 bg-red-900/20 p-4 rounded-lg border border-red-900/50">Error contacting qBittorrent</div>`)
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var result []struct {
		Name     string  `json:"name"`
		Progress float64 `json:"progress"`
	}
	json.Unmarshal(body, &result)

	var sb strings.Builder
	count := 0
	for _, t := range result {
		if t.Progress < 1.0 {
			count++
			pct := t.Progress * 100
			sb.WriteString(fmt.Sprintf(`
				<div class="mb-4 p-5 bg-panelHover rounded-xl border border-gray-800 shadow-sm hover:border-gray-600 transition-colors">
					<div class="flex justify-between items-center mb-3">
						<strong class="text-sm font-medium text-gray-200 truncate pr-4">%s</strong>
						<span class="text-sm font-bold text-morb bg-morb/10 px-2 py-1 rounded-md">%.1f%%</span>
					</div>
					<div class="w-full bg-gray-900 rounded-full h-2.5 overflow-hidden ring-1 ring-inset ring-black/20">
						<div class="bg-gradient-to-r from-morb to-red-400 h-2.5 rounded-full transition-all duration-500 ease-out" style="width: %.1f%%"></div>
					</div>
				</div>
			`, t.Name, pct, pct))
		}
	}
	if count == 0 {
		sb.WriteString(`
			<div class="flex flex-col items-center justify-center py-10 text-gray-500">
				<svg class="w-12 h-12 mb-3 text-gray-700" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="1.5" d="M9 12l2 2 4-4m6 2a9 9 0 11-18 0 9 9 0 0118 0z"></path></svg>
				<p>No active downloads.</p>
			</div>
		`)
	}
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(sb.String()))
}

func handleTorrentCompleted(c *gin.Context) {
	hash := c.PostForm("hash")
	name := c.PostForm("name")
	if name == "" {
		return
	}
	fullPath := filepath.Join(hostMoviesDir, name)
	videos := findAllVideos(fullPath)

	for _, videoPath := range videos {
		relPath, _ := filepath.Rel(hostMoviesDir, videoPath)
		title := strings.TrimSuffix(filepath.Base(videoPath), filepath.Ext(videoPath))
		duration := getVideoDuration(videoPath)

		dir := filepath.Dir(relPath)
		if dir == "." {
			dir = ""
		}

		_, err := db.Exec("INSERT OR IGNORE INTO movies (hash, title, folder, file_path, duration) VALUES (?, ?, ?, ?, ?)", hash, title, dir, relPath, duration)
		if err == nil {
			log.Printf("[Morbflix] Indexed Episode: %s\n", title)
		}
	}
	c.JSON(http.StatusOK, gin.H{"message": "Indexed"})
}

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
				activeStreams[filename] = &StreamState{Cmd: cmd, Start: startParam, Subs: subsParam}
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

func main() {
	os.RemoveAll(ramDiskDir)
	os.MkdirAll(ramDiskDir, 0755)

	initDB()

	r := gin.Default()
	r.Static("/static", "./static")

	r.GET("/", func(c *gin.Context) { c.File("./index.html") })
	r.GET("/watch", func(c *gin.Context) { c.File("./watch.html") })
	r.GET("/library", func(c *gin.Context) { c.File("./library.html") })
	r.GET("/downloads", func(c *gin.Context) { c.File("./downloads.html") })

	r.GET("/htmx/movies", getLibraryHTMX)
	r.POST("/htmx/library/scan", scanLibraryHTMX)
	r.POST("/htmx/torrent/add", addTorrentHTMX)
	r.GET("/htmx/torrent/status", getTorrentStatusHTMX)
	r.GET("/htmx/video/nav", getVideoNavHTMX)

	r.POST("/morb/torrent/completed", handleTorrentCompleted)
	r.GET("/video/hls/*filepath", serveHLS)

	log.Println("Morbflix Go Server starting on port 3000...")
	r.Run(":3000")
}
