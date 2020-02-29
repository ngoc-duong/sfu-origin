package plugins

import (
	"fmt"

	"github.com/pion/ion/pkg/log"
	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"
	"github.com/pion/webrtc/v2"
)

const (
	maxSN      = 65536
	maxPktSize = 1000

	// kProcessIntervalMs=20 ms
	//https://chromium.googlesource.com/external/webrtc/+/ad34dbe934/webrtc/modules/video_coding/nack_module.cc#28

	// vp8 vp9 h264 clock rate 90000Hz
	videoClock = 90000
	// buffer time 2s
	maxBufferTSDelta = videoClock * 2

	//1+16(FSN+BLP) https://tools.ietf.org/html/rfc2032#page-9
	maxNackLostSize = 17
)

func tsDelta(x, y uint32) uint32 {
	if x > y {
		return x - y
	}
	return y - x
}

// Buffer contains all packets
type Buffer struct {
	maxLate     uint16
	pktBuffer   [maxSN]*rtp.Packet
	lastNackSN  uint16
	lastClearTS uint32
	lastClearSN uint16

	// Last seqnum that has been added to buffer
	lastPushSN uint16

	ssrc        uint32
	payloadType uint8

	//calc lost rate
	receivedPkt int
	lostPkt     int

	//response nack channel
	rtcpCh chan rtcp.Packet

	//calc bindwidth
	totalByte uint64
	byteRate  uint64

	stop bool
}

// NewBuffer constructs a new Buffer
func NewBuffer(maxLate uint16) *Buffer {
	b := &Buffer{
		maxLate: maxLate,
		rtcpCh:  make(chan rtcp.Packet, maxPktSize),
	}
	return b
}

// Push adds a RTP Packet, out of order, new packet may be arrived later
func (b *Buffer) Push(p *rtp.Packet) {
	// log.Infof("Buffer.Push pt=%v sn=%v ts=%v", p.PayloadType, p.SequenceNumber, p.Timestamp)
	b.receivedPkt++
	b.totalByte += uint64(p.MarshalSize())

	// init ssrc payloadType
	if b.ssrc == 0 || b.payloadType == 0 {
		b.ssrc = p.SSRC
		b.payloadType = p.PayloadType
	}

	// init lastClearTS
	if b.lastClearTS == 0 {
		b.lastClearTS = p.Timestamp
	}

	// init lastClearSN
	if b.lastClearSN == 0 {
		b.lastClearSN = p.SequenceNumber
	}

	// init lastNackSN
	if b.lastNackSN == 0 {
		b.lastNackSN = p.SequenceNumber
	}

	// log.Infof("p.SequenceNumber=%d", p.SequenceNumber)
	b.pktBuffer[p.SequenceNumber] = p
	b.lastPushSN = p.SequenceNumber

	// clear old packet by timestamp
	b.clearOldPkt(p.Timestamp, p.SequenceNumber)

	// limit nack range
	if b.lastPushSN-b.lastNackSN >= 16 {
		b.lastNackSN = b.lastPushSN - 16
	}

	var nackPairs []rtcp.NackPair
	if b.lastPushSN-b.lastNackSN >= 16 && (b.lastPushSN-b.lastNackSN)/8 <= 2 {
		// calc [lastNackSN, lastpush-8] if has keyframe
		nackPair, lostPkt := b.NackPair(b.pktBuffer, b.lastNackSN, b.lastPushSN-8, true)
		b.lostPkt += lostPkt
		b.lastNackSN += 8
		if nackPair != nil {
			nackPairs = append(nackPairs, *nackPair)
			nack := &rtcp.TransportLayerNack{
				//origin ssrc
				SenderSSRC: b.ssrc,
				MediaSSRC:  b.ssrc,
				Nacks:      nackPairs,
			}
			// log.Infof("nackPairs=%+v", nackPairs)
			// log.Infof("nack=%+v", nack)
			b.rtcpCh <- nack
		}
	}
}

// clearOldPkt clear old packet
func (b *Buffer) clearOldPkt(pushPktTS uint32, pushPktSN uint16) {
	clearTS := b.lastClearTS
	clearSN := b.lastClearSN
	if tsDelta(pushPktTS, clearTS) >= maxBufferTSDelta {
		for i := clearSN + 1; i <= pushPktSN; i++ {
			if b.pktBuffer[i] == nil {
				log.Infof("b.pktBuffer[i] == nil")
				continue
			}
			if tsDelta(pushPktTS, b.pktBuffer[i].Timestamp) >= maxBufferTSDelta {
				b.lastClearTS = b.pktBuffer[i].Timestamp
				b.lastClearSN = i
				b.pktBuffer[i] = nil
			} else {
				break
			}
		}
	}
}

