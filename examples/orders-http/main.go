// Package main exposes the orders domain over HTTP using chi as the
// driving adapter. The domain core, commands, queries, and projection
// are shared with ../orders/main.go via the ../orders/domain package
// — this binary only owns the HTTP-to-CQRS translation.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/dmitrysharkov/goaxon/command"
	"github.com/dmitrysharkov/goaxon/event"
	"github.com/dmitrysharkov/goaxon/examples/orders/domain"
	"github.com/dmitrysharkov/goaxon/query"
	"github.com/dmitrysharkov/goaxon/store/memory"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
)

func main() {
	addr := ":8080"
	log.Printf("orders-http listening on %s", addr)
	if err := http.ListenAndServe(addr, newServer()); err != nil {
		log.Fatal(err)
	}
}

// newServer wires in-memory infrastructure, registers domain handlers,
// and returns a chi router. Split out from main so tests can drive it
// without binding a port.
func newServer() http.Handler {
	store := memory.NewStore()
	eventBus := memory.NewBus()
	commandBus := command.New()
	queryBus := query.New()

	domain.Wire(commandBus, queryBus, eventBus, store)

	h := &handler{commandBus: commandBus, queryBus: queryBus}

	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(5 * time.Second))

	r.Post("/orders", h.placeOrder)
	r.Post("/orders/{id}/ship", h.shipOrder)
	r.Get("/orders/{id}", h.getOrder)

	return r
}

type handler struct {
	commandBus *command.Bus
	queryBus   *query.Bus
}

type placeOrderRequest struct {
	Customer string `json:"customer"`
	Amount   int    `json:"amount"`
}

type placeOrderResponse struct {
	OrderID uuid.UUID `json:"order_id"`
}

func (h *handler) placeOrder(w http.ResponseWriter, r *http.Request) {
	var req placeOrderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON: %v", err))
		return
	}
	orderID := uuid.Must(uuid.NewV7())
	if _, err := command.Send[domain.PlaceOrder, struct{}](
		r.Context(), h.commandBus,
		domain.PlaceOrder{OrderID: orderID, Customer: req.Customer, Amount: req.Amount},
	); err != nil {
		// Anything the command handler returns at this layer is a
		// validation/business-rule failure. A real app would distinguish
		// validation from infra errors with typed errors or wrapping.
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, placeOrderResponse{OrderID: orderID})
}

func (h *handler) shipOrder(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid order id")
		return
	}
	if _, err := command.Send[domain.ShipOrder, struct{}](
		r.Context(), h.commandBus, domain.ShipOrder{OrderID: id},
	); err != nil {
		if errors.Is(err, event.ErrStreamNotFound) {
			writeError(w, http.StatusNotFound, "order not found")
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) getOrder(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid order id")
		return
	}
	summary, err := query.Ask[domain.GetOrderSummary, domain.OrderSummary](
		r.Context(), h.queryBus, domain.GetOrderSummary{OrderID: id},
	)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, summary)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
