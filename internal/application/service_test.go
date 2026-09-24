package application

import (
	"bytes"
	"context"
	"encoding/base64"
	"testing"

	"erp/services/invoicing-service/internal/domain"
)

type memInvoices struct {
	byID    map[string]domain.Invoice
	byOrder map[string]domain.Invoice
	n       int
}

func (m *memInvoices) NextNumber(context.Context) (int, error) {
	m.n++
	return m.n, nil
}

func (m *memInvoices) Create(_ context.Context, inv domain.Invoice) (domain.Invoice, error) {
	if m.byID == nil {
		m.byID = map[string]domain.Invoice{}
		m.byOrder = map[string]domain.Invoice{}
	}
	inv.ID = "i1"
	if inv.InvoiceNumber == 0 {
		m.n++
		inv.InvoiceNumber = m.n
	}
	m.byID[inv.ID] = inv
	if inv.SalesOrderID != nil {
		m.byOrder[*inv.SalesOrderID] = inv
	}
	return inv, nil
}

func (m *memInvoices) Get(_ context.Context, id string) (domain.Invoice, error) {
	inv, ok := m.byID[id]
	if !ok {
		return domain.Invoice{}, domain.ErrNotFound
	}
	return inv, nil
}

func (m *memInvoices) GetBySalesOrder(_ context.Context, orderID string) (domain.Invoice, error) {
	inv, ok := m.byOrder[orderID]
	if !ok {
		return domain.Invoice{}, domain.ErrNotFound
	}
	return inv, nil
}

func (m *memInvoices) List(context.Context, string) ([]domain.Invoice, error) { return nil, nil }

func (m *memInvoices) MarkIssued(_ context.Context, id, accessKey, mongoID string) error {
	inv, ok := m.byID[id]
	if !ok {
		return domain.ErrNotFound
	}
	inv.Status = "AUTHORIZED"
	inv.AccessKey = &accessKey
	inv.MongoXMLID = &mongoID
	m.byID[id] = inv
	return nil
}

func (m *memInvoices) MarkConfirmed(_ context.Context, id string) error {
	inv, ok := m.byID[id]
	if !ok {
		return domain.ErrNotFound
	}
	if inv.Status != "IMPORTED" {
		return domain.ErrInvalid
	}
	inv.Status = "CONFIRMED"
	m.byID[id] = inv
	return nil
}

func (m *memInvoices) Delete(_ context.Context, id string) error {
	if _, ok := m.byID[id]; !ok {
		return domain.ErrNotFound
	}
	delete(m.byID, id)
	return nil
}

func (m *memInvoices) LinkDocument(_ context.Context, id, accessKey, mongoID string) error {
	inv, ok := m.byID[id]
	if !ok {
		return domain.ErrNotFound
	}
	if accessKey != "" {
		inv.AccessKey = &accessKey
	}
	inv.MongoXMLID = &mongoID
	m.byID[id] = inv
	return nil
}

type memDocs struct {
	byInv map[string]domain.InvoiceDocument
}

func (m *memDocs) Save(_ context.Context, doc domain.InvoiceDocument) (string, error) {
	if m.byInv == nil {
		m.byInv = map[string]domain.InvoiceDocument{}
	}
	if doc.ID == "" {
		doc.ID = "d1"
	}
	m.byInv[doc.InvoiceID] = doc
	return doc.ID, nil
}

func (m *memDocs) GetByInvoice(_ context.Context, invoiceID string) (domain.InvoiceDocument, error) {
	d, ok := m.byInv[invoiceID]
	if !ok {
		return domain.InvoiceDocument{}, domain.ErrNotFound
	}
	return d, nil
}

type pubSpy struct{ n int }

func (p *pubSpy) PublishIssued(context.Context, domain.IssuedEvent) error {
	p.n++
	return nil
}

