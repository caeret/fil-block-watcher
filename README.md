Tool to watch fil p2p block message.

## Usage

```go
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/caeret/logging"
	"github.com/filecoin-project/lotus/chain/types"
	"github.com/libp2p/go-libp2p/core/crypto"

	filblockwatcher "github.com/caeret/fil-block-watcher"
)

func main() {
	logger := logging.NewDefault()

	prik, _, err := crypto.GenerateKeyPair(crypto.RSA, 2048)
	if err != nil {
		logger.Error("fail to generate key.", "error", err)
		os.Exit(1)
	}

	options := []filblockwatcher.Option{
		filblockwatcher.WithLogger(logger),
		filblockwatcher.BootstrapTimeout(time.Second * 15),
		filblockwatcher.WithBlockEventHandler(func(msg types.BlockMsg) {
			fmt.Println("-> received new block msg.", msg.Header.Miner, msg.Header.Cid(), msg.Header.Height)
		}),
	}

	node, err := filblockwatcher.NewNode(context.TODO(), prik, options...)
	if err != nil {
		logger.Error("fail to create node.", "error", err)
		os.Exit(1)
	}

	err = node.Run(context.TODO())
	if err != nil {
		logger.Error("fail to run node.", "error", err)
		os.Exit(1)
	}

	go func() {
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, syscall.SIGINT)
		<-ch
		node.Close()
	}()

	node.Wait()
}
```