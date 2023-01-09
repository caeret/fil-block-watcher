package filblockwatcher

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/caeret/logging"
	"github.com/filecoin-project/lotus/chain/types"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/event"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/host/eventbus"
	routedhost "github.com/libp2p/go-libp2p/p2p/host/routed"

	"github.com/caeret/fil-block-watcher/build"
)

const (
	HelloProtocol = "/fil/hello/1.0.0"
	BlockProtocol = "/fil/blocks/" + build.NetWorkName
)

type BlockEventHandler func(msg types.BlockMsg)

type Node struct {
	logger logging.Logger
	ha     *routedhost.RoutedHost
	ps     *pubsub.PubSub

	hello     *HelloMessage
	helloCond *sync.Cond
	handler   BlockEventHandler

	cancels []func()
	wg      sync.WaitGroup
}

func NewNode(ctx context.Context, privkey crypto.PrivKey, opts ...Option) (*Node, error) {
	cfg := defaultCfg
	for _, opt := range opts {
		opt(&cfg)
	}

	if privkey == nil {
		return nil, errors.New("private key is required")
	}

	peers, err := convertPeers(strings.Split(build.FilPeers, "\n"))
	if err != nil {
		return nil, err
	}
	ha, err := makeRoutedHost(ctx, cfg, privkey, peers)
	if err != nil {
		return nil, err
	}

	n := &Node{logger: cfg.logger, ha: ha, helloCond: sync.NewCond(&sync.Mutex{}), handler: cfg.BlockEventHandler}
	if n.handler == nil {
		n.handler = n.handleBlockEvent
	}

	return n, nil
}

func (n *Node) Run(ctx context.Context) error {
	n.ha.SetStreamHandler(HelloProtocol, n.handleHello)
	err := n.runSayHello()
	if err != nil {
		return err
	}

	n.ps, err = pubsub.NewGossipSub(ctx, n.ha)
	if err != nil {
		return nil
	}

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

	n.helloCond.L.Lock()
	n.hello = &hmsg
	n.helloCond.L.Unlock()
	n.helloCond.Broadcast()

	n.logger.Debug("received hello.", "addr", s.Conn().RemoteMultiaddr(), "peer", s.Conn().RemotePeer(), "height", hmsg.HeaviestTipSetHeight)

	protos, err := n.ha.Peerstore().GetProtocols(s.Conn().RemotePeer())
	if err != nil {
		n.logger.Warn("got error from peerstore.GetProtocols.", "error", err)
	}
	if len(protos) == 0 {
		n.logger.Warn("other peer hasnt completed libp2p identify, waiting a bit")
		time.Sleep(time.Millisecond * 300)
	}

	defer s.Close()
	sent := time.Now()
	msg := &LatencyMessage{
		TArrival: arrived.UnixNano(),
		TSent:    sent.UnixNano(),
	}
	_ = msg.MarshalCBOR(s)
}

func (n *Node) runSayHello() error {
	sub, err := n.ha.EventBus().Subscribe(new(event.EvtPeerIdentificationCompleted), eventbus.BufSize(1024))
	if err != nil {
		return fmt.Errorf("failed to subscribe to event bus: %w", err)
	}
	n.cancels = append(n.cancels, func() {
		err := sub.Close()
		if err != nil {
			n.logger.Error("fail to close say hello sub.", "error", err)
		}
	})
	n.wg.Add(1)
	go func() {
		defer n.logger.Debug("shutdown sayhello.")
		defer n.wg.Done()
		for evt := range sub.Out() {
			pic := evt.(event.EvtPeerIdentificationCompleted)
			go func() {
				if err := n.sayHello(context.TODO(), pic.Peer); err != nil {
					n.logger.Debug("failed to say hello", "error", err, "peer", pic.Peer)
					return
				}
			}()
		}
	}()
	return nil
}

func (n *Node) sayHello(ctx context.Context, pid peer.ID) error {
	n.logger.Debug("say hello", "peer", pid, "addr", n.ha.Peerstore().Addrs(pid))

	n.helloCond.L.Lock()
	if n.hello == nil {
		n.helloCond.Wait()
	}
	hmsg := *n.hello
	n.helloCond.L.Unlock()

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
	ctx, cancel := context.WithCancel(context.TODO())
	n.cancels = append(n.cancels, func() {
		cancel()
	})

	sub, err := n.ps.Subscribe(BlockProtocol)
	if err != nil {
		return err
	}
	n.wg.Add(1)

	go func() {
		defer n.wg.Done()
		for {
			msg, err := sub.Next(ctx)
			if err != nil {
				select {
				case <-ctx.Done():
					n.logger.Debug("shutdown incoming blocks handler.")
					return
				default:
				}
				n.logger.Error("fail to reading next block.", "error", err)
				continue
			}

			bmsg, err := types.DecodeBlockMsg(msg.GetData())
			if err != nil {
				n.logger.Error("fail to decode block", "error", err)
				continue
			}

			n.handler(*bmsg)
		}
	}()
	return nil
}

func (n *Node) Publish(bmsg types.BlockMsg) error {
	b, err := bmsg.Serialize()
	if err != nil {
		return err
	}
	return n.ps.Publish(BlockProtocol, b)
}

func (n *Node) handleBlockEvent(bmsg types.BlockMsg) {
	n.logger.Info("received new block msg.", "miner", bmsg.Header.Miner, "cid", bmsg.Cid(), "height", bmsg.Header.Height)
}

func (n *Node) Peers() peer.IDSlice {
	return n.ha.Peerstore().PeersWithAddrs()
}

func (n *Node) Wait() {
	n.wg.Wait()
}

func (n *Node) Close() {
	n.ha.RemoveStreamHandler(HelloProtocol)
	for _, cancel := range n.cancels {
		cancel()
	}
}
