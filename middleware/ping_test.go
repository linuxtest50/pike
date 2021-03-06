package middleware

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/vicanso/pike/pike"
)

func TestPing(t *testing.T) {
	var disabledPing int32
	t.Run("hit ping", func(t *testing.T) {
		pingConfig := PingConfig{
			URL:          "/ping",
			DisabledPing: &disabledPing,
		}
		fn := Ping(pingConfig)
		req := httptest.NewRequest(http.MethodGet, "/ping", nil)
		c := pike.NewContext(req)
		err := fn(c, pike.NoopNext)
		if err != nil {
			t.Fatalf("ping middleware fail, %v", err)
		}
		if c.Response.Status() != http.StatusOK {
			t.Fatalf("ping response status should be ok")
		}
		if string(c.Response.Bytes()) != "pong" {
			t.Fatalf("ping response should be pong")
		}
	})

	t.Run("next", func(t *testing.T) {
		pingConfig := PingConfig{
			URL: "/ping",
		}
		fn := Ping(pingConfig)
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		c := pike.NewContext(req)
		passErr := errors.New("pass")
		err := fn(c, func() error {
			return passErr
		})
		if err != passErr {
			t.Fatalf("should pass next")
		}
	})

	t.Run("disabled", func(t *testing.T) {
		atomic.StoreInt32(&disabledPing, 1)
		pingConfig := PingConfig{
			URL:          "/ping",
			DisabledPing: &disabledPing,
		}
		fn := Ping(pingConfig)
		req := httptest.NewRequest(http.MethodGet, "/ping", nil)
		c := pike.NewContext(req)
		err := fn(c, pike.NoopNext)
		if err != pike.ErrDisableServer {
			t.Fatalf("ping middleware should return err disable server")
		}
	})

}
