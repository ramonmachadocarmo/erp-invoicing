package rabbitadapter

import (
	"context"
	"encoding/json"
	"log"

	"erp/pkg/rabbit"
	"erp/services/invoicing-service/internal/application"
	"erp/services/invoicing-service/internal/domain"
)

type Publisher struct {
	client *rabbit.Client
}

func NewPublisher(client *rabbit.Client) *Publisher {
	return &Publisher{client: client}
}

func (p *Publisher) PublishIssued(ctx context.Context, ev domain.IssuedEvent) error {
	body, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	if err := p.client.EnsureTopology("invoicing.exchange", "", ""); err != nil {
		return err
	}
	return p.client.Publish(ctx, "invoicing.exchange", "invoicing.nfe.issued", body)
}

func Consume(client *rabbit.Client, svc *application.Service) error {
	if err := client.EnsureTopology("stock.exchange", "invoicing.stock-reserved.queue", "stock.reserved"); err != nil {
		return err
	}
	if err := client.EnsureTopology("invoicing.exchange", "", ""); err != nil {
		return err
	}
	if err := client.Consume("invoicing.stock-reserved.queue", func(body []byte) error {
		var ev domain.OrderEvent
		if err := json.Unmarshal(body, &ev); err != nil {
			return err
		}
		if err := svc.OnStockReserved(context.Background(), ev); err != nil {
			log.Printf("draft invoice %s: %v", ev.OrderID, err)
			return err
		}
		return nil
	}); err != nil {
		return err
	}
	if err := client.EnsureTopology("sales.exchange", "invoicing.order-cancelled.queue", "sales.order.cancelled"); err != nil {
		return err
	}
	return client.Consume("invoicing.order-cancelled.queue", func(body []byte) error {
		var ev domain.OrderEvent
		if err := json.Unmarshal(body, &ev); err != nil {
			return err
		}
		if err := svc.OnOrderCancelled(context.Background(), ev); err != nil {
			log.Printf("void draft invoice for order %s: %v", ev.OrderID, err)
			return err
		}
		return nil
	})
}
