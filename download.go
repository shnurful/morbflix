package main

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"mime/multipart"
	"morbflix/views"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
)

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

func getTorrentStatusHTMX(c *gin.Context) {
	resp, err := http.Get("http://127.0.0.1:8090/api/v2/torrents/info")
	if err != nil {
		c.String(http.StatusInternalServerError, "<p>Error contacting qBittorrent</p>")
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var rawData []struct {
		Name     string  `json:"name"`
		Progress float64 `json:"progress"`
	}
	json.Unmarshal(body, &rawData)

	var torrents []views.Torrent
	for _, t := range rawData {
		if t.Progress < 1.0 {
			torrents = append(torrents, views.Torrent{Name: t.Name, Progress: t.Progress * 100})
		}
	}

	render(c, 200, views.TorrentList(torrents))
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

	render(c, 200, views.TorrentAddedInput())
}
