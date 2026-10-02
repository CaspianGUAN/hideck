package imscore

import (
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/iniwex5/vowifi-go/internal/vowifi/logging"
)

// UDP uses the negotiated ports/SAs: send from port-c to the P-CSCF's port-s,
// receive at port-s from the P-CSCF's port-c (TS 24.229).
type protectedUDPTransport struct {
	client, server             net.PacketConn
	remoteClient, remoteServer *net.UDPAddr
	registrar                  string
}

func (s *Service) startProtectedUDP(client, server securityMechanism) error {
	s.mu.RLock()
	skip := s.externalTransport || s.protectedUDP != nil
	remote := cloneUDPAddr(s.registrationRemote)
	registrar := s.registrar
	reserved, reservedOK := s.securityServerIO.(*protectedSIPListener)
	s.mu.RUnlock()
	if skip {
		return nil
	}
	if remote == nil || remote.IP == nil {
		return errors.New("imscore: protected UDP registrar address is missing")
	}
	if !reservedOK || tcpPortFromAddr(reserved.udpClient.LocalAddr()) != int(client.PortC) ||
		tcpPortFromAddr(reserved.udpServer.LocalAddr()) != int(client.PortS) {
		return errors.New("imscore: IMS network lacks the negotiated protected UDP reservations or protection capability")
	}
	portC, portS := reserved.udpClient, reserved.udpServer
	path := &protectedUDPTransport{
		client: portC, server: portS, registrar: registrar,
		remoteClient: &net.UDPAddr{IP: remote.IP, Port: int(server.PortC)},
		remoteServer: &net.UDPAddr{IP: remote.IP, Port: int(server.PortS)},
	}
	s.mu.Lock()
	if s.stopped() {
		s.mu.Unlock()
		path.close()
		return net.ErrClosed
	}
	s.protectedUDP = path
	s.networkDone.Add(1)
	s.mu.Unlock()
	logging.Info("IMS protected UDP receiver listening", "device", s.DeviceID(),
		"pcscf", registrar, "local", portS.LocalAddr(), "remote", path.remoteClient)
	go s.readProtectedUDP(path)
	return nil
}

func (path *protectedUDPTransport) close() {
	_ = path.server.Close()
	_ = path.client.Close()
}

func (s *Service) closeProtectedUDP() {
	s.mu.Lock()
	path := s.protectedUDP
	s.protectedUDP = nil
	s.protectedRegistrationUDP = false
	s.udpDownlinkProven.Store(false)
	s.mu.Unlock()
	if path != nil {
		path.close()
	}
}

func (s *Service) readProtectedUDP(path *protectedUDPTransport) {
	var readErr error
	defer func() {
		// StopCurrent can wait for readers while REGISTER owns registerMu.
		// Retire this reader before entering serialized failure recovery.
		s.networkDone.Done()
		if readErr != nil {
			s.handleProtectedUDPFailure(path, readErr)
		}
	}()
	s.receiverStarted()
	defer s.receiverStopped()
	buffer := make([]byte, 64*1024)
	for {
		n, remote, err := path.server.ReadFrom(buffer)
		if err != nil {
			readErr = err
			return
		}
		if err := s.dispatchProtectedUDP(path, remote, string(buffer[:n])); err != nil {
			logging.WarnRate("ims-protected-udp-"+s.DeviceID(), "IMS protected UDP handling failed",
				"device", s.DeviceID(), "err", err)
		}
	}
}

func (s *Service) dispatchProtectedUDP(path *protectedUDPTransport, remote net.Addr, raw string) error {
	address, ok := remote.(*net.UDPAddr)
	if !ok || !address.IP.Equal(path.remoteClient.IP) || address.Port != path.remoteClient.Port {
		return errors.New("imscore: protected UDP packet from an unnegotiated endpoint")
	}
	peer := &protectedUDPPeer{service: s, path: path}
	s.mu.RLock()
	current := s.currentProtectedUDPPeerLocked(peer)
	s.mu.RUnlock()
	if !current {
		return net.ErrClosed
	}
	message, err := parseSIPMessage(raw)
	if err != nil {
		return fmt.Errorf("imscore: parse protected UDP SIP: %w", err)
	}
	message.SetTransport("UDP")
	message.SetSource(remote.String())
	message.SetDestination(path.server.LocalAddr().String())
	return s.dispatchInboundSIPMessageWithPeer(message, raw, func(response string) error {
		_, err := peer.Write([]byte(response))
		return err
	}, peer)
}

