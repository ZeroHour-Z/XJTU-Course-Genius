package session

import (
	"fmt"
	"math/rand"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/go-resty/resty/v2"
)

var uaList = []string{
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36 Edg/130.0.0.0",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36 Edg/128.0.0.0",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/127.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36 Edg/126.0.0.0",
}

func randomUA() string {
	return uaList[rand.Intn(len(uaList))]
}

type State struct {
	Account      string
	Password     string
	StudentCode  string
	BatchCode    string // xklcdm (elective batch code)
	BatchType    string // typeCode: "02"=正选
	Campus       string
	CampusList   []CampusInfo
	Token        string
	Cookies      []*http.Cookie
	FpVisitorID  string

	mu sync.Mutex
}

type CampusInfo struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

var state = &State{}

func Get() *State { return state }

func NewClient() *resty.Client {
	jar, _ := cookiejar.New(nil)
	client := resty.New().
		SetCookieJar(jar).
		SetTimeout(15 * time.Second).
		SetHeader("User-Agent", randomUA()).
		SetHeader("Accept", "application/json, text/plain, */*").
		SetHeader("Accept-Language", "zh-CN,zh;q=0.9")

	// load saved cookies
	xkfwURL, _ := url.Parse("https://xkfw.xjtu.edu.cn")
	casURL, _ := url.Parse("https://login.xjtu.edu.cn")
	for _, c := range state.Cookies {
		client.GetClient().Jar.SetCookies(xkfwURL, []*http.Cookie{c})
		client.GetClient().Jar.SetCookies(casURL, []*http.Cookie{c})
	}
	if state.Token != "" {
		client.SetHeader("Token", state.Token)
	}
	return client
}

var allCookiesURLs = []*url.URL{
	{Scheme: "https", Host: "xkfw.xjtu.edu.cn"},
	{Scheme: "https", Host: "login.xjtu.edu.cn"},
}

func jarCookies(jar http.CookieJar) []*http.Cookie {
	if jar == nil {
		return nil
	}
	var all []*http.Cookie
	for _, u := range allCookiesURLs {
		all = append(all, jar.Cookies(u)...)
	}
	return all
}

func SaveCookies(client *resty.Client) {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.Cookies = jarCookies(client.GetClient().Jar)
}

func SaveCookiesFromHTTP(httpClient *http.Client) {
	state.mu.Lock()
	defer state.mu.Unlock()
	if httpClient.Jar != nil {
		state.Cookies = jarCookies(httpClient.Jar)
	}
}

func SetToken(t string) {
	state.mu.Lock()
	state.Token = t
	state.mu.Unlock()
}

func SetStudentCode(code string) {
	state.mu.Lock()
	state.StudentCode = code
	state.mu.Unlock()
}

func SetBatchCode(code string) {
	state.mu.Lock()
	state.BatchCode = code
	state.mu.Unlock()
}

func SetBatchType(tc string) {
	state.mu.Lock()
	state.BatchType = tc
	state.mu.Unlock()
}

func SetCampus(c string) {
	state.mu.Lock()
	state.Campus = c
	state.mu.Unlock()
}

func SetCampusList(list []CampusInfo) {
	state.mu.Lock()
	state.CampusList = list
	state.mu.Unlock()
}

func SetFpVisitorID(id string) {
	state.mu.Lock()
	state.FpVisitorID = id
	state.mu.Unlock()
}

// ── Auto-relogin interceptor ──

// ReloginFunc is called when the session expires during an API call.
type ReloginFunc func(client *resty.Client) error

// EnableAutoRelogin adds an OnAfterResponse interceptor that detects session
// expiry (xkfw API returns HTML instead of JSON) and automatically re-logs in
// then retries the original request.
//
// Detection: body starts with '<' AND the original request URL contains
// "/xsxkapp/*default/index.do") was an API call expecting JSON. Also matches /xsxkapp/sys/xsxkapp/elective/..." (i.e. it was an API call expecting JSON).
// CAS login flow uses raw http.Client, not resty, so it never triggers.
//
// Backoff: if two relogin attempts happen within 30 seconds, the request
// is failed immediately to prevent infinite loops.
func EnableAutoRelogin(client *resty.Client, relogin ReloginFunc) {
	var mu sync.Mutex
	var inProgress bool
	var lastRelogin time.Time

	client.OnAfterResponse(func(c *resty.Client, resp *resty.Response) error {
		body := resp.Body()
		if len(body) == 0 || body[0] != '<' {
			return nil
		}

		// Check the original request URL (before redirects). When xkfw
		// redirects to CAS, resp.Request.URL is login.xjtu.edu.cn but
		// the original request was to xkfw.
		origURL := resp.Request.URL
		if resp.Request.RawRequest != nil {
			origURL = resp.Request.RawRequest.URL.String()
		}
		if !strings.Contains(origURL, "/xsxkapp/sys/xsxkapp/") {
			return nil
		}

		mu.Lock()
		if inProgress {
			mu.Unlock()
			return nil
		}
		if time.Since(lastRelogin) < 30*time.Second {
			mu.Unlock()
			return fmt.Errorf("会话已过期，重新登录失败")
		}
		inProgress = true
		mu.Unlock()

		defer func() {
			mu.Lock()
			inProgress = false
			mu.Unlock()
		}()

		// Re-login
		if err := relogin(c); err != nil {
			mu.Lock()
			lastRelogin = time.Now()
			mu.Unlock()
			return fmt.Errorf("自动重新登录失败: %w", err)
		}

		// Replay the original request with fresh token
		r := c.R()
		if rawReq := resp.Request.RawRequest; rawReq != nil {
			for k, vals := range rawReq.Header {
				for _, v := range vals {
					r.SetHeader(k, v)
				}
			}
			r.SetQueryString(rawReq.URL.RawQuery)
			r.Method = rawReq.Method
			r.URL = rawReq.URL.String()
		}

		retryResp, reErr := r.Execute(r.Method, r.URL)
		if reErr != nil {
			mu.Lock()
			lastRelogin = time.Now()
			mu.Unlock()
			return reErr
		}
		if retryResp.Body()[0] == '<' {
			mu.Lock()
			lastRelogin = time.Now()
			mu.Unlock()
			return fmt.Errorf("会话已过期，重新登录后仍失败")
		}

		resp.RawResponse = retryResp.RawResponse
		resp.SetBody(retryResp.Body())
		return nil
	})
}
