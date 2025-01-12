package sockets

import (
	"fmt"

	"github.com/gofiber/contrib/socketio"
	"github.com/gofiber/fiber/v2"
)

var clients = make(map[string]string, 1)

func NewSocketHandler() fiber.Handler {
	return socketio.New(func(kws *socketio.Websocket) {

		// Retrieve the user id from endpoint
		userId := kws.Params("id")

		// Add the connection to the list of the connected clients
		// The UUID is generated randomly and is the key that allow
		// socketio to manage Emit/EmitTo/Broadcast
		clients[userId] = kws.UUID

		// Every websocket connection has an optional session key => value storage
		kws.SetAttribute("user_id", userId)

		//Broadcast to all the connected users the newcomer
		kws.Broadcast([]byte(fmt.Sprintf("New user connected: %s and UUID: %s", userId, kws.UUID)), true, socketio.TextMessage)
		//Write welcome message
		kws.Emit([]byte(fmt.Sprintf("Hello user: %s with UUID: %s", userId, kws.UUID)), socketio.TextMessage)
	})
}
