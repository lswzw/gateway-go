package errors

import (
	"errors"
	"net/http"
	"testing"
)

func TestErrors(t *testing.T) {
	// New and Wrap
	base := errors.New("underlying cause")
	wrapped := Wrap(base, 500, "something failed")
	if wrapped.Error() != "something failed: underlying cause" {
		t.Errorf("unexpected error string: %s", wrapped.Error())
	}
	if wrapped.Unwrap() != base {
		t.Errorf("expected unwrapped error to be base")
	}

	errNoCause := New(400, "bad input")
	if errNoCause.Error() != "bad input" {
		t.Errorf("unexpected error string: %s", errNoCause.Error())
	}
	if errNoCause.Unwrap() != nil {
		t.Errorf("expected nil unwrapped error")
	}

	// HTTPStatus
	if errNoCause.HTTPStatus() != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", errNoCause.HTTPStatus())
	}

	// WithDetails
	errWithDetails := errNoCause.WithDetails(map[string]string{"field": "username"})
	if errWithDetails.Details == nil {
		t.Errorf("expected non-nil details")
	}

	// Subtypes: GatewayError, UpstreamError, PluginError
	gwErr := NewGatewayError(502, "bad gateway")
	if !IsGatewayError(gwErr) {
		t.Errorf("expected IsGatewayError to be true")
	}
	if IsGatewayError(nil) || IsGatewayError(base) {
		t.Errorf("expected IsGatewayError to be false for non-gateway errors")
	}
	if gwErr.Error() != "bad gateway" {
		t.Errorf("unexpected gateway error string: %s", gwErr.Error())
	}

	upErr := NewUpstreamError(504, "gateway timeout")
	if !IsUpstreamError(upErr) {
		t.Errorf("expected IsUpstreamError to be true")
	}
	if IsUpstreamError(nil) {
		t.Errorf("expected false for nil")
	}

	plErr := NewPluginError(403, "plugin forbidden")
	if !IsPluginError(plErr) {
		t.Errorf("expected IsPluginError to be true")
	}
	if IsPluginError(nil) {
		t.Errorf("expected false for nil")
	}

	// Is helper
	if !Is(errNoCause, 400) {
		t.Errorf("expected Is(..., 400) to be true")
	}
	if Is(errNoCause, 500) {
		t.Errorf("expected Is(..., 500) to be false")
	}
	if Is(base, 500) {
		t.Errorf("expected Is to return false for non-Error type")
	}

	// As helper
	e, ok := As(base)
	if !ok || e == nil || e.Code != 500 {
		t.Errorf("expected As to return Error with 500, got %v, %v", e, ok)
	}
}
