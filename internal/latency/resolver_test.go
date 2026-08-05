package latency

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestResolveDoHReturnsOnlyAddressAnswers(t *testing.T) {
	t.Parallel()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("type") == "A" {
			_, _ = w.Write([]byte(`{"Status":0,"Answer":[{"type":5,"data":"alias.example"},{"type":1,"data":"203.0.113.10"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"Status":0,"Answer":[{"type":28,"data":"2001:db8::10"}]}`))
	}))
	defer server.Close()
	addresses, err := resolveDoH(context.Background(), server.Client(), server.URL, "node.example")
	if err != nil {
		t.Fatal(err)
	}
	if len(addresses) != 2 || addresses[0].String() != "203.0.113.10" || addresses[1].String() != "2001:db8::10" {
		t.Fatalf("addresses = %v", addresses)
	}
}
