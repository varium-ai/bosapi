package geoip

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestLookupAndFormat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "geo.json")
	if err := os.WriteFile(path, []byte(`[{"cidr":"1.2.0.0/16","city":"ChongQing","country":"China"},{"cidr":"1.2.3.0/24","city":"Yuzhong","country":"China"}]`), 0600); err != nil {
		t.Fatal(err)
	}
	resolver, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	ip := net.ParseIP("1.2.3.4")
	if got := resolver.Format(ip, "1.2.3.0"); got != "1.2.3.0(Yuzhong, China)" {
		t.Fatalf("format=%q", got)
	}
	if got := resolver.Format(net.ParseIP("1.2.4.4"), "1.2.4.0"); got != "1.2.4.0(ChongQing, China)" {
		t.Fatalf("city format=%q", got)
	}
	if got := resolver.Format(net.ParseIP("9.9.9.9"), "9.9.9.0"); got != "9.9.9.0" {
		t.Fatalf("unknown format=%q", got)
	}
	city, country := resolver.Lookup(net.ParseIP("9.9.9.9"))
	if city != "Unknown" || country != "Unknown" {
		t.Fatalf("unknown=%s,%s", city, country)
	}
}
