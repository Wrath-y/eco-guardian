package main

import (
	"context"
	"log"
	"os/signal"
	"syscall"

	"github.com/zouyi/eco-guardian/internal/app"
)

func main() {
	application, err := app.New(app.Config{})
	if err != nil {
		log.Fatal(err)
	}
	if err := application.Start(); err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
	if err := application.Close(context.Background()); err != nil {
		log.Print(err)
	}
}
