package pve

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/coder/websocket"

	"quicksand/internal/config"
)

// TestTerm runs the terminal protocol against a fake termproxy.
func TestTerm(t *testing.T) {
	got := make(chan string, 10)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api2/json/nodes/n1/qemu/220/termproxy":
			r.ParseForm()
			if r.Form.Get("serial") != "serial0" {
				http.Error(w, "bad serial", 400)
				return
			}
			w.Write([]byte(`{"data":{"port":5900,"ticket":"PVEVNC:abc","user":"test@pve!test","upid":"x"}}`))
		case "/api2/json/nodes/n1/qemu/220/vncwebsocket":
			if r.URL.Query().Get("port") != "5900" || r.URL.Query().Get("vncticket") != "PVEVNC:abc" {
				http.Error(w, "bad ticket", 401)
				return
			}
			ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{"binary"}})
			if err != nil {
				return
			}
			ctx := context.Background()
			if _, auth, _ := ws.Read(ctx); string(auth) != "test@pve!test:PVEVNC:abc\n" {
				ws.Close(websocket.StatusPolicyViolation, "auth")
				return
			}
			ws.Write(ctx, websocket.MessageBinary, []byte("OKlogin: "))
			for {
				_, msg, err := ws.Read(ctx)
				if err != nil {
					return
				}
				got <- string(msg)
				if string(msg) == "0:4:quit" {
					ws.Close(websocket.StatusNormalClosure, "")
					return
				}
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c, err := New(config.PVE{URL: srv.URL, TokenID: "test@pve!test", TokenSecret: "s", InsecureSkipVerify: true})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	ticket, err := c.TermProxy(ctx, "n1", 220, "serial0")
	if err != nil {
		t.Fatal(err)
	}
	term, err := c.DialTerm(ctx, "n1", 220, ticket)
	if err != nil {
		t.Fatal(err)
	}
	defer term.Close()
	if out, err := term.ReadOutput(); string(out) != "login: " || err != nil {
		t.Errorf("first output %q, %v", out, err)
	}
	term.Resize(100, 30)
	term.Input([]byte("héllo\r"))
	term.Input([]byte("quit"))
	for _, want := range []string{"1:100:30:", "0:7:héllo\r", "0:4:quit"} {
		if msg := <-got; msg != want {
			t.Errorf("sent %q, want %q", msg, want)
		}
	}
	if _, err := term.ReadOutput(); err == nil {
		t.Error("no error after the console closed")
	}
}
