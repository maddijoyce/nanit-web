package rtmpserver

import (
	"sync"

	"github.com/notedit/rtmp/av"
)

// Packet types carrying stream configuration rather than media. At most one of
// each is current at any moment - a repeat from the cam replaces the previous
// one - and they are replayed in this order to every subscriber before its
// first media packet, so clients joining mid-stream can decode.
//
// Together with the media types below this covers every type the RTMP library
// produces, so no packet goes unclassified.
var headerPktTypes = []int{
	av.Metadata,
	av.H264DecoderConfig,
	av.H264SPSPPSNALU,
	av.AACDecoderConfig,
}

func isMediaPkt(pkt av.Packet) bool {
	switch pkt.Type {
	case av.H264, av.AAC, av.OPUS:
		return true
	default:
		return false
	}
}

type subscriber struct {
	initialized bool
	pktC        chan av.Packet
}

type broadcaster struct {
	headerPkts  map[int]av.Packet
	subscribers sync.Map
}

func newBroadcaster() *broadcaster {
	return &broadcaster{
		headerPkts: make(map[int]av.Packet, len(headerPktTypes)),
	}
}

func (b *broadcaster) newSubscriber() *subscriber {
	sub := &subscriber{
		initialized: false,
		pktC:        make(chan av.Packet, 10),
	}

	b.subscribers.Store(sub, sub)
	return sub
}

func (b *broadcaster) unsubscribe(sub *subscriber) {
	b.subscribers.Delete(sub)
}

func (b *broadcaster) broadcast(pkt av.Packet) {
	if !isMediaPkt(pkt) {
		// Keep only the latest of each configuration type. Accumulating every
		// one the cam ever sends grows without bound and eventually exceeds a
		// new subscriber's channel buffer during replay, which blocks the
		// publisher that is doing the replaying.
		b.headerPkts[pkt.Type] = pkt
		return
	}

	b.subscribers.Range(func(key, value interface{}) bool {
		sub := value.(*subscriber)

		// Send header packets before sending any data
		if !sub.initialized {
			sub.initialized = true
			for _, pktType := range headerPktTypes {
				if headerPkt, ok := b.headerPkts[pktType]; ok {
					sub.pktC <- headerPkt
				}
			}
		}

		sub.pktC <- pkt

		return true
	})
}

func (b *broadcaster) closeSubscribers() {
	b.subscribers.Range(func(key, value interface{}) bool {
		sub := value.(*subscriber)
		close(sub.pktC)
		return true
	})
}
