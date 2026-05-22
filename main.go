package main

import (
	"database/sql"
	"log"
	"morbflix/views"
	"os"

	"github.com/a-h/templ"
	"github.com/gin-gonic/gin"
	_ "modernc.org/sqlite" // Pure Go SQLite driver
)

var db *sql.DB

const hostMoviesDir = "./movies/"
const ramDiskDir = "/dev/shm/morbflix"

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

func render(c *gin.Context, status int, template templ.Component) {
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.Status(status)
	template.Render(c.Request.Context(), c.Writer)
}

func main() {
	os.RemoveAll(ramDiskDir)
	os.MkdirAll(ramDiskDir, 0755)

	initDB()

	go cleanupDeadStreams()

	gin.SetMode(gin.ReleaseMode)
	r := gin.Default()
	r.Static("/static", "./static")

	r.GET("/", func(c *gin.Context) { render(c, 200, views.Home()) })
	r.GET("/watch", func(c *gin.Context) { render(c, 200, views.Watch()) })
	r.GET("/library", func(c *gin.Context) { render(c, 200, views.Library()) })
	r.GET("/downloads", func(c *gin.Context) { render(c, 200, views.Downloads()) })

	r.GET("/htmx/movies", getLibraryHTMX)
	r.POST("/htmx/library/scan", scanLibraryHTMX)
	r.POST("/htmx/torrent/add", addTorrentHTMX)
	r.GET("/htmx/torrent/status", getTorrentStatusHTMX)
	r.GET("/htmx/video/nav", getVideoNavHTMX)

	r.POST("/morb/stream/ping", pingStream)
	r.POST("/morb/stream/stop", stopStream)
	r.POST("/morb/torrent/completed", handleTorrentCompleted)

	r.GET("/video/hls/*filepath", serveHLS)

	log.Println("Morbflix Go Server starting on port 3000...")
	r.Run(":3000")
}
