// Command geopatch corrects the country of given IPs in a GeoLite2-City database.
// An IP is patched when two of GeoLite2, DB-IP and the RIR delegation files agree on a country GeoLite2 does not give.
package main

import (
	"bufio"
	"encoding/binary"
	"flag"
	"fmt"
	"log"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/maxmind/mmdbwriter"
	"github.com/maxmind/mmdbwriter/inserter"
	"github.com/maxmind/mmdbwriter/mmdbtype"
	"github.com/oschwald/maxminddb-golang/v2"
)

type rirRange struct {
	start, end uint32
	cc         string
}

type countryRec struct {
	Country struct {
		ISOCode string `maxminddb:"iso_code"`
	} `maxminddb:"country"`
}

type change struct {
	ip                       netip.Addr
	geolite, dbip, rir, vote string
}

func main() {
	in := flag.String("in", "", "GeoLite2-City .mmdb to patch (uncompressed)")
	out := flag.String("out", "", "where to write the patched .mmdb")
	dbip := flag.String("dbip", "", "DB-IP country or city lite .mmdb")
	rirDir := flag.String("rir", "", "directory holding the delegated-*-extended-latest files")
	ipsFile := flag.String("ips", "", "file with one IPv4 address per line")
	dryRun := flag.Bool("n", false, "only report what would change")
	flag.Parse()
	if *in == "" || *dbip == "" || *rirDir == "" || *ipsFile == "" || (*out == "" && !*dryRun) {
		flag.Usage()
		os.Exit(2)
	}

	rirCountry, err := loadRIR(*rirDir)
	if err != nil {
		log.Fatal(err)
	}
	gl, err := maxminddb.Open(*in)
	if err != nil {
		log.Fatal(err)
	}
	dr, err := maxminddb.Open(*dbip)
	if err != nil {
		log.Fatal(err)
	}

	ips, err := readIPs(*ipsFile)
	if err != nil {
		log.Fatal(err)
	}
	var changes []change
	need := map[string]bool{}
	for _, a := range ips {
		c := change{ip: a, geolite: country(gl, a), dbip: country(dr, a), rir: rirCountry(a)}
		c.vote = majority(c.geolite, c.dbip, c.rir)
		if c.vote == "" && c.geolite != c.dbip {
			fmt.Printf("NO-MAJORITY %-16s geolite=%-2s dbip=%-2s rir=%-2s (left as is)\n", a, c.geolite, c.dbip, c.rir)
		}
		if c.vote == "" || c.vote == c.geolite {
			continue
		}
		changes = append(changes, c)
		need[c.vote] = true
	}
	for _, c := range changes {
		fmt.Printf("PATCH %-16s geolite=%-2s dbip=%-2s rir=%-2s -> %s\n", c.ip, c.geolite, c.dbip, c.rir, c.vote)
	}
	fmt.Printf("checked=%d patched=%d\n", len(ips), len(changes))
	if *dryRun || len(changes) == 0 {
		return
	}

	recs, err := countryRecords(gl, need)
	if err != nil {
		log.Fatal(err)
	}
	w, err := mmdbwriter.Load(*in, mmdbwriter.Options{IncludeReservedNetworks: true})
	if err != nil {
		log.Fatal(err)
	}
	for _, c := range changes {
		_, n, err := net.ParseCIDR(c.ip.String() + "/32")
		if err != nil {
			log.Fatal(err)
		}
		if err := w.InsertFunc(n, inserter.ReplaceWith(recs[c.vote])); err != nil {
			log.Fatal(err)
		}
	}
	f, err := os.Create(*out) //nolint:gosec
	if err != nil {
		log.Fatal(err)
	}
	if _, err := w.WriteTo(f); err != nil {
		log.Fatal(err)
	}
	if err := f.Close(); err != nil {
		log.Fatal(err)
	}
}

// majority is the country at least two of the sources name, or "".
func majority(cs ...string) string {
	votes := map[string]int{}
	for _, c := range cs {
		if c != "" {
			votes[c]++
			if votes[c] >= 2 {
				return c
			}
		}
	}
	return ""
}

