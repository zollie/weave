package gossip

import (
	"fmt"
	"math/rand"
	"time"

	"github.com/weaveworks/weave/common"
	"github.com/weaveworks/weave/router"
)

// Router to convey gossip from one gossiper to another, for testing
type unicastMessage struct {
	sender router.PeerName
	buf    []byte
}
type broadcastMessage struct {
	data router.GossipData
}
type exitMessage struct {
	exitChan chan struct{}
}
type flushMessage struct {
	flushChan chan struct{}
}

type TestRouter struct {
	gossipChans map[router.PeerName]chan interface{}
	loss        float32 // 0.0 means no loss
}

func NewTestRouter(loss float32) *TestRouter {
	return &TestRouter{make(map[router.PeerName]chan interface{}, 100), loss}
}

func (grouter *TestRouter) GossipBroadcast(update router.GossipData) error {
	for _, gossipChan := range grouter.gossipChans {
		select {
		case gossipChan <- broadcastMessage{data: update}:
		default: // drop the message if we cannot send it
			common.Log.Errorf("Dropping message")
		}
	}
	return nil
}

func (grouter *TestRouter) Flush() {
	for _, gossipChan := range grouter.gossipChans {
		flushChan := make(chan struct{})
		gossipChan <- flushMessage{flushChan: flushChan}
		<-flushChan
	}
}

func (grouter *TestRouter) RemovePeer(peer router.PeerName) {
	gossipChan := grouter.gossipChans[peer]
	resultChan := make(chan struct{})
	gossipChan <- exitMessage{exitChan: resultChan}
	<-resultChan
	delete(grouter.gossipChans, peer)
}

type TestRouterClient struct {
	router *TestRouter
	sender router.PeerName
}

func (grouter *TestRouter) run(gossiper router.Gossiper, gossipChan chan interface{}) {
	gossipTimer := time.Tick(10 * time.Second)
	for {
		select {
		case gossip := <-gossipChan:
			switch message := gossip.(type) {
			case exitMessage:
				close(message.exitChan)
				return

			case flushMessage:
				close(message.flushChan)

			case unicastMessage:
				if rand.Float32() > (1.0 - grouter.loss) {
					continue
				}
				if err := gossiper.OnGossipUnicast(message.sender, message.buf); err != nil {
					panic(fmt.Sprintf("Error doing gossip unicast to %s: %s", message.sender, err))
				}

			case broadcastMessage:
				if rand.Float32() > (1.0 - grouter.loss) {
					continue
				}
				for _, msg := range message.data.Encode() {
					if _, err := gossiper.OnGossipBroadcast(msg); err != nil {
						panic(fmt.Sprintf("Error doing gossip broadcast: %s", err))
					}
				}
			}
		case <-gossipTimer:
			grouter.GossipBroadcast(gossiper.Gossip())
		}
	}
}

func (grouter *TestRouter) Connect(sender router.PeerName, gossiper router.Gossiper) router.Gossip {
	gossipChan := make(chan interface{}, 100)

	go grouter.run(gossiper, gossipChan)

	grouter.gossipChans[sender] = gossipChan
	return TestRouterClient{grouter, sender}
}

func (client TestRouterClient) GossipUnicast(dstPeerName router.PeerName, buf []byte) error {
	select {
	case client.router.gossipChans[dstPeerName] <- unicastMessage{sender: client.sender, buf: buf}:
	default: // drop the message if we cannot send it
		common.Log.Errorf("Dropping message")
	}
	return nil
}

func (client TestRouterClient) GossipBroadcast(update router.GossipData) error {
	return client.router.GossipBroadcast(update)
}
