package main

import (
	"log"
	"os"
	"os/signal"

	"github.com/Cris10Lobo/mhotas/pkg/logger"
	"github.com/Cris10Lobo/mhotas/pkg/server"
	"github.com/common-nighthawk/go-figure"
)

func main() {
	printInitName("mhotas")

	logger.New()
	app := server.Start()

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	go func() {
		<-c
		log.Println("Received Interrupt, Gracefully shutting down...")
		_ = app.Shutdown()
	}()

	if err := app.Listen(":8080"); err != nil {
		log.Panic(err)
	}

	log.Println("Running cleanup tasks...")
}

func printInitName(name string) {
	mhotas := figure.NewColorFigure(name, "larry3d", "green", true)
	mhotas.Print()
}
