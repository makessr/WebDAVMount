package main

import (
	"embed"

	"webdavmount/desktop/windows"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	desktop.Run(assets)
}
