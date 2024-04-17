package notification

import (
	"log"
	"net/http"

	"github.com/gorilla/websocket"
)

type appwebsocket struct {
	con *websocket.Conn
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

var savedsocketreader []*appwebsocket

// SubscribeNotificationsWebsocket sends all notifications from the app to a websocket
func (c Handler) SubscribeNotificationsWebsocket(w http.ResponseWriter, r *http.Request) {
	c.log.Debug("socket request")
	if savedsocketreader == nil {
		savedsocketreader = make([]*appwebsocket, 0)
	}

	defer func() {
		err := recover()
		if err != nil {
			c.log.Errorln(err)
		}
		err = r.Body.Close()
		if err != nil {
			c.log.Errorln(err)
		}

	}()
	con, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		c.log.Errorln(err)
	}

	ptrSocketReader := &appwebsocket{
		con: con,
	}

	savedsocketreader = append(savedsocketreader, ptrSocketReader)

	// instantiate the channel
	c.ns.InitChannel()

	// close the channel after exit the function
	defer func() {
		c.ns.DeferChannel()
	}()

	for {
		select {
		case msg, ok := <-c.ns.GetChannel():
			if !ok {
				c.log.Debugln("GetChannel not ok")
				return
			}
			err := ptrSocketReader.con.WriteMessage(websocket.TextMessage, []byte(msg))
			if err != nil {
				log.Println(err)
			}
		case <-r.Context().Done():
			c.log.Debugln("SSE: connection was closed.")
			return
		}
	}
}
