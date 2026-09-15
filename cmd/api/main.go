package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"erp/pkg/config"
	"erp/pkg/httpserver"
	"erp/pkg/mongox"
	"erp/pkg/postgres"
	"erp/pkg/rabbit"
	httpadapter "erp/services/invoicing-service/internal/adapters/http"
	mongoadapter "erp/services/invoicing-service/internal/adapters/mongo"
	pgadapter "erp/services/invoicing-service/internal/adapters/postgres"
	rabbitadapter "erp/services/invoicing-service/internal/adapters/rabbit"
	"erp/services/invoicing-service/internal/application"
	"erp/services/invoicing-service/migrations"
)

func main() {
	cfg := config.Load()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := postgres.EnsureDatabase(ctx, cfg.Postgres.Host, cfg.Postgres.Port, cfg.Postgres.User, cfg.Postgres.Password, cfg.Postgres.DB); err != nil {
		log.Fatal(err)
	}
	pool, err := postgres.Connect(ctx, cfg.Postgres.DSN())
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()
	if err := postgres.Migrate(ctx, pool, migrations.FS, "."); err != nil {
		log.Fatal(err)
	}

	db, disconnect, err := mongox.Connect(ctx, cfg.Mongo.URI(), cfg.Mongo.DB)
	if err != nil {
		log.Fatal(err)
	}
	defer disconnect()

	bus, err := rabbit.Wait(cfg.Rabbit.URI(), 15)
	if err != nil {
		log.Fatal(err)
	}
	defer bus.Close()

	svc := application.New(pgadapter.New(pool), mongoadapter.New(db), rabbitadapter.NewPublisher(bus))
	if err := rabbitadapter.Consume(bus, svc); err != nil {
		log.Fatal(err)
	}

	engine := httpserver.New(cfg.ServiceName)
	httpadapter.New(svc).Register(engine, httpserver.JWT(cfg.JWTSecret, cfg.JWTIssuer))

	srv := &http.Server{Addr: ":" + cfg.HTTPPort, Handler: engine}
	go func() {
		log.Printf("%s listening on %s", cfg.ServiceName, srv.Addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()
	<-ctx.Done()
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdown)
}
