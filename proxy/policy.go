// Package proxy policy的实现使用了caddy的实现代码
// https://github.com/mholt/caddy/blob/master/caddyhttp/proxy/policy.go
package proxy

import (
	"hash/fnv"
	"math"
	"math/rand"
	"net"
	"sync"

	"github.com/valyala/fasthttp"
	"github.com/vicanso/pike/vars"
)

// Policy 从upstream pool中选择一个upstream
type Policy interface {
	Select(pool UpstreamHostPool, ctx *fasthttp.RequestCtx) *UpstreamHost
}

func init() {
	RegisterPolicy(vars.Random, func(arg string) Policy { return &Random{} })
	RegisterPolicy(vars.RoundRobin, func(arg string) Policy { return &RoundRobin{} })
	RegisterPolicy(vars.LeastConn, func(arg string) Policy { return &LeastConn{} })
	RegisterPolicy(vars.IPHash, func(arg string) Policy { return &IPHash{} })
	RegisterPolicy(vars.First, func(arg string) Policy { return &First{} })
	RegisterPolicy(vars.URIHash, func(arg string) Policy { return &URIHash{} })
	RegisterPolicy(vars.Header, func(arg string) Policy { return &Header{arg} })

}

// Random is a policy that selects up hosts from a pool at random.
type Random struct{}

// Select selects an up host at random from the specified pool.
func (r *Random) Select(pool UpstreamHostPool, ctx *fasthttp.RequestCtx) *UpstreamHost {

	// Because the number of available hosts isn't known
	// up front, the host is selected via reservoir sampling
	// https://en.wikipedia.org/wiki/Reservoir_sampling
	var randHost *UpstreamHost
	count := 0
	for _, host := range pool {
		if !host.Available() {
			continue
		}

		// (n % 1 == 0) holds for all n, therefore randHost
		// will always get assigned a value if there is
		// at least 1 available host
		count++
		if (rand.Int() % count) == 0 {
			randHost = host
		}
	}
	return randHost
}

// RoundRobin is a policy that selects hosts based on round-robin ordering.
type RoundRobin struct {
	robin uint32
	mutex sync.Mutex
}

// Select selects an up host from the pool using a round-robin ordering scheme.
func (r *RoundRobin) Select(pool UpstreamHostPool, ctx *fasthttp.RequestCtx) *UpstreamHost {
	poolLen := uint32(len(pool))
	r.mutex.Lock()
	defer r.mutex.Unlock()
	// Return next available host
	for i := uint32(0); i < poolLen; i++ {
		r.robin++
		host := pool[r.robin%poolLen]
		if host.Available() {
			return host
		}
	}
	return nil
}

// LeastConn is a policy that selects the host with the least connections.
type LeastConn struct{}

// Select selects the up host with the least number of connections in the
// pool. If more than one host has the same least number of connections,
// one of the hosts is chosen at random.
func (r *LeastConn) Select(pool UpstreamHostPool, ctx *fasthttp.RequestCtx) *UpstreamHost {
	var bestHost *UpstreamHost
	count := 0
	leastConn := int64(math.MaxInt64)
	for _, host := range pool {
		if !host.Available() {
			continue
		}

		if host.Conns < leastConn {
			leastConn = host.Conns
			count = 0
		}

		// Among hosts with same least connections, perform a reservoir
		// sample: https://en.wikipedia.org/wiki/Reservoir_sampling
		if host.Conns == leastConn {
			count++
			if (rand.Int() % count) == 0 {
				bestHost = host
			}
		}
	}
	return bestHost
}

// hostByHashing returns an available host from pool based on a hashable string
func hostByHashing(pool UpstreamHostPool, s string) *UpstreamHost {
	poolLen := uint32(len(pool))
	index := hash(s) % poolLen
	for i := uint32(0); i < poolLen; i++ {
		index += i
		host := pool[index%poolLen]
		if host.Available() {
			return host
		}
	}
	return nil
}

// hash calculates a hash based on string s
func hash(s string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(s))
	return h.Sum32()
}

// IPHash is a policy that selects hosts based on hashing the request IP
type IPHash struct{}

// Select selects an up host from the pool based on hashing the request IP
func (r *IPHash) Select(pool UpstreamHostPool, ctx *fasthttp.RequestCtx) *UpstreamHost {
	remoteAddr := ctx.RemoteAddr().String()
	clientIP, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		clientIP = remoteAddr
	}
	return hostByHashing(pool, clientIP)
}

// URIHash is a policy that selects the host based on hashing the request URI
type URIHash struct{}

// Select selects the host based on hashing the URI
func (r *URIHash) Select(pool UpstreamHostPool, ctx *fasthttp.RequestCtx) *UpstreamHost {
	return hostByHashing(pool, string(ctx.RequestURI()))
}

// First is a policy that selects the first available host
type First struct{}

// Select selects the first available host from the pool
func (r *First) Select(pool UpstreamHostPool, ctx *fasthttp.RequestCtx) *UpstreamHost {
	for _, host := range pool {
		if host.Available() {
			return host
		}
	}
	return nil
}

// Header is a policy that selects based on a hash of the given header
type Header struct {
	// The name of the request header, the value of which will determine
	// how the request is routed
	Name string
}

// Select selects the host based on hashing the header value
func (r *Header) Select(pool UpstreamHostPool, ctx *fasthttp.RequestCtx) *UpstreamHost {
	if r.Name == "" {
		return nil
	}

	val := string(ctx.Request.Header.Peek(r.Name))
	if val == "" {
		return nil
	}
	return hostByHashing(pool, val)
}
