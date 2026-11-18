package extensions

import (
	"context"
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dop251/goja"

	"github.com/ryoku-dev/ryotunes/internal/model"
)

const (
	loadBudget     = 3 * time.Second
	maxBody        = 5 << 20
	maxStorage     = 512 << 10
	requestsPerMin = 120
)

// extension is one loaded extension: its VM lives on its own goroutine and
// runs one job at a time, like Myspoti's extension thread.
type extension struct {
	dir      string
	manifest Manifest
	log      *slog.Logger
	hosts    []string

	jobs  chan func()
	stop  chan struct{}
	once  sync.Once
	vm    *goja.Runtime
	hooks map[string]goja.Callable

	deadline time.Time // of the running call, read on the VM goroutine

	reqMu    sync.Mutex
	requests []time.Time

	storeMu sync.Mutex
}

func loadExtension(dir string, log *slog.Logger) (*extension, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return nil, fmt.Errorf("read manifest.json: %w", err)
	}
	var manifest Manifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return nil, fmt.Errorf("manifest.json: %w", err)
	}
	if manifest.ID == "" {
		return nil, errors.New("manifest.json has no id")
	}
	if manifest.Name == "" {
		manifest.Name = manifest.ID
	}
	main := manifest.Main
	if main == "" {
		main = "main.js"
	}
	script, err := os.ReadFile(filepath.Join(dir, filepath.Clean("/" + main)[1:]))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", main, err)
	}
	ext := &extension{
		dir: dir, manifest: manifest, log: log.With("extension", manifest.ID),
		jobs: make(chan func()), stop: make(chan struct{}), hooks: map[string]goja.Callable{},
	}
	for _, action := range manifest.Actions {
		if action.Name == "network" {
			ext.hosts = append(ext.hosts, action.Hosts...)
		}
	}
	go ext.loop()

	errc := make(chan error, 1)
	ext.jobs <- func() {
		ext.vm = goja.New()
		ext.vm.SetFieldNameMapper(goja.TagFieldNameMapper("json", true))
		if err := ext.install(); err != nil {
			errc <- err
			return
		}
		ext.deadline = time.Now().Add(loadBudget)
		timer := time.AfterFunc(loadBudget, func() { ext.vm.Interrupt("loading took more than 3 s") })
		defer timer.Stop()
		_, err := ext.vm.RunScript(main, string(script))
		ext.vm.ClearInterrupt()
		errc <- err
	}
	if err := <-errc; err != nil {
		ext.close()
		return nil, err
	}
	return ext, nil
}

func (e *extension) loop() {
	for {
		select {
		case job := <-e.jobs:
			job()
		case <-e.stop:
			return
		}
	}
}

func (e *extension) close() { e.once.Do(func() { close(e.stop) }) }

func (e *extension) has(hook string) bool {
	result := make(chan bool, 1)
	select {
	case e.jobs <- func() { _, ok := e.hooks[hook]; result <- ok }:
		return <-result
	case <-e.stop:
		return false
	}
}

func (e *extension) hookNames() []string {
	result := make(chan []string, 1)
	e.jobs <- func() {
		names := make([]string, 0, len(e.hooks))
		for name := range e.hooks {
			names = append(names, name)
		}
		sort.Strings(names)
		result <- names
	}
	return <-result
}

// call runs hook with arg and returns its value exported to Go.
func (e *extension) call(ctx context.Context, hook string, arg any) (any, error) {
	type answer struct {
		value any
		err   error
	}
	result := make(chan answer, 1)
	job := func() {
		fn, ok := e.hooks[hook]
		if !ok {
			result <- answer{}
			return
		}
		deadline, ok := ctx.Deadline()
		if !ok {
			deadline = time.Now().Add(lyricsBudget)
		}
		e.deadline = deadline
		timer := time.AfterFunc(time.Until(deadline), func() { e.vm.Interrupt("took too long") })
		defer timer.Stop()
		value, err := fn(goja.Undefined(), e.vm.ToValue(arg))
		e.vm.ClearInterrupt()
		if err != nil {
			result <- answer{err: err}
			return
		}
		if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
			result <- answer{}
			return
		}
		// Through JSON: plain Go maps and slices, numbers as float64.
		encoded, err := json.Marshal(value.Export())
		if err != nil {
			result <- answer{err: err}
			return
		}
		var out any
		err = json.Unmarshal(encoded, &out)
		result <- answer{value: out, err: err}
	}
	select {
	case e.jobs <- job:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-e.stop:
		return nil, errors.New("extension stopped")
	}
	select {
	case got := <-result:
		return got.value, got.err
	case <-e.stop:
		return nil, errors.New("extension stopped")
	}
}

