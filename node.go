package filblockwatcher

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/caeret/logging"
	"github.com/filecoin-project/lotus/chain/types"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/event"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/host/eventbus"
	routedhost "github.com/libp2p/go-libp2p/p2p/host/routed"
)

const (
	HelloProtocol = "/fil/hello/1.0.0"
	BlockProtocol = "/fil/blocks/testnetnet"
)

type Node struct {
	logger logging.Logger
	ha     *routedhost.RoutedHost
	hello  atomic.Value
}

func NewNode(ctx context.Context, opts ...Option) (*Node, error) {
	cfg := defaultCfg
	for _, opt := range opts {
		opt(&cfg)
	}
	peers, err := convertPeers(strings.Split(FilPeers, "\n"))
	if err != nil {
		return nil, err
	}
	bootstrapCtx, cancel := context.WithTimeout(ctx, cfg.bootstrapTimeout)
	defer cancel()
	ha, err := makeRoutedHost(bootstrapCtx, cfg.logger, cfg.listenPort, 0, peers)
	if err != nil {
		return nil, err
	}
	return &Node{logger: cfg.logger, ha: ha}, nil
}

func (n *Node) Run() error {
	n.ha.SetStreamHandler(HelloProtocol, n.handleHello)
	sub, err := n.ha.EventBus().Subscribe(new(event.EvtPeerIdentificationCompleted), eventbus.BufSize(1024))
	if err != nil {
		return fmt.Errorf("failed to subscribe to event bus: %w", err)
	}
	go n.runSayHello(sub)

	return n.handleIncomingBlocks()
}

func (n *Node) handleHello(s network.Stream) {
	n.logger.Debug("got new stream!", "id", s.ID(), "addr", s.Conn().RemoteMultiaddr(), "peer", s.Conn().RemotePeer())
	var hmsg HelloMessage
	if err := hmsg.UnmarshalCBOR(s); err != nil {
		n.logger.Warn("failed to read hello message, disconnecting", "error", err)
		_ = s.Conn().Close()
		return
	}
	arrived := time.Now()

	n.hello.Store(hmsg)

	n.logger.Debug("received hello.", "addr", s.Conn().RemoteMultiaddr(), "peer", s.Conn().RemotePeer(), "height", hmsg.HeaviestTipSetHeight)

	defer s.Close()
	sent := time.Now()
	msg := &LatencyMessage{
		TArrival: arrived.UnixNano(),
		TSent:    sent.UnixNano(),
	}
	_ = msg.MarshalCBOR(s)
}

func (n *Node) runSayHello(sub event.Subscription) {
	for evt := range sub.Out() {
		pic := evt.(event.EvtPeerIdentificationCompleted)
		go func() {
			if err := n.sayHello(context.TODO(), pic.Peer); err != nil {
				n.logger.Debug("failed to say hello", "error", err, "peer", pic.Peer)
				return
			}
		}()
	}
}

func (n *Node) sayHello(ctx context.Context, pid peer.ID) error {
	n.logger.Debug("say hello", "peer", pid, "addr", n.ha.Peerstore().Addrs(pid))

	hmsg, ok := n.hello.Load().(HelloMessage)
	if !ok {
		n.logger.Warn("skip for no hello.")
		return nil
	}

	s, err := n.ha.NewStream(ctx, pid, HelloProtocol)
	if err != nil {
		return err
	}

	defer s.Close()

	err = hmsg.MarshalCBOR(s)
	if err != nil {
		return err
	}

	lmsg := &LatencyMessage{}
	_ = s.SetReadDeadline(time.Now().Add(time.Second * 10))
	err = lmsg.UnmarshalCBOR(s)
	if err != nil {
		n.logger.Warn("fail to read latency message.", "error", err)
		return nil
	}

	return nil
}

func (n *Node) handleIncomingBlocks() error {
	ps, err := pubsub.NewGossipSub(context.TODO(), n.ha)
	if err != nil {
		return nil
	}
	sub, err := ps.Subscribe(BlockProtocol)
	if err != nil {
		return err
	}
	for {
		msg, err := sub.Next(context.TODO())
		if err != nil {
			n.logger.Error("fail to reading next block.", "error", err)
			continue
		}

		bmsg, err := types.DecodeBlockMsg(msg.GetData())
		if err != nil {
			n.logger.Error("fail to decode block", "error", err)
			continue
		}

		n.logger.Info("received new block msg.", "miner", bmsg.Header.Miner, "cid", bmsg.Cid(), "height", bmsg.Header.Height)
	}
}

func (n *Node) Peers() peer.IDSlice {
	return n.ha.Peerstore().PeersWithAddrs()
}
