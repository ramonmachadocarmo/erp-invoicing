package application

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"erp/services/invoicing-service/internal/domain"

	"github.com/google/uuid"
)

type Service struct {
	invoices domain.InvoiceRepository
	docs     domain.DocumentRepository
	pub      domain.EventPublisher
}

func New(invoices domain.InvoiceRepository, docs domain.DocumentRepository, pub domain.EventPublisher) *Service {
	return &Service{invoices: invoices, docs: docs, pub: pub}
}

// Delete removes a draft/unconfirmed invoice — PENDING_SEFAZ (never issued), REJECTED (issue
// failed), or IMPORTED (uploaded but not yet confirmed). AUTHORIZED and CONFIRMED are refused:
// both are only reached after Issue/Confirm already published an IssuedEvent that stock-service
// acted on, so deleting the record at that point would leave a real stock movement with no
// invoice behind it.
func (s *Service) Delete(ctx context.Context, id string) error {
	inv, err := s.invoices.Get(ctx, id)
	if err != nil {
		return err
	}
	if inv.Status == "AUTHORIZED" || inv.Status == "CONFIRMED" {
		return domain.ErrInUse
	}
	return s.invoices.Delete(ctx, id)
}

// OnOrderCancelled removes a sales order's draft NFe when the order itself is cancelled or
// deleted in sales-service — otherwise the draft (auto-created by OnStockReserved as soon as
// stock reserves, well before cancellation is possible) sits around fully confirmable, letting
// staff issue a real NFe for an order that no longer exists. Mirrors Delete's own guard: an
// AUTHORIZED/CONFIRMED invoice is a real fiscal document already, so it's left alone here —
// unwinding it needs an actual NFe cancellation, not a silent delete.
func (s *Service) OnOrderCancelled(ctx context.Context, ev domain.OrderEvent) error {
	inv, err := s.invoices.GetBySalesOrder(ctx, ev.OrderID)
	if errors.Is(err, domain.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if inv.Status == "AUTHORIZED" || inv.Status == "CONFIRMED" {
		return nil
	}
	return s.invoices.Delete(ctx, inv.ID)
}

func (s *Service) OnStockReserved(ctx context.Context, ev domain.OrderEvent) error {
	if _, err := s.invoices.GetBySalesOrder(ctx, ev.OrderID); err == nil {
		return nil
	}
	n, err := s.invoices.NextNumber(ctx)
	if err != nil {
		return err
	}
	icms := ev.TotalAmount * 0.18
	pis := ev.TotalAmount * 0.0165
	cofins := ev.TotalAmount * 0.076
	taxes := icms + pis + cofins
	orderID := ev.OrderID
	_, err = s.invoices.Create(ctx, domain.Invoice{
		SalesOrderID:  &orderID,
		InvoiceNumber: n,
		Series:        "1",
		Status:        "PENDING_SEFAZ",
		Direction:     domain.DirectionOut,
		Source:        domain.SourceOrder,
		TotalProducts: ev.TotalAmount,
		TotalTaxes:    taxes,
		TotalInvoice:  ev.TotalAmount + taxes,
	})
	return err
}

func (s *Service) List(ctx context.Context, direction string) ([]domain.Invoice, error) {
	return s.invoices.List(ctx, direction)
}

func (s *Service) Get(ctx context.Context, id string) (domain.Invoice, error) {
	return s.invoices.Get(ctx, id)
}

func (s *Service) Document(ctx context.Context, id string) (domain.InvoiceDocument, error) {
	return s.docs.GetByInvoice(ctx, id)
}

func (s *Service) Import(ctx context.Context, in domain.ImportFile) (domain.Invoice, error) {
	if in.Direction != domain.DirectionIn && in.Direction != domain.DirectionOut {
		return domain.Invoice{}, domain.ErrInvalid
	}
	po := strings.TrimSpace(in.PurchaseOrderID)
	if len(in.Content) == 0 {
		if in.Direction != domain.DirectionIn || po == "" {
			return domain.Invoice{}, domain.ErrInvalid
		}
		return s.importWithoutFile(ctx, in, po)
	}
	imgMime := imageMime(in.Content)
	if imgMime != "" && len(in.Content) > maxImageBytes {
		return domain.Invoice{}, domain.ErrInvalid
	}
	if !validImportFile(in.FileName, in.MimeType, in.Content) {
		return domain.Invoice{}, domain.ErrInvalid
	}
	parsed := parseImport(in)
	n, err := s.invoices.NextNumber(ctx)
	if err != nil {
		return domain.Invoice{}, err
	}
	if parsed.Number == 0 {
		parsed.Number = n
	}
	now := time.Now().UTC()
	inv := domain.Invoice{
		InvoiceNumber: parsed.Number,
		Series:        parsed.Series,
		Status:        "IMPORTED",
		Direction:     in.Direction,
		Source:        domain.SourceImport,
		FileName:      in.FileName,
		TotalProducts: parsed.Products,
		TotalTaxes:    parsed.Taxes,
		TotalInvoice:  parsed.Total,
		IssuedAt:      &now,
	}
	if po := strings.TrimSpace(in.PurchaseOrderID); po != "" {
		inv.PurchaseOrderID = &po
	}
	if parsed.AccessKey != "" {
		inv.AccessKey = &parsed.AccessKey
	}
	out, err := s.invoices.Create(ctx, inv)
	if err != nil {
		return domain.Invoice{}, err
	}
	doc := domain.InvoiceDocument{
		ID:        uuid.NewString(),
		InvoiceID: out.ID,
		AccessKey: parsed.AccessKey,
		FileName:  in.FileName,
		MimeType:  in.MimeType,
		CreatedAt: now,
		SEFAZResponse: map[string]any{
			"cStat":   100,
			"xMotivo": "Documento importado",
		},
	}
	if imgMime != "" {
		// Photo of a paper note/receipt: nothing to parse, just keep the picture.
		doc.ImageContent = base64.StdEncoding.EncodeToString(in.Content)
		doc.MimeType = imgMime
		doc.SEFAZResponse["xMotivo"] = "Foto de nota/recibo anexada"
	} else if strings.Contains(strings.ToLower(in.FileName+in.MimeType), "pdf") {
		doc.PDFContent = base64.StdEncoding.EncodeToString(in.Content)
		doc.MimeType = "application/pdf"
	} else {
		doc.XMLContent = string(in.Content)
		doc.MimeType = "application/xml"
	}
	mongoID, err := s.docs.Save(ctx, doc)
	if err != nil {
		return domain.Invoice{}, err
	}
	if err := s.invoices.LinkDocument(ctx, out.ID, parsed.AccessKey, mongoID); err != nil {
		return domain.Invoice{}, err
	}
	return s.invoices.Get(ctx, out.ID)
}

func (s *Service) importWithoutFile(ctx context.Context, in domain.ImportFile, po string) (domain.Invoice, error) {
	n, err := s.invoices.NextNumber(ctx)
	if err != nil {
		return domain.Invoice{}, err
	}
	now := time.Now().UTC()
	return s.invoices.Create(ctx, domain.Invoice{
		PurchaseOrderID: &po,
		InvoiceNumber:   n,
		Series:          "1",
		Status:          "IMPORTED",
		Direction:       in.Direction,
		Source:          domain.SourceManual,
		FileName:        "Sem nota",
		IssuedAt:        &now,
	})
}

// maxImageBytes keeps the base64 copy well under Mongo's 16 MB document limit.
const maxImageBytes = 8 << 20

// imageMime recognizes a JPEG or PNG by its magic bytes (the client-sent name/type is not
// trusted). Empty when the content is not one of those images.
func imageMime(content []byte) string {
	switch {
	case bytes.HasPrefix(content, []byte{0xFF, 0xD8, 0xFF}):
		return "image/jpeg"
	case bytes.HasPrefix(content, []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}):
		return "image/png"
	}
	return ""
}

