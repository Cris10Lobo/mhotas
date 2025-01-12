package server

import (
	sockH "github.com/Cris10Lobo/mhotas/pkg/sockets"
	"github.com/gofiber/fiber/v2"
)

func loadRoutes(app *fiber.App) {
	router := app.Group("mhotas")

	router.Get("/health", func(c *fiber.Ctx) error {
		return c.SendString("mhotas Online")
	})

	sockets(router)
}

func sockets(r fiber.Router) {
	sock := r.Group("/ws", sockio())
	sock.Get("/:id", sockH.NewSocketHandler())
}
