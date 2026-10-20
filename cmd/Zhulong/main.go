package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/nikonikowuw/Zhulong/internal/app"
	"github.com/nikonikowuw/Zhulong/internal/network"
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
		fmt.Fprintf(os.Stderr, "configuration error: %v\n", err)
		os.Exit(1)
	}

	if *resetNetwork {
		if err := resetNetworkCommand(config); err != nil {
			fmt.Fprintf(os.Stderr, "network reset failed: %v\n", err)
			os.Exit(1)
		}
		return
	}

	app.New(config).Run()
}

func resetNetworkCommand(config app.Config) (resultErr error) {
	logger, _, sink, err := app.NewLogger(config)
	if err != nil {
		return fmt.Errorf("logger initialization failed: %w", err)
	}
	defer func() {
		resultErr = errors.Join(resultErr, logger.Sync(), sink.Close())
	}()
	if err := sink.Prepare(); err != nil {
		return fmt.Errorf("logger sink initialization failed: %w", err)
	}

	provider := network.DetectProvider(config.CustomScript, logger)
	watchdog := network.NewWatchdogManager(config.DataDir, provider, logger)
	svc := network.NewNetworkService(provider, watchdog, logger)

	if err := svc.ResetNetwork(context.Background()); err != nil {
		return err
	}
	fmt.Println("Zhulong network emergency reset successfully completed:")
	fmt.Println("  - Primary interface configured to 192.168.1.168/24 (no gateway)")
	fmt.Println("  - Secondary interfaces restored to DHCP")
	return nil
}