func validImportFile(name, mime string, content []byte) bool {
	if imageMime(content) != "" {
		return true
	}
	n := strings.ToLower(name + " " + mime)
	if strings.Contains(n, "pdf") || bytes.HasPrefix(content, []byte("%PDF")) {
		return true
	}
	if strings.Contains(n, "xml") {
		return true
	}
	head := content
	if len(head) > 256 {
		head = head[:256]
	}
	return bytes.Contains(head, []byte("<"))
}

func (s *Service) Issue(ctx context.Context, id string) (domain.Invoice, error) {
	inv, err := s.invoices.Get(ctx, id)
	if err != nil {
		return domain.Invoice{}, err
	}
	if inv.Status != "PENDING_SEFAZ" && inv.Status != "REJECTED" {
		return domain.Invoice{}, domain.ErrInvalid
	}
	accessKey := fmt.Sprintf("%044d", time.Now().UnixNano()%1e16)
	if len(accessKey) > 44 {
		accessKey = accessKey[:44]
	}
	for len(accessKey) < 44 {
		accessKey = "1" + accessKey
	}
	xml := fmt.Sprintf(`<NFe><infNFe Id="NFe%s"><ide><nNF>%d</nNF><serie>%s</serie></ide><total><vNF>%.2f</vNF></total></infNFe></NFe>`,
		accessKey, inv.InvoiceNumber, inv.Series, inv.TotalInvoice)
	mongoID, err := s.docs.Save(ctx, domain.InvoiceDocument{
		ID:         uuid.NewString(),
		InvoiceID:  inv.ID,
		AccessKey:  accessKey,
		FileName:   fmt.Sprintf("nfe-%d.xml", inv.InvoiceNumber),
		MimeType:   "application/xml",
		XMLContent: xml,
		SEFAZResponse: map[string]any{
			"cStat":    100,
			"xMotivo":  "Autorizado o uso da NF-e",
			"protocol": fmt.Sprintf("11326%010d", inv.InvoiceNumber),
		},
		CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		return domain.Invoice{}, err
	}
	if err := s.invoices.MarkIssued(ctx, inv.ID, accessKey, mongoID); err != nil {
		return domain.Invoice{}, err
	}
	orderID := ""
	if inv.SalesOrderID != nil {
		orderID = *inv.SalesOrderID
	}
	if err := s.pub.PublishIssued(ctx, domain.IssuedEvent{
		InvoiceID: inv.ID, SalesOrderID: orderID, Direction: inv.Direction, AccessKey: accessKey,
	}); err != nil {
		return domain.Invoice{}, err
	}
	return s.invoices.Get(ctx, inv.ID)
}

func (s *Service) Confirm(ctx context.Context, id string, in domain.ConfirmInput) (domain.Invoice, error) {
	inv, err := s.invoices.Get(ctx, id)
	if err != nil {
		return domain.Invoice{}, err
	}
	if inv.Status != "IMPORTED" {
		return domain.Invoice{}, domain.ErrInvalid
	}
	if strings.TrimSpace(in.WarehouseID) == "" {
		return domain.Invoice{}, domain.ErrInvalid
	}
	var items []domain.InvoiceLine
	for _, it := range in.Items {
		if it.ProductID == "" || it.Quantity <= 0 {
			continue
		}
		items = append(items, it)
	}
	if len(items) == 0 {
		return domain.Invoice{}, domain.ErrInvalid
	}
	if err := s.invoices.MarkConfirmed(ctx, inv.ID); err != nil {
		return domain.Invoice{}, err
	}
	salesID, purchaseID := "", ""
	if inv.SalesOrderID != nil {
		salesID = *inv.SalesOrderID
	}
	if inv.PurchaseOrderID != nil {
		purchaseID = *inv.PurchaseOrderID
	}
	key := ""
	if inv.AccessKey != nil {
		key = *inv.AccessKey
	}
	if err := s.pub.PublishIssued(ctx, domain.IssuedEvent{
		InvoiceID: inv.ID, SalesOrderID: salesID, PurchaseOrderID: purchaseID,
		Direction: inv.Direction, WarehouseID: in.WarehouseID, AccessKey: key, Items: items,
	}); err != nil {
		return domain.Invoice{}, err
	}
	return s.invoices.Get(ctx, inv.ID)
}

type parsedNFe struct {
	Number    int
	Series    string
	AccessKey string
	Products  float64
	Taxes     float64
	Total     float64
}

func parseImport(in domain.ImportFile) parsedNFe {
	if imageMime(in.Content) != "" {
		return parsedNFe{Series: "1"}
	}
	name := strings.ToLower(in.FileName + " " + in.MimeType)
	if strings.Contains(name, "pdf") || bytes.HasPrefix(in.Content, []byte("%PDF")) {
		return parsePDF(in.Content)
	}
	return parseXML(in.Content)
}

func parseXML(raw []byte) parsedNFe {
	raw = regexp.MustCompile(`xmlns(:[A-Za-z0-9]+)?="[^"]*"`).ReplaceAll(raw, nil)
	type tot struct {
		VProd   string `xml:"vProd"`
		VNF     string `xml:"vNF"`
		VICMS   string `xml:"vICMS"`
		VPIS    string `xml:"vPIS"`
		VCOFINS string `xml:"vCOFINS"`
	}
	type inf struct {
		ID  string `xml:"Id,attr"`
		Ide struct {
			NNF   string `xml:"nNF"`
			Serie string `xml:"serie"`
		} `xml:"ide"`
		Total struct {
			ICMSTot tot `xml:"ICMSTot"`
		} `xml:"total"`
	}
	type nfe struct {
		Inf inf `xml:"infNFe"`
	}
	type proc struct {
		NFe nfe    `xml:"NFe"`
		Ch  string `xml:"protNFe>infProt>chNFe"`
	}
	out := parsedNFe{Series: "1"}
	var p proc
	_ = xml.Unmarshal(raw, &p)
	info := p.NFe.Inf
	if info.ID == "" {
		var n nfe
		_ = xml.Unmarshal(raw, &n)
		info = n.Inf
	}
	out.Number, _ = strconv.Atoi(info.Ide.NNF)
	if info.Ide.Serie != "" {
		out.Series = info.Ide.Serie
	}
	out.AccessKey = strings.TrimPrefix(info.ID, "NFe")
	if p.Ch != "" {
		out.AccessKey = p.Ch
	}
	if out.AccessKey == "" {
		out.AccessKey = extractKey(string(raw))
	}
	out.Products = parseNum(info.Total.ICMSTot.VProd)
	out.Total = parseNum(info.Total.ICMSTot.VNF)
	out.Taxes = parseNum(info.Total.ICMSTot.VICMS) + parseNum(info.Total.ICMSTot.VPIS) + parseNum(info.Total.ICMSTot.VCOFINS)
	if out.Total == 0 {
		out.Total = out.Products + out.Taxes
	}
	return out
}

func parsePDF(raw []byte) parsedNFe {
	text := string(raw)
	out := parsedNFe{Series: "1", AccessKey: extractKey(text)}
	return out
}

var keyRe = regexp.MustCompile(`\d{44}`)

func extractKey(s string) string {
	return keyRe.FindString(s)
}

func parseNum(s string) float64 {
	s = strings.ReplaceAll(strings.TrimSpace(s), ",", ".")
	n, _ := strconv.ParseFloat(s, 64)
	return n
}
