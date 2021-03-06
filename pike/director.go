package pike

import (
	"errors"
	"hash/fnv"
	"math/rand"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	log "github.com/sirupsen/logrus"
	funk "github.com/thoas/go-funk"
	"github.com/vicanso/pike/util"
)

type (
	// Director 服务器列表
	Director struct {
		// 名称
		Name string `json:"name"`
		// backend的选择策略
		Policy string `json:"policy"`
		// ping设置（检测backend是否要用）
		Ping string `json:"ping"`
		// backend列表
		Backends []string `json:"backends"`
		// 可用的backend列表（通过ping检测）
		AvailableBackends []string `json:"availableBackends"`
		// host列表
		Hosts []string `json:"hosts"`
		// url前缀
		Prefixs []string `json:"prefixs"`
		// Rewrites 需要重写的url配置
		Rewrites []string `json:"rewrites"`
		// RequestHeader 需要设置的请求头
		RequestHeader []string `json:"-"`
		// RequestHeaderMap 请求头
		RequestHeaderMap map[string]string `json:"requestHeader"`
		// Header 需要设置的响应头
		Header []string `json:"-"`
		// HeaderMap 响应头
		HeaderMap map[string]string `json:"header"`
		// RewriteRegexp 需要重写的正则匹配
		RewriteRegexp map[*regexp.Regexp]string `json:"-"`
		// 优先级
		Priority int `json:"priority"`
		// 读写锁
		sync.RWMutex
		// roubin 的次数
		roubin uint32
		// transport 指定transport
		Transport *http.Transport `json:"-"`
		// TargetURLMap 每个backend对应的URL对象
		TargetURLMap map[string]*url.URL `json:"-"`
	}
	// Directors 用于director排序
	Directors []*Director
	// SelectFunc 用于选择排序的方法
	SelectFunc func(*Context, *Director) uint32
)

const (
	first            = "first"
	random           = "random"
	roundRobin       = "roundRobin"
	ipHash           = "ipHash"
	uriHash          = "uriHash"
	headerHashPrefix = "header:"
	cookieHashPrefix = "cookie:"
)

var (
	selectFuncMap       = make(map[string]SelectFunc)
	errNotSupportPolicy = errors.New("not support the policy")
)

// hash calculates a hash based on string s
func hash(s string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(s))
	return h.Sum32()
}

// AddSelect 增加select的处理函数
func AddSelect(name string, fn SelectFunc) {
	selectFuncMap[name] = fn
}

// AddSelectByHeader 根据http header的字段来选择
func AddSelectByHeader(name, headerField string) {
	fn := func(c *Context, d *Director) uint32 {
		s := c.Request.Header.Get(headerField)
		return hash(s)
	}
	AddSelect(name, fn)
}

// AddSelectByCookie 根据cookie来选择backend
func AddSelectByCookie(name, cookieName string) {
	fn := func(c *Context, d *Director) uint32 {
		s := ""
		cookie, _ := c.Request.Cookie(cookieName)
		if cookie != nil {
			s = cookie.Value
		}
		return hash(s)
	}
	AddSelect(name, fn)
}

func init() {
	AddSelect(first, func(c *Context, d *Director) uint32 {
		return 0
	})
	AddSelect(random, func(c *Context, d *Director) uint32 {
		return rand.Uint32()
	})
	AddSelect(roundRobin, func(c *Context, d *Director) uint32 {
		return atomic.AddUint32(&d.roubin, 1)
	})
	AddSelect(ipHash, func(c *Context, d *Director) uint32 {
		return hash(c.RealIP())
	})
	AddSelect(uriHash, func(c *Context, d *Director) uint32 {
		return hash(c.Request.RequestURI)
	})
}

// AddPolicySelectFunc 增加新的policy选择函数
func AddPolicySelectFunc(policy string) (err error) {
	if len(policy) == 0 {
		return
	}
	switch policy {
	case first:
		break
	case random:
		break
	case roundRobin:
		break
	case ipHash:
		break
	case uriHash:
		break
	default:
		if strings.HasPrefix(policy, headerHashPrefix) {

			header := policy[len(headerHashPrefix):]
			// 增加自定义的header select function
			AddSelectByHeader(policy, header)
		} else if strings.HasPrefix(policy, cookieHashPrefix) {
			cookie := policy[len(cookieHashPrefix):]
			// 增加自定义的cookie select function
			AddSelectByCookie(policy, cookie)
		} else {
			err = errNotSupportPolicy
		}
	}
	return
}