func TestOnStockReservedIdempotent(t *testing.T) {
	inv := &memInvoices{}
	svc := New(inv, &memDocs{}, &pubSpy{})
	ev := domain.OrderEvent{OrderID: "so1", TotalAmount: 1000}
	if err := svc.OnStockReserved(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
	if err := svc.OnStockReserved(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
	if len(inv.byID) != 1 {
		t.Fatalf("%d", len(inv.byID))
	}
	got := inv.byOrder["so1"]
	if got.Status != "PENDING_SEFAZ" || got.TotalProducts != 1000 {
		t.Fatalf("%+v", got)
	}
}

func TestIssue(t *testing.T) {
	inv := &memInvoices{}
	pub := &pubSpy{}
	svc := New(inv, &memDocs{}, pub)
	if err := svc.OnStockReserved(context.Background(), domain.OrderEvent{OrderID: "so1", TotalAmount: 100}); err != nil {
		t.Fatal(err)
	}
	got, err := svc.Issue(context.Background(), "i1")
	if err != nil || got.Status != "AUTHORIZED" || pub.n != 1 {
		t.Fatalf("%v %+v pub=%d", err, got, pub.n)
	}
	if _, err := svc.Issue(context.Background(), "i1"); err != domain.ErrInvalid {
		t.Fatalf("%v", err)
	}
}

func TestImportXMLAndConfirm(t *testing.T) {
	inv := &memInvoices{}
	pub := &pubSpy{}
	svc := New(inv, &memDocs{}, pub)
	xml := []byte(`<NFe><infNFe Id="NFe11111111111111111111111111111111111111111111"><ide><nNF>10</nNF><serie>1</serie></ide><total><ICMSTot><vProd>100</vProd><vNF>118</vNF><vICMS>18</vICMS><vPIS>0</vPIS><vCOFINS>0</vCOFINS></ICMSTot></total></infNFe></NFe>`)
	got, err := svc.Import(context.Background(), domain.ImportFile{
		FileName: "nfe.xml", MimeType: "application/xml", Content: xml, Direction: domain.DirectionIn, PurchaseOrderID: "po1",
	})
	if err != nil || got.Status != "IMPORTED" || got.TotalProducts != 100 || got.InvoiceNumber != 10 {
		t.Fatalf("%v %+v", err, got)
	}
	got, err = svc.Confirm(context.Background(), got.ID, domain.ConfirmInput{
		WarehouseID: "w1", Items: []domain.InvoiceLine{{ProductID: "p1", Quantity: 2}},
	})
	if err != nil || got.Status != "CONFIRMED" || pub.n != 1 {
		t.Fatalf("%v %+v pub=%d", err, got, pub.n)
	}
}

func TestImportRejectsEmpty(t *testing.T) {
	svc := New(&memInvoices{}, &memDocs{}, &pubSpy{})
	if _, err := svc.Import(context.Background(), domain.ImportFile{Direction: domain.DirectionOut}); err != domain.ErrInvalid {
		t.Fatalf("%v", err)
	}
}

func TestDeletePendingSefazSucceeds(t *testing.T) {
	inv := &memInvoices{}
	svc := New(inv, &memDocs{}, &pubSpy{})
	if err := svc.OnStockReserved(context.Background(), domain.OrderEvent{OrderID: "so1", TotalAmount: 100}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Delete(context.Background(), "i1"); err != nil {
		t.Fatalf("%v", err)
	}
	if _, err := svc.Get(context.Background(), "i1"); err != domain.ErrNotFound {
		t.Fatalf("expected invoice gone, got: %v", err)
	}
}

func TestDeleteAuthorizedRefused(t *testing.T) {
	inv := &memInvoices{}
	svc := New(inv, &memDocs{}, &pubSpy{})
	if err := svc.OnStockReserved(context.Background(), domain.OrderEvent{OrderID: "so1", TotalAmount: 100}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Issue(context.Background(), "i1"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Delete(context.Background(), "i1"); err != domain.ErrInUse {
		t.Fatalf("%v", err)
	}
}

func TestDeleteConfirmedRefused(t *testing.T) {
	inv := &memInvoices{}
	svc := New(inv, &memDocs{}, &pubSpy{})
	xml := []byte(`<NFe><infNFe Id="NFe11111111111111111111111111111111111111111111"><ide><nNF>10</nNF><serie>1</serie></ide><total><ICMSTot><vProd>100</vProd><vNF>118</vNF><vICMS>18</vICMS><vPIS>0</vPIS><vCOFINS>0</vCOFINS></ICMSTot></total></infNFe></NFe>`)
	got, err := svc.Import(context.Background(), domain.ImportFile{
		FileName: "nfe.xml", MimeType: "application/xml", Content: xml, Direction: domain.DirectionIn, PurchaseOrderID: "po1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Confirm(context.Background(), got.ID, domain.ConfirmInput{
		WarehouseID: "w1", Items: []domain.InvoiceLine{{ProductID: "p1", Quantity: 1}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Delete(context.Background(), got.ID); err != domain.ErrInUse {
		t.Fatalf("%v", err)
	}
}

func TestDeleteNotFound(t *testing.T) {
	svc := New(&memInvoices{}, &memDocs{}, &pubSpy{})
	if err := svc.Delete(context.Background(), "missing"); err != domain.ErrNotFound {
		t.Fatalf("%v", err)
	}
}

func TestImportPhotoStoresImageWithoutParsing(t *testing.T) {
	docs := &memDocs{}
	svc := New(&memInvoices{}, docs, &pubSpy{})
	jpeg := append([]byte{0xFF, 0xD8, 0xFF, 0xE0}, []byte("fake-jpeg-body")...)
	got, err := svc.Import(context.Background(), domain.ImportFile{
		FileName: "recibo.jpg", MimeType: "image/jpeg", Content: jpeg, Direction: domain.DirectionIn, PurchaseOrderID: "po1",
	})
	if err != nil || got.Status != "IMPORTED" || got.Source != domain.SourceImport || got.TotalInvoice != 0 {
		t.Fatalf("%v %+v", err, got)
	}
	doc, err := svc.Document(context.Background(), got.ID)
	if err != nil {
		t.Fatal(err)
	}
	if doc.MimeType != "image/jpeg" || doc.XMLContent != "" || doc.PDFContent != "" {
		t.Fatalf("%+v", doc)
	}
	raw, _ := base64.StdEncoding.DecodeString(doc.ImageContent)
	if !bytes.Equal(raw, jpeg) {
		t.Fatalf("image not stored intact")
	}
}

func TestImportPhotoTrustsMagicBytesNotName(t *testing.T) {
	svc := New(&memInvoices{}, &memDocs{}, &pubSpy{})
	png := append([]byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}, 1, 2, 3)
	got, err := svc.Import(context.Background(), domain.ImportFile{
		FileName: "nota.pdf", MimeType: "application/pdf", Content: png, Direction: domain.DirectionIn, PurchaseOrderID: "po1",
	})
	if err != nil {
		t.Fatal(err)
	}
	doc, _ := svc.Document(context.Background(), got.ID)
	if doc.MimeType != "image/png" || doc.ImageContent == "" || doc.PDFContent != "" {
		t.Fatalf("%+v", doc)
	}
}

func TestImportRejectsOversizedPhoto(t *testing.T) {
	svc := New(&memInvoices{}, &memDocs{}, &pubSpy{})
	big := append([]byte{0xFF, 0xD8, 0xFF}, make([]byte, maxImageBytes)...)
	_, err := svc.Import(context.Background(), domain.ImportFile{
		FileName: "x.jpg", Content: big, Direction: domain.DirectionIn, PurchaseOrderID: "po1",
	})
	if err != domain.ErrInvalid {
		t.Fatalf("%v", err)
	}
}

func TestImportRejectsBinaryThatIsNotAnImage(t *testing.T) {
	svc := New(&memInvoices{}, &memDocs{}, &pubSpy{})
	_, err := svc.Import(context.Background(), domain.ImportFile{
		FileName: "x.jpg", MimeType: "image/jpeg", Content: []byte{0x00, 0x01, 0x02}, Direction: domain.DirectionIn, PurchaseOrderID: "po1",
	})
	if err != domain.ErrInvalid {
		t.Fatalf("%v", err)
	}
}
