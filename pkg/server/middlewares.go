package server

import (
	"github.com/Cris10Lobo/mhotas/pkg/logger"
	"github.com/gofiber/contrib/fiberzap"
	"github.com/gofiber/contrib/websocket"
	"github.com/gofiber/fiber/v2"
)

func zap() func(*fiber.Ctx) error {
	return fiberzap.New(fiberzap.Config{
		Logger: logger.Log,
	})
}

func sockio() func(*fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		// IsWebSocketUpgrade returns true if the client
		// requested upgrade to the WebSocket protocol.
		if websocket.IsWebSocketUpgrade(c) {
			c.Locals("allowed", true)
			return c.Next()
		}
		return fiber.ErrUpgradeRequired
	}
}