// install defines the myspoti object (and console) on the VM.
func (e *extension) install() error {
	vm := e.vm
	throwf := func(format string, args ...any) { panic(vm.NewGoError(fmt.Errorf(format, args...))) }
	noStrings := func(goja.FunctionCall) goja.Value {
		throwf("code from strings is not allowed in extensions")
		return nil
	}
	_ = vm.Set("eval", noStrings)
	_ = vm.Set("Function", noStrings)

	logLine := func(call goja.FunctionCall) goja.Value {
		parts := make([]string, 0, len(call.Arguments))
		for _, arg := range call.Arguments {
			if s, ok := arg.Export().(string); ok {
				parts = append(parts, s)
				continue
			}
			encoded, _ := json.Marshal(arg.Export())
			parts = append(parts, string(encoded))
		}
		line := strings.Join(parts, " ")
		if len(line) > 2000 {
			line = line[:2000]
		}
		e.log.Info(line)
		return goja.Undefined()
	}

	ms := vm.NewObject()
	_ = ms.Set("apiVersion", 1)
	_ = ms.Set("locale", "en")
	_ = ms.Set("features", []string{"http.all", "util.sleep"})
	_ = ms.Set("extension", map[string]string{"id": e.manifest.ID, "name": e.manifest.Name, "version": e.manifest.Version})
	_ = ms.Set("provide", func(hook string, fn goja.Value) {
		callable, ok := goja.AssertFunction(fn)
		if !ok {
			throwf("provide: %s needs a function", hook)
		}
		e.hooks[hook] = callable
	})
	_ = ms.Set("on", func(string, goja.Value) {}) // events Ryotunes does not send yet
	_ = ms.Set("log", logLine)
	_ = ms.Set("notify", logLine)
	_ = ms.Set("query", func(values map[string]any) string {
		keys := make([]string, 0, len(values))
		for key, value := range values {
			if value != nil {
				keys = append(keys, key)
			}
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, key := range keys {
			parts = append(parts, url.QueryEscape(key)+"="+strings.ReplaceAll(url.QueryEscape(fmt.Sprint(values[key])), "+", "%20"))
		}
		return strings.Join(parts, "&")
	})

	httpObj := vm.NewObject()
	_ = httpObj.Set("request", func(req map[string]any) goja.Value { return e.httpValue(e.request(req)) })
	_ = httpObj.Set("get", func(rawURL string, options map[string]any) goja.Value {
		req := map[string]any{"url": rawURL, "method": "GET"}
		for k, v := range options {
			req[k] = v
		}
		return e.httpValue(e.request(req))
	})
	_ = httpObj.Set("post", func(rawURL string, body goja.Value, options map[string]any) goja.Value {
		req := map[string]any{"url": rawURL, "method": "POST", "body": body.Export()}
		for k, v := range options {
			req[k] = v
		}
		return e.httpValue(e.request(req))
	})
	_ = httpObj.Set("all", func(reqs []map[string]any) []goja.Value {
		if len(reqs) > 4 {
			throwf("http.all: at most 4 requests at once")
		}
		answers := make([]httpAnswer, len(reqs))
		var wg sync.WaitGroup
		for i, req := range reqs {
			wg.Add(1)
			go func(i int, req map[string]any) {
				defer wg.Done()
				answers[i] = e.request(req)
			}(i, req)
		}
		wg.Wait()
		out := make([]goja.Value, len(answers))
		for i, answer := range answers {
			if answer.err != nil {
				out[i] = vm.ToValue(map[string]string{"error": answer.err.Error()})
			} else {
				out[i] = e.httpValue(answer)
			}
		}
		return out
	})
	_ = ms.Set("http", httpObj)

	settings := vm.NewObject()
	_ = settings.Set("get", func(key string) goja.Value { return vm.ToValue(e.setting(key)) })
	_ = ms.Set("settings", settings)

	storage := vm.NewObject()
	_ = storage.Set("get", func(key string) goja.Value {
		values := e.loadStore()
		value, ok := values[key]
		if !ok {
			return goja.Undefined()
		}
		var out any
		_ = json.Unmarshal(value, &out)
		return vm.ToValue(out)
	})
	_ = storage.Set("set", func(key string, value goja.Value) {
		encoded, err := json.Marshal(value.Export())
		if err != nil {
			throwf("storage.set: %v", err)
		}
		values := e.loadStore()
		values[key] = encoded
		if err := e.saveStore(values); err != nil {
			throwf("%v", err)
		}
	})
	_ = storage.Set("remove", func(key string) {
		values := e.loadStore()
		delete(values, key)
		_ = e.saveStore(values)
	})
	_ = ms.Set("storage", storage)

	player := vm.NewObject()
	_ = player.Set("current", func() goja.Value { return goja.Null() })
	_ = ms.Set("player", player)

	util := vm.NewObject()
	_ = util.Set("base64Encode", func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) })
	_ = util.Set("base64Decode", func(s string) string {
		out, err := base64.StdEncoding.DecodeString(s)
		if err != nil {
			throwf("base64Decode: %v", err)
		}
		return string(out)
	})
	_ = util.Set("md5", func(s string) string { sum := md5.Sum([]byte(s)); return hex.EncodeToString(sum[:]) })
	_ = util.Set("sha1", func(s string) string { sum := sha1.Sum([]byte(s)); return hex.EncodeToString(sum[:]) })
	_ = util.Set("sha256", func(s string) string { sum := sha256.Sum256([]byte(s)); return hex.EncodeToString(sum[:]) })
	_ = util.Set("hmacSha256", func(key, text string) string {
		mac := hmac.New(sha256.New, []byte(key))
		mac.Write([]byte(text))
		return hex.EncodeToString(mac.Sum(nil))
	})
	_ = util.Set("sleep", func(ms int64) {
		if ms < 0 {
			throwf("sleep: ms must be a non-negative integer")
		}
		wait := min(time.Duration(ms)*time.Millisecond, 2*time.Second, time.Until(e.deadline))
		if wait > 0 {
			time.Sleep(wait)
		}
	})
	_ = ms.Set("util", util)

	if err := vm.Set("myspoti", ms); err != nil {
		return err
	}
	console := vm.NewObject()
	for _, name := range []string{"log", "info", "warn", "error", "debug"} {
		_ = console.Set(name, logLine)
	}
	return vm.Set("console", console)
}

