// Copyright (c) 2019 - for information on the respective copyright owner
// see the NOTICE file and/or the repository at
//     https://github.com/direct-state-transfer/dst-go/NOTICE
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package channel

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/gorilla/websocket"
)

type wsConnInterface interface {
	Close() error

	SetWriteDeadline(time.Time) error
	WriteMessage(int, []byte) error
	WriteJSON(interface{}) error

	ReadJSON(interface{}) error
	SetReadLimit(int64)
	SetPongHandler(func(string) error)
	SetReadDeadline(time.Time) error
}

type wsConfigType struct {
	writeWait      time.Duration
	pongWait       time.Duration
	pingPeriod     time.Duration
	maxMessageSize int64
}

var wsConfig = wsConfigType{
	writeWait:      10 * time.Second,
	pongWait:       60 * time.Second,
	pingPeriod:     ((60 * time.Second) * 9) / 10, //ping period = (pongWait * 9)/10
	maxMessageSize: 1024,
}

type wsChannel struct {
	*genericChannelAdapter
	wsConn *websocket.Conn
}

//Shutdown enforces the specific adapter to provide a mechanism to shutdown listener
type Shutdown interface {
	Shutdown(context.Context) error
}

func wsStartListener(addr, endpoint string, maxConn uint32) (
	sh Shutdown, inConn chan *Instance, err error) {

	inConn = make(chan *Instance, maxConn)

	listnerMux := http.NewServeMux()
	listnerMux.HandleFunc(endpoint, func(w http.ResponseWriter, r *http.Request) {
		wsConnHandler(inConn, w, r)
	})

	srv := &http.Server{
		Addr:    addr,
		Handler: listnerMux,
	}

	if addr == "" {
		addr = ":http"
	}

	///Starting listener and server separately enables the program to catch
	//errors when listening has failed to start
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return srv, nil, err
	}

	go func() {
		err := srv.Serve(tcpKeepAliveListener{ln.(*net.TCPListener)})
		if err != nil {
			//ErrServerClosed is returned when the server is shutdown by user intentionally
			if err == http.ErrServerClosed {
				logger.Info("Listener at ", addr, " shutdown successfully")
			} else {
				logger.Error("Listener at ", addr, " shutdown with error -", err.Error())
			}
		}
	}()

	return srv, inConn, nil
}

func wsConnHandler(inConn chan *Instance, w http.ResponseWriter, r *http.Request) {

	var upgrader = websocket.Upgrader{}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		//Errors returned by upgrader.Upgrade are due to issues in the
		//incoming request. Hence log and ignore the connection
		logger.Error("Error in incoming request format :", err.Error())
		return
	}

	ch := &wsChannel{
		genericChannelAdapter: &genericChannelAdapter{
			connected:        true,
			writeHandlerPipe: newHandlerPipe(handlerPipeModeWrite),
			readHandlerPipe:  newHandlerPipe(handlerPipeModeRead),
		},
		wsConn: conn,
	}

	//start read and write handler go routines
	go wsWriteHandler(wsConfig, ch.wsConn, ch.writeHandlerPipe, ch)
	go wsReadHandler(wsConfig, ch.wsConn, ch.readHandlerPipe, ch)

	cha := &Instance{
		adapter: ch,
	}

	inConn <- cha
}

func newWsChannel(addr, endpoint string) (cha *Instance, err error) {

	peerURL := url.URL{Scheme: "ws", Host: addr, Path: endpoint}

	conn, _, err := websocket.DefaultDialer.Dial(peerURL.String(), nil)
	if err != nil {
		return nil, err
	}

	ch := &wsChannel{
		genericChannelAdapter: &genericChannelAdapter{
			connected:        true,
			writeHandlerPipe: newHandlerPipe(handlerPipeModeWrite),
			readHandlerPipe:  newHandlerPipe(handlerPipeModeRead),
		},
		wsConn: conn,
	}

	//start read and write handler go routines
	go wsWriteHandler(wsConfig, ch.wsConn, ch.writeHandlerPipe, ch)
	go wsReadHandler(wsConfig, ch.wsConn, ch.readHandlerPipe, ch)

	cha = &Instance{
		adapter: ch,
	}

	return cha, err
}

