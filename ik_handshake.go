package noise

import (
	"context"
	"fmt"
	"github.com/gogo/protobuf/proto"
	"github.com/libp2p/go-libp2p-core/peer"
	"github.com/libp2p/go-libp2p-noise/handshake"
	"github.com/libp2p/go-libp2p-noise/pb"
)

// IK:
//     <- s
//     ...
//     -> e, es, s, ss
//     <- e, ee, se
// returns last successful message upon error
func (s *secureSession) runIK(ctx context.Context, payload []byte) ([]byte, error) {
	kp := handshake.NewKeypair(s.noiseKeypair.publicKey, s.noiseKeypair.privateKey)
	remoteNoiseKey := s.noiseStaticKeyCache.Load(s.remotePeer)

	// new IK noise session
	s.ns = handshake.IKInitSession(s.initiator, s.prologue, kp, remoteNoiseKey)

	if s.initiator {
		return s.runIKAsInitiator(ctx, payload)
	}
	return s.runIKAsResponder(ctx, payload)
}

func (s *secureSession) ikSendHandshakeMessage(payload []byte, initial_stage bool) error {
	if initial_stage {
		return s.sendHandshakeMessage(payload, handshake.IKEncode0, handshake.IKSendMessage)
	}
	return s.sendHandshakeMessage(payload, handshake.IKEncode1, handshake.IKSendMessage)
}

func (s *secureSession) ikRecvHandshakeMessage(initial_stage bool) (buf []byte, plaintext []byte, err error) {
	if initial_stage {
		return s.recvHandshakeMessage(handshake.IKDecode0, handshake.IKRecvMessage)
	}
	return s.recvHandshakeMessage(handshake.IKDecode1, handshake.IKRecvMessage)
}

func (s *secureSession) runIKAsInitiator(ctx context.Context, payload []byte) ([]byte, error) {
	remoteNoiseKey := s.noiseStaticKeyCache.Load(s.remotePeer)
	// bail out early if we don't know the remote Noise key
	if remoteNoiseKey == [32]byte{} {
		return nil, fmt.Errorf("runIK aborting - unknown static key for peer %s", s.remotePeer.Pretty())
	}

	// stage 0 //
	err := s.ikSendHandshakeMessage(payload, true)
	if err != nil {
		return nil, fmt.Errorf("runIK stage=0 initiator=true err=%s", err)
	}

	// stage 1 //

	// read message
	buf, plaintext, err := s.ikRecvHandshakeMessage(false)
	if err != nil {
		return buf, fmt.Errorf("runIK stage=1 initiator=true err=%s", err)
	}

	// unmarshal payload
	nhp := new(pb.NoiseHandshakePayload)
	err = proto.Unmarshal(plaintext, nhp)
	if err != nil {
		return buf, fmt.Errorf("runIK stage=1 initiator=true err=validation fail: cannot unmarshal payload")
	}

	// set remote libp2p public key
	err = s.setRemotePeerInfo(nhp.GetIdentityKey())
	if err != nil {
		return buf, fmt.Errorf("runIK stage=1 initiator=true err=read remote libp2p key fail")
	}

	// assert that remote peer ID matches libp2p public key
	pid, err := peer.IDFromPublicKey(s.RemotePublicKey())
	if pid != s.remotePeer {
		return buf, fmt.Errorf("runIK stage=1 initiator=true  check remote peer id err: expected %x got %x", s.remotePeer, pid)
	} else if err != nil {
		return buf, fmt.Errorf("runIK stage=1 initiator=true  check remote peer id err %s", err)
	}

	// verify payload is signed by libp2p key
	err = s.verifyPayload(nhp, remoteNoiseKey)
	if err != nil {
		return buf, fmt.Errorf("runIK stage=1 initiator=true verify payload err=%s", err)
	}
	return buf, nil
}

func (s *secureSession) runIKAsResponder(ctx context.Context, payload []byte) ([]byte, error) {
	// stage 0 //

	// read message
	buf, plaintext, err := s.ikRecvHandshakeMessage(true)
	if err != nil {
		return buf, fmt.Errorf("runIK stage=0 initiator=false err=%s", err)
	}

	// unmarshal payload
	nhp := new(pb.NoiseHandshakePayload)
	err = proto.Unmarshal(plaintext, nhp)
	if err != nil {
		return buf, fmt.Errorf("runIK stage=0 initiator=false err=validation fail: cannot unmarshal payload")
	}

	// set remote libp2p public key
	err = s.setRemotePeerInfo(nhp.GetIdentityKey())
	if err != nil {
		return buf, fmt.Errorf("runIK stage=0 initiator=false err=read remote libp2p key fail")
	}

	// assert that remote peer ID matches libp2p key
	err = s.setRemotePeerID(s.RemotePublicKey())
	if err != nil {
		return buf, fmt.Errorf("runIK stage=0 initiator=false set remote peer id err=%s:", err)
	}

	// verify payload is signed by libp2p key
	err = s.verifyPayload(nhp, s.ns.RemoteKey())
	if err != nil {
		return buf, fmt.Errorf("runIK stage=0 initiator=false verify payload err=%s", err)
	}

	// stage 1 //

	err = s.ikSendHandshakeMessage(payload, false)
	if err != nil {
		return nil, fmt.Errorf("runIK stage=1 initiator=false send err=%s", err)
	}
	return buf, nil
}