func (e *extension) setting(key string) any {
	if raw, err := os.ReadFile(filepath.Join(e.dir, "settings.json")); err == nil {
		var values map[string]any
		if json.Unmarshal(raw, &values) == nil {
			if value, ok := values[key]; ok {
				return value
			}
		}
	}
	for _, setting := range e.manifest.Settings {
		if setting.Key == key && len(setting.Default) > 0 {
			var value any
			if json.Unmarshal(setting.Default, &value) == nil {
				return value
			}
		}
	}
	return nil
}

func (e *extension) storePath() string { return filepath.Join(e.dir, "storage.json") }

func (e *extension) loadStore() map[string]json.RawMessage {
	e.storeMu.Lock()
	defer e.storeMu.Unlock()
	values := map[string]json.RawMessage{}
	if raw, err := os.ReadFile(e.storePath()); err == nil {
		_ = json.Unmarshal(raw, &values)
	}
	return values
}

func (e *extension) saveStore(values map[string]json.RawMessage) error {
	e.storeMu.Lock()
	defer e.storeMu.Unlock()
	encoded, err := json.Marshal(values)
	if err != nil {
		return err
	}
	if len(encoded) > maxStorage {
		return errors.New("storage full (512 KB)")
	}
	return os.WriteFile(e.storePath(), encoded, 0o600)
}

// --- myspoti.http ----------------------------------------------------------

type httpAnswer struct {
	status  int
	headers map[string]string
	body    string
	err     error
}

var noRedirects = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

