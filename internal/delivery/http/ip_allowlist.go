package http

import (
	"log"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// IPAllowlist пропускает запросы только с адресов из ALLOWED_IPS и с адресов самого хоста.
// Пустой ALLOWED_IPS не открывает API всем: остаются только локальные адреса.
type IPAllowlist struct {
	allowed map[netip.Addr]bool
}

func NewIPAllowlist(configured []string) *IPAllowlist {
	a := &IPAllowlist{allowed: map[netip.Addr]bool{}}
	for _, s := range configured {
		addr, err := netip.ParseAddr(strings.TrimSpace(s))
		if err != nil {
			log.Printf("ALLOWED_IPS: skipping invalid address %q", s)
			continue
		}
		a.allowed[addr.Unmap()] = true
	}
	// ponytail: адреса интерфейсов читаются один раз при старте; сменился IP хоста — нужен рестарт
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, ia := range addrs {
			if ipnet, ok := ia.(*net.IPNet); ok {
				if addr, ok := netip.AddrFromSlice(ipnet.IP); ok {
					a.allowed[addr.Unmap()] = true
				}
			}
		}
	} else {
		log.Printf("Cannot read host interface addresses, only loopback and ALLOWED_IPS are allowed: %v", err)
	}
	return a
}

func (a *IPAllowlist) Allows(addr netip.Addr) bool {
	addr = addr.Unmap()
	return addr.IsLoopback() || a.allowed[addr]
}

func (a *IPAllowlist) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.Allows(a.clientIP(r)) {
			writeError(w, http.StatusForbidden, "Forbidden - IP not allowed")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// clientIP верит заголовкам прокси только от разрешённого соседа: иначе любой клиент
// подставил бы X-Real-IP: 127.0.0.1 и прошёл бы проверку.
func (a *IPAllowlist) clientIP(r *http.Request) netip.Addr {
	peer, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil {
		return netip.Addr{}
	}
	if !a.Allows(peer.Addr()) {
		return peer.Addr()
	}
	forwarded := r.Header.Get("X-Real-IP")
	if forwarded == "" {
		// Берём последний адрес: его дописал наш прокси, а начало цепочки присылает сам клиент
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			forwarded = xff[strings.LastIndex(xff, ",")+1:]
		}
	}
	if addr, err := netip.ParseAddr(strings.TrimSpace(forwarded)); err == nil {
		return addr
	}
	return peer.Addr()
}
