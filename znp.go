package znp

import (
	"fmt"
	"time"

	unpi "github.com/dyrkin/unpi-go"
)

type Sync struct {
	frame   *unpi.Frame
	syncRsp chan *unpi.Frame
	syncErr chan error
}

type Async struct {
	frame *unpi.Frame
}

type Outgoing interface {
	Frame() *unpi.Frame
}

func (s *Sync) Frame() *unpi.Frame {
	return s.frame
}

func (a *Async) Frame() *unpi.Frame {
	return a.frame
}

type Znp struct {
	u            *unpi.Unpi
	outbound     chan Outgoing
	inbound      chan *unpi.Frame
	AsyncInbound chan *unpi.Frame
	Errors       chan error
}

type Network struct {
	NeighborPanID   uint16
	LogicalChannel  uint8
	StackProfile    uint8
	ZigbeeVersion   uint8
	BeaconOrder     uint8
	SuperFrameOrder uint8
	PermitJoin      uint8
}

type ResetRequest struct {
	ResetType byte
}

type Capabilities struct {
	Sys   uint8
	Mac   uint8
	Nwk   uint8
	Af    uint8
	Zdo   uint8
	Sapi  uint8
	Util  uint8
	Debug uint8
	App   uint8
	Zoad  uint8
}

type PingResponse struct {
	Capabilities *Capabilities
}

type VersionResponse struct {
	TransportRev uint8
	Product      uint8
	MajorRel     uint8
	MinorRel     uint8
	MaintRel     uint8
}

type LedControlRequest struct {
	LedID uint8
	Mode  uint8
}

type LedControlResponse struct {
	Status uint8
}

func New(u *unpi.Unpi) *Znp {
	znp := &Znp{
		u:            u,
		outbound:     make(chan Outgoing),
		inbound:      make(chan *unpi.Frame),
		AsyncInbound: make(chan *unpi.Frame),
		Errors:       make(chan error),
	}
	go znp.startProcessor()
	go znp.incomingLoop()
	return znp
}

func (znp *Znp) Reset(resetType byte) {
	znp.ProcessRequest(unpi.C_AREQ, unpi.S_SYS, 0, &ResetRequest{resetType}, nil)
}

//This command issues PING requests to verify if a device is active and check the capability of the device.
func (znp *Znp) Ping() (*PingResponse, error) {
	rsp := &PingResponse{}
	err := znp.ProcessRequest(unpi.C_SREQ, unpi.S_SYS, 1, nil, rsp)
	if err != nil {
		return nil, err
	}
	return rsp, nil
}

func (znp *Znp) Version() (*VersionResponse, error) {
	rsp := &VersionResponse{}
	err := znp.ProcessRequest(unpi.C_SREQ, unpi.S_SYS, 2, nil, rsp)
	if err != nil {
		return nil, err
	}
	return rsp, nil
}

func (znp *Znp) LedControl(ledid uint8, mode uint8) (*LedControlResponse, error) {
	req := &LedControlRequest{LedID: ledid, Mode: mode}
	rsp := &LedControlResponse{}
	err := znp.ProcessRequest(unpi.C_SREQ, unpi.S_UTIL, 10, req, rsp)
	if err != nil {
		return nil, err
	}
	return rsp, nil
}

func (znp *Znp) ProcessRequest(commandType unpi.CommandType, subsystem unpi.Subsystem, command byte, request interface{}, response interface{}) (err error) {
	frame := &unpi.Frame{
		CommandType: commandType,
		Subsystem:   subsystem,
		Command:     command,
		Payload:     serialize(request),
	}
	done := make(chan bool, 1)
	go func() {
		if commandType == unpi.C_SREQ {
			outgoing := &Sync{frame: frame,
				syncRsp: make(chan *unpi.Frame, 1),
				syncErr: make(chan error, 1),
			}
			znp.outbound <- outgoing
			select {
			case frame := <-outgoing.syncRsp:
				deserialize(frame.Payload, response)
			case err = <-outgoing.syncErr:
			}
		} else {
			outgoing := &Async{frame: frame}
			znp.outbound <- outgoing
		}
		done <- true
	}()
	<-done
	return
}

func (znp *Znp) startProcessor() {
	registry := NewRequestRegistry()
	for {
		select {
		case outgoing := <-znp.outbound:
			switch req := outgoing.(type) {
			case *Sync:
				frame := req.Frame()
				deadline := &deadline{
					time.NewTimer(5 * time.Second),
					make(chan bool, 1),
				}
				key := &registryKey{frame.Subsystem, frame.Command}
				value := &registryValue{req.syncRsp, req.syncErr, deadline}
				registry.Register(key, value)
				znp.u.Write(req.frame)
				go func() {
					select {
					case _ = <-deadline.timer.C:
						if !deadline.timer.Stop() {
							req.syncErr <- fmt.Errorf("timed out while waiting response for command: %b sent to subsystem: %s ", frame.Command, frame.Subsystem)
						}
					case _ = <-deadline.cancelled:
					}
					registry.Unregister(key)
				}()
			case *Async:
				znp.u.Write(req.frame)
			}
		case frame := <-znp.inbound:
			if frame.CommandType == unpi.C_SRSP {
				key := &registryKey{frame.Subsystem, frame.Command}
				value, ok := registry.Get(key)
				if !ok {
					znp.Errors <- fmt.Errorf("Unknown response received: %v", frame)
					continue
				}
				value.deadline.Cancel()
				value.syncRsp <- frame
			} else {
				znp.AsyncInbound <- frame
			}
		}
	}
}

func (znp *Znp) incomingLoop() {
	for {
		frame, err := znp.u.Read()
		if err != nil {
			znp.Errors <- err
		} else {
			znp.inbound <- frame
		}
	}
}
