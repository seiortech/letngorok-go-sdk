package main

import (
	"log"

	sdk "github.com/seiortech/letngorok-go-sdk"
)

func main() {
	client, err := sdk.NewTunnelClient(nil, "YTLc3n8fjd8tIdFUnfGPRgD1Sjmi6flb")
	if err != nil {
		log.Fatalln(err)
	}

	client.Start("8080", nil)
}
