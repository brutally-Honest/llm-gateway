package config

import "net"

// ReasonNotLoopback is why a listener address off loopback is refused (spec 022).
const ReasonNotLoopback = "not loopback: v1 has no auth, and captures hold prompts and secrets " +
	"as sent; use 127.0.0.1, ::1 or localhost"

// Listener is one address the gateway binds, by its config key.
type Listener struct {
	Key  string
	Addr string
}

// Listeners returns the addresses c binds: the API always, the UI when it is on, and
// the proxies when network effects are on (SOCKS unless its address is ""). A listener
// that is off binds nothing, so it is not listed (spec 022 research D2).
func Listeners(c Config) []Listener {
	ls := []Listener{{"listen_addr", c.ListenAddr}}
	if c.UI.Enabled {
		ls = append(ls, Listener{uiListenKey, c.UI.ListenAddr})
	}
	if n := c.Effects.Network; c.Effects.Enabled && n.Enabled {
		ls = append(ls, Listener{effectsHTTPProxyKey, n.HTTPProxyAddr})
		if n.SocksProxyAddr != "" {
			ls = append(ls, Listener{effectsSocksProxyKey, n.SocksProxyAddr})
		}
	}
	return ls
}

// CheckListenAddr is the one loopback rule (spec 022 AC1). It allows a host in
// 127.0.0.0/8, ::1 or localhost, and refuses anything else, including an empty host,
// 0.0.0.0 and ::. It never resolves a name. A refusal names key, and the value once the
// address has parsed as host:port; the caller adds the source.
func CheckListenAddr(key, addr string) *Error {
	if !validAddress(addr) {
		return &Error{Key: key, Reason: reasonInvalidAddress}
	}
	host, _, _ := net.SplitHostPort(addr)
	if !loopbackHost(host) {
		return &Error{Key: key, Value: addr, Reason: ReasonNotLoopback}
	}
	return nil
}

// CheckListeners runs CheckListenAddr on every listener of c and returns the first
// refusal, or nil. Config loading, gateway install and gateway doctor all call it.
func CheckListeners(c Config) *Error {
	for _, l := range Listeners(c) {
		if e := CheckListenAddr(l.Key, l.Addr); e != nil {
			return e
		}
	}
	return nil
}

// checkListeners is CheckListeners with the source of the refused key, and its line
// when it came from the file at path.
func checkListeners(c *Config, origin map[string]string, path string, lines map[string]int) error {
	e := CheckListeners(*c)
	if e == nil {
		return nil
	}
	e.Source = origin[e.Key]
	switch e.Source {
	case "":
		e.Source = "default"
	case path:
		e.Line = lines[e.Key]
	}
	return e
}