func (e *extension) httpValue(answer httpAnswer) goja.Value {
	if answer.err != nil {
		panic(e.vm.NewGoError(answer.err))
	}
	obj := e.vm.NewObject()
	_ = obj.Set("status", answer.status)
	_ = obj.Set("headers", answer.headers)
	_ = obj.Set("body", answer.body)
	_ = obj.Set("ok", answer.status >= 200 && answer.status < 300)
	body := answer.body
	_ = obj.Set("json", func() goja.Value {
		var out any
		if err := json.Unmarshal([]byte(body), &out); err != nil {
			panic(e.vm.NewGoError(fmt.Errorf("json: %v", err)))
		}
		return e.vm.ToValue(out)
	})
	return obj
}

func (e *extension) request(req map[string]any) httpAnswer {
	rawURL, _ := req["url"].(string)
	target, err := url.Parse(rawURL)
	if err != nil || target.Host == "" {
		return httpAnswer{err: fmt.Errorf("http: bad url %q", rawURL)}
	}
	if err := e.allowed(target); err != nil {
		return httpAnswer{err: err}
	}
	if err := e.rateLimit(); err != nil {
		return httpAnswer{err: err}
	}
	method := strings.ToUpper(fmt.Sprint(valueOr(req["method"], "GET")))
	switch method {
	case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD":
	default:
		return httpAnswer{err: fmt.Errorf("http: method %s is not allowed", method)}
	}
	timeout := 15 * time.Second
	if ms, ok := req["timeout"].(float64); ok {
		timeout = time.Duration(ms) * time.Millisecond
	} else if ms, ok := req["timeout"].(int64); ok {
		timeout = time.Duration(ms) * time.Millisecond
	}
	timeout = min(max(timeout, time.Second), 20*time.Second)
	if left := time.Until(e.deadline); left > 0 && left < timeout {
		timeout = left
	}
	var body io.Reader
	contentType := ""
	switch value := req["body"].(type) {
	case nil:
	case string:
		body = strings.NewReader(value)
	default:
		encoded, _ := json.Marshal(value)
		body = strings.NewReader(string(encoded))
		contentType = "application/json"
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, method, target.String(), body)
	if err != nil {
		return httpAnswer{err: fmt.Errorf("http: %v", err)}
	}
	request.Header.Set("User-Agent", "Myspoti-Extension/1 ("+e.manifest.ID+")")
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	if headers, ok := req["headers"].(map[string]any); ok {
		for name, value := range headers {
			switch strings.ToLower(name) {
			case "host", "content-length", "connection", "cookie", "transfer-encoding":
				continue
			}
			request.Header.Set(name, fmt.Sprint(value))
		}
	}
	response, err := noRedirects.Do(request)
	if err != nil {
		return httpAnswer{err: fmt.Errorf("http: %v", err)}
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, maxBody+1))
	if err != nil {
		return httpAnswer{err: fmt.Errorf("http: %v", err)}
	}
	if len(data) > maxBody {
		return httpAnswer{err: errors.New("http: response larger than 5 MB")}
	}
	headers := map[string]string{}
	for name, values := range response.Header {
		headers[strings.ToLower(name)] = strings.Join(values, ", ")
	}
	return httpAnswer{status: response.StatusCode, headers: headers, body: string(data)}
}

func valueOr(value any, fallback any) any {
	if value == nil {
		return fallback
	}
	return value
}

// allowed applies the manifest's network hosts and Myspoti's rules.
func (e *extension) allowed(target *url.URL) error {
	host := strings.ToLower(target.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") {
		return fmt.Errorf("http: local address %s refused", host)
	}
	if ip := net.ParseIP(host); ip != nil {
		if ip.To4() == nil || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() || ip.To4()[0] == 0 {
			return fmt.Errorf("http: local address %s refused", host)
		}
	}
	exact, matched := false, false
	for _, allowed := range e.hosts {
		allowed = strings.ToLower(strings.TrimSpace(allowed))
		switch {
		case allowed == "*":
			matched = true
		case strings.HasPrefix(allowed, "*."):
			domain := allowed[2:]
			if host == domain || strings.HasSuffix(host, "."+domain) {
				matched = true
			}
		case allowed == host:
			matched, exact = true, true
		}
	}
	if !matched {
		return fmt.Errorf("http: %s is not in the extension's network hosts", host)
	}
	switch target.Scheme {
	case "https":
	case "http":
		if !exact {
			return fmt.Errorf("http: plain http only to a host the manifest names exactly")
		}
	default:
		return fmt.Errorf("http: scheme %s is not allowed", target.Scheme)
	}
	return nil
}

