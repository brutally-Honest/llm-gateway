package netproxy

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strconv"
	"time"

	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/logging"
)

// SOCKS5 (RFC 1928): version 5, the no-authentication method, CONNECT only.
const (
	socksVersion  = 5
	methodNoAuth  = 0x00
	methodNone    = 0xff
	cmdConnect    = 0x01
	atypIPv4      = 0x01
	atypDomain    = 0x03
	atypIPv6      = 0x04
	repOK         = 0x00
	repFailure    = 0x01
	repRefused    = 0x05
	repCmdNotSup  = 0x07
	repAtypNotSup = 0x08
)

// handshakeTimeout bounds a SOCKS5 greeting and request.
const handshakeTimeout = 10 * time.Second

// ListenSOCKS starts the SOCKS5 proxy on ln (AC9).
func (p *Proxy) ListenSOCKS(ln net.Listener) {
	p.mu.Lock()
	p.lns = append(p.lns, ln)
	p.mu.Unlock()
	logging.Go(p.log, "netproxy_socks", func() error {
		for {
			c, err := ln.Accept()
			if err != nil {
				if errors.Is(err, net.ErrClosed) {
					return nil
				}
				var ne net.Error
				if errors.As(err, &ne) && ne.Timeout() {
					continue
				}
				return err
			}
			logging.Go(p.log, "netproxy_socks_conn", func() error {
				p.socks(c)
				return nil
			})
		}
	})
}

// socks serves one SOCKS5 connection: greeting, one CONNECT request, then the tunnel.
// Anything else gets its failure reply and, once the target is known, an error effect.
func (p *Proxy) socks(c net.Conn) {
	_ = c.SetDeadline(time.Now().Add(handshakeTimeout))
	hdr := make([]byte, 2)
	if _, err := io.ReadFull(c, hdr); err != nil || hdr[0] != socksVersion {
		_ = c.Close()
		return
	}
	methods := make([]byte, int(hdr[1]))
	if _, err := io.ReadFull(c, methods); err != nil {
		_ = c.Close()
		return
	}
	noAuth := false
	for _, m := range methods {
		noAuth = noAuth || m == methodNoAuth
	}
	if !noAuth {
		_, _ = c.Write([]byte{socksVersion, methodNone})
		_ = c.Close()
		return
	}
	if _, err := c.Write([]byte{socksVersion, methodNoAuth}); err != nil {
		_ = c.Close()
		return
	}
	req := make([]byte, 4)
	if _, err := io.ReadFull(c, req); err != nil || req[0] != socksVersion {
		_ = c.Close()
		return
	}
	host, port, ok := readAddr(c, req[3])
	if !ok {
		p.rec.Record(core.Effect{At: time.Now(), EndedAt: time.Now(), Via: "socks", Outcome: core.NetError})
		reply(c, repAtypNotSup)
		_ = c.Close()
		return
	}
	t := p.open(host, port, "socks", "")
	if req[1] != cmdConnect {
		reply(c, repCmdNotSup)
		_ = c.Close()
		p.finish(t, core.NetError)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), dialTimeout)
	up, err := p.dial(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	cancel() // the dial is done; the connection outlives its context
	if err != nil {
		outcome := dialOutcome(err)
		code := byte(repFailure)
		if outcome == core.NetRefused {
			code = repRefused
		}
		reply(c, code)
		_ = c.Close()
		p.finish(t, outcome)
		return
	}
	reply(c, repOK)
	_ = c.SetDeadline(time.Time{})
	p.pipe(t, c, nil, up)
}

// readAddr reads a request's address of type atyp and its port.
func readAddr(c net.Conn, atyp byte) (string, int, bool) {
	var host string
	switch atyp {
	case atypIPv4, atypIPv6:
		n := 4
		if atyp == atypIPv6 {
			n = 16
		}
		b := make([]byte, n)
		if _, err := io.ReadFull(c, b); err != nil {
			return "", 0, false
		}
		host = net.IP(b).String()
	case atypDomain:
		l := make([]byte, 1)
		if _, err := io.ReadFull(c, l); err != nil {
			return "", 0, false
		}
		b := make([]byte, int(l[0]))
		if _, err := io.ReadFull(c, b); err != nil {
			return "", 0, false
		}
		host = string(b)
	default:
		return "", 0, false
	}
	pb := make([]byte, 2)
	if _, err := io.ReadFull(c, pb); err != nil {
		return "", 0, false
	}
	return host, int(binary.BigEndian.Uint16(pb)), true
}

// reply writes a SOCKS5 reply with an all-zero IPv4 bound address.
func reply(c net.Conn, code byte) {
	_, _ = c.Write([]byte{socksVersion, code, 0x00, atypIPv4, 0, 0, 0, 0, 0, 0})
}
