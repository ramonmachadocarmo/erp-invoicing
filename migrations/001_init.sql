CREATE TABLE invoices (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    sales_order_id UUID,
    purchase_order_id UUID,
    invoice_number INT NOT NULL,
    series VARCHAR(5) NOT NULL,
    access_key VARCHAR(44) UNIQUE,
    status VARCHAR(30) NOT NULL,
    direction VARCHAR(10) NOT NULL DEFAULT 'OUT',
    source VARCHAR(20) NOT NULL DEFAULT 'ORDER',
    file_name VARCHAR(255) NOT NULL DEFAULT '',
    total_products NUMERIC(15,2) NOT NULL,
    total_taxes NUMERIC(15,2) NOT NULL,
    total_invoice NUMERIC(15,2) NOT NULL,
    mongo_xml_id VARCHAR(50),
    issued_at TIMESTAMPTZ
);

CREATE INDEX idx_invoices_access_key ON invoices(access_key);
CREATE INDEX idx_invoices_sales_order ON invoices(sales_order_id);
CREATE INDEX idx_invoices_status ON invoices(status);
CREATE INDEX idx_invoices_direction ON invoices(direction);