func country(r *maxminddb.Reader, a netip.Addr) string {
	var c countryRec
	if err := r.Lookup(a).Decode(&c); err != nil {
		return ""
	}
	return c.Country.ISOCode
}

func readIPs(path string) ([]netip.Addr, error) {
	fh, err := os.Open(path) //nolint:gosec
	if err != nil {
		return nil, err
	}
	defer fh.Close() //nolint:errcheck
	seen := map[netip.Addr]bool{}
	var ips []netip.Addr
	sc := bufio.NewScanner(fh)
	for sc.Scan() {
		a, err := netip.ParseAddr(strings.TrimSpace(sc.Text()))
		if err != nil || !a.Is4() || !a.IsGlobalUnicast() || a.IsPrivate() || seen[a] {
			continue
		}
		seen[a] = true
		ips = append(ips, a)
	}
	return ips, sc.Err()
}

func u32(a netip.Addr) uint32 {
	b := a.As4()
	return binary.BigEndian.Uint32(b[:])
}

// loadRIR reads the RIRs' delegated-extended files into a country lookup.
// These name the country of the block's holder, not where a host is.
func loadRIR(dir string) (func(netip.Addr) string, error) {
	files, err := filepath.Glob(filepath.Join(dir, "delegated-*"))
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no delegated-* files in %s", dir)
	}
	var rs []rirRange
	for _, f := range files {
		fh, err := os.Open(f) //nolint:gosec
		if err != nil {
			return nil, err
		}
		sc := bufio.NewScanner(fh)
		for sc.Scan() {
			p := strings.Split(sc.Text(), "|")
			if len(p) < 7 || p[2] != "ipv4" || len(p[1]) != 2 {
				continue
			}
			a, err := netip.ParseAddr(p[3])
			if err != nil {
				continue
			}
			n, err := strconv.ParseUint(p[4], 10, 32)
			if err != nil || n == 0 {
				continue
			}
			rs = append(rs, rirRange{u32(a), u32(a) + uint32(n) - 1, p[1]})
		}
		fh.Close() //nolint:errcheck,gosec
	}
	sort.Slice(rs, func(i, j int) bool { return rs[i].start < rs[j].start })
	return func(a netip.Addr) string {
		x := u32(a)
		i := sort.Search(len(rs), func(i int) bool { return rs[i].start > x }) - 1
		if i >= 0 && x <= rs[i].end {
			return rs[i].cc
		}
		return ""
	}, nil
}

// countryRecords builds a country-only record for each needed country,
// copying the country and continent data the database itself holds for it.
func countryRecords(r *maxminddb.Reader, need map[string]bool) (map[string]mmdbtype.Map, error) {
	recs := map[string]mmdbtype.Map{}
	for res := range r.Networks() {
		if len(recs) == len(need) {
			break
		}
		var raw map[string]any
		if err := res.Decode(&raw); err != nil {
			continue
		}
		c, _ := raw["country"].(map[string]any)
		iso, _ := c["iso_code"].(string)
		if !need[iso] || recs[iso] != nil {
			continue
		}
		cont, _ := raw["continent"].(map[string]any)
		cm, err := toMap(c)
		if err != nil {
			return nil, err
		}
		contm, err := toMap(cont)
		if err != nil {
			return nil, err
		}
		recs[iso] = mmdbtype.Map{"country": cm, "registered_country": cm, "continent": contm}
	}
	for cc := range need {
		if recs[cc] == nil {
			return nil, fmt.Errorf("the database has no record for country %s to copy", cc)
		}
	}
	return recs, nil
}

func toMap(m map[string]any) (mmdbtype.Map, error) {
	out := mmdbtype.Map{}
	for k, v := range m {
		t, err := toType(v)
		if err != nil {
			return nil, err
		}
		out[mmdbtype.String(k)] = t
	}
	return out, nil
}

func toType(v any) (mmdbtype.DataType, error) {
	switch t := v.(type) {
	case string:
		return mmdbtype.String(t), nil
	case uint64:
		return mmdbtype.Uint64(t), nil
	case bool:
		return mmdbtype.Bool(t), nil
	case float64:
		return mmdbtype.Float64(t), nil
	case map[string]any:
		return toMap(t)
	}
	return nil, fmt.Errorf("unhandled value type %T", v)
}
