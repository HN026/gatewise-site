package decision

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
)

type HTTPProvider struct {
	url       string
	client    *http.Client
	transport *http.Transport
}

// NewHTTPProvider reads a small observation document from an operator-selected
// endpoint. It disables redirects/environment proxies and validates TLS normally.
func NewHTTPProvider(address string) (*HTTPProvider, error) {
	u, e := url.Parse(address)
	if e != nil || u.Hostname() == "" || u.Scheme != "http" && u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return nil, fmt.Errorf("decision: source needs HTTP(S) URL without credentials, query or fragment")
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return nil, fmt.Errorf("decision: invalid source port")
		}
	}
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = nil
	t.MaxIdleConns = 1
	t.MaxIdleConnsPerHost = 1
	return &HTTPProvider{url: u.String(), transport: t, client: &http.Client{Transport: t, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (p *HTTPProvider) Close() { p.transport.CloseIdleConnections() }
func (p *HTTPProvider) Sample(ctx context.Context) (Observation, error) {
	r, e := http.NewRequestWithContext(ctx, "GET", p.url, nil)
	if e != nil {
		return Observation{}, fmt.Errorf("decision: source request failed")
	}
	resp, e := p.client.Do(r)
	if e != nil {
		return Observation{}, fmt.Errorf("decision: source unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return Observation{}, fmt.Errorf("decision: source status rejected")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4097))
	if err != nil || len(body) > 4096 {
		return Observation{}, fmt.Errorf("decision: source document exceeds 4096 bytes or read failed")
	}
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	var o Observation
	if e = d.Decode(&o); e != nil {
		return Observation{}, fmt.Errorf("decision: invalid source document")
	}
	if e = d.Decode(new(any)); e != io.EOF {
		return Observation{}, fmt.Errorf("decision: source document too large or trailing data")
	}
	return o, nil
}
