package filblockwatcher

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/caeret/logging"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/peerstore"
	ma "github.com/multiformats/go-multiaddr"
)

func convertPeers(peers []string) ([]peer.AddrInfo, error) {
	pinfos := make([]peer.AddrInfo, len(peers))
	for i, addr := range peers {
		maddr := ma.StringCast(addr)
		p, err := peer.AddrInfoFromP2pAddr(maddr)
		if err != nil {
			return nil, err
		}
		pinfos[i] = *p
	}
	return pinfos, nil
}

func bootstrapConnect(ctx context.Context, logger logging.Logger, ph host.Host, peers []peer.AddrInfo) error {
	if len(peers) < 1 {
		return errors.New("not enough bootstrap peers")
	}

	errs := make(chan error, len(peers))
	var wg sync.WaitGroup
	for _, p := range peers {
		wg.Add(1)
		go func(p peer.AddrInfo) {
			defer wg.Done()
			defer logger.Debug("finish bootstrap to peer.", "peer", p.ID)
			logger.Debug("start bootstrap to peer.", "peer", p.ID)

			ph.Peerstore().AddAddrs(p.ID, p.Addrs, peerstore.PermanentAddrTTL)
			if err := ph.Connect(ctx, p); err != nil {
				logger.Warn("failed to bootstrap", "peer", p.ID, "error", err)
				errs <- err
				return
			}
			logger.Debug("bootstrap successfully.", "peer", p.ID)
		}(p)
	}
	wg.Wait()
	close(errs)
	count := 0
	var err error
	for err = range errs {
		if err != nil {
			count++
		}
	}
	logger.Debug("bootstrap all finish.", "success", len(peers)-count)
	if count == len(peers) {
		return fmt.Errorf("fail to bootstrap: %w", err)
	}
	return nil
}
