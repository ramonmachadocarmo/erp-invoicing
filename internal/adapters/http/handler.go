package httpadapter

import (
	"errors"
	"io"
	"net/http"
	"strings"

	"erp/pkg/httpserver"
	"erp/services/invoicing-service/internal/application"
	"erp/services/invoicing-service/internal/domain"

	"github.com/gin-gonic/gin"
)

type Handler struct {
	svc *application.Service
}

func New(svc *application.Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) Register(r *gin.Engine, jwt gin.HandlerFunc) {
	api := r.Group("/", jwt)
	api.GET("/invoices", h.list)
	api.POST("/invoices/import", h.importFile)
	api.GET("/invoices/:id", h.get)
	api.GET("/invoices/:id/document", h.document)
	api.POST("/invoices/:id/issue", h.issue)
	api.POST("/invoices/:id/confirm", h.confirm)
	api.DELETE("/invoices/:id", h.delete)
}

func (h *Handler) delete(c *gin.Context) {
	if err := h.svc.Delete(c.Request.Context(), c.Param("id")); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, domain.ErrNotFound) {
			status = http.StatusNotFound
		} else if errors.Is(err, domain.ErrInUse) {
			status = http.StatusConflict
		}
		httpserver.Error(c, status, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) list(c *gin.Context) {
	out, err := h.svc.List(c.Request.Context(), c.Query("direction"))
	if err != nil {
		httpserver.Error(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

func (h *Handler) get(c *gin.Context) {
	out, err := h.svc.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		httpserver.Error(c, http.StatusNotFound, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

func (h *Handler) document(c *gin.Context) {
	out, err := h.svc.Document(c.Request.Context(), c.Param("id"))
	if err != nil {
		httpserver.Error(c, http.StatusNotFound, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

func (h *Handler) issue(c *gin.Context) {
	out, err := h.svc.Issue(c.Request.Context(), c.Param("id"))
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, domain.ErrNotFound) {
			status = http.StatusNotFound
		}
		if errors.Is(err, domain.ErrInvalid) {
			status = http.StatusConflict
		}
		httpserver.Error(c, status, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

func (h *Handler) confirm(c *gin.Context) {
	var in domain.ConfirmInput
	if err := c.ShouldBindJSON(&in); err != nil {
		httpserver.Error(c, http.StatusBadRequest, err)
		return
	}
	out, err := h.svc.Confirm(c.Request.Context(), c.Param("id"), in)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, domain.ErrNotFound) {
			status = http.StatusNotFound
		}
		if errors.Is(err, domain.ErrInvalid) {
			status = http.StatusBadRequest
		}
		httpserver.Error(c, status, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

func (h *Handler) importFile(c *gin.Context) {
	direction := strings.ToUpper(c.PostForm("direction"))
	po := strings.TrimSpace(c.PostForm("purchase_order_id"))
	withoutNote := strings.EqualFold(c.PostForm("without_note"), "true") || c.PostForm("without_note") == "1"
	file, err := c.FormFile("file")
	if err != nil {
		if !withoutNote {
			httpserver.Error(c, http.StatusBadRequest, err)
			return
		}
		out, err := h.svc.Import(c.Request.Context(), domain.ImportFile{
			Direction:       direction,
			PurchaseOrderID: po,
		})
		if err != nil {
			httpserver.Error(c, http.StatusBadRequest, err)
			return
		}
		c.JSON(http.StatusCreated, out)
		return
	}
	src, err := file.Open()
	if err != nil {
		httpserver.Error(c, http.StatusBadRequest, err)
		return
	}
	defer src.Close()
	body, err := io.ReadAll(src)
	if err != nil {
		httpserver.Error(c, http.StatusBadRequest, err)
		return
	}
	out, err := h.svc.Import(c.Request.Context(), domain.ImportFile{
		FileName:        file.Filename,
		MimeType:        file.Header.Get("Content-Type"),
		Content:         body,
		Direction:       direction,
		PurchaseOrderID: po,
	})
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, domain.ErrInvalid) {
			status = http.StatusBadRequest
		}
		httpserver.Error(c, status, err)
		return
	}
	c.JSON(http.StatusCreated, out)
}
