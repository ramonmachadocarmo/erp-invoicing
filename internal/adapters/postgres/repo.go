package postgres

import (
	"context"
	"errors"

	"erp/services/invoicing-service/internal/domain"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repo struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Repo {
	return &Repo{pool: pool}
}

func (r *Repo) NextNumber(ctx context.Context) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx, `SELECT COALESCE(MAX(invoice_number),0)+1 FROM invoices`).Scan(&n)
	return n, err
}

func (r *Repo) Create(ctx context.Context, inv domain.Invoice) (domain.Invoice, error) {
	err := r.pool.QueryRow(ctx, `
		INSERT INTO invoices (sales_order_id, purchase_order_id, invoice_number, series, status, direction, source, file_name, total_products, total_taxes, total_invoice, access_key, mongo_xml_id, issued_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		RETURNING id
	`, inv.SalesOrderID, inv.PurchaseOrderID, inv.InvoiceNumber, inv.Series, inv.Status, inv.Direction, inv.Source, inv.FileName,
		inv.TotalProducts, inv.TotalTaxes, inv.TotalInvoice, inv.AccessKey, inv.MongoXMLID, inv.IssuedAt).Scan(&inv.ID)
	return inv, err
}

func (r *Repo) Get(ctx context.Context, id string) (domain.Invoice, error) {
	inv, err := scanInvoice(r.pool.QueryRow(ctx, `
		SELECT id, sales_order_id, purchase_order_id, invoice_number, series, access_key, status, direction, source, file_name,
		       total_products, total_taxes, total_invoice, mongo_xml_id, issued_at
		FROM invoices WHERE id=$1
	`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Invoice{}, domain.ErrNotFound
	}
	return inv, err
}

func (r *Repo) GetBySalesOrder(ctx context.Context, orderID string) (domain.Invoice, error) {
	inv, err := scanInvoice(r.pool.QueryRow(ctx, `
		SELECT id, sales_order_id, purchase_order_id, invoice_number, series, access_key, status, direction, source, file_name,
		       total_products, total_taxes, total_invoice, mongo_xml_id, issued_at
		FROM invoices WHERE sales_order_id=$1
	`, orderID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Invoice{}, domain.ErrNotFound
	}
	return inv, err
}

func (r *Repo) List(ctx context.Context, direction string) ([]domain.Invoice, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, sales_order_id, purchase_order_id, invoice_number, series, access_key, status, direction, source, file_name,
		       total_products, total_taxes, total_invoice, mongo_xml_id, issued_at
		FROM invoices
		WHERE ($1 = '' OR direction = $1)
		ORDER BY invoice_number DESC
	`, direction)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Invoice
	for rows.Next() {
		inv, err := scanInvoice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, inv)
	}
	if out == nil {
		out = []domain.Invoice{}
	}
	return out, rows.Err()
}

func (r *Repo) MarkIssued(ctx context.Context, id, accessKey, mongoID string) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE invoices SET status='AUTHORIZED', access_key=$2, mongo_xml_id=$3, issued_at=NOW() WHERE id=$1
	`, id, accessKey, mongoID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *Repo) MarkConfirmed(ctx context.Context, id string) error {
	tag, err := r.pool.Exec(ctx, `UPDATE invoices SET status='CONFIRMED' WHERE id=$1 AND status='IMPORTED'`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// Delete only ever runs on a PENDING_SEFAZ/REJECTED/IMPORTED row — Service.Delete checks the
// status before calling this — so no WHERE status filter is needed here; the guard already
// happened.
func (r *Repo) Delete(ctx context.Context, id string) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM invoices WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *Repo) LinkDocument(ctx context.Context, id, accessKey, mongoID string) error {
	var key any
	if accessKey != "" {
		key = accessKey
	}
	tag, err := r.pool.Exec(ctx, `UPDATE invoices SET access_key=$2, mongo_xml_id=$3 WHERE id=$1`, id, key, mongoID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanInvoice(row scanner) (domain.Invoice, error) {
	var inv domain.Invoice
	err := row.Scan(&inv.ID, &inv.SalesOrderID, &inv.PurchaseOrderID, &inv.InvoiceNumber, &inv.Series, &inv.AccessKey,
		&inv.Status, &inv.Direction, &inv.Source, &inv.FileName, &inv.TotalProducts, &inv.TotalTaxes, &inv.TotalInvoice,
		&inv.MongoXMLID, &inv.IssuedAt)
	return inv, err
}
