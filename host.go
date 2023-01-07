package filblockwatcher

import (
	"context"
	"fmt"

	datastore "github.com/ipfs/go-datastore"
	dsync "github.com/ipfs/go-datastore/sync"
	libp2p "github.com/libp2p/go-libp2p"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	routedhost "github.com/libp2p/go-libp2p/p2p/host/routed"
	ma "github.com/multiformats/go-multiaddr"
)

func makeRoutedHost(ctx context.Context, cfg config, priv crypto.PrivKey, bootstrapPeers []peer.AddrInfo) (*routedhost.RoutedHost, error) {
	opts := []libp2p.Option{
		libp2p.ListenAddrStrings(fmt.Sprintf("/ip4/0.0.0.0/tcp/%d", cfg.listenPort)),
		libp2p.Identity(priv),
		libp2p.DefaultTransports,
		libp2p.DefaultMuxers,
		libp2p.DefaultSecurity,
		libp2p.NATPortMap(),
	}

	bh, err := libp2p.New(opts...)
	if err != nil {
		return nil, err
	}

	dstore := dsync.MutexWrap(datastore.NewMapDatastore())
	dht, err := dht.New(ctx, bh,
		dht.Mode(dht.ModeAuto),
		dht.Datastore(dstore),
		dht.ProtocolPrefix("/fil/kad/testnetnet"),
	)
	if err != nil {
		return nil, err
	}
	routedHost := routedhost.Wrap(bh, dht)

	bootstrapCtx, cancel := context.WithTimeout(ctx, cfg.bootstrapTimeout)
	defer cancel()
	err = bootstrapConnect(bootstrapCtx, cfg.logger, routedHost, bootstrapPeers)
	if err != nil {
		return nil, err
	}

	err = dht.Bootstrap(ctx)
	if err != nil {
		return nil, err
	}

	hostAddr, _ := ma.NewMultiaddr(fmt.Sprintf("/ipfs/%s", routedHost.ID().String()))

	for _, addr := range routedHost.Addrs() {
		cfg.logger.Debug("service is running.", "addr", addr.Encapsulate(hostAddr))
	}

	return routedHost, nil
}
