package main

import (
	"context"
	"github.com/pluto4816/steam-knife-monitor-go/internal/sales"
	"os"
)

func runSales(ctx context.Context, c commandConfig) error {
	if c.SalesListen == "" {
		c.SalesListen = "127.0.0.1:8080"
	}
	if c.SalesOrigin == "" {
		c.SalesOrigin = "http://127.0.0.1:8080"
	}
	web := sales.WebConfig{Listen: c.SalesListen, Origin: c.SalesOrigin, CertFile: c.SalesCert, KeyFile: c.SalesKey, Password: os.Getenv("SALES_PASSWORD"), CandidatePath: c.CandidatePath}
	if err := web.Validate(); err != nil {
		return err
	}
	s, err := sales.Open(c.DBPath)
	if err != nil {
		return err
	}
	defer s.Close()
	return sales.Serve(ctx, s, web)
}
