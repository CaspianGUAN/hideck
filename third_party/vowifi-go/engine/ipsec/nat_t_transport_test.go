package ipsec

import (
	"bytes"
	"net"
	"testing"
)

func TestDirectNATTDemuxSurvivesPeerPortChange(t *testing.T) {
	s := &SocketManager{
		RemoteAddr: &net.UDPAddr{IP: net.IPv4(192, 0, 2, 1), Port: 500},
		IKEChan:    make(chan []byte, 1), ESPChan: make(chan []byte, 1), NetEvents: make(chan NetEvent, 1),
	}
	s.SetRemotePort(4500)
	peer := &net.UDPAddr{IP: s.RemoteAddr.IP, Port: 34567}
	if !s.acceptSource(peer) {
		t.Fatal("peer port change rejected")
	}
	packet := fakeIKEInit() // Nonzero ESP SPI with IKE-looking encrypted bytes.
	s.dispatchDatagram(packet, peer.Port)
	assertESPDelivery(t, s.ESPChan, packet)
}

func TestSocksNATTDemuxSurvivesPeerPortChange(t *testing.T) {
	s := &Socks5Transport{ikeChan: make(chan []byte, 1), espChan: make(chan []byte, 1)}
	s.SetRemotePort(4500)
	packet := fakeIKEInit()
	peer := &net.UDPAddr{IP: net.IPv4(192, 0, 2, 1), Port: 34567}
	s.processSocks5Datagram(EncodeSocks5UDPDatagram(peer, packet))
	assertESPDelivery(t, s.espChan, packet)
}

func assertESPDelivery(t *testing.T, stream <-chan []byte, want []byte) {
	t.Helper()
	select {
	case got := <-stream:
		if !bytes.Equal(got, want) {
			t.Fatal("ESP payload changed")
		}
	default:
		t.Fatal("NAT-T packet with a remapped source port was not delivered as ESP")
	}
}
