package swarm

import (
	"encoding/hex"
	"github.com/UnrulyOS/go-unruly/log"
	"github.com/UnrulyOS/go-unruly/p2p2/swarm"
	"github.com/UnrulyOS/go-unruly/p2p2/swarm/pb"
	"github.com/gogo/protobuf/proto"
)

// pattern: [protocol][version][method-name]
const pingReq = "/ping/1.0/ping-req/"
const pingResp = "/ping/1.0/ping-resp/"

// Ping protocol - example simple app-level p2p protocol
type Ping interface {

	// send a ping request to a remoteNode
	// reqId: allows the client to match responses with requests by id
	SendPing(msg string, reqId []byte, remoteNodeId string) error

	// app logic registers incoming ping response
	RegisterCallback(callback chan *pb.PingRespData)
}

type Callbacks chan chan *pb.PingRespData

type pingProtocolImpl struct {
	swarm swarm.Swarm

	callbacks []chan *pb.PingRespData

	// ops
	incomingRequests  swarm.MessagesChan
	incomingResponses swarm.MessagesChan

	// a channel of channels that receive callbacks
	callbacksRegReq Callbacks
}

func NewPingProtocol(s swarm.Swarm) Ping {

	p := &pingProtocolImpl{
		swarm:             s,
		incomingRequests:  make(swarm.MessagesChan, 10),
		incomingResponses: make(swarm.MessagesChan, 10),
		callbacksRegReq:   make(Callbacks, 10),
		callbacks:         make([]chan *pb.PingRespData, 1),
	}

	go p.processEvents()

	// protocol demuxer registration
	s.GetDemuxer().RegisterProtocolHandler(swarm.ProtocolRegistration{pingReq, p.incomingRequests})
	s.GetDemuxer().RegisterProtocolHandler(swarm.ProtocolRegistration{pingResp, p.incomingResponses})

	return p
}

func (p *pingProtocolImpl) SendPing(msg string, reqId []byte, remoteNodeId string) error {

	// todo: send a ping message to the remote node

	metadata := p.swarm.GetLocalNode().NewProtocolMessageMetadata(pingReq, reqId, false)

	data := &pb.PingReqData{metadata, msg}

	// Sign, Pack and Send :-)

	// TODO: factor this out to local node so it can sign any protocol message
	// and so we don't need to have this boilerplate code in every protocol impl
	/////////////////////////////////////////////

	bin, err := proto.Marshal(data)
	if err != nil {
		return err
	}

	sign, err := p.swarm.GetLocalNode().PrivateKey().Sign(bin)
	if err != nil {
		return err
	}

	// place signature - hex encoded string
	data.Metadata.AuthorSign = hex.EncodeToString(sign)

	// marshal the signed data
	payload, err := proto.Marshal(data)
	if err != nil {
		return err
	}

	req := swarm.SendMessageReq{remoteNodeId, reqId, payload}
	p.swarm.SendMessage(req)

	return nil
}

func (p *pingProtocolImpl) RegisterCallback(callback chan *pb.PingRespData) {
	p.callbacksRegReq <- callback
}

func (p *pingProtocolImpl) handleIncomingRequest(msg swarm.IncomingMessage) {

	// process request
	req := &pb.PingReqData{}
	err := proto.Unmarshal(msg.Payload(), req)
	if err != nil {
		log.Warning("Invalid ping request data: %v", err)
		return
	}

	peer := msg.Sender()
	pingText := req.Ping
	log.Info("Incoming peer request from %s. Message: %", peer.Pretty(), pingText)

	// generate pong response

	metadata := p.swarm.GetLocalNode().NewProtocolMessageMetadata(pingResp, req.Metadata.ReqId, false)
	respData := &pb.PingRespData{metadata, pingText}

	// sign response

	bin, err := proto.Marshal(respData)
	if err != nil {
		return
	}

	sign, err := p.swarm.GetLocalNode().PrivateKey().Sign(bin)
	if err != nil {
		return
	}

	// place signature - hex encoded string
	respData.Metadata.AuthorSign = hex.EncodeToString(sign)

	// marshal the signed data
	signedPayload, err := proto.Marshal(respData)
	if err != nil {
		return
	}

	// send signed data payload

	resp := swarm.SendMessageReq{msg.Sender().String(),
		req.Metadata.ReqId,
		signedPayload}

	p.swarm.SendMessage(resp)

}

func (p *pingProtocolImpl) handleIncomingResponse(msg swarm.IncomingMessage) {

	// process request
	resp := &pb.PingRespData{}
	err := proto.Unmarshal(msg.Payload(), resp)
	if err != nil {
		log.Warning("Invalid ping request data: %v", err)
		return
	}

	reqId := hex.EncodeToString(resp.Metadata.ReqId)

	log.Info("Got pong response from %s. Ping req id: %", msg.Sender().Pretty(), resp.Pong, reqId)

	// notify clients
	for _, c := range p.callbacks {
		// todo: verify that this style of closure is kosher
		go func() { c <- resp }()
	}

}

func (p *pingProtocolImpl) processEvents() {
	for {
		select {
		case r := <-p.incomingRequests:
			p.handleIncomingRequest(r)

		case r := <-p.incomingResponses:
			p.handleIncomingResponse(r)

		case c := <-p.callbacksRegReq:
			p.callbacks = append(p.callbacks, c)
		}
	}
}
