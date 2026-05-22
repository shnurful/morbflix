package main

import (
	"fmt"
	"log"
	"morbflix/views"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
)

func getLibraryHTMX(c *gin.Context) {
	folderQuery := c.Query("folder")
	var movies []views.Movie
	var folders []views.Folder

	if folderQuery != "" {
		rows, _ := db.Query("SELECT title, file_path, duration FROM movies WHERE folder = ? ORDER BY title ASC", folderQuery)
		defer rows.Close()
		for rows.Next() {
			var m views.Movie
			rows.Scan(&m.Title, &m.Path, &m.Duration)
			movies = append(movies, m)
		}
	} else {
		folderRows, _ := db.Query("SELECT folder, COUNT(*) FROM movies WHERE folder != '' GROUP BY folder ORDER BY folder ASC")
		defer folderRows.Close()
		for folderRows.Next() {
			var f views.Folder
			folderRows.Scan(&f.Name, &f.Count)
			folders = append(folders, f)
		}

		movieRows, _ := db.Query("SELECT title, file_path, duration FROM movies WHERE folder = '' ORDER BY title ASC")
		defer movieRows.Close()
		for movieRows.Next() {
			var m views.Movie
			movieRows.Scan(&m.Title, &m.Path, &m.Duration)
			movies = append(movies, m)
		}
	}

	// Just call the component!
	render(c, 200, views.MovieList(folderQuery, movies, folders))
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
		return
	}
	currentPath := u.Query().Get("v")

	var currentTitle, currentFolder string
	db.QueryRow("SELECT title, folder FROM movies WHERE file_path = ?", currentPath).Scan(&currentTitle, &currentFolder)

	var next, prev views.NavItem
	hasPrev := db.QueryRow("SELECT title, file_path, duration FROM movies WHERE folder = ? AND title < ? ORDER BY title DESC LIMIT 1", currentFolder, currentTitle).Scan(&prev.Title, &prev.Path, &prev.Duration) == nil
	hasNext := db.QueryRow("SELECT title, file_path, duration FROM movies WHERE folder = ? AND title > ? ORDER BY title ASC LIMIT 1", currentFolder, currentTitle).Scan(&next.Title, &next.Path, &next.Duration) == nil

	var pPtr, nPtr *views.NavItem
	if hasPrev {
		pPtr = &prev
	}
	if hasNext {
		nPtr = &next
	}

	render(c, 200, views.VideoNav(currentTitle, pPtr, nPtr))
}
