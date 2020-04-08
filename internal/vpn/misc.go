package vpn

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"strconv"
)

func CopyTraffic(from, to io.ReadWriteCloser) error {
	//buf := make([]byte, bufSize)

	// TODO: test if it's stable
	if _, err := io.Copy(from, to); err != nil {
		return err
	}

	return nil
	/*for {
		rn, rerr := from.Read(buf)
		if rerr != nil {
			return fmt.Errorf("error reading from RWC: %v", rerr)
		}

		header, err := ipv4.ParseHeader(buf[:rn])
		if err != nil {
			log.Errorf("Error parsing IP header, skipping...")
			continue
		}

		// TODO: match IPs?
		log.Infof("Sending IP packet %v->%v", header.Src, header.Dst)

		totalWritten := 0
		for totalWritten != rn {
			wn, werr := to.Write(buf[:rn])
			if werr != nil {
				return fmt.Errorf("error writing to RWC: %v", err)
			}

			totalWritten += wn
		}
	}*/
}

func IPFromEnv(key string) (net.IP, bool, error) {
	addr := os.Getenv(key)
	if addr == "" {
		return nil, false, nil
	}

	ip := net.ParseIP(addr)
	if ip != nil {
		return ip, true, nil
	}

	ips, err := net.LookupIP(addr)
	if err != nil {
		return nil, false, err
	}
	if len(ips) == 0 {
		return nil, false, fmt.Errorf("couldn't resolve IPs of %s", addr)
	}

	// initially take just the first one
	ip = ips[0]

	return ip, true, nil
}

func run(bin string, args ...string) {
	//cmd := exec.Command("sh -c \"ip " + strings.Join(args, " ") + "\"")
	cmd := exec.Command(bin, args...)
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout
	cmd.Stdin = os.Stdin
	err := cmd.Run()
	if nil != err {
		log.Fatalf("Error running %s: %v\n", bin, err)
	}
}

func SetupTUN(ifcName, ip, netmask, gateway string, mtu int) {
	run("/sbin/ifconfig", ifcName, ip, gateway, "mtu", strconv.Itoa(mtu), "netmask", netmask, "up")
}

func AddRoute(ip, gateway, netmask string) {
	if netmask == "" {
		run("/sbin/route", "add", "-net", ip, gateway)
	} else {
		run("/sbin/route", "add", "-net", ip, gateway, netmask)
	}
}

func DeleteRoute(ip, gateway, netmask string) {
	if netmask == "" {
		run("/sbin/route", "delete", "-net", ip, gateway)
	} else {
		run("/sbin/route", "delete", "-net", ip, gateway, netmask)
	}
}

func GetDefaultGatewayIP() (net.IP, error) {
	cmd := "netstat -rn | grep default | grep en | awk '{print $2}'"
	outBytes, err := exec.Command("bash", "-c", cmd).Output()
	if err != nil {
		return nil, fmt.Errorf("error running command: %w", err)
	}

	outBytes = bytes.TrimRight(outBytes, "\n")

	outLines := bytes.Split(outBytes, []byte{'\n'})

	for _, l := range outLines {
		if bytes.Count(l, []byte{'.'}) != 3 {
			// initially look for IPv4 address
			continue
		}

		ip := net.ParseIP(string(l))
		if ip != nil {
			return ip, nil
		}
	}

	return nil, errors.New("couldn't find default gateway IP")
}
