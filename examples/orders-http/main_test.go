package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

// do runs a request through the server and returns the recorder.
// Each test gets a fresh server so state is isolated.
func do(t *testing.T, server http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *bytes.Reader
	if body == "" {
		rdr = bytes.NewReader(nil)
	} else {
		rdr = bytes.NewReader([]byte(body))
	}
	req := httptest.NewRequest(method, path, rdr)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)
	return w
}

func decode[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %s: %v", w.Body.String(), err)
	}
	return out
}

func TestPlaceOrder(t *testing.T) {
	s := newServer()
	w := do(t, s, "POST", "/orders", `{"customer_name":"Alice","amount":4200}`)

	if w.Code != http.StatusCreated {
		t.Fatalf("status %d, body=%s", w.Code, w.Body.String())
	}
	resp := decode[placeOrderResponse](t, w)
	if resp.OrderID == "" {
		t.Fatal("expected non-empty order_id")
	}
	if _, err := uuid.Parse(resp.OrderID); err != nil {
		t.Fatalf("order_id is not a valid UUID: %v", err)
	}
}

func TestPlaceOrderBadJSON(t *testing.T) {
	s := newServer()
	w := do(t, s, "POST", "/orders", `not json`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", w.Code)
	}
}

func TestPlaceOrderInvalidAmount(t *testing.T) {
	s := newServer()
	w := do(t, s, "POST", "/orders", `{"customer_name":"Alice","amount":0}`)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, want 422", w.Code)
	}
	body := decode[map[string]map[string]string](t, w)
	if _, ok := body["errors"]["amount"]; !ok {
		t.Fatalf("missing amount field error: %+v", body)
	}
	if _, ok := body["errors"]["customer_name"]; ok {
		t.Fatalf("unexpected customer error (only amount was bad): %+v", body)
	}
}

func TestPlaceOrderMultipleValidationErrors(t *testing.T) {
	s := newServer()
	w := do(t, s, "POST", "/orders", `{"customer_name":"","amount":-5}`)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, want 422", w.Code)
	}
	body := decode[map[string]map[string]string](t, w)
	if _, ok := body["errors"]["amount"]; !ok {
		t.Fatalf("missing amount field error: %+v", body)
	}
	if _, ok := body["errors"]["customer_name"]; !ok {
		t.Fatalf("missing customer field error: %+v", body)
	}
}

func TestShipOrderBadID(t *testing.T) {
	s := newServer()
	w := do(t, s, "POST", "/orders/not-a-uuid/ship", "")
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, want 422 (id failed to parse)", w.Code)
	}
	body := decode[map[string]map[string]string](t, w)
	if _, ok := body["errors"]["id"]; !ok {
		t.Fatalf("missing id field error: %+v", body)
	}
}

func TestShipOrderUnknownID(t *testing.T) {
	s := newServer()
	id := uuid.Must(uuid.NewV7()).String()
	w := do(t, s, "POST", "/orders/"+id+"/ship", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404 (no such order stream)", w.Code)
	}
}

func TestPlaceThenShipThenGet(t *testing.T) {
	s := newServer()

	// Place
	w := do(t, s, "POST", "/orders", `{"customer_name":"Bob","amount":100}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("place status %d", w.Code)
	}
	id := decode[placeOrderResponse](t, w).OrderID

	// Ship
	w = do(t, s, "POST", "/orders/"+id+"/ship", "")
	if w.Code != http.StatusNoContent {
		t.Fatalf("ship status %d, body=%s", w.Code, w.Body.String())
	}

	// Get
	w = do(t, s, "GET", "/orders/"+id, "")
	if w.Code != http.StatusOK {
		t.Fatalf("get status %d, body=%s", w.Code, w.Body.String())
	}
	got := decode[map[string]any](t, w)
	if got["customer_name"] != "Bob" {
		t.Fatalf("customer=%v want Bob", got["customer_name"])
	}
	if got["shipped"] != true {
		t.Fatalf("shipped=%v want true", got["shipped"])
	}
}

func TestGetOrderNotFound(t *testing.T) {
	s := newServer()
	id := uuid.Must(uuid.NewV7()).String()
	w := do(t, s, "GET", "/orders/"+id, "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", w.Code)
	}
}

func TestGetOrderBadID(t *testing.T) {
	s := newServer()
	w := do(t, s, "GET", "/orders/nope", "")
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, want 422", w.Code)
	}
}