func (e *extension) rateLimit() error {
	e.reqMu.Lock()
	defer e.reqMu.Unlock()
	now := time.Now()
	kept := e.requests[:0]
	for _, at := range e.requests {
		if now.Sub(at) < time.Minute {
			kept = append(kept, at)
		}
	}
	e.requests = kept
	if len(e.requests) >= requestsPerMin {
		return errors.New("http: more than 120 requests in a minute")
	}
	e.requests = append(e.requests, now)
	return nil
}

// --- lyrics answers --------------------------------------------------------

// parseLyrics turns a lyrics hook's answer into Ryotunes' lyrics.
func parseLyrics(value any, extensionName string) (*model.Lyrics, error) {
	answer, ok := value.(map[string]any)
	if !ok {
		return nil, nil
	}
	source, _ := answer["provider"].(string)
	if source = strings.TrimSpace(source); source == "" {
		source = extensionName
	}
	if len(source) > 40 {
		source = source[:40]
	}
	out := &model.Lyrics{Source: source}
	rawLines, hasLines := answer["lines"].([]any)
	if !hasLines {
		plain, _ := answer["plain"].(string)
		for _, row := range strings.Split(strings.ReplaceAll(plain, "\r\n", "\n"), "\n") {
			out.Lines = append(out.Lines, model.LyricsLine{Text: strings.TrimSpace(row)})
		}
		if !hasText(out.Lines) {
			return nil, nil
		}
		return out, nil
	}
	if len(rawLines) > 3000 {
		return nil, errors.New("more than 3000 lyrics lines")
	}
	anyTime := false
	for _, raw := range rawLines {
		line, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		text := clip(stringOf(line["text"]), 500)
		entry := model.LyricsLine{Text: text}
		if t, ok := millis(line["time"]); ok {
			entry.TimeMS = &t
			anyTime = true
		} else {
			zero := uint64(0)
			entry.TimeMS = &zero
		}
		if end, ok := millis(line["end"]); ok {
			entry.EndTimeMS = &end
		}
		if translation, ok := line["translation"].(string); ok && translation != "" {
			entry.Translation = &translation
		}
		if roman, ok := line["romanization"].(string); ok && roman != "" {
			entry.Romanized = &roman
		}
		if words, ok := line["words"].([]any); ok {
			for i, rawWord := range words {
				if i >= 200 {
					break
				}
				word, ok := rawWord.(map[string]any)
				if !ok {
					continue
				}
				start, ok := millis(word["time"])
				if !ok {
					continue
				}
				end, _ := millis(word["end"])
				entry.Words = append(entry.Words, model.LyricsWord{Text: clip(stringOf(word["text"]), 500), StartMS: start, EndMS: end})
			}
		}
		out.Lines = append(out.Lines, entry)
	}
	synced := anyTime
	if value, ok := answer["synced"].(bool); ok {
		synced = value
	}
	out.Synced = synced
	if !synced {
		for i := range out.Lines {
			out.Lines[i].TimeMS, out.Lines[i].EndTimeMS, out.Lines[i].Words = nil, nil, nil
		}
	}
	if !hasText(out.Lines) {
		return nil, nil
	}
	return out, nil
}

func hasText(lines []model.LyricsLine) bool {
	for _, line := range lines {
		if strings.TrimSpace(line.Text) != "" {
			return true
		}
	}
	return false
}

func stringOf(value any) string {
	if s, ok := value.(string); ok {
		return s
	}
	return ""
}

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// millis reads a time in ms: a number or a numeric string.
func millis(value any) (uint64, bool) {
	switch v := value.(type) {
	case float64:
		if v >= 0 {
			return uint64(v), true
		}
	case string:
		if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil && f >= 0 {
			return uint64(f), true
		}
	}
	return 0, false
}
