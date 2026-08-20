package rtmpserver

import (
	"testing"

	"github.com/notedit/rtmp/av"
)

func drain(sub *subscriber) []av.Packet {
	var pkts []av.Packet
	for {
		select {
		case pkt := <-sub.pktC:
			pkts = append(pkts, pkt)
		default:
			return pkts
		}
	}
}

// The cam re-sends configuration periodically. Only the latest of each type
// should be retained, otherwise the slice grows for the life of the publisher.
func TestBroadcasterKeepsOnlyLatestHeaderPerType(t *testing.T) {
	b := newBroadcaster()

	for i := 0; i < 100; i++ {
		b.broadcast(av.Packet{Type: av.Metadata, Data: []byte{byte(i)}})
		b.broadcast(av.Packet{Type: av.H264DecoderConfig, Data: []byte{byte(i)}})
	}

	if len(b.headerPkts) != 2 {
		t.Fatalf("expected 2 retained header packets, got %d", len(b.headerPkts))
	}

	if got := b.headerPkts[av.Metadata].Data[0]; got != 99 {
		t.Errorf("expected the newest metadata packet (99), got %d", got)
	}
}

// A subscriber that joins mid-stream needs the configuration replayed before
// any media, or it has nothing to decode with.
func TestBroadcasterReplaysHeadersToLateSubscriber(t *testing.T) {
	b := newBroadcaster()

	b.broadcast(av.Packet{Type: av.Metadata})
	b.broadcast(av.Packet{Type: av.H264DecoderConfig})
	b.broadcast(av.Packet{Type: av.AACDecoderConfig})
	b.broadcast(av.Packet{Type: av.H264, IsKeyFrame: true})

	sub := b.newSubscriber()
	b.broadcast(av.Packet{Type: av.H264, IsKeyFrame: true})

	got := drain(sub)
	want := []int{av.Metadata, av.H264DecoderConfig, av.AACDecoderConfig, av.H264}
	if len(got) != len(want) {
		t.Fatalf("expected %d packets, got %d", len(want), len(got))
	}
	for i, wantType := range want {
		if got[i].Type != wantType {
			t.Errorf("packet %d: expected type %d, got %d", i, wantType, got[i].Type)
		}
	}

	// Headers are replayed once, not before every subsequent packet
	b.broadcast(av.Packet{Type: av.H264})
	if got := drain(sub); len(got) != 1 || got[0].Type != av.H264 {
		t.Errorf("expected a single media packet after init, got %v", got)
	}
}

func TestBroadcasterTreatsAllMediaTypesAsMedia(t *testing.T) {
	for _, pktType := range []int{av.H264, av.AAC, av.OPUS} {
		if !isMediaPkt(av.Packet{Type: pktType}) {
			t.Errorf("packet type %d should be media", pktType)
		}
	}

	for _, pktType := range headerPktTypes {
		if isMediaPkt(av.Packet{Type: pktType}) {
			t.Errorf("packet type %d should not be media", pktType)
		}
	}
}
