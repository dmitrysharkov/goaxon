// Package main exposes the orders bounded context over HTTP using
// chi as the driving adapter. The HTTP handlers translate requests
// to / from the typed application service in ../orders/app — they
// only deal in standard Go types (string, int, etc.) and never touch
// the command/query buses, the event store, or domain value objects
// directly.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/dmitrysharkov/goaxon/examples/orders/app"
	"github.com/dmitrysharkov/goaxon/store/memory"
	"github.com/dmitrysharkov/goaxon/validation"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

func main() {
	addr := ":8080"
	log.Printf("orders-http listening on %s", addr)
	if err := http.ListenAndServe(addr, newServer()); err != nil {
		log.Fatal(err)
	}
}

// newServer wires in-memory infrastructure, builds the application
// service, and returns a chi router. Split out from main so tests
// can drive it without binding a port.
func newServer() http.Handler {
	orders := app.New(memory.NewBus(), memory.NewStore())

	h := &handler{orders: orders}

	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(5 * time.Second))

	r.Post("/orders", h.placeOrder)
	r.Post("/orders/{id}/ship", h.shipOrder)
	r.Get("/orders/{id}", h.getOrder)

	return r
}

type handler struct {
	orders *app.Orders
}

type placeOrderRequest struct {
	Customer string `json:"customer"`
	Amount   int    `json:"amount"`
}

type placeOrderResponse struct {
	OrderID string `json:"order_id"`
}

func (h *handler) placeOrder(w http.ResponseWriter, r *http.Request) {
	var req placeOrderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON: %v", err))
		return
	}
	id, err := h.orders.PlaceOrder(r.Context(), req.Customer, req.Amount)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, placeOrderResponse{OrderID: id})
}

func (h *handler) shipOrder(w http.ResponseWriter, r *http.Request) {
	if err := h.orders.ShipOrder(r.Context(), chi.URLParam(r, "id")); err != nil {
		writeAppError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) getOrder(w http.ResponseWriter, r *http.Request) {
	summary, err := h.orders.GetOrder(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, summary)
}

// writeAppError maps the app layer's three error categories to HTTP:
// *ValidationError → 422 with per-field detail; ErrNotFound → 404;
// anything else → 400 (domain-rule failures land here).
func writeAppError(w http.ResponseWriter, err error) {
	var verr *validation.Error
	if errors.As(err, &verr) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"errors": verr.Fields})
		return
	}
	if errors.Is(err, app.ErrNotFound) {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeError(w, http.StatusBadRequest, err.Error())
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