func wsReadHandler(wsConfig wsConfigType, wsConn wsConnInterface, pipe handlerPipe, ch Closer) {
	defer func() {
		err := wsConn.Close()
		if err != nil {
			logger.Error("Error closing connection -", err)
		}
		logger.Debug("Exiting messageReceiver")
		pipe.quit <- true
	}()

	//Set initial configuration for reading on the websocket connection
	wsConn.SetReadLimit(wsConfig.maxMessageSize)
	err := wsConn.SetReadDeadline(time.Now().Add(wsConfig.pongWait))
	if err != nil {
		logger.Error("Error setting read deadline -", err)
		return
	}
	wsConn.SetPongHandler(func(string) error {
		return wsConn.SetReadDeadline(time.Now().Add(wsConfig.pongWait))
	})

	var message chMsgPkt

	//Timeperiod to do repeat reads
	ticker := time.NewTicker(100 * time.Millisecond)
	for {
		select {
		case <-pipe.quit:
			ticker.Stop()
			return
		case <-ticker.C:
			//ReadJSON caused only two types of error
			//1. Close error - when websocket connections is closed. It is permanent
			//2. io.UnexpectedEOF error - due to json parsing
			err := wsConn.ReadJSON(&message)

			if err != nil && websocket.IsUnexpectedCloseError(err) {
				//Websocket connection closed
				logger.Info("Connection closed by peer -", err)
				ticker.Stop()
				//If receiver has obtained lock, signal handler error it so that it exists
				//And Lock will be available for Close()
				pipe.handlerError <- err
				go func() {
					err := ch.Close()
					if err != nil {
						logger.Error("Error closing channel-", err)
					}
				}()
				return
			}

			msgPacket := jsonMsgPacket{message, err}
			pipe.msgPacket <- msgPacket
		}
	}
}

func wsWriteHandler(wsConfig wsConfigType, wsConn wsConnInterface, pipe handlerPipe, ch Closer) {

	ticker := time.NewTicker(wsConfig.pingPeriod)

	defer func() {
		ticker.Stop()
		err := wsConn.Close()
		if err != nil {
			logger.Info("error already closed by peer -", err)
		}
		logger.Debug("Exiting messageSender")
		pipe.quit <- true
	}()

	for {
		select {
		case msgPacket := <-pipe.msgPacket:
			err := wsConn.SetWriteDeadline(time.Now().Add(wsConfig.writeWait))
			if err != nil {
				logger.Error("Error setting write deadline -", err)
				msgPacket.err = err
				pipe.msgPacket <- msgPacket
				return
			}

			err = wsConn.WriteJSON(msgPacket.message)
			if err != nil && websocket.IsUnexpectedCloseError(err) {
				//Websocket connection closed
				logger.Info("Connection closed by peer -", err)
				ticker.Stop()
				//If writer has obtained lock, signal handler error it so that it exists
				//And Lock will be available for Close()
				pipe.handlerError <- err
				go func() {
					err := ch.Close()
					if err != nil {
						logger.Error("Error closing channel-", err)
					}
				}()
				return
			}
			msgPacket.err = err
			pipe.msgPacket <- msgPacket

		case <-ticker.C:
			//Ping period has passed, send ping message
			err := wsConn.SetWriteDeadline(time.Now().Add(wsConfig.writeWait))
			if err != nil {
				logger.Error("Error setting write deadline -", err)
				pipe.handlerError <- err
				return
			}
			err = wsConn.WriteMessage(websocket.PingMessage, nil)
			if err != nil {
				pipe.handlerError <- err
				return
			}
		case <-pipe.quit:
			return

		}
	}
}

// tcpKeepAliveListener is defined to override Accept method of default listener to enable keepAlive
type tcpKeepAliveListener struct {
	*net.TCPListener
}

// Accept sets keepAlive option and timeout on incoming connections so dead TCP connections go away eventually
func (ln tcpKeepAliveListener) Accept() (c net.Conn, err error) {
	tc, err := ln.AcceptTCP()
	if err != nil {
		return
	}
	err = tc.SetKeepAlive(true)
	if err != nil {
		return
	}
	err = tc.SetKeepAlivePeriod(3 * time.Minute)
	if err != nil {
		return
	}
	return tc, nil
}