// Len 获取director slice的长度
func (s Directors) Len() int {
	return len(s)
}

// Swap 元素互换
func (s Directors) Swap(i, j int) {
	s[i], s[j] = s[j], s[i]
}

func (s Directors) Less(i, j int) bool {
	return s[i].Priority < s[j].Priority
}

// RefreshPriority 刷新优先级计算
func (d *Director) RefreshPriority() {
	priority := 8
	// 如果有配置host，优先前提升4
	if len(d.Hosts) != 0 {
		priority -= 4
	}
	// 如果有配置prefix，优先级提升2
	if len(d.Prefixs) != 0 {
		priority -= 2
	}
	d.Priority = priority
}

// AddBackend 增加backend
func (d *Director) AddBackend(backend string) {
	backends := d.Backends
	if !funk.ContainsString(backends, backend) {
		d.Backends = append(backends, backend)
	}
}

// RemoveBackend 删除backend
func (d *Director) RemoveBackend(backend string) {
	backends := d.Backends

	index := funk.IndexOfString(backends, backend)
	if index != -1 {
		d.Backends = append(backends[0:index], backends[index+1:]...)
	}
}

// AddAvailableBackend 增加可用backend列表
func (d *Director) AddAvailableBackend(backend string) {
	d.Lock()
	defer d.Unlock()
	backends := d.AvailableBackends
	if !funk.ContainsString(backends, backend) {
		d.AvailableBackends = append(backends, backend)
	}
}

// RemoveAvailableBackend 删除可用的backend
func (d *Director) RemoveAvailableBackend(backend string) {
	d.Lock()
	defer d.Unlock()
	backends := d.AvailableBackends
	index := funk.IndexOfString(backends, backend)
	if index != -1 {
		d.AvailableBackends = append(backends[0:index], backends[index+1:]...)
	}
}

// GetAvailableBackends 获取可用的backend
func (d *Director) GetAvailableBackends() []string {
	d.RLock()
	defer d.RUnlock()
	return d.AvailableBackends
}

// AddHost 添加host
func (d *Director) AddHost(host string) {
	hosts := d.Hosts
	if !funk.ContainsString(hosts, host) {
		d.Hosts = append(hosts, host)
		d.RefreshPriority()
	}
}

// RemoveHost 删除host
func (d *Director) RemoveHost(host string) {
	hosts := d.Hosts
	index := funk.IndexOfString(hosts, host)
	if index != -1 {
		d.Hosts = append(hosts[0:index], hosts[index+1:]...)
		d.RefreshPriority()
	}
}

// AddPrefix 增加前缀
func (d *Director) AddPrefix(prefix string) {
	prefixs := d.Prefixs
	if !funk.ContainsString(prefixs, prefix) {
		d.Prefixs = append(prefixs, prefix)
		d.RefreshPriority()
	}
}

// RemovePrefix 删除前缀
func (d *Director) RemovePrefix(prefix string) {
	prefixs := d.Prefixs
	index := funk.IndexOfString(prefixs, prefix)
	if index != -1 {
		d.Prefixs = append(prefixs[0:index], prefixs[index+1:]...)
		d.RefreshPriority()
	}
}

// Match 判断是否符合
func (d *Director) Match(host, uri string) (match bool) {
	d.RLock()
	defer d.RUnlock()
	hosts := d.Hosts
	prefixs := d.Prefixs
	// 如果未配置host与prefix，则所有请求都匹配
	if len(hosts) == 0 && len(prefixs) == 0 {
		return true
	}
	// 判断host是否符合
	if len(hosts) != 0 {
		hostBytes := []byte(host)
		for _, item := range hosts {
			if match {
				break
			}
			reg := regexp.MustCompile(item)
			if reg.Match(hostBytes) {
				match = true
			}
		}
		// 如果host不匹配，直接返回
		if !match {
			return
		}
	}

	// 判断prefix是否符合
	if len(prefixs) != 0 {
		// 重置match状态，再判断prefix
		match = false
		for _, item := range prefixs {
			if !match && strings.HasPrefix(uri, item) {
				match = true
			}
		}
	}
	return
}