// FindPacket find packet from buffer
func (b *Buffer) FindPacket(sn uint16) *rtp.Packet {
	return b.pktBuffer[sn]
}

// Stop stop buffer
func (b *Buffer) Stop() {
	b.stop = true
	close(b.rtcpCh)
}

// GetPayloadType get payloadtype
func (b *Buffer) GetPayloadType() uint8 {
	return b.payloadType
}

// GetStat get status from buffer
func (b *Buffer) GetStat() string {
	out := fmt.Sprintf("buffer:[%d, %d] | lastNackSN:%d | lostRate:%.2f |\n", b.lastPushSN-b.maxLate, b.lastPushSN, b.lastNackSN, float64(b.lostPkt)/float64(b.receivedPkt+b.lostPkt))
	return out
}

// NackPair calc nackpair
func (b *Buffer) NackPair(buffer [65536]*rtp.Packet, begin, end uint16, keyFrame bool) (*rtcp.NackPair, int) {

	var lostPkt int

	//size is <= 16
	if end-begin > 16 {
		return nil, lostPkt
	}

	//only check key frame if keyFrame=true
	var keyBegin, keyEnd uint16
	if keyFrame {
		//find key frame begin pkt
		for i := begin; i < end; i++ {
			if IsVP8KeyFrame(buffer[i]) {
				keyBegin = i
				break
			}
		}

		//find key frame end pkt
		if keyBegin != 0 {
			for i := keyBegin; i < end; i++ {
				if !IsVP8KeyFrame(buffer[i]) {
					keyEnd = i
					break
				}
			}
		}
	}
	//Bitmask of following lost packets (BLP)
	blp := uint16(0)
	lost := uint16(0)

	if keyFrame {
		if keyBegin != 0 {
			begin = keyBegin
		}
		if keyEnd != 0 {
			end = keyEnd
		}
	}

	//find first lost pkt
	for i := begin; i < end; i++ {
		if buffer[i] == nil {
			lost = i
			lostPkt++
			break
		}
	}

	//no packet lost
	if lost == 0 {
		return nil, lostPkt
	}

	//calc blp
	for i := lost; i < end; i++ {
		//calc from next lost packet
		if i > lost && buffer[i] == nil {
			blp = blp | (1 << (i - lost - 1))
			lostPkt++
		}
	}
	log.Debugf("NackPair begin=%v end=%v buffer=%v\n", begin, end, buffer[begin:end])
	return &rtcp.NackPair{PacketID: lost, LostPackets: rtcp.PacketBitmap(blp)}, lostPkt
}

// SetSSRCPT set ssrc payloadtype
func (b *Buffer) SetSSRCPT(ssrc uint32, pt uint8) {
	b.ssrc = ssrc
	b.payloadType = pt
}

// GetSSRC get ssrc
func (b *Buffer) GetSSRC() uint32 {
	return b.ssrc
}

// GetRTCPChan return rtcp channel
func (b *Buffer) GetRTCPChan() chan rtcp.Packet {
	return b.rtcpCh
}

// CalcLostRateByteRate calc lostRate and byteRate by cycle
func (b *Buffer) CalcLostRateByteRate(cycle uint64) (float64, uint64) {
	lostRate := float64(b.lostPkt) / float64(b.receivedPkt+b.lostPkt)
	byteRate := b.totalByte / cycle
	log.Debugf("Buffer.CalcLostRateByteRate b.receivedPkt=%d b.lostPkt=%d   lostRate=%v byteRate=%v", b.receivedPkt, b.lostPkt, lostRate, byteRate)
	b.receivedPkt, b.lostPkt, b.totalByte = 0, 0, 0
	return lostRate, byteRate
}

// GetPacket get packet by sequence number
func (b *Buffer) GetPacket(sn uint16) *rtp.Packet {
	return b.pktBuffer[sn]
}

// IsVP8KeyFrame check key frame
func IsVP8KeyFrame(pkt *rtp.Packet) bool {
	if pkt != nil && pkt.PayloadType == webrtc.DefaultPayloadTypeVP8 {
		vp8 := &codecs.VP8Packet{}
		vp8.Unmarshal(pkt.Payload)
		// start of a frame, there is a payload header  when S == 1
		if vp8.S == 1 && vp8.Payload[0]&0x01 == 0 {
			//key frame
			// log.Infof("vp8.Payload[0]=%b pkt=%v", vp8.Payload[0], pkt)
			return true
		}
	}
	return false
}
