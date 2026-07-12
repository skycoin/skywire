package cliconfig

import (
	"testing"
)

func TestBuildCoinNodes(t *testing.T) {
	defer func() { coinNodes = "" }()

	t.Run("empty", func(t *testing.T) {
		coinNodes = ""
		got, err := buildCoinNodes()
		if err != nil {
			t.Fatal(err)
		}
		if got != nil {
			t.Fatalf("want nil, got %v", got)
		}
	})

	t.Run("addr only -> dmsg port defaults to local port", func(t *testing.T) {
		coinNodes = "127.0.0.1:6420"
		got, err := buildCoinNodes()
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0].LocalAddr != "127.0.0.1:6420" || got[0].DmsgPort != 6420 {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("explicit dmsg port", func(t *testing.T) {
		coinNodes = "127.0.0.1:6420@7000"
		got, err := buildCoinNodes()
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0].LocalAddr != "127.0.0.1:6420" || got[0].DmsgPort != 7000 {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("multiple CSV", func(t *testing.T) {
		coinNodes = "127.0.0.1:6420,127.0.0.1:6430@6430"
		got, err := buildCoinNodes()
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 || got[1].LocalAddr != "127.0.0.1:6430" || got[1].DmsgPort != 6430 {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("invalid addr errors", func(t *testing.T) {
		coinNodes = "not-an-addr"
		if _, err := buildCoinNodes(); err == nil {
			t.Fatal("expected error for missing host:port")
		}
	})

	t.Run("invalid dmsg port errors", func(t *testing.T) {
		coinNodes = "127.0.0.1:6420@notaport"
		if _, err := buildCoinNodes(); err == nil {
			t.Fatal("expected error for bad dmsg port")
		}
	})
}
