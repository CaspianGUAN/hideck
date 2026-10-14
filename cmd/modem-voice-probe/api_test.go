package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestProbeDoesNotTreatHTTP200AsATAcceptance(t *testing.T) {
	for _, response := range []string{"ATD10010;\r\nERROR\r\n", "+CME ERROR: 3", "NO CARRIER", "", "ATD10010;\r\nOK\r\n"} {
		t.Run(response, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer test" {
					t.Error("missing authorization")
				}
				_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "response": response})
			}))
			defer srv.Close()
			a := &probeAPI{client: srv.Client(), base: srv.URL, device: "wwan1", token: "test"}
			_, err := a.ExecuteATContext(context.Background(), "ATD10010;", time.Second)
			wantOK := response == "ATD10010;\r\nOK\r\n"
			if (err == nil) != wantOK {
				t.Fatalf("err=%v, wantOK=%v", err, wantOK)
			}
		})
	}
}
