package main

import (
	"log"

	"github.com/nikonikowuw/Zhulong/internal/app"
)

// @title           Zhulong API
// @version         1.0
// @description     Local API for the Zhulong edge operations host.
// @BasePath        /api/v1
func main() {
	config, err := loadConfig()
	if err != nil {
		log.Fatal(err)
	}

	app.New(config).Run()
}
