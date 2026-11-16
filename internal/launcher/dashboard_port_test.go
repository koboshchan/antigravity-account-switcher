package launcher

import (
	"context"
	"github.com/Muriel-Gasparini/antigravity-account-switcher/internal/store/sqlite"
	"net"
	"strings"
	"testing"
)

func TestWrapOccupiedDashboardDoesNotPickRandomPort(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	port := occupied.Addr().(*net.TCPAddr).Port
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	code, err := Wrap(context.Background(), []string{"definitely-not-an-executable"}, WithDB(db), WithPort(port))
	if err == nil || code != 1 || !strings.Contains(err.Error(), "choose --dashboard-port") {
		t.Fatalf("should fail clear before child spawn: code=%d err=%v", code, err)
	}
}