// GetTargetURL 获取backend对应的*URL
func (d *Director) GetTargetURL(backend *string) (*url.URL, error) {
	if d.TargetURLMap == nil {
		return nil, ErrTargetURLNotInit
	}
	name := *backend
	result := d.TargetURLMap[name]
	if result != nil {
		return result, nil
	}
	d.Lock()
	defer d.Unlock()
	// 如果在lock的时候，同时有其它的已lock并生成，则重复生成（概率较低而无不良影响，忽略）
	result, _ = url.Parse(name)
	if result == nil {
		return nil, ErrParseBackendURLFail
	}
	d.TargetURLMap[name] = result
	return result, nil
}

func genHeaderMap(header []string) map[string]string {
	if len(header) == 0 {
		return nil
	}
	m := make(map[string]string)
	for _, v := range header {
		arr := strings.Split(v, ":")
		if len(arr) != 2 {
			continue
		}
		value := arr[1]
		v := util.CheckAndGetValueFromEnv(value)
		if len(v) != 0 {
			value = v
		}
		m[arr[0]] = value
	}
	return m
}

// GenRequestHeaderMap 生成请求头
func (d *Director) GenRequestHeaderMap() {
	d.RequestHeaderMap = genHeaderMap(d.RequestHeader)
}

// GenHeaderMap 生成响应头的header
func (d *Director) GenHeaderMap() {
	d.HeaderMap = genHeaderMap(d.Header)
}

// Prepare 调用生成、刷新配置
func (d *Director) Prepare() {
	d.RefreshPriority()
	d.GenRewriteRegexp()
	d.GenRequestHeaderMap()
	d.GenHeaderMap()
}

// 检测url，如果5次有3次通过则认为是healthy
func doCheck(url string) (healthy bool) {
	var wg sync.WaitGroup
	var successCount int32
	p := &successCount
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			client := http.Client{
				Timeout: time.Duration(3 * time.Second),
			}
			resp, _ := client.Get(url)
			if resp != nil {
				statusCode := resp.StatusCode
				if statusCode >= 200 && statusCode < 400 {
					atomic.AddInt32(p, 1)
				}
			}
		}()
	}
	wg.Wait()
	if successCount >= 3 {
		healthy = true
	}
	return
}

// Select 根据Policy选择一个backend
func (d *Director) Select(c *Context) string {
	policy := d.Policy
	if len(policy) == 0 {
		policy = roundRobin
	}
	fn := selectFuncMap[policy]
	if fn == nil {
		return ""
	}
	availableBackends := d.GetAvailableBackends()
	count := uint32(len(availableBackends))
	if count == 0 {
		return ""
	}

	index := fn(c, d)

	return availableBackends[index%count]
}

// HealthCheck 对director下的服务器做健康检测
func (d *Director) HealthCheck() {
	backends := d.Backends
	for _, item := range backends {
		go func(backend string) {
			ping := d.Ping
			if len(ping) == 0 {
				ping = "/ping"
			}
			url := backend + ping
			healthy := doCheck(url)
			if healthy {
				d.AddAvailableBackend(backend)
			} else {
				d.RemoveAvailableBackend(backend)
			}
		}(item)
	}
}

// StartHealthCheck 启用health check
func (d *Director) StartHealthCheck(interval time.Duration) {
	defer func() {
		if err := recover(); err != nil {
			// 如果异常，等待后继续检测
			log.Error("health check fail, ", err)
			time.Sleep(time.Second)
			d.StartHealthCheck(interval)
		}
	}()
	d.HealthCheck()
	ticker := time.NewTicker(interval)
	for _ = range ticker.C {
		d.HealthCheck()
	}
}

// GenRewriteRegexp 生成重写url的正则
func (d *Director) GenRewriteRegexp() {
	d.RewriteRegexp = util.GetRewriteRegexp(d.Rewrites)
}

// SetTransport 设置transport
func (d *Director) SetTransport(transport *http.Transport) {
	d.Transport = transport
}
