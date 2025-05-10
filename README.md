# Letngorok Golang SDK

`Letngorok Golang SDK`  is a Golang SDK for creating tunnel connections to Letngorok servers.

## Installation

```bash
go get github.com/letngorok/letngorok-go
```

## Usage

First, you need to get the token from Letngorok. You can get it from the [Letngorok Dashboard](https://letngorok.studio/). Then, you can create a tunnel client with the token.

```go
package main

import (
	"log"

	sdk "github.com/seiortech/letngorok-go-sdk"
)

func main() {
	client, err := sdk.NewTunnelClient(nil, "set-your-token-here")
	if err != nil {
		log.Fatalln(err)
	}

	client.Start("set-your-local-port-here", nil)
}
```

## Notes
- Currently, the local test server didn't works. I'm currently still working on the local analytics dashboard.
