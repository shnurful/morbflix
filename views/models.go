package views

import "fmt"

type Movie struct {
	Title    string
	Path     string
	Duration int
}

type Folder struct {
	Name  string
	Count int
}

type Torrent struct {
	Name     string
	Progress float64
}

type NavItem struct {
	Title    string
	Path     string
	Duration int
}

// Quick helper to format duration inside the templates
func FormatDuration(seconds int) string {
	return fmt.Sprintf("%d:%02d", seconds/60, seconds%60)
}
