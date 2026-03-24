package main

import (
	"embed"
	"io/fs"
	"log"
)

//go:embed all:dist
var guiAssets embed.FS

func guiFS() fs.FS {
	sub, err := fs.Sub(guiAssets, "dist")
	if err != nil {
		log.Fatalf("failed to create GUI sub-filesystem: %v", err)
	}
	return sub
}
