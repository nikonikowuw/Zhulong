package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/nikonikowuw/Zhulong/internal/app"
	"github.com/nikonikowuw/Zhulong/internal/network"
	"go.uber.org/zap"
)

// @title           Zhulong API
// @version         1.0
// @description     Local API for the Zhulong edge operations host.
// @BasePath        /api/v1
func main() {
	resetNetwork := flag.Bool("reset-network", false, "Emergency recovery: reset primary network interface to factory maintenance IP (192.168.1.168/24) and all others to DHCP")
	flag.Parse()

	config, err := loadConfig()
	if err != nil {
		log.Fatal(err)
	}

	if *resetNetwork {
		logger, _ := zap.NewProduction()
		if config.Development {
			logger, _ = zap.NewDevelopment()
		}
		defer func() { _ = logger.Sync() }()

		provider := network.DetectProvider(config.CustomScript, logger)
		watchdog := network.NewWatchdogManager(config.DataDir, provider, logger)
		svc := network.NewNetworkService(provider, watchdog, logger)

		ctx := context.Background()
		if err := svc.ResetNetwork(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "Error resetting network: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Zhulong network emergency reset successfully completed:")
		fmt.Println("  - Primary interface configured to 192.168.1.168/24 (no gateway)")
		fmt.Println("  - Secondary interfaces restored to DHCP")
		os.Exit(0)
	}

	app.New(config).Run()
}
