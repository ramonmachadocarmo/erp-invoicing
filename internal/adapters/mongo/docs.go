package mongoadapter

import (
	"context"
	"errors"

	"erp/services/invoicing-service/internal/domain"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

type Docs struct {
	col *mongo.Collection
}

func New(db *mongo.Database) *Docs {
	return &Docs{col: db.Collection("invoice_documents")}
}

func (r *Docs) Save(ctx context.Context, doc domain.InvoiceDocument) (string, error) {
	if doc.ID == "" {
		return "", errors.New("missing document id")
	}
	_, err := r.col.InsertOne(ctx, doc)
	return doc.ID, err
}

func (r *Docs) GetByInvoice(ctx context.Context, invoiceID string) (domain.InvoiceDocument, error) {
	var doc domain.InvoiceDocument
	err := r.col.FindOne(ctx, bson.M{"invoice_id": invoiceID}).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return domain.InvoiceDocument{}, domain.ErrNotFound
	}
	return doc, err
}
