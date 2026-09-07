package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStatusWriterUnwrapsResponseControllerFlush(t *testing.T) {
	recorder := httptest.NewRecorder()
	if err := http.NewResponseController(&statusWriter{ResponseWriter: recorder}).Flush(); err != nil {
		t.Fatal(err)
	}
	if !recorder.Flushed {
		t.Fatal("flush did not reach wrapped response writer")
	}
}
