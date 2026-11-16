package web

import (
	"context"
	"net"
	"strconv"
	"strings"
	"testing"
)

func TestDashboardFixedDefaultLoopbackAndBindFailure(t *testing.T) {
	_, accounts, quotas, _, metrics, broadcaster, events := setupTestWeb(t)
	server, err := NewServer(accounts, quotas, metrics, broadcaster, events, nil, WithBindAddr("0.0.0.0"))
	if err != nil {
		t.Fatal(err)
	}
	if server.cfg.Port != 8080 || server.cfg.BindAddr != "127.0.0.1" {
		t.Fatalf("defaults port=%d bind=%s", server.cfg.Port, server.cfg.BindAddr)
	}
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	port := occupied.Addr().(*net.TCPAddr).Port
	server, err = NewServer(accounts, quotas, metrics, broadcaster, events, nil, WithPort(port))
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Start(); err == nil || !strings.Contains(err.Error(), strconv.Itoa(port)) {
		t.Fatalf("occupied port silently succeeded or unclear error %v", err)
	}
	server, err = NewServer(accounts, quotas, metrics, broadcaster, events, nil, WithPort(0), WithBindAddr("::"))
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	defer server.Stop(context.Background())
	host, _, err := net.SplitHostPort(server.addr)
	if err != nil || host != "127.0.0.1" {
		t.Fatalf("not loopback %s %v", server.addr, err)
	}
}
