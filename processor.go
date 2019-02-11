package znp

import (
	"errors"
	"fmt"
	"time"

	unp "github.com/dyrkin/unp-go"

	"github.com/dyrkin/bin"
	"github.com/dyrkin/znp-go/reflection"
	"github.com/dyrkin/znp-go/request"
)

func (znp *Znp) Start() {
	startProcessors(znp)
	startIncomingFrameLoop(znp)
	znp.started = true
}

func (znp *Znp) Stop() {
	znp.started = false
}

func (znp *Znp) ProcessRequest(commandType unp.CommandType, subsystem unp.Subsystem, command byte, req interface{}, resp interface{}) (err error) {
	frame := &unp.Frame{
		CommandType: commandType,
		Subsystem:   subsystem,
		Command:     command,
		Payload:     bin.Encode(req),
	}
	done := make(chan bool, 1)
	go func() {
		switch commandType {
		case unp.C_SREQ:
			outgoing := request.NewSync(frame)
			znp.outbound <- outgoing
			select {
			case frame := <-outgoing.SyncRsp():
				bin.Decode(frame.Payload, resp)
			case err = <-outgoing.SyncErr():
			}
		case unp.C_AREQ:
			outgoing := request.NewAsync(frame)
			znp.outbound <- outgoing
		default:
			err = fmt.Errorf("Unsupported command type: %s ", commandType)
		}
		done <- true
	}()
	<-done
	return
}

func startProcessors(znp *Znp) {
	syncRsp := make(chan *unp.Frame)
	syncErr := make(chan error)
	syncRequestProcessor := makeSyncRequestProcessor(znp, syncRsp, syncErr)
	asyncRequestProcessor := makeAsyncRequestProcessor(znp)
	syncResponseProcessor := makeSyncResponseProcessor(syncRsp, syncErr)
	asyncResponseProcessor := makeAsyncResponseProcessor(znp)
	outgoingProcessor := func() {
		for znp.started {
			select {
			case outgoing := <-znp.outbound:
				switch req := outgoing.(type) {
				case *request.Sync:
					syncRequestProcessor(req)
				case *request.Async:
					asyncRequestProcessor(req)
				}
			}
		}
	}
	incomingProcessor := func() {
		for znp.started {
			select {
			case frame := <-znp.inbound:
				switch frame.CommandType {
				case unp.C_SRSP:
					syncResponseProcessor(frame)
				case unp.C_AREQ:
					asyncResponseProcessor(frame)
				}
			}
		}
	}
	go incomingProcessor()
	go outgoingProcessor()
}

func startIncomingFrameLoop(znp *Znp) {
	incomingLoop := func() {
		for znp.started {
			frame, err := znp.u.ReadFrame()
			if err != nil {
				znp.errors <- err
			} else {
				logFrame(frame, znp.logInFrames, znp.inFramesLog)
				znp.inbound <- frame
			}
		}
	}
	go incomingLoop()
}

func makeSyncRequestProcessor(znp *Znp, syncRsp chan *unp.Frame, syncErr chan error) func(req *request.Sync) {
	return func(req *request.Sync) {
		frame := req.Frame()
		deadline := time.NewTimer(5 * time.Second)
		logFrame(frame, znp.logOutFrames, znp.outFramesLog)
		znp.u.WriteFrame(frame)
		select {
		case _ = <-deadline.C:
			if !deadline.Stop() {
				req.SyncErr() <- fmt.Errorf("timed out while waiting response for command: 0x%x sent to subsystem: %s ", frame.Command, frame.Subsystem)
			}
		case response := <-syncRsp:
			deadline.Stop()
			req.SyncRsp() <- response
		case err := <-syncErr:
			deadline.Stop()
			req.SyncErr() <- err
		}
	}
}

func makeAsyncRequestProcessor(znp *Znp) func(req *request.Async) {
	return func(req *request.Async) {
		logFrame(req.Frame(), znp.logOutFrames, znp.outFramesLog)
		znp.u.WriteFrame(req.Frame())
	}
}

func makeSyncResponseProcessor(syncRsp chan *unp.Frame, syncErr chan error) func(frame *unp.Frame) {
	return func(frame *unp.Frame) {
		if frame.Subsystem == unp.S_RES0 && frame.Command == 0 {
			errorCode := frame.Payload[0]
			var errorMessage string
			switch errorCode {
			case 1:
				errorMessage = "Invalid subsystem"
			case 2:
				errorMessage = "Invalid command ID"
			case 3:
				errorMessage = "Invalid parameter"
			case 4:
				errorMessage = "Invalid length"
			}
			syncErr <- errors.New(errorMessage)
		} else {
			syncRsp <- frame
		}
	}
}

func makeAsyncResponseProcessor(znp *Znp) func(frame *unp.Frame) {
	return func(frame *unp.Frame) {
		key := key{frame.Subsystem, frame.Command}
		if value, ok := asyncCommandRegistry[key]; ok {
			copy := reflection.Copy(value)
			bin.Decode(frame.Payload, copy)
			znp.asyncInbound <- copy
		} else {
			znp.errors <- fmt.Errorf("Unknown async command received: %v", frame)
		}
	}
}

func logFrame(frame *unp.Frame, log bool, logger chan *unp.Frame) {
	if log {
		logger <- frame
	}
}
