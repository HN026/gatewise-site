package main

import (
	"context"
	"github.com/HN026/gatewise-examples/commerce/internal/config"
	"github.com/HN026/gatewise-examples/commerce/internal/server"
	"log"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	c, e := config.Load(os.Args[1:])
	if e != nil {
		log.Fatal(e)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if e = server.Run(ctx, c); e != nil {
		log.Fatal("orders server failed: ", e)
	}
}