func (s *Service) currentProtectedUDPPeerLocked(peer *protectedUDPPeer) bool {
	return !s.stopped() && peer.path == s.protectedUDP && peer.path.registrar == s.registrar
}

func (s *Service) handleProtectedUDPFailure(path *protectedUDPTransport, cause error) {
	s.mu.RLock()
	current := !s.stopped() && s.protectedUDP == path
	s.mu.RUnlock()
	if !current {
		return
	}
	s.registerMu.Lock()
	defer s.registerMu.Unlock()
	s.mu.Lock()
	if s.stopped() || s.protectedUDP != path {
		s.mu.Unlock()
		return
	}
	s.protectedUDP = nil
	s.protectedRegistrationUDP = false
	s.udpDownlinkProven.Store(false)
	s.regState = regFailed
	s.signalingReady = false
	err := fmt.Errorf("imscore: protected UDP receiver failed: %w", cause)
	s.signalingFailureReason, s.lastError = err.Error(), err.Error()
	s.abandonReplacementDownlinkWaitLocked()
	s.mu.Unlock()
	path.close()
	s.transitionRegStatus(registrationRejectedTemporary)
	s.notifySMSReadiness()
	s.reportRegistrationRuntimeError(err)
}

// A datagram peer keeps asynchronous replies/evidence tied to one negotiation.
type protectedUDPPeer struct {
	service *Service
	path    *protectedUDPTransport
}

func (peer *protectedUDPPeer) Write(data []byte) (int, error) {
	peer.service.sipWriteMu.Lock()
	defer peer.service.sipWriteMu.Unlock()
	peer.service.mu.RLock()
	defer peer.service.mu.RUnlock()
	if !peer.service.currentProtectedUDPPeerLocked(peer) {
		return 0, net.ErrClosed
	}
	return peer.path.client.WriteTo(data, peer.path.remoteServer)
}

func (peer *protectedUDPPeer) Read([]byte) (int, error) {
	return 0, errors.New("imscore: UDP peer is write-only")
}
func (peer *protectedUDPPeer) Close() error {
	return errors.New("imscore: UDP peer is owned by the IMS service")
}
func (peer *protectedUDPPeer) LocalAddr() net.Addr            { return peer.path.client.LocalAddr() }
func (peer *protectedUDPPeer) RemoteAddr() net.Addr           { return peer.path.remoteServer }
func (peer *protectedUDPPeer) SetDeadline(at time.Time) error { return peer.SetWriteDeadline(at) }
func (peer *protectedUDPPeer) SetReadDeadline(time.Time) error {
	return errors.New("imscore: UDP peer is write-only")
}
func (peer *protectedUDPPeer) SetWriteDeadline(at time.Time) error {
	return peer.path.client.SetWriteDeadline(at)
}

// activateProtectedRegistrationUDP routes the protected REGISTER over the
// negotiated UDP SAs (port-c -> P-CSCF port-s) when protected TCP is not used.
func (s *Service) activateProtectedRegistrationUDP() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := s.protectedUDP
	if path == nil {
		return errors.New("imscore: protected UDP path is not running")
	}
	peer := &protectedUDPPeer{service: s, path: path}
	s.protectedRegistrationUDP = true
	s.registrationTransport = "udp"
	s.transport.SetSendFn(func(request string) error {
		_, err := peer.Write([]byte(request))
		return err
	})
	logging.Info("IMS protected REGISTER using UDP", "device", s.DeviceID(),
		"local", path.client.LocalAddr(), "remote", path.remoteServer)
	return nil
}
