package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	sdk "github.com/seiortech/letngorok-go-sdk"
)

func main() {
	config := sdk.DefaultConfig("ngorok.site:9000")
	config.LocalPort = "8080"                             // this used to test the tunnel on your loca  machine
	config.AuthToken = "6lB1JCZ3LS0VygbH3YfobSnKOIbhW1yr" // replace with your token

	client, err := sdk.NewClient(config, "")
	if err != nil {
		log.Fatalln(err)
	}

	if c, ok := client.(*sdk.TunnelClient); ok {
		c.EnableLogging(os.Stdout)
	}

	go func() {
		if err := client.Start(); err != nil {
			log.Fatalln(err)
		}
		log.Println("Started")
	}()

	for client.Status() != sdk.StatusConnected {
	}

	localURL, prodURL := client.URLs()
	fmt.Printf("Tunnel established!\n")
	fmt.Printf("Local URL: %s\n", localURL)
	fmt.Printf("Production URL: %s\n", prodURL)

	signalCh := make(chan os.Signal, 1)
	signal.Notify(signalCh, syscall.SIGINT, syscall.SIGTERM)
	<-signalCh

	fmt.Println("Shutting down tunnel...")
	client.Stop()
}
