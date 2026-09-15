package domain

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound = errors.New("not found")
	ErrInvalid  = errors.New("invalid status")
	// ErrInUse: the invoice already caused a real stock movement (AUTHORIZED/CONFIRMED —
	// both are only reached after Service.Issue/Confirm already published IssuedEvent) and
	// can no longer be deleted without leaving that movement unaccounted for.
	ErrInUse = errors.New("cannot delete: invoice already issued")
)

const (
	DirectionIn  = "IN"
	DirectionOut = "OUT"
	SourceOrder  = "ORDER"
	SourceImport = "IMPORT"
	SourceManual = "MANUAL"
)

type Invoice struct {
	ID              string     `json:"id"`
	SalesOrderID    *string    `json:"sales_order_id"`
	PurchaseOrderID *string    `json:"purchase_order_id"`
	InvoiceNumber   int        `json:"invoice_number"`
	Series          string     `json:"series"`
	AccessKey       *string    `json:"access_key"`
	Status          string     `json:"status"`
	Direction       string     `json:"direction"`
	Source          string     `json:"source"`
	FileName        string     `json:"file_name"`
	TotalProducts   float64    `json:"total_products"`
	TotalTaxes      float64    `json:"total_taxes"`
	TotalInvoice    float64    `json:"total_invoice"`
	MongoXMLID      *string    `json:"mongo_xml_id"`
	IssuedAt        *time.Time `json:"issued_at"`
}

type InvoiceDocument struct {
	ID            string         `bson:"_id,omitempty" json:"id"`
	InvoiceID     string         `bson:"invoice_id" json:"invoice_id"`
	AccessKey     string         `bson:"access_key" json:"access_key"`
	FileName      string         `bson:"file_name" json:"file_name"`
	MimeType      string         `bson:"mime_type" json:"mime_type"`
	XMLContent    string         `bson:"xml_content" json:"xml_content"`
	PDFContent    string         `bson:"pdf_content,omitempty" json:"pdf_content,omitempty"`
	SEFAZResponse map[string]any `bson:"sefaz_response" json:"sefaz_response"`
	CreatedAt     time.Time      `bson:"created_at" json:"created_at"`
}

type ImportFile struct {
	FileName        string
	MimeType        string
	Content         []byte
	Direction       string
	PurchaseOrderID string
}

type OrderEvent struct {
	OrderID     string  `json:"order_id"`
	WarehouseID string  `json:"warehouse_id"`
	CustomerID  string  `json:"customer_id"`
	TotalAmount float64 `json:"total_amount"`
}

type IssuedEvent struct {
	InvoiceID       string        `json:"invoice_id"`
	SalesOrderID    string        `json:"sales_order_id"`
	PurchaseOrderID string        `json:"purchase_order_id"`
	Direction       string        `json:"direction"`
	WarehouseID     string        `json:"warehouse_id"`
	AccessKey       string        `json:"access_key"`
	Items           []InvoiceLine `json:"items"`
}

type InvoiceLine struct {
	ProductID string  `json:"product_id"`
	Quantity  float64 `json:"quantity"`
}

type ConfirmInput struct {
	WarehouseID string        `json:"warehouse_id"`
	Items       []InvoiceLine `json:"items"`
}

type InvoiceRepository interface {
	NextNumber(ctx context.Context) (int, error)
	Create(ctx context.Context, inv Invoice) (Invoice, error)
	Get(ctx context.Context, id string) (Invoice, error)
	GetBySalesOrder(ctx context.Context, orderID string) (Invoice, error)
	List(ctx context.Context, direction string) ([]Invoice, error)
	MarkIssued(ctx context.Context, id, accessKey, mongoID string) error
	MarkConfirmed(ctx context.Context, id string) error
	LinkDocument(ctx context.Context, id, accessKey, mongoID string) error
	Delete(ctx context.Context, id string) error
}

type DocumentRepository interface {
	Save(ctx context.Context, doc InvoiceDocument) (string, error)
	GetByInvoice(ctx context.Context, invoiceID string) (InvoiceDocument, error)
}

type EventPublisher interface {
	PublishIssued(ctx context.Context, ev IssuedEvent) error
}
