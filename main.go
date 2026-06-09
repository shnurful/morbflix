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

        // 1. Create the Route Group for the /morbflix subpath
        morbflixGroup := r.Group("/morbflix")
        {
                // 2. Serve static assets inside this group (accessible at /morbflix/static)
                morbflixGroup.Static("/static", "./static")

                // 3. Page routes (now at /morbflix, /morbflix/watch, etc.)
                morbflixGroup.GET("", func(c *gin.Context) { render(c, 200, views.Home()) })
                morbflixGroup.GET("/", func(c *gin.Context) { render(c, 200, views.Home()) })
                morbflixGroup.GET("/watch", func(c *gin.Context) { render(c, 200, views.Watch()) })
                morbflixGroup.GET("/library", func(c *gin.Context) { render(c, 200, views.Library()) })
                morbflixGroup.GET("/downloads", func(c *gin.Context) { render(c, 200, views.Downloads()) })

                // 4. HTMX routes (now prefixed with /morbflix)
                morbflixGroup.GET("/htmx/movies", getLibraryHTMX)
                morbflixGroup.POST("/htmx/library/scan", scanLibraryHTMX)
                morbflixGroup.POST("/htmx/torrent/add", addTorrentHTMX)
                morbflixGroup.GET("/htmx/torrent/status", getTorrentStatusHTMX)
                morbflixGroup.GET("/htmx/video/nav", getVideoNavHTMX)

                // 5. App-specific API routes (now prefixed with /morbflix)
                morbflixGroup.GET("/morb/subs/*filepath", extractSubs)
                morbflixGroup.POST("/morb/stream/ping", pingStream)
                morbflixGroup.POST("/morb/stream/stop", stopStream)
                morbflixGroup.POST("/morb/torrent/completed", handleTorrentCompleted)

                // 6. Video streaming routes (now prefixed with /morbflix)
                morbflixGroup.GET("/video/hls/*filepath", serveHLS)
        }

        log.Println("Morbflix Go Server starting on port 3000...")
        r.Run(":3000")
}
