package worker

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/klauspost/compress/zstd"

	"github.com/jesse/codex-app-proxy/internal/constants"
	"github.com/jesse/codex-app-proxy/internal/module"
)

type Worker struct {
	snapshots *snapshotHolder
	client    *http.Client
}

type Options struct {
	Snapshot RuntimeConfigSnapshot
	Client   *http.Client
}

func New(opts Options) *Worker {
	client := opts.Client
	if client == nil {
		client = http.DefaultClient
	}
	return &Worker{
		snapshots: newSnapshotHolder(opts.Snapshot),
		client:    client,
	}
}

func (w *Worker) UpdateSnapshot(snapshot RuntimeConfigSnapshot) error {
	if err := snapshot.Validate(); err != nil {
		return err
	}
	w.snapshots.Store(snapshot)
	return nil
}

func (w *Worker) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, constants.ProxyPathPrefix) {
		w.serveManagement(rw, r)
		return
	}

	snapshot := w.snapshots.Load()
	if err := w.proxyRequest(rw, r, snapshot); err != nil {
		http.Error(rw, err.Error(), http.StatusBadGateway)
	}
}

func (w *Worker) proxyRequest(rw http.ResponseWriter, r *http.Request, snapshot RuntimeConfigSnapshot) error {
	body, contentType, err := readRequestBody(r)
	if err != nil {
		return err
	}
	proxyReq := &module.ProxyRequest{
		Method:       r.Method,
		Path:         r.URL.Path,
		Headers:      r.Header.Clone(),
		Body:         body,
		ContentType:  contentType,
		OriginalPath: r.URL.Path,
	}

	ctx := r.Context()
	for _, middleware := range snapshot.Modules {
		if err := middleware.ProcessRequest(ctx, proxyReq); err != nil {
			return err
		}
	}

	upstreamURL, err := joinURL(snapshot.Upstream.BaseURL, proxyReq.Path, r.URL.RawQuery)
	if err != nil {
		return err
	}
	upstreamReq, err := http.NewRequestWithContext(ctx, proxyReq.Method, upstreamURL, bytes.NewReader(proxyReq.Body))
	if err != nil {
		return err
	}
	upstreamReq.Header = proxyReq.Headers.Clone()
	if snapshot.Upstream.APIKey != "" {
		upstreamReq.Header.Set("Authorization", "Bearer "+snapshot.Upstream.APIKey)
	}
	if len(proxyReq.Body) > 0 {
		upstreamReq.ContentLength = int64(len(proxyReq.Body))
	}

	upstreamHTTPResp, err := w.client.Do(upstreamReq)
	if err != nil {
		return err
	}
	proxyResp := &module.ProxyResponse{
		StatusCode:  upstreamHTTPResp.StatusCode,
		Headers:     upstreamHTTPResp.Header.Clone(),
		Body:        upstreamHTTPResp.Body,
		ContentType: upstreamHTTPResp.Header.Get("Content-Type"),
	}

	for i := len(snapshot.Modules) - 1; i >= 0; i-- {
		proxyResp, err = snapshot.Modules[i].WrapResponse(ctx, proxyReq, proxyResp)
		if err != nil {
			_ = upstreamHTTPResp.Body.Close()
			return err
		}
	}

	return copyProxyResponse(ctx, rw, proxyResp)
}

func readRequestBody(r *http.Request) ([]byte, string, error) {
	if r.Body == nil {
		return nil, r.Header.Get("Content-Type"), nil
	}
	defer r.Body.Close()

	var reader io.Reader = r.Body
	switch strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Encoding"))) {
	case "", "identity":
	case "gzip":
		gz, err := gzip.NewReader(r.Body)
		if err != nil {
			return nil, "", err
		}
		defer gz.Close()
		reader = gz
	case "deflate":
		fl := flate.NewReader(r.Body)
		defer fl.Close()
		reader = fl
	case "zstd":
		zr, err := zstd.NewReader(r.Body)
		if err != nil {
			return nil, "", err
		}
		defer zr.Close()
		reader = zr
	default:
		return nil, "", fmt.Errorf("unsupported content encoding %q", r.Header.Get("Content-Encoding"))
	}
	body, err := io.ReadAll(reader)
	return body, r.Header.Get("Content-Type"), err
}

func joinURL(baseURL string, requestPath string, rawQuery string) (string, error) {
	upstream, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}
	basePath := strings.TrimRight(upstream.Path, "/")
	if requestPath == "" {
		requestPath = "/"
	}
	upstream.Path = basePath + requestPath
	upstream.RawQuery = rawQuery
	return upstream.String(), nil
}

func copyProxyResponse(ctx context.Context, rw http.ResponseWriter, resp *module.ProxyResponse) error {
	defer resp.Body.Close()
	for key, values := range resp.Headers {
		for _, value := range values {
			rw.Header().Add(key, value)
		}
	}
	if resp.StatusCode != 0 {
		rw.WriteHeader(resp.StatusCode)
	}

	flusher, _ := rw.(http.Flusher)
	buf := make([]byte, 32*1024)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		n, err := resp.Body.Read(buf)
		if n > 0 {
			if _, writeErr := rw.Write(buf[:n]); writeErr != nil {
				return writeErr
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}
