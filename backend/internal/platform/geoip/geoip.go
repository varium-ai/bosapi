// Package geoip provides a small, local-only IP to location resolver.
package geoip

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	geoip2 "github.com/oschwald/geoip2-golang"
	"io"
	"net"
	"os"
	"sort"
	"strings"
)

type entry struct {
	network *net.IPNet
	city    string
	country string
}

// Resolver resolves IP addresses from a local JSON CIDR database.
type Resolver struct {
	entries []entry
	mmdb    *geoip2.Reader
}

// New loads a database. The file is a JSON array of {"cidr":"...","city":"...","country":"..."}.
func New(path string) (*Resolver, error) {
	r := &Resolver{}
	if path == "" {
		return r, nil
	}
	if strings.HasSuffix(path, ".mmdb.gz") {
		file, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		gz, err := gzip.NewReader(file)
		if err != nil {
			file.Close()
			return nil, err
		}
		data, err := io.ReadAll(gz)
		gz.Close()
		file.Close()
		if err != nil {
			return nil, err
		}
		reader, err := geoip2.FromBytes(data)
		if err != nil {
			return nil, fmt.Errorf("open geoip database: %w", err)
		}
		r.mmdb = reader
		return r, nil
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read geoip database: %w", err)
	}
	var records []struct{ CIDR, City, Country string }
	if err := json.Unmarshal(payload, &records); err != nil {
		return nil, fmt.Errorf("decode geoip database: %w", err)
	}
	for _, record := range records {
		_, network, err := net.ParseCIDR(strings.TrimSpace(record.CIDR))
		if err != nil || strings.TrimSpace(record.City) == "" || strings.TrimSpace(record.Country) == "" {
			return nil, fmt.Errorf("invalid geoip record %q", record.CIDR)
		}
		r.entries = append(r.entries, entry{network: network, city: strings.TrimSpace(record.City), country: strings.TrimSpace(record.Country)})
	}
	sort.SliceStable(r.entries, func(i, j int) bool {
		a, _ := r.entries[i].network.Mask.Size()
		b, _ := r.entries[j].network.Mask.Size()
		return a > b
	})
	return r, nil
}

// Lookup returns city and country for an IP. It never performs network access.
func (r *Resolver) Lookup(ip net.IP) (string, string) {
	if r.mmdb != nil {
		record, err := r.mmdb.City(ip)
		if err == nil {
			city := record.City.Names["en"]
			country := record.Country.Names["en"]
			if city == "" {
				city = "Unknown"
			}
			if country == "" {
				country = "Unknown"
			}
			return city, country
		}
	}
	for _, candidate := range r.entries {
		if candidate.network.Contains(ip) {
			return candidate.city, candidate.country
		}
	}
	return "Unknown", "Unknown"
}

// Format returns the display form used by API responses.
func (r *Resolver) Format(ip net.IP, masked string) string {
	city, country := r.Lookup(ip)
	if city == "Unknown" && country == "Unknown" {
		return masked
	}
	if city == "Unknown" {
		return fmt.Sprintf("%s(%s)", masked, country)
	}
	if country == "Unknown" {
		return fmt.Sprintf("%s(%s)", masked, city)
	}
	return fmt.Sprintf("%s(%s, %s)", masked, city, country)
}
