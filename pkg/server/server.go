package server

import (
	"github.com/gofiber/fiber/v2"
)

func fiberApp() *fiber.App {
	c := fiber.Config{
		ServerHeader:          "mhotas-server",
		AppName:               "mhotas",
		EnablePrintRoutes:     false,
		DisableStartupMessage: true,
	}

	app := fiber.New(c)

	app.Use(zap(), sockio())

	return app
}

func Start() *fiber.App {
	app := fiberApp()
	loadRoutes(app)

	return app
}
