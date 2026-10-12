package http

import (
	"encoding/binary"
	"encoding/hex"
	"log"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strings"
)

// IPAllowlist пропускает запросы только с адресов из ALLOWED_IPS и с хоста, где запущен сервис.
// Пустой ALLOWED_IPS не открывает API всем: остаются только адреса хоста.
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
	if gw, ok := containerHostAddr(); ok {
		log.Printf("Running in a container, allowing host address %s", gw)
		a.allowed[gw] = true
	}
	return a
}

// containerHostAddr возвращает адрес хоста для сервиса в Docker/Podman: изнутри bridge-сети
// хост виден как шлюз по умолчанию, а не как loopback.
// Вне контейнера шлюз — это роутер, его пускать нельзя, поэтому сначала проверяем маркер контейнера.
func containerHostAddr() (netip.Addr, bool) {
	inContainer := false
	for _, marker := range []string{"/.dockerenv", "/run/.containerenv"} {
		if _, err := os.Stat(marker); err == nil {
			inContainer = true
		}
	}
	if !inContainer {
		return netip.Addr{}, false
	}
	table, err := os.ReadFile("/proc/net/route")
	if err != nil {
		log.Printf("Cannot read /proc/net/route, host address is not allowed: %v", err)
		return netip.Addr{}, false
	}
	return defaultGateway(string(table))
}

// defaultGateway разбирает /proc/net/route: строка с Destination 00000000 — маршрут по умолчанию,
// Gateway записан в hex в порядке байт little-endian (0100A8C0 = 192.168.0.1).
func defaultGateway(routeTable string) (netip.Addr, bool) {
	for _, line := range strings.Split(routeTable, "\n")[1:] {
		fields := strings.Fields(line)
		if len(fields) < 3 || fields[1] != "00000000" {
			continue
		}
		raw, err := hex.DecodeString(fields[2])
		if err != nil || len(raw) != 4 {
			continue
		}
		var ip [4]byte
		binary.BigEndian.PutUint32(ip[:], binary.LittleEndian.Uint32(raw))
		if addr := netip.AddrFrom4(ip); !addr.IsUnspecified() {
			return addr, true
		}
	}
	return netip.Addr{}, false
}

func (a *IPAllowlist) Allows(addr netip.Addr) bool {
	addr = addr.Unmap()
	return addr.IsLoopback() || a.allowed[addr]
}

func (a *IPAllowlist) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ip := a.clientIP(r); !a.Allows(ip) {
			log.Printf("Request from %s rejected: not in ALLOWED_IPS (peer %s)", ip, r.RemoteAddr)
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
